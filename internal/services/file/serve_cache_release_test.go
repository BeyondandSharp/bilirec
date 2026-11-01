package file

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBeginServeCacheRelease_DropDisabledCreatesNoState(t *testing.T) {
	withServeCacheReadOnly(t, false, 60)
	var drops atomic.Int32
	withTestDropPageCache(t, func(string) error {
		drops.Add(1)
		return nil
	})
	s := newServiceForServeCacheTest()

	done := s.BeginServeCacheRelease("/tmp/video.mp4")
	done()
	done()

	if drops.Load() != 0 {
		t.Fatalf("expected no drop when DROP_FILE_PAGE_CACHE=false, got %d", drops.Load())
	}
	s.serveCacheMu.Lock()
	defer s.serveCacheMu.Unlock()
	if len(s.serveCache) != 0 {
		t.Fatalf("expected disabled mode to create no tracking state, got %d entries", len(s.serveCache))
	}
}

func TestBeginServeCacheRelease_ReleasesTwiceThenDeletesSession(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	withTestDropPageCache(t, func(string) error {
		drops.Add(1)
		return nil
	})
	s := newServiceForServeCacheTest()

	path := "/tmp/video.mp4"
	done := s.BeginServeCacheRelease(path)
	done()
	sess, gen := stopServeCacheTimer(t, s, path)
	runBothServeCacheReleases(s, path, sess, gen)
	waitForSessionDeleted(t, s, path)
	if drops.Load() != 2 {
		t.Fatalf("expected exactly two releases, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_NewRequestResetsTwoStageRelease(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	withTestDropPageCache(t, func(string) error {
		drops.Add(1)
		return nil
	})
	s := newServiceForServeCacheTest()

	path := "/tmp/video.mp4"
	done1 := s.BeginServeCacheRelease(path)
	done1()
	sess, gen := stopServeCacheTimer(t, s, path)
	s.runServeCacheRelease(path, sess, gen, 1)
	firstGeneration := gen

	done2 := s.BeginServeCacheRelease(path)
	done2()

	s.serveCacheMu.Lock()
	active := s.serveCache[path]
	if active.generation <= firstGeneration || active.attempts != 0 || active.timer == nil {
		s.serveCacheMu.Unlock()
		t.Fatalf("expected new request to reset release stages: generation=%d attempts=%d timer=%v", active.generation, active.attempts, active.timer != nil)
	}
	s.serveCacheMu.Unlock()

	sess, gen = stopServeCacheTimer(t, s, path)
	runBothServeCacheReleases(s, path, sess, gen)
	waitForSessionDeleted(t, s, path)
	if drops.Load() != 3 {
		t.Fatalf("expected old attempt 1 plus two reset attempts, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_StaleTimerCannotDeleteNewGeneration(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	withTestDropPageCache(t, func(string) error {
		drops.Add(1)
		return nil
	})
	s := newServiceForServeCacheTest()

	path := "/tmp/video.mp4"
	done1 := s.BeginServeCacheRelease(path)
	done1()

	s.serveCacheMu.Lock()
	oldSession := s.serveCache[path]
	oldGeneration := oldSession.generation
	oldSession.timer.Stop()
	oldSession.timer = nil
	s.serveCacheMu.Unlock()

	done2 := s.BeginServeCacheRelease(path)
	done2()
	s.runServeCacheRelease(path, oldSession, oldGeneration, 1)

	s.serveCacheMu.Lock()
	active := s.serveCache[path]
	if active.generation == oldGeneration || active.timer == nil {
		s.serveCacheMu.Unlock()
		t.Fatal("stale callback changed or deleted the new generation")
	}
	s.serveCacheMu.Unlock()
	if drops.Load() != 0 {
		t.Fatalf("expected stale callback not to release cache, got %d releases", drops.Load())
	}

	sess, gen := stopServeCacheTimer(t, s, path)
	runBothServeCacheReleases(s, path, sess, gen)
	waitForSessionDeleted(t, s, path)
}

