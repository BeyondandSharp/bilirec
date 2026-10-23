package file

import (
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/bilirec/bilirec/utils"
)

func filterByRecordingTimeRange(entries []fs.DirEntry, start, end *time.Time) []fs.DirEntry {
	if start == nil && end == nil {
		return entries
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			out = append(out, entry)
			continue
		}
		fileTime, _, ok := utils.RecordingFilenameTime(entry.Name())
		if !ok {
			continue
		}
		if start != nil && fileTime.Before(*start) {
			continue
		}
		if end != nil && fileTime.After(*end) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func sortBrowseEntries(entries []fs.DirEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.IsDir() != b.IsDir() {
			return a.IsDir()
		}
		if a.IsDir() {
			return dirBrowseOrder(a, b)
		}
		return fileBrowseOrder(a, b)
	})
}

func dirBrowseOrder(a, b fs.DirEntry) bool {
	aTime, aOK := dirModTime(a)
	bTime, bOK := dirModTime(b)
	if aOK && bOK && !aTime.Equal(bTime) {
		return aTime.After(bTime)
	}
	if aOK != bOK {
		return aOK
	}
	return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
}

func fileBrowseOrder(a, b fs.DirEntry) bool {
	aTime, aSeg, aOK := utils.RecordingFilenameTime(a.Name())
	bTime, bSeg, bOK := utils.RecordingFilenameTime(b.Name())
	if aOK && bOK {
		if !aTime.Equal(bTime) {
			return aTime.After(bTime)
		}
		if aSeg != bSeg {
			return aSeg > bSeg
		}
		return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
	}
	if aOK != bOK {
		return aOK
	}
	return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
}

func dirModTime(entry fs.DirEntry) (time.Time, bool) {
	info, err := entry.Info()
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}
