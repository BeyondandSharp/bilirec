// SPDX-License-Identifier: GPL-3.0-or-later

package flv

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Errors raised by the split detectors (docs/FEATURES-V2.md §5.2). They map to
// a recording-file rotation with the current sequence headers carried over.
var (
	// ErrScriptTagSplit signals a Script (type 18) tag appearing after the
	// first media data tag.
	ErrScriptTagSplit = errors.New("检测到媒体流中的 Script 标签，按配置切段")
	// ErrAnnexBSplit signals Annex-B start codes in AVC/HEVC NALU payloads.
	ErrAnnexBSplit = errors.New("检测到 Annex-B 视频数据，按配置切段")
)

// tagWalker scans a raw FLV byte stream incrementally (chunks may split tags).
type tagWalker struct {
	pending       []byte
	headerChecked bool
}

// walk visits every complete tag in data+pending. It returns the first visit
// error and keeps the unterminated tail for the next call.
func (w *tagWalker) walk(data []byte, visit func(tagType byte, tagData []byte) error) error {
	var buf []byte
	if len(w.pending) > 0 {
		buf = make([]byte, 0, len(w.pending)+len(data))
		buf = append(buf, w.pending...)
		buf = append(buf, data...)
	} else {
		buf = data
	}

	if !w.headerChecked {
		switch {
		case len(buf) >= FlvHeaderSize+PrevTagSizeBytes && buf[0] == 'F' && buf[1] == 'L' && buf[2] == 'V':
			// Drop the FLV file header + PreviousTagSize0.
			buf = buf[FlvHeaderSize+PrevTagSizeBytes:]
			w.headerChecked = true
		case len(buf) < 3 && isFLVPrefix(buf):
			// Not enough bytes yet to tell whether this is an FLV file; wait.
			w.pending = append(w.pending[:0], buf...)
			return nil
		case len(buf) < FlvHeaderSize+PrevTagSizeBytes && buf[0] == 'F' && buf[1] == 'L' && buf[2] == 'V':
			// FLV header split across chunks; wait for the full preamble.
			w.pending = append(w.pending[:0], buf...)
			return nil
		default:
			// Mid-stream chunk without an FLV file header.
			w.headerChecked = true
		}
	}

	offset := 0

	for offset+TagHeaderSize+PrevTagSizeBytes <= len(buf) {
		tagType := buf[offset]
		dataSize := int(buf[offset+1])<<16 | int(buf[offset+2])<<8 | int(buf[offset+3])
		tagEnd := offset + TagHeaderSize + dataSize
		boundary := tagEnd + PrevTagSizeBytes
		if boundary > len(buf) {
			break
		}
		if err := visit(tagType, buf[offset+TagHeaderSize:tagEnd]); err != nil {
			w.pending = w.pending[:0]
			return err
		}
		offset = boundary
	}

	w.pending = append(w.pending[:0], buf[offset:]...)
	return nil
}

// ScriptTagSplitDetector raises ErrScriptTagSplit once when a Script tag
// appears after the first media data tag. Sequence-header tags (AVC/AAC config)
// do not count as media data, so an onMetaData tag before any media is fine.
type ScriptTagSplitDetector struct {
	walker   tagWalker
	sawMedia bool
	fired    bool
}

// NewScriptTagSplitDetector returns a ready-to-use detector.
func NewScriptTagSplitDetector() *ScriptTagSplitDetector {
	return &ScriptTagSplitDetector{}
}

// Reset clears the detector state (call when starting a new recording file).
func (d *ScriptTagSplitDetector) Reset() {
	d.walker.pending = nil
	d.sawMedia = false
	d.fired = false
}

// Detect scans one chunk. It returns ErrScriptTagSplit exactly once.
func (d *ScriptTagSplitDetector) Detect(data []byte) error {
	if d.fired {
		return nil
	}
	err := d.walker.walk(data, func(tagType byte, tagData []byte) error {
		switch tagType {
		case TagTypeScript:
			if d.sawMedia {
				d.fired = true
				return ErrScriptTagSplit
			}
		case TagTypeVideo:
			if _, isHeader, _ := ClassifyVideoTag(tagData); !isHeader {
				d.sawMedia = true
			}
		case TagTypeAudio:
			if !isAudioSequenceHeader(tagData) {
				d.sawMedia = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// isAudioSequenceHeader reports whether an audio tag payload is an AAC
// sequence header (AAC config), which does not count as media data.
func isAudioSequenceHeader(tagData []byte) bool {
	if len(tagData) < 2 {
		return false
	}
	return tagData[0]>>4 == 10 && tagData[1] == 0
}

// AnnexBDetector raises ErrAnnexBSplit once when a video media tag's NALU
// payload contains an Annex-B start code (00 00 01 / 00 00 00 01). Video
// sequence headers are ignored.
type AnnexBDetector struct {
	walker tagWalker
	fired  bool
}

// NewAnnexBDetector returns a ready-to-use detector.
func NewAnnexBDetector() *AnnexBDetector {
	return &AnnexBDetector{}
}

// Reset clears the detector state (call when starting a new recording file).
func (d *AnnexBDetector) Reset() {
	d.walker.pending = nil
	d.fired = false
}

// Detect scans one chunk. It returns ErrAnnexBSplit exactly once.
func (d *AnnexBDetector) Detect(data []byte) error {
	if d.fired {
		return nil
	}
	err := d.walker.walk(data, func(tagType byte, tagData []byte) error {
		if tagType != TagTypeVideo {
			return nil
		}
		if _, isHeader, _ := ClassifyVideoTag(tagData); isHeader {
			return nil
		}
		if len(tagData) <= 5 {
			return nil
		}
		payload := tagData[5:]
		// An explicit 4-byte Annex-B start code is unambiguous (as an AVCC
		// length prefix it would mean a 1-byte NALU). Otherwise fall back to
		// AVCC structural validation: a well-formed length-prefixed NALU
		// stream is not Annex-B.
		if bytes.HasPrefix(payload, []byte{0x00, 0x00, 0x00, 0x01}) || !validAVCCNALUStream(payload) {
			d.fired = true
			return ErrAnnexBSplit
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

// validAVCCNALUStream reports whether data is a well-formed sequence of
// 4-byte-length-prefixed NAL units (the AVCC/HEVC-in-FLV layout). Annex-B
// streams (00 00 01 / 00 00 00 01 start codes) fail this check, while genuine
// AVCC also fails only when the byte stream cannot be parsed as NALUs.
func validAVCCNALUStream(data []byte) bool {
	for len(data) > 0 {
		if len(data) < 4 {
			return false
		}
		size := int(binary.BigEndian.Uint32(data[:4]))
		if size <= 0 || size > len(data)-4 {
			return false
		}
		data = data[4+size:]
	}
	return true
}

// isFLVPrefix reports whether data is a prefix of the FLV signature "FLV".
func isFLVPrefix(data []byte) bool {
	const magic = "FLV"
	if len(data) > len(magic) {
		return false
	}
	return magic[:len(data)] == string(data)
}
