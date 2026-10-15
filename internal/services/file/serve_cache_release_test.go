package file

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
)

func TestBeginServeCacheRelease_DropDisabledCreatesNoState(t *testing.T) {
	withServeCacheReadOnly(t, false, 60)
	var drops atomic.Int32
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		return nil
	}, 10*time.Millisecond)

	done := s.BeginServeCacheRelease("/tmp/video.mp4")
	done()
	done()
	time.Sleep(30 * time.Millisecond)

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
	dropped := make(chan struct{}, 3)
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		dropped <- struct{}{}
		return nil
	}, 15*time.Millisecond)

	path := "/tmp/video.mp4"
	done := s.BeginServeCacheRelease(path)
	done()

	waitForSignal(t, dropped, "first cache release")
	waitForSignal(t, dropped, "final cache release")
	waitForSessionDeleted(t, s, path)
	time.Sleep(30 * time.Millisecond)
	if drops.Load() != 2 {
		t.Fatalf("expected exactly two releases, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_NewRequestResetsTwoStageRelease(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	dropped := make(chan struct{}, 4)
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		dropped <- struct{}{}
		return nil
	}, 25*time.Millisecond)

	path := "/tmp/video.mp4"
	done1 := s.BeginServeCacheRelease(path)
	done1()
	waitForSignal(t, dropped, "first generation initial release")

	s.serveCacheMu.Lock()
	firstGeneration := s.serveCache[path].generation
	s.serveCacheMu.Unlock()

	done2 := s.BeginServeCacheRelease(path)
	done2()

	s.serveCacheMu.Lock()
	sess := s.serveCache[path]
	if sess.generation <= firstGeneration || sess.attempts != 0 || sess.timer == nil {
		s.serveCacheMu.Unlock()
		t.Fatalf("expected new request to reset release stages: generation=%d attempts=%d timer=%v", sess.generation, sess.attempts, sess.timer != nil)
	}
	s.serveCacheMu.Unlock()

	waitForSignal(t, dropped, "new generation initial release")
	waitForSignal(t, dropped, "new generation final release")
	waitForSessionDeleted(t, s, path)
	time.Sleep(40 * time.Millisecond)
	if drops.Load() != 3 {
		t.Fatalf("expected old attempt 1 plus two reset attempts, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_StaleTimerCannotDeleteNewGeneration(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	dropped := make(chan struct{}, 2)
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		dropped <- struct{}{}
		return nil
	}, 40*time.Millisecond)

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
	if active != oldSession || active.generation == oldGeneration || active.timer == nil {
		s.serveCacheMu.Unlock()
		t.Fatal("stale callback changed or deleted the new generation")
	}
	s.serveCacheMu.Unlock()
	if drops.Load() != 0 {
		t.Fatalf("expected stale callback not to release cache, got %d releases", drops.Load())
	}

	waitForSignal(t, dropped, "new generation initial release")
	waitForSignal(t, dropped, "new generation final release")
	waitForSessionDeleted(t, s, path)
}

func TestBeginServeCacheRelease_RequestDuringAttemptInvalidatesOldCallback(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	firstStarted := make(chan struct{})
	unblockFirst := make(chan struct{})
	dropped := make(chan struct{}, 3)
	s := newServiceForServeCacheTest(func(string) error {
		if drops.Add(1) == 1 {
			close(firstStarted)
			<-unblockFirst
		}
		dropped <- struct{}{}
		return nil
	}, 30*time.Millisecond)

	path := "/tmp/video.mp4"
	done1 := s.BeginServeCacheRelease(path)
	done1()
	waitForSignal(t, firstStarted, "first release to start")

	done2 := s.BeginServeCacheRelease(path)
	done2()
	close(unblockFirst)

	waitForSignal(t, dropped, "old generation in-flight release")
	waitForSignal(t, dropped, "new generation initial release")
	waitForSignal(t, dropped, "new generation final release")
	waitForSessionDeleted(t, s, path)
	if drops.Load() != 3 {
		t.Fatalf("expected in-flight old release plus two new releases, got %d", drops.Load())
	}
}

