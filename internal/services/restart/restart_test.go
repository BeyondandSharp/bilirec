package restart

import (
	"errors"
	"testing"
	"time"
)

func TestRequestOnceAndWatch(t *testing.T) {
	ResetForTest()
	t.Cleanup(ResetForTest)

	if err := Request(); !errors.Is(err, ErrDisabled) {
		t.Fatalf("disabled request err = %v", err)
	}

	Enable()
	stopped := make(chan struct{}, 1)
	Watch(func() error {
		stopped <- struct{}{}
		return nil
	})

	if err := Request(); err != nil {
		t.Fatal(err)
	}
	if err := Request(); !errors.Is(err, ErrAlreadyRequested) {
		t.Fatalf("second request err = %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Watch did not invoke stop")
	}
}
