// SPDX-License-Identifier: GPL-3.0-only

package recorder

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/internal/services/repair"
	"github.com/bilirec/bilirec/pkg/pool"
)

func writeFakeRepairSidecar(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sidecars use /bin/sh")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "flvrepair")
	if err := os.WriteFile(p, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWrapFlvRepair(t *testing.T) {
	fake := writeFakeRepairSidecar(t)
	repairSvc := repair.NewService(&config.Config{
		FLVRepair:            "on",
		FLVRepairBin:         fake,
		FLVRepairMode:        "best-effort",
		FLVRepairMaxBufferMB: 16,
	})
	if !repairSvc.Enabled() {
		t.Fatal("expected repair service to be enabled with a resolvable binary")
	}
	chunkPool := pool.NewBucketedBytesPool(64 * 1024)
	svc := &Service{repair: repairSvc}
	ctx := context.Background()

	t.Run("flv sessions are wrapped", func(t *testing.T) {
		in := make(chan []byte, 4)
		out, ok := svc.wrapFlvRepair(ctx, 42, in, "flv", chunkPool, true)
		if !ok {
			t.Fatal("expected FLV session to be wrapped")
		}
		if out == in {
			t.Fatal("expected a new channel when repair is enabled")
		}

		payload := [][]byte{[]byte("flv-data-1"), []byte("flv-data-2")}
		go func() {
			for _, chunk := range payload {
				in <- chunk
			}
			close(in)
		}()
		var got []byte
		for buf := range out {
			got = append(got, buf...)
			chunkPool.Put(buf)
		}
		if want := bytes.Join(payload, nil); !bytes.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("hls sessions are not wrapped", func(t *testing.T) {
		in := make(chan []byte, 1)
		out, ok := svc.wrapFlvRepair(ctx, 42, in, "ts", chunkPool, true)
		if ok {
			t.Fatal("HLS/TS sessions must not be repaired")
		}
		if out != in {
			t.Fatal("expected the original channel for non-FLV sessions")
		}
	})

	t.Run("nil repair service", func(t *testing.T) {
		in := make(chan []byte, 1)
		out, ok := (&Service{}).wrapFlvRepair(ctx, 42, in, "flv", chunkPool, true)
		if ok || out != in {
			t.Fatal("expected the original channel without a repair service")
		}
	})

	t.Run("disabled repair service", func(t *testing.T) {
		disabled := repair.NewService(&config.Config{FLVRepair: "off", FLVRepairBin: fake})
		in := make(chan []byte, 1)
		out, ok := (&Service{repair: disabled}).wrapFlvRepair(ctx, 42, in, "flv", chunkPool, true)
		if ok || out != in {
			t.Fatal("expected the original channel when repair is disabled")
		}
	})
}

func TestWrapFlvRepairPassesWriteMetadata(t *testing.T) {
	argFile := filepath.Join(t.TempDir(), "sidecar-args")
	fake := writeFakeRepairSidecarScript(t, fmt.Sprintf("echo \"$@\" > %q\ncat", argFile))
	repairSvc := repair.NewService(&config.Config{
		FLVRepair:            "on",
		FLVRepairBin:         fake,
		FLVRepairMode:        "best-effort",
		FLVRepairMaxBufferMB: 16,
	})
	svc := &Service{repair: repairSvc}
	chunkPool := pool.NewBucketedBytesPool(64 * 1024)

	run := func(writeMetadata bool) string {
		t.Helper()
		in := make(chan []byte, 1)
		out, ok := svc.wrapFlvRepair(context.Background(), 7, in, "flv", chunkPool, writeMetadata)
		if !ok {
			t.Fatal("expected repair to be enabled")
		}
		in <- []byte("x")
		close(in)
		for range out {
		}
		data, err := os.ReadFile(argFile)
		if err != nil {
			t.Fatalf("sidecar args not recorded: %v", err)
		}
		return string(data)
	}

	if args := run(true); strings.Contains(args, "--write-metadata") {
		t.Fatalf("metadata keep must omit the flag, args=%q", args)
	}
	if args := run(false); !strings.Contains(args, "--write-metadata drop") {
		t.Fatalf("metadata drop must pass the flag, args=%q", args)
	}
}

func writeFakeRepairSidecarScript(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sidecars use /bin/sh")
	}
	p := filepath.Join(t.TempDir(), "flvrepair")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}