func TestBeginServeCacheRelease_RequestDuringAttemptInvalidatesOldCallback(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	firstStarted := make(chan struct{})
	unblockFirst := make(chan struct{})
	withTestDropPageCache(t, func(string) error {
		if drops.Add(1) == 1 {
			close(firstStarted)
			<-unblockFirst
		}
		return nil
	})
	s := newServiceForServeCacheTest()

	path := "/tmp/video.mp4"
	done1 := s.BeginServeCacheRelease(path)
	done1()
	sess, gen := stopServeCacheTimer(t, s, path)
	releaseDone := make(chan struct{})
	go func() {
		s.runServeCacheRelease(path, sess, gen, 1)
		close(releaseDone)
	}()
	waitForSignal(t, firstStarted, "first release to start")

	done2 := s.BeginServeCacheRelease(path)
	done2()
	close(unblockFirst)
	waitForSignal(t, releaseDone, "old generation in-flight release")

	sess, gen = stopServeCacheTimer(t, s, path)
	runBothServeCacheReleases(s, path, sess, gen)
	waitForSessionDeleted(t, s, path)
	if drops.Load() != 3 {
		t.Fatalf("expected in-flight old release plus two new releases, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_DoneIsIdempotent(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	withTestDropPageCache(t, func(string) error { return nil })
	s := newServiceForServeCacheTest()

	path := "/tmp/video.mp4"
	done := s.BeginServeCacheRelease(path)
	done()
	done()

	s.serveCacheMu.Lock()
	refCount := s.serveCache[path].refCount
	s.serveCacheMu.Unlock()
	if refCount != 0 {
		t.Fatalf("expected duplicate done to leave refCount at zero, got %d", refCount)
	}
	sess, gen := stopServeCacheTimer(t, s, path)
	runBothServeCacheReleases(s, path, sess, gen)
}

func TestBeginServeCacheRelease_SlowDropDoesNotBlockOtherPath(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	firstStarted := make(chan struct{})
	unblockFirst := make(chan struct{})
	firstFinished := make(chan struct{})
	withTestDropPageCache(t, func(path string) error {
		if path == "/tmp/slow.mp4" {
			close(firstStarted)
			<-unblockFirst
			close(firstFinished)
		}
		return nil
	})
	s := newServiceForServeCacheTest()

	slowPath := "/tmp/slow.mp4"
	doneSlow := s.BeginServeCacheRelease(slowPath)
	doneSlow()
	slowSession, slowGeneration := stopServeCacheTimer(t, s, slowPath)
	go s.runServeCacheRelease(slowPath, slowSession, slowGeneration, 1)
	waitForSignal(t, firstStarted, "slow release to start")

	otherDone := make(chan struct{})
	go func() {
		done := s.BeginServeCacheRelease("/tmp/other.mp4")
		done()
		close(otherDone)
	}()
	waitForSignal(t, otherDone, "Begin/done on another path")

	close(unblockFirst)
	waitForSignal(t, firstFinished, "slow release to finish")
	s.stopServeCacheRelease()
}

func TestStopServeCacheRelease_DuringFirstReleaseDoesNotScheduleFinal(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	firstStarted := make(chan struct{})
	unblockFirst := make(chan struct{})
	firstFinished := make(chan struct{})
	withTestDropPageCache(t, func(string) error {
		drops.Add(1)
		close(firstStarted)
		<-unblockFirst
		close(firstFinished)
		return nil
	})
	s := newServiceForServeCacheTest()

	path := "/tmp/video.mp4"
	done := s.BeginServeCacheRelease(path)
	done()
	sess, gen := stopServeCacheTimer(t, s, path)
	go s.runServeCacheRelease(path, sess, gen, 1)
	waitForSignal(t, firstStarted, "first release to start")
	s.stopServeCacheRelease()
	close(unblockFirst)
	waitForSignal(t, firstFinished, "first release to finish")

	if drops.Load() != 1 {
		t.Fatalf("expected shutdown to prevent final release, got %d releases", drops.Load())
	}
	s.serveCacheMu.Lock()
	defer s.serveCacheMu.Unlock()
	if len(s.serveCache) != 0 {
		t.Fatalf("expected shutdown to remove all sessions, got %d", len(s.serveCache))
	}
}

func TestStopServeCacheRelease_CancelsPendingTimer(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	withTestDropPageCache(t, func(string) error {
		drops.Add(1)
		return nil
	})
	s := newServiceForServeCacheTest()

	done := s.BeginServeCacheRelease("/tmp/video.mp4")
	done()
	s.stopServeCacheRelease()

	if drops.Load() != 0 {
		t.Fatalf("expected shutdown to cancel pending timer, got %d releases", drops.Load())
	}
}

func TestServeCacheFinalReleaseDelayIsFiveMinutes(t *testing.T) {
	s := &Service{}
	if got := s.serveCacheFinalDelay(); got != 5*time.Minute {
		t.Fatalf("expected final release delay 5m, got %s", got)
	}
}

func TestBeginServeCacheRelease_NoDropWhileActive(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	withTestDropPageCache(t, func(string) error {
		drops.Add(1)
		return nil
	})
	s := newServiceForServeCacheTest()

	path := "/tmp/video.mp4"
	done1 := s.BeginServeCacheRelease(path)
	done2 := s.BeginServeCacheRelease(path)
	done1()
	if drops.Load() != 0 {
		t.Fatal("expected no release while second serve is active")
	}

	done2()
	sess, gen := stopServeCacheTimer(t, s, path)
	runBothServeCacheReleases(s, path, sess, gen)
	if drops.Load() != 2 {
		t.Fatalf("expected two releases after all serves end, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_ConcurrentBegin(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	withTestDropPageCache(t, func(string) error {
		drops.Add(1)
		return nil
	})
	s := newServiceForServeCacheTest()

	path := "/tmp/video.mp4"
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done := s.BeginServeCacheRelease(path)
			time.Sleep(5 * time.Millisecond)
			done()
		}()
	}
	wg.Wait()

	sess, gen := stopServeCacheTimer(t, s, path)
	runBothServeCacheReleases(s, path, sess, gen)
	if drops.Load() != 2 {
		t.Fatalf("expected exactly two releases after concurrent serves, got %d", drops.Load())
	}
}

