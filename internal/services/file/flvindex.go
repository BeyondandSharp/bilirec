// SPDX-License-Identifier: GPL-3.0-only

package file

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	flvTagHeaderSize = 11
	flvTagFooterSize = 4
	maxFLVTagBody    = 32 << 20
)

// ErrFLVIndexUnsupported is returned when a file is not a FLV, cannot be
// scanned, or contains no keyframes.
var ErrFLVIndexUnsupported = errors.New("无法生成 FLV 关键帧索引")

// FLVKeyframeIndex lets the web player seek in files whose onMetaData has no
// keyframes/filepositions index (for example old fork recordings such as
// test_2.flv). Times are milliseconds and filepositions are absolute byte
// offsets in the original file, matching flv.js's internal keyframesIndex.
type FLVKeyframeIndex struct {
	DurationMS    int64   `json:"duration_ms"`
	TimesMS       []int64 `json:"times_ms"`
	FilePositions []int64 `json:"filepositions"`
}

type flvIndexCacheEntry struct {
	size    int64
	modTime time.Time
	index   *FLVKeyframeIndex
}

var (
	flvIndexMu    sync.Mutex
	flvIndexCache = make(map[string]flvIndexCacheEntry)
)

// FLVIndex returns (and caches) a keyframe index for an existing FLV file.
// The scan reads only tag headers, not media payloads.
func (s *Service) FLVIndex(relPath string) (*FLVKeyframeIndex, error) {
	fullPath, err := s.path.ValidatePath(relPath)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	if st.IsDir() || !strings.EqualFold(filepath.Ext(fullPath), ".flv") {
		return nil, ErrFLVIndexUnsupported
	}

	flvIndexMu.Lock()
	defer flvIndexMu.Unlock()
	if entry, ok := flvIndexCache[fullPath]; ok && entry.size == st.Size() && entry.modTime.Equal(st.ModTime()) {
		return entry.index, nil
	}

	index, err := scanFLVIndex(fullPath, st.Size())
	if err != nil {
		return nil, err
	}
	flvIndexCache[fullPath] = flvIndexCacheEntry{size: st.Size(), modTime: st.ModTime(), index: index}
	return index, nil
}

func scanFLVIndex(fullPath string, size int64) (*FLVKeyframeIndex, error) {
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var header [9]byte
	if _, err := f.ReadAt(header[:], 0); err != nil {
		return nil, ErrFLVIndexUnsupported
	}
	if string(header[:3]) != "FLV" || header[3] != 1 {
		return nil, ErrFLVIndexUnsupported
	}
	dataOffset := int64(binary.BigEndian.Uint32(header[5:9]))
	if dataOffset < 9 || dataOffset+flvTagFooterSize > size {
		return nil, ErrFLVIndexUnsupported
	}

	var (
		pos       = dataOffset + flvTagFooterSize
		index     = &FLVKeyframeIndex{}
		lastTS    int64
		keyframes int
		scratch   [13]byte // 11-byte tag header + first 2 payload bytes
	)
	for pos+flvTagHeaderSize <= size {
		n, readErr := f.ReadAt(scratch[:], pos)
		if n < flvTagHeaderSize || (readErr != nil && n < 11) {
			break
		}
		typ := scratch[0]
		bodySize := int64(scratch[1])<<16 | int64(scratch[2])<<8 | int64(scratch[3])
		if (typ != 8 && typ != 9 && typ != 18) || bodySize <= 0 || bodySize > maxFLVTagBody {
			break
		}
		if pos+flvTagHeaderSize+bodySize+flvTagFooterSize > size {
			break
		}
		ts := int64(scratch[4])<<16 | int64(scratch[5])<<8 | int64(scratch[6]) | int64(scratch[7])<<24
		if typ == 8 || typ == 9 {
			lastTS = ts
		}
		if typ == 9 && bodySize >= 2 && n >= 13 && scratch[11]>>4 == 1 && scratch[12] == 1 {
			index.TimesMS = append(index.TimesMS, ts)
			index.FilePositions = append(index.FilePositions, pos)
			keyframes++
			if keyframes > 200000 { // sanity bound
				break
			}
		}
		pos += flvTagHeaderSize + bodySize + flvTagFooterSize
	}
	if keyframes == 0 {
		return nil, ErrFLVIndexUnsupported
	}
	if lastTS < 0 || lastTS > math.MaxInt32 {
		return nil, ErrFLVIndexUnsupported
	}
	index.DurationMS = lastTS
	return index, nil
}
