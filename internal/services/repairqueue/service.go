// SPDX-License-Identifier: GPL-3.0-only

// Package repairqueue persists a small batch list of recorded FLV files and
// runs them through the flvrepair one-shot `file` command. The list is kept in
// DATABASE_DIR/repair-queue.json so it survives browser refreshes and service
// restarts.
package repairqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/path"
	"github.com/bilirec/bilirec/internal/services/repair"
	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx"
)

var log = logger.Named("repairqueue")

var (
	ErrNotAvailable = errors.New("视频修复工具不可用")
	ErrNotFound     = errors.New("修复列表中没有该文件")
	ErrRunning      = errors.New("正在修复的文件不能删除")
	ErrNotFLV       = errors.New("只能添加 FLV 视频")
)

// Status is the batch-repair lifecycle state shown by the UI.
type Status string

const (
	StatusPending Status = "pending"
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// Entry is one file in the repair list. Paths are relative to OUTPUT_DIR.
type Entry struct {
	Path       string `json:"path"`
	OutputPath string `json:"output_path"`
	Size       int64  `json:"size"`
	Status     Status `json:"status"`
	Error      string `json:"error,omitempty"`
	AddedAt    int64  `json:"added_at"`
	StartedAt  int64  `json:"started_at,omitempty"`
	FinishedAt int64  `json:"finished_at,omitempty"`
}

type fileRepairer interface {
	Enabled() bool
	RepairFile(ctx context.Context, input, output string) error
}

type diskState struct {
	Entries []*Entry `json:"entries"`
}

// Service owns the persisted repair list and its single sequential worker.
type Service struct {
	cfg      *config.Config
	pathSvc  *path.Service
	repairer fileRepairer

	mu      sync.Mutex
	entries map[string]*Entry
	file    string

	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}
	wg     sync.WaitGroup
}

func NewService(lc fx.Lifecycle, cfg *config.Config, pathSvc *path.Service, repairSvc *repair.Service) *Service {
	return newService(lc, cfg, pathSvc, repairSvc)
}

func newService(lc fx.Lifecycle, cfg *config.Config, pathSvc *path.Service, repairer fileRepairer) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		cfg:      cfg,
		pathSvc:  pathSvc,
		repairer: repairer,
		entries:  make(map[string]*Entry),
		file:     filepath.Join(cfg.DatabaseDir, "repair-queue.json"),
		ctx:      ctx,
		cancel:   cancel,
		wake:     make(chan struct{}, 1),
	}
	lc.Append(fx.StartStopHook(s.start, s.stop))
	return s
}

func (s *Service) start() error {
	if s == nil || s.pathSvc == nil || s.cfg == nil {
		return errors.New("repairqueue：缺少依赖")
	}
	s.load()
	s.wg.Go(s.worker)
	return nil
}

func (s *Service) stop() error {
	if s == nil {
		return nil
	}
	s.cancel()
	s.wg.Wait()
	s.save()
	return nil
}

// Enabled reports whether the underlying flvrepair binary is available.
func (s *Service) Enabled() bool {
	return s != nil && s.repairer != nil && s.repairer.Enabled()
}

// List returns a copy of the list in insertion order.
func (s *Service) List() []Entry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, *entry)
	}
	sortEntries(out)
	return out
}

// Add validates FLV paths and appends them to the list. Existing paths are
// ignored (idempotent add), and the output name is picked beside the source
// with the required `_fix` suffix.
func (s *Service) Add(paths []string) ([]Entry, error) {
	if s == nil {
		return nil, errors.New("repairqueue：服务未初始化")
	}
	if !s.Enabled() {
		return nil, ErrNotAvailable
	}
	added := make([]Entry, 0, len(paths))
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, raw := range paths {
		rel, full, err := s.normalize(raw)
		if err != nil {
			return nil, err
		}
		if existing, ok := s.entries[rel]; ok {
			added = append(added, *existing)
			continue
		}
		info, err := os.Stat(full)
		if err != nil {
			return nil, err
		}
		if info.IsDir() || !strings.EqualFold(filepath.Ext(full), ".flv") {
			return nil, ErrNotFLV
		}
		output, err := s.nextOutputPath(full)
		if err != nil {
			return nil, err
		}
		outputRel, err := s.pathSvc.GetRelativePath(output)
		if err != nil {
			return nil, err
		}
		entry := &Entry{
			Path:       rel,
			OutputPath: filepath.ToSlash(outputRel),
			Size:       info.Size(),
			Status:     StatusPending,
			AddedAt:    time.Now().Unix(),
		}
		s.entries[rel] = entry
		added = append(added, *entry)
	}
	s.saveLocked()
	return added, nil
}

// Remove deletes entries from the list. A running entry is protected so a
// background process is never silently orphaned.
func (s *Service) Remove(paths []string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, raw := range paths {
		rel, _, err := s.normalize(raw)
		if err != nil {
			return err
		}
		entry, ok := s.entries[rel]
		if !ok {
			continue
		}
		if entry.Status == StatusRunning {
			return ErrRunning
		}
		delete(s.entries, rel)
	}
	s.saveLocked()
	return nil
}

