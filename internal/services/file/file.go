package file

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/path"
	"go.uber.org/fx"
)

var log = logger.Named("file")

var ErrIsDirectory = fmt.Errorf("路径是目录")

type Service struct {
	cfg *config.Config
	ctx context.Context

	path *path.Service

	serveCacheMu      sync.Mutex
	serveCache        map[string]*serveCacheSession
	serveCacheStopped bool
}

type Tree struct {
	Name        string `json:"name"`
	IsDir       bool   `json:"is_dir"`
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ModTime     int64  `json:"mtime,omitempty"` // Unix milliseconds
	IsRecording bool   `json:"is_recording,omitempty"`
}

type PagedTree struct {
	Total int    `json:"total"`
	Items []Tree `json:"items"`
}

type ListOptions struct {
	Filter    func(fs.DirEntry) bool // nil = no filter
	Search    string                 // empty = no search
	OnlyMedia bool                   // keep directories; hide non-media files
	Sort      string                 // name|mtime; empty = browse ordering (see ListTreeWithOptions)
	Order     string                 // asc|desc; used together with Sort
	Start     *time.Time             // nil = no lower bound (files only, by filename time, mtime fallback)
	End       *time.Time             // nil = no upper bound (files only, by filename time, mtime fallback)
	Offset    int
	Limit     int // 0 = all
}

func NewService(ls fx.Lifecycle, cfg *config.Config, pathSvc *path.Service) *Service {
	ctx, cancel := context.WithCancel(context.Background())

	s := &Service{
		cfg:        cfg,
		ctx:        ctx,
		path:       pathSvc,
		serveCache: make(map[string]*serveCacheSession),
	}

	ls.Append(fx.StopHook(func() {
		cancel()
		s.stopServeCacheRelease()
	}))
	return s
}

func (s *Service) ListTree(path string) ([]Tree, error) {
	return s.ListTreeWithFilter(path, func(f fs.DirEntry) bool {
		return !strings.HasSuffix(f.Name(), ".tmp") // ignore .tmp files
	})
}

func (s *Service) ListTreeWithFilter(path string, filter func(fs.DirEntry) bool) ([]Tree, error) {
	fullPath, err := s.path.ValidatePath(path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}

	relativePath, err := s.path.GetRelativePath(fullPath)
	if err != nil {
		return nil, err
	}

	files := make([]Tree, 0)
	for _, entry := range entries {
		if filter(entry) {
			entryPath := filepath.Join(relativePath, entry.Name())
			info, _ := entry.Info()
			files = append(files, Tree{
				Name:  entry.Name(),
				IsDir: entry.IsDir(),
				Path:  entryPath,
				Size: func() int64 {
					if info != nil && !entry.IsDir() {
						return info.Size()
					}
					return 0
				}(),
				ModTime: func() int64 {
					if info != nil {
						return info.ModTime().UnixMilli()
					}
					return 0
				}(),
				IsRecording: false,
			})
		}
	}
	return files, nil
}

// sortDirEntries sorts filtered entries for pagination. Directories always come
// first; the selected field/sort is applied inside the file and directory sets.
func sortDirEntries(entries []fs.DirEntry, sortBy, order string) {
	sortBy = strings.ToLower(strings.TrimSpace(sortBy))
	if sortBy != "mtime" {
		sortBy = "name"
	}
	desc := strings.EqualFold(strings.TrimSpace(order), "desc")
	lessName := func(a, b fs.DirEntry) bool {
		return strings.ToLower(a.Name()) < strings.ToLower(b.Name())
	}
	less := func(a, b fs.DirEntry) bool {
		if sortBy == "mtime" {
			ai, aerr := a.Info()
			bi, berr := b.Info()
			var am, bm int64
			if aerr == nil && ai != nil {
				am = ai.ModTime().UnixMilli()
			}
			if berr == nil && bi != nil {
				bm = bi.ModTime().UnixMilli()
			}
			if am != bm {
				if desc {
					return am > bm
				}
				return am < bm
			}
		} else if lessName(a, b) != lessName(b, a) {
			if desc {
				return lessName(b, a)
			}
			return lessName(a, b)
		}
		return lessName(a, b)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return less(entries[i], entries[j])
	})
}

func (s *Service) ListTreeWithOptions(path string, opts ListOptions) (*PagedTree, error) {
	fullPath, err := s.path.ValidatePath(path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}

	relativePath, err := s.path.GetRelativePath(fullPath)
	if err != nil {
		return nil, err
	}

	// Apply filter + search to collect matching entries (cheap: no stat)
	filtered := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if opts.Filter != nil && !opts.Filter(entry) {
			continue
		}
		if opts.Search != "" && !strings.Contains(strings.ToLower(entry.Name()), strings.ToLower(opts.Search)) {
			continue
		}
		if opts.OnlyMedia && !entry.IsDir() && !IsRecordingMediaFilename(entry.Name()) {
			continue
		}
		filtered = append(filtered, entry)
	}

	filtered = filterByRecordingTimeRange(filtered, opts.Start, opts.End)

	// Sorting: an explicit sort/order from the caller keeps the fork's
	// name/mtime ordering (directories first, then files/dirs by the requested
	// field). With no explicit sort/order the upstream browse ordering is used:
	// directories by mtime newest-first, files by filename timestamp
	// newest-first.
	if opts.Sort != "" || opts.Order != "" {
		sortDirEntries(filtered, opts.Sort, opts.Order)
	} else {
		sortBrowseEntries(filtered)
	}

	total := len(filtered)

	// Determine the page slice
	start := min(opts.Offset, total)
	end := total
	if opts.Limit > 0 {
		end = min(start+opts.Limit, total)
	}
	page := filtered[start:end]

	// Build Tree items — only call entry.Info() (stat) for files in this page
	items := make([]Tree, 0, len(page))
	for _, entry := range page {
		entryPath := filepath.Join(relativePath, entry.Name())
		info, _ := entry.Info()
		items = append(items, Tree{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Path:  entryPath,
			Size: func() int64 {
				if info != nil && !entry.IsDir() {
					return info.Size()
				}
				return 0
			}(),
			ModTime: func() int64 {
				if info != nil {
					return info.ModTime().UnixMilli()
				}
				return 0
			}(),
		})
	}

	return &PagedTree{
		Total: total,
		Items: items,
	}, nil
}

func (s *Service) GetFileStream(path string) (io.ReadCloser, os.FileInfo, error) {
	fullPath, err := s.path.ValidatePath(path)
	if err != nil {
		return nil, nil, err
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, ErrIsDirectory
	}

	if f, err := os.Open(fullPath); err != nil {
		return nil, nil, err
	} else {
		return f, info, nil
	}
}

func (s *Service) DeleteDirectory(path string) error {
	fullPath, err := s.path.ValidatePath(path)
	if err != nil {
		return err
	}
	return os.RemoveAll(fullPath)
}

func (s *Service) DeleteFiles(paths ...string) error {
	var fullPaths []string
	for _, path := range paths {
		fullPath, err := s.path.ValidatePath(path)
		if err != nil {
			return err
		}
		fullPaths = append(fullPaths, fullPath)
	}
	for _, fullPath := range fullPaths {
		if err := os.Remove(fullPath); err != nil {
			return err
		}
	}
	return nil
}