func TestBeginServeCacheRelease_DoneIsIdempotent(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	dropped := make(chan struct{}, 2)
	s := newServiceForServeCacheTest(func(string) error {
		dropped <- struct{}{}
		return nil
	}, 30*time.Millisecond)

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
	waitForSignal(t, dropped, "initial release")
	waitForSignal(t, dropped, "final release")
}

func TestBeginServeCacheRelease_SlowDropDoesNotBlockOtherPath(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	firstStarted := make(chan struct{})
	unblockFirst := make(chan struct{})
	firstFinished := make(chan struct{})
	s := newServiceForServeCacheTest(func(path string) error {
		if path == "/tmp/slow.mp4" {
			close(firstStarted)
			<-unblockFirst
			close(firstFinished)
		}
		return nil
	}, time.Second)

	slowPath := "/tmp/slow.mp4"
	doneSlow := s.BeginServeCacheRelease(slowPath)
	doneSlow()
	s.serveCacheMu.Lock()
	slowSession := s.serveCache[slowPath]
	slowGeneration := slowSession.generation
	slowSession.timer.Stop()
	slowSession.timer = nil
	s.serveCacheMu.Unlock()
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
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		close(firstStarted)
		<-unblockFirst
		close(firstFinished)
		return nil
	}, 20*time.Millisecond)

	path := "/tmp/video.mp4"
	done := s.BeginServeCacheRelease(path)
	done()
	waitForSignal(t, firstStarted, "first release to start")
	s.stopServeCacheRelease()
	close(unblockFirst)
	waitForSignal(t, firstFinished, "first release to finish")
	time.Sleep(60 * time.Millisecond)

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
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		return nil
	}, 40*time.Millisecond)

	done := s.BeginServeCacheRelease("/tmp/video.mp4")
	done()
	s.stopServeCacheRelease()
	time.Sleep(100 * time.Millisecond)

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
	dropped := make(chan struct{}, 2)
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		dropped <- struct{}{}
		return nil
	}, 20*time.Millisecond)

	path := "/tmp/video.mp4"
	done1 := s.BeginServeCacheRelease(path)
	done2 := s.BeginServeCacheRelease(path)
	done1()
	time.Sleep(50 * time.Millisecond)
	if drops.Load() != 0 {
		t.Fatal("expected no release while second serve is active")
	}

	done2()
	waitForSignal(t, dropped, "initial release after all serves end")
	waitForSignal(t, dropped, "final release after all serves end")
}

func TestBeginServeCacheRelease_ConcurrentBegin(t *testing.T) {
	withServeCacheReadOnly(t, true, 60)
	var drops atomic.Int32
	dropped := make(chan struct{}, 2)
	s := newServiceForServeCacheTest(func(string) error {
		drops.Add(1)
		dropped <- struct{}{}
		return nil
	}, 30*time.Millisecond)

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

	waitForSignal(t, dropped, "initial release after concurrent serves")
	waitForSignal(t, dropped, "final release after concurrent serves")
	if drops.Load() != 2 {
		t.Fatalf("expected exactly two releases after concurrent serves, got %d", drops.Load())
	}
}

func withServeCacheReadOnly(t *testing.T, drop bool, coldSecs int) {
	t.Helper()
	old := config.ReadOnly
	config.ReadOnly = config.NewGlobalReadOnlyForTest(drop, coldSecs)
	t.Cleanup(func() { config.ReadOnly = old })
}

func newServiceForServeCacheTest(dropFn func(string) error, idleDelay time.Duration) *Service {
	s := &Service{
		serveCache: make(map[string]*serveCacheSession),
	}
	if dropFn != nil {
		s.dropPageCacheFn = dropFn
	}
	if idleDelay > 0 {
		s.serveCacheIdleDelayOverride = idleDelay
	}
	return s
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
