// SPDX-License-Identifier: GPL-3.0-or-later

package processors

import (
	"context"

	"github.com/bilirec/bilirec/pkg/flv"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pipeline"
)

// ErrScriptTagSplit / ErrAnnexBSplit are re-exported so recorder strategies can
// match them without importing pkg/flv directly.
var (
	ErrScriptTagSplit = flv.ErrScriptTagSplit
	ErrAnnexBSplit    = flv.ErrAnnexBSplit
)

type flvSplitDetectorProcessor struct {
	name     string
	detector interface {
		Detect([]byte) error
		Reset()
	}
}

func (p *flvSplitDetectorProcessor) Open(ctx context.Context, log logger.Logger) error {
	p.detector.Reset()
	return nil
}

func (p *flvSplitDetectorProcessor) Process(ctx context.Context, log logger.Logger, data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}
	if err := p.detector.Detect(data); err != nil {
		log.Infof("%s 触发管道轮转：%v", p.name, err)
		return data, err
	}
	return data, nil
}

func (p *flvSplitDetectorProcessor) Close() error { return nil }

// NewFlvScriptTagSplitDetector signals ErrScriptTagSplit when a Script tag
// appears after the first media data tag (docs/FEATURES-V2.md §5.2).
func NewFlvScriptTagSplitDetector() *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"flv-script-tag-split-detector",
		&flvSplitDetectorProcessor{
			name:     "Script 标签切段",
			detector: flv.NewScriptTagSplitDetector(),
		},
	)
}

// NewFlvAnnexBSplitDetector signals ErrAnnexBSplit when Annex-B start codes are
// detected in video NALU payloads (docs/FEATURES-V2.md §5.2).
func NewFlvAnnexBSplitDetector() *pipeline.ProcessorInfo[[]byte] {
	return pipeline.NewProcessorInfo(
		"flv-annexb-split-detector",
		&flvSplitDetectorProcessor{
			name:     "Annex-B 切段",
			detector: flv.NewAnnexBDetector(),
		},
	)
}
