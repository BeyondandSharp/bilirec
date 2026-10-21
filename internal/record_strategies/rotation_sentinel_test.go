// SPDX-License-Identifier: GPL-3.0-only

package record_strategies

import (
	"errors"
	"testing"
)

func TestErrRotationRequestedSentinel(t *testing.T) {
	if !errors.Is(ErrRotationRequested, ErrRotationRequested) {
		t.Fatal("sentinel must match itself")
	}
}

func TestFlvStrategyRotationRequestedCarriesHeaders(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewFlvStrategy(10000)
	defer strategy.Close()

	video := []byte("video-header-tag")
	audio := []byte("audio-header-tag")
	strategy.observeHeaders(video, audio)

	result := strategy.HandleErr(ErrRotationRequested)
	if result.Action != ErrActionRotate {
		t.Fatalf("action = %v, want rotate", result.Action)
	}
	if result.State == nil {
		t.Fatal("expected rotation state with headers")
	}
	if got := string(result.State.Data[flvStateVideoHdr]); got != string(video) {
		t.Fatalf("video header = %q, want %q", got, video)
	}
	if got := string(result.State.Data[flvStateAudioHdr]); got != string(audio) {
		t.Fatalf("audio header = %q, want %q", got, audio)
	}
}

func TestFlvStrategyRotationRequestedWithoutHeaders(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewFlvStrategy(10000)
	defer strategy.Close()

	result := strategy.HandleErr(ErrRotationRequested)
	if result.Action != ErrActionRotate {
		t.Fatalf("action = %v, want rotate", result.Action)
	}
	if result.State == nil {
		t.Fatal("expected empty rotation state")
	}
	if len(result.State.Data) != 0 {
		t.Fatalf("expected no carried headers, got %v", result.State.Data)
	}
}

func TestFlvStrategyOtherErrorsStillAbort(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewFlvStrategy(10000)
	defer strategy.Close()

	if got := strategy.HandleErr(errors.New("boom")).Action; got != ErrActionAbort {
		t.Fatalf("action = %v, want abort", got)
	}
}

func TestHlsTsStrategyRotationRequested(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewHlsTsStrategy(10000)
	defer strategy.Close()

	result := strategy.HandleErr(ErrRotationRequested)
	if result.Action != ErrActionRotate {
		t.Fatalf("action = %v, want rotate", result.Action)
	}
	if result.State != nil {
		t.Fatalf("TS rotation needs no state, got %+v", result.State)
	}
	if got := strategy.HandleErr(errors.New("boom")).Action; got != ErrActionAbort {
		t.Fatalf("other errors must abort, got %v", got)
	}
}

func TestHlsFmp4StrategyRotationRequestedCarriesInit(t *testing.T) {
	ensureBenchmarkConfig()
	strategy := NewHlsFmp4Strategy(10000)
	defer strategy.Close()

	initSegment := []byte("init-segment")
	strategy.lastInit = append([]byte(nil), initSegment...)
	strategy.bases[1] = 123

	result := strategy.HandleErr(ErrRotationRequested)
	if result.Action != ErrActionRotate {
		t.Fatalf("action = %v, want rotate", result.Action)
	}
	if result.State == nil || string(result.State.Data[fmp4StatePendingInit]) != string(initSegment) {
		t.Fatalf("pending init not carried: %+v", result.State)
	}
	if len(strategy.bases) != 0 {
		t.Fatalf("fMP4 bases should reset on rotation: %v", strategy.bases)
	}
}
