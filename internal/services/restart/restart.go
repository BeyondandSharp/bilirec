// SPDX-License-Identifier: GPL-3.0-only

// Package restart coordinates an in-process graceful shutdown followed by a
// re-exec of the backend binary, so configuration changes can be applied
// without a Docker socket or manual container restart.
package restart

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
	"go.uber.org/fx"
)

var log = logger.Named("restart")

var (
	ErrDisabled         = errors.New("当前运行模式不支持重启")
	ErrAlreadyRequested = errors.New("重启请求已在进行中")
	current             atomic.Pointer[Coordinator]
	shutdownMu          sync.RWMutex
	shutdowner          fx.Shutdowner
)

// Coordinator tracks a single restart request for this process.
type Coordinator struct {
	enabled   atomic.Bool
	requested atomic.Bool
	done      chan struct{}
	once      sync.Once
}

func NewCoordinator() *Coordinator {
	return &Coordinator{done: make(chan struct{})}
}

// Enable marks this process as restartable. Called by cmd/backend.
func Enable() *Coordinator {
	c := NewCoordinator()
	c.enabled.Store(true)
	current.Store(c)
	return c
}

// Enabled reports whether an enabled coordinator is installed.
func Enabled() bool {
	c := current.Load()
	return c != nil && c.enabled.Load()
}

// Request records a restart request. Only the first request succeeds.
func Request() error {
	c := current.Load()
	if c == nil || !c.enabled.Load() {
		return ErrDisabled
	}
	if !c.requested.CompareAndSwap(false, true) {
		return ErrAlreadyRequested
	}
	c.once.Do(func() { close(c.done) })
	return nil
}

// Requested reports whether a restart has been accepted.
func Requested() bool {
	c := current.Load()
	return c != nil && c.requested.Load()
}

// Wait returns a channel closed when a restart request is accepted.
func Wait() <-chan struct{} {
	c := current.Load()
	if c == nil {
		return nil
	}
	return c.done
}

// SetShutdowner installs the fx shutdowner used to signal App.Run.
func SetShutdowner(s fx.Shutdowner) {
	shutdownMu.Lock()
	shutdowner = s
	shutdownMu.Unlock()
}

// Watch waits for a restart request, gives the HTTP handler time to flush its
// 202 response, then shuts the fx app down. stop is an optional test/custom
// override; when nil the registered fx.Shutdowner is used.
func Watch(stop func() error) {
	done := Wait()
	if done == nil {
		return
	}
	go func() {
		<-done
		time.Sleep(500 * time.Millisecond)
		if stop != nil {
			if err := stop(); err != nil {
				log.Errorf("优雅停止服务失败：%v", err)
			}
			return
		}
		shutdownMu.RLock()
		s := shutdowner
		shutdownMu.RUnlock()
		if s == nil {
			log.Error("重启请求已接受，但 shutdowner 未初始化")
			return
		}
		if err := s.Shutdown(); err != nil {
			log.Errorf("触发服务关闭失败：%v", err)
		}
	}()
}

// ResetForTest clears the process-global coordinator.
func ResetForTest() {
	current.Store(nil)
}
