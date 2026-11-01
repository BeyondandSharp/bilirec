package flv

import "encoding/binary"

// FindRotationBoundary locates a tag start suitable for splitting an FLV
// stream. BililiveRecorder groups tags so a new data group starts at a video
// keyframe; splitting at an arbitrary byte boundary can leave the next file
// starting with a non-keyframe, which some players cannot decode.
//
// The returned offset points at the tag header (the preceding PrevTagSize is
// still in the shared fixer's buffer). allowAudioFallback is used for
// audio-only streams, where no video keyframe exists.
func FindRotationBoundary(data []byte, allowAudioFallback bool) int {
	for offset := 0; offset+TagHeaderSize+PrevTagSizeBytes <= len(data); offset++ {
		tagType := data[offset]
		if tagType != TagTypeVideo && tagType != TagTypeAudio {
			continue
		}
		dataSize := int(data[offset+1])<<16 | int(data[offset+2])<<8 | int(data[offset+3])
		if dataSize <= 0 || dataSize > MaxTagDataSize {
			continue
		}
		tagEnd := offset + TagHeaderSize + dataSize
		totalEnd := tagEnd + PrevTagSizeBytes
		if totalEnd > len(data) {
			continue
		}
		// A real tag's trailing PreviousTagSize must match its header+body size;
		// this filters most tag-looking bytes inside payload data.
		if binary.BigEndian.Uint32(data[tagEnd:totalEnd]) != uint32(TagHeaderSize+dataSize) {
			continue
		}
		tagData := data[offset+TagHeaderSize : tagEnd]
		switch tagType {
		case TagTypeVideo:
			_, isHeader, isKeyframe := ClassifyVideoTag(tagData)
			if isKeyframe && !isHeader {
				return offset
			}
		case TagTypeAudio:
			if allowAudioFallback {
				return offset
			}
		}
	}
	return -1
}
