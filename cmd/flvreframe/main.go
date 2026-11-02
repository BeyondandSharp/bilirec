// SPDX-License-Identifier: GPL-3.0-only

// Command flvreframe rebuilds FLV tag framing for files damaged by the fork's
// pre-0.6 split bug. It is fork-owned code, separate from the ported
// BililiveRecorder repair pipeline; see docs/FLV-REFRAME.md.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/bilirec/bilirec/internal/flvreframe"
)

func main() {
	var (
		input  = flag.String("input", "", "damaged FLV file to read")
		output = flag.String("output", "", "repaired FLV file to write")
		trim   = flag.Bool("trim-broken-video-gops", false, "drop broken video frames after each gap until the next keyframe (audio is kept)")
		keepTS = flag.Bool("keep-timestamps", false, "keep original timestamps instead of rebasing to the first media timestamp")
		resync = flag.Int64("resync-size", 0, "maximum bytes scanned for the next tag after a gap (0 = 16 MiB)")
		asJSON = flag.Bool("json", false, "print the repair summary as JSON")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: flvreframe --input <broken.flv> --output <fixed.flv> [options]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *input == "" || *output == "" {
		flag.Usage()
		os.Exit(2)
	}

	result, err := flvreframe.Repair(*input, *output, flvreframe.Options{
		TrimBrokenVideoGOPs: *trim,
		KeepTimestamps:      *keepTS,
		ResyncSize:          *resync,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "flvreframe: %v\n", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			fmt.Fprintf(os.Stderr, "flvreframe: encode summary: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("input:  %s (%d bytes)\n", *input, result.InputBytes)
	fmt.Printf("output: %s (%d bytes)\n", *output, result.OutputBytes)
	fmt.Printf("tags:   in=%d out=%d\n", result.TagsIn, result.TagsOut)
	fmt.Printf("gaps:   %d (%d bytes skipped), trailing=%d bytes, prev_size_mismatches=%d\n",
		len(result.Gaps), result.DroppedBytes-result.TrailingBytes, result.TrailingBytes, result.PrevSizeMismatches)
	if result.DroppedVideoTags > 0 {
		fmt.Printf("trim:   dropped %d broken video tags\n", result.DroppedVideoTags)
	}
	fmt.Printf("time:   first=%dms last=%dms\n", result.FirstTimestampMS, result.LastTimestampMS)
	for i, gap := range result.Gaps {
		fmt.Printf("gap %d: offset=%d bytes=%d prev_ts=%dms next_ts=%dms\n",
			i+1, gap.Offset, gap.Bytes, gap.PreviousTimestampMS, gap.NextTimestampMS)
	}
}