// Queue marks selected entries for the worker. Other pending entries stay
// untouched, so the Repair button only repairs the user's current selection.
func (s *Service) Queue(paths []string) (int, error) {
	if s == nil {
		return 0, nil
	}
	if !s.Enabled() {
		return 0, ErrNotAvailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	queued := 0
	for _, raw := range paths {
		rel, _, err := s.normalize(raw)
		if err != nil {
			return queued, err
		}
		entry, ok := s.entries[rel]
		if !ok {
			continue
		}
		if entry.Status == StatusRunning {
			continue
		}
		entry.Status = StatusQueued
		entry.Error = ""
		entry.StartedAt = 0
		entry.FinishedAt = 0
		queued++
	}
	if queued == 0 {
		return 0, ErrNotFound
	}
	s.saveLocked()
	s.signal()
	return queued, nil
}

func (s *Service) worker() {
	for {
		entry := s.nextQueued()
		if entry == nil {
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
				continue
			}
		}
		s.repairEntry(entry)
	}
}

func (s *Service) nextQueued() *Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var selected *Entry
	for _, entry := range s.entries {
		if entry.Status != StatusQueued {
			continue
		}
		if selected == nil || entry.AddedAt < selected.AddedAt {
			selected = entry
		}
	}
	if selected == nil {
		return nil
	}
	selected.Status = StatusRunning
	selected.StartedAt = time.Now().Unix()
	selected.FinishedAt = 0
	selected.Error = ""
	s.saveLocked()
	copy := *selected
	return &copy
}

func (s *Service) repairEntry(entry *Entry) {
	input, err := s.pathSvc.ValidatePath(entry.Path)
	if err == nil {
		var info os.FileInfo
		info, err = os.Stat(input)
		if err == nil && info.IsDir() {
			err = errors.New("不是文件")
		}
	}
	if err != nil {
		s.finish(entry.Path, StatusFailed, err.Error())
		return
	}

	output, outputFull, err := s.prepareOutput(entry.Path, input)
	if err != nil {
		s.finish(entry.Path, StatusFailed, err.Error())
		return
	}
	if err := s.repairer.RepairFile(s.ctx, input, outputFull); err != nil {
		_ = os.Remove(outputFull) // do not leave a half-written repair output
		if s.ctx.Err() != nil {
			s.finish(entry.Path, StatusPending, "服务停止，任务已重置")
			return
		}
		s.finish(entry.Path, StatusFailed, err.Error())
		return
	}
	s.mu.Lock()
	if current, ok := s.entries[entry.Path]; ok {
		current.Status = StatusDone
		current.OutputPath = filepath.ToSlash(output)
		current.Error = ""
		current.FinishedAt = time.Now().Unix()
	}
	s.saveLocked()
	s.mu.Unlock()
	log.Infof("批量修复完成：%s -> %s", entry.Path, output)
}

func (s *Service) finish(path string, status Status, message string) {
	s.mu.Lock()
	if entry, ok := s.entries[path]; ok {
		entry.Status = status
		entry.Error = message
		entry.FinishedAt = time.Now().Unix()
	}
	s.saveLocked()
	s.mu.Unlock()
	log.Warnf("批量修复失败：%s：%s", path, message)
}

func (s *Service) prepareOutput(entryPath, input string) (relative, full string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	full, err = s.nextOutputPath(input)
	if err != nil {
		return "", "", err
	}
	relative, err = s.pathSvc.GetRelativePath(full)
	if err != nil {
		return "", "", err
	}
	if entry, ok := s.entries[entryPath]; ok {
		entry.OutputPath = filepath.ToSlash(relative)
	}
	return filepath.ToSlash(relative), full, nil
}

func (s *Service) normalize(raw string) (relative, full string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", errors.New("文件路径为空")
	}
	full, err = s.pathSvc.ValidatePath(raw)
	if err != nil {
		return "", "", err
	}
	rel, err := s.pathSvc.GetRelativePath(full)
	if err != nil {
		return "", "", err
	}
	return filepath.ToSlash(rel), full, nil
}

func (s *Service) nextOutputPath(input string) (string, error) {
	if _, err := os.Stat(input); err != nil {
		return "", err
	}
	ext := filepath.Ext(input)
	stem := strings.TrimSuffix(input, ext)
	candidate := stem + "_fix" + ext
	for i := 1; ; i++ {
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
		candidate = fmt.Sprintf("%s_fix_%d%s", stem, i, ext)
	}
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) load() {
	data, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		log.Warnf("读取修复列表失败：%v", err)
		return
	}
	var state diskState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Warnf("解析修复列表失败：%v", err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range state.Entries {
		if entry == nil || entry.Path == "" {
			continue
		}
		if entry.Status == StatusRunning {
			entry.Status = StatusPending
			entry.Error = "服务重启，任务已重置"
			entry.StartedAt = 0
		}
		s.entries[entry.Path] = entry
	}
}

func (s *Service) save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveLocked()
}

func (s *Service) saveLocked() {
	if s == nil || s.file == "" {
		return
	}
	entries := make([]*Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		entries = append(entries, entry)
	}
	sortEntriesPtr(entries)
	state := diskState{Entries: entries}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		log.Warnf("序列化修复列表失败：%v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.file), 0o755); err != nil {
		log.Warnf("创建修复列表目录失败：%v", err)
		return
	}
	tmp := s.file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Warnf("写入修复列表失败：%v", err)
		return
	}
	if err := os.Rename(tmp, s.file); err != nil {
		_ = os.Remove(tmp)
		log.Warnf("替换修复列表失败：%v", err)
	}
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].AddedAt < entries[j].AddedAt })
}

func sortEntriesPtr(entries []*Entry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].AddedAt < entries[j].AddedAt })
}