func withServeCacheReadOnly(t *testing.T, drop bool, coldSecs int) {
	bootServeCacheReadOnly(t, drop, coldSecs)
}

func newServiceForServeCacheTest() *Service {
	return &Service{
		serveCache: make(map[string]*serveCacheSession),
	}
}

func withTestDropPageCache(t *testing.T, fn func(string) error) {
	t.Helper()
	prev := dropFilePageCache
	dropFilePageCache = fn
	t.Cleanup(func() { dropFilePageCache = prev })
}

func stopServeCacheTimer(t *testing.T, s *Service, path string) (*serveCacheSession, uint64) {
	t.Helper()
	s.serveCacheMu.Lock()
	sess := s.serveCache[path]
	if sess == nil {
		s.serveCacheMu.Unlock()
		t.Fatalf("no serve-cache session for %q", path)
	}
	if sess.timer != nil {
		sess.timer.Stop()
		sess.timer = nil
	}
	gen := sess.generation
	s.serveCacheMu.Unlock()
	return sess, gen
}

func runBothServeCacheReleases(s *Service, path string, sess *serveCacheSession, generation uint64) {
	s.runServeCacheRelease(path, sess, generation, 1)
	s.runServeCacheRelease(path, sess, generation, 2)
}

func waitForSignal(t *testing.T, ch <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForSessionDeleted(t *testing.T, s *Service, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.serveCacheMu.Lock()
		_, exists := s.serveCache[path]
		s.serveCacheMu.Unlock()
		if !exists {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for session deletion: %s", path)
}
