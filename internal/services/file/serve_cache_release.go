package file

import (
	"sync"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/filecache"
)

const serveCacheFinalReleaseDelay = 5 * time.Minute

// BeginServeCacheRelease pairs with the returned done callback around SendFile.
// When DROP_FILE_PAGE_CACHE is enabled, drops page cache after the last active
// serve ends and ServeCacheIdleReleaseSecs of idle (zero config uses default 60s),
// then performs one final compensating release five minutes later.
func (s *Service) BeginServeCacheRelease(fullPath string) (done func()) {
	if config.ReadOnly == nil || !config.ReadOnly.DropFilePageCache() {
		return func() {}
	}
	if s.serveCacheIdleDelay() <= 0 {
		return func() {}
	}

	s.serveCacheMu.Lock()
	if s.serveCacheStopped {
		s.serveCacheMu.Unlock()
		return func() {}
	}

	sess := s.serveCache[fullPath]
	if sess == nil {
		sess = &serveCacheSession{}
		s.serveCache[fullPath] = sess
	}
	if sess.timer != nil {
		sess.timer.Stop()
		sess.timer = nil
		log.Tracef("serve-cache-release: 取消待释放定时器 path=%s", fullPath)
	}
	sess.generation++
	sess.attempts = 0
	sess.refCount++
	refCount := sess.refCount
	s.serveCacheMu.Unlock()
	log.Tracef("serve-cache-release: 开始服务 path=%s refCount=%d", fullPath, refCount)

	var once sync.Once
	return func() {
		once.Do(func() {
			s.endServeCacheRelease(fullPath, sess)
		})
	}
}

type serveCacheSession struct {
	refCount   int
	timer      *time.Timer
	generation uint64
	attempts   int
}

func (s *Service) endServeCacheRelease(fullPath string, sess *serveCacheSession) {
	idleDelay := s.serveCacheIdleDelay()
	if idleDelay <= 0 {
		return
	}

	s.serveCacheMu.Lock()
	active := s.serveCache[fullPath]
	if s.serveCacheStopped || active != sess || sess.refCount <= 0 {
		s.serveCacheMu.Unlock()
		return
	}
	sess.refCount--
	refCount := sess.refCount
	if refCount > 0 {
		s.serveCacheMu.Unlock()
		log.Tracef("serve-cache-release: 服务结束 path=%s refCount=%d 仍有活动服务", fullPath, refCount)
		return
	}

	generation := sess.generation
	log.Tracef("serve-cache-release: 进入空闲释放窗口 path=%s idleDelay=%s generation=%d", fullPath, idleDelay, generation)
	s.scheduleServeCacheReleaseLocked(fullPath, sess, generation, 1, idleDelay)
	s.serveCacheMu.Unlock()
}

func (s *Service) runServeCacheRelease(fullPath string, sess *serveCacheSession, generation uint64, attempt int) {
	s.serveCacheMu.Lock()
	active := s.serveCache[fullPath]
	if s.serveCacheStopped || active != sess || sess.generation != generation || sess.refCount > 0 || sess.attempts != attempt-1 {
		s.serveCacheMu.Unlock()
		return
	}
	sess.timer = nil
	sess.attempts = attempt
	s.serveCacheMu.Unlock()

	s.dropPageCache(fullPath, attempt)

	s.serveCacheMu.Lock()
	defer s.serveCacheMu.Unlock()
	active = s.serveCache[fullPath]
	if s.serveCacheStopped || active != sess || sess.generation != generation || sess.refCount > 0 || sess.attempts != attempt {
		return
	}
	if attempt == 1 {
		s.scheduleServeCacheReleaseLocked(fullPath, sess, generation, 2, s.serveCacheFinalDelay())
		return
	}
	delete(s.serveCache, fullPath)
}

func (s *Service) scheduleServeCacheReleaseLocked(fullPath string, sess *serveCacheSession, generation uint64, attempt int, delay time.Duration) {
	sess.timer = time.AfterFunc(delay, func() {
		s.runServeCacheRelease(fullPath, sess, generation, attempt)
	})
}

func (s *Service) dropPageCache(fullPath string, attempt int) {
	fn := s.dropPageCacheFn
	if fn == nil {
		fn = filecache.DropFilePageCache
	}
	if err := fn(fullPath); err != nil {
		log.Debugf("serve-cache-release: 释放 page cache 失败 path=%s attempt=%d err=%v", fullPath, attempt, err)
	} else {
		log.Debugf("serve-cache-release: 释放 page cache 成功 path=%s attempt=%d", fullPath, attempt)
	}
}

func (s *Service) serveCacheIdleDelay() time.Duration {
	if s.serveCacheIdleDelayOverride > 0 {
		return s.serveCacheIdleDelayOverride
	}
	if config.ReadOnly == nil {
		return 0
	}
	secs := config.ReadOnly.ServeCacheIdleReleaseSecs()
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func (s *Service) serveCacheFinalDelay() time.Duration {
	if s.serveCacheIdleDelayOverride > 0 {
		return s.serveCacheIdleDelayOverride
	}
	return serveCacheFinalReleaseDelay
}

func (s *Service) stopServeCacheRelease() {
	s.serveCacheMu.Lock()
	defer s.serveCacheMu.Unlock()
	s.serveCacheStopped = true
	for path, sess := range s.serveCache {
		if sess.timer != nil {
			sess.timer.Stop()
			log.Debugf("serve-cache-release: 停止时取消待释放定时器 path=%s refCount=%d", path, sess.refCount)
		}
		delete(s.serveCache, path)
	}
}
