// SPDX-License-Identifier: GPL-3.0-only

package flvmeta

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"
)

const (
	// MaxKeyframes and MinKeyframeIntervalMS match BililiveRecorder's
	// KeyframesScriptDataValue. Players read the keyframes arrays directly, so
	// keeping the same capacity/interval shape is important for compatibility.
	// The spacer array pads the arrays to 2*MaxKeyframes numbers; this also
	// keeps the metadata tag size fixed while recording.
	MaxKeyframes          = 6300
	MinKeyframeIntervalMS = 1900

	// MaxDataCount is kept as an alias for readers familiar with BLR naming.
	MaxDataCount = MaxKeyframes
)

// AppIdentity is written into the BLR-compatible "BililiveRecorder" metadata
// object. It identifies the recorder rather than the stream source.
const (
	RecordedBy   = "BeyondandSharp/bilirec"
	RecorderSite = "https://github.com/BeyondandSharp/bilirec"
)

// RecordingInfo carries the per-segment room/stream information that
// BililiveRecorder writes into onMetaData (Title/Artist/Comment and the
// BililiveRecorder object). Zero-value fields are omitted from the output.
type RecordingInfo struct {
	// Title is the live title at file open. StreamTitle defaults to Title.
	Title       string
	StreamTitle string
	// Artist overrides the generated "<anchor> (<roomId>)" value.
	Artist string
	// Comment overrides the generated multi-line description.
	Comment string

	Name           string // anchor name (BLR RoomInfo.Name)
	RoomID         int64
	ShortID        int64
	AreaNameParent string
	AreaNameChild  string

	StreamHost      string // hostname of the connected stream server
	StreamHostFull  string // connected server description (BLR StreamServers)
	RecorderVersion string
	RecordedBy      string
	StartTime       time.Time
}

func (info RecordingInfo) hasContent() bool {
	return info.RoomID != 0 || info.Title != "" || info.Name != "" || info.StreamHost != ""
}

func (info RecordingInfo) recordedBy() string {
	if info.RecordedBy != "" {
		return info.RecordedBy
	}
	return RecordedBy
}

func (info RecordingInfo) version() string {
	if info.RecorderVersion != "" {
		return info.RecorderVersion
	}
	return "dev"
}

func (info RecordingInfo) startTime() time.Time {
	if info.StartTime.IsZero() {
		return time.Now()
	}
	return info.StartTime
}

func (info RecordingInfo) streamTitle() string {
	if info.StreamTitle != "" {
		return info.StreamTitle
	}
	return info.Title
}

func (info RecordingInfo) artist() string {
	if info.Artist != "" {
		return info.Artist
	}
	return fmt.Sprintf("%s (%d)", info.Name, info.RoomID)
}

func (info RecordingInfo) comment() string {
	if info.Comment != "" {
		return info.Comment
	}
	return fmt.Sprintf(
		"%s 直播间 %d 的直播录像\n"+
			"主播名: %s\n"+
			"直播标题: %s\n"+
			"直播分区: %s·%s\n"+
			"录制时间: %s\n"+
			"直播服务器:\n"+
			"%s\n"+
			"\n"+
			"使用 %s 录制 %s\n"+
			"%s 版本: %s\n",
		info.recordedBy(), info.RoomID, info.Name, info.streamTitle(), info.AreaNameParent, info.AreaNameChild,
		info.startTime().Format(time.RFC3339Nano), info.StreamHostFull, info.recordedBy(), RecorderSite, info.recordedBy(), info.version(),
	)
}

// Keyframe is one entry of the onMetaData.keyframes index. Time is in
// milliseconds (serialized as seconds by the builder) and FilePosition is the
// absolute byte offset of the video tag header inside the output file.
type Keyframe struct {
	TimeMS       int64
	FilePosition int64
}

// IsOnMetaData reports whether payload is a script tag body containing a
// top-level onMetaData value. It is used to decide whether a script tag should
// be replaced by the generated metadata tag.
func IsOnMetaData(payload []byte) bool {
	_, err := parseScriptData(payload)
	return err == nil
}

// LooksLikeOnMetaData is a cheap prefix check used when the AMF body contains
// value types this fork does not decode: the tag is still dropped so the output
// has exactly one onMetaData tag.
func LooksLikeOnMetaData(payload []byte) bool {
	const prefixLen = 3 + len("onMetaData")
	if len(payload) < prefixLen || payload[0] != amfString {
		return false
	}
	n := int(payload[1])<<8 | int(payload[2])
	return n == len("onMetaData") && string(payload[3:3+n]) == "onMetaData"
}

// Builder accumulates the onMetaData content for one output FLV segment and
// serializes it into a complete script tag. The tag length is fixed after the
// first BuildTag call, so it can be rewritten in place via WriteAt while the
// segment is still being recorded.
type Builder struct {
	mu         sync.Mutex
	base       []amfProperty
	hasBase    bool
	frozen     bool
	durationMS int64
	keyframes  []Keyframe
	info       RecordingInfo
	version    int64
	fixedLen   int
}

// NewBuilder returns an empty metadata builder. Until SetBaseFromScript is
// called the written metadata only contains duration/keyframes, which is what
// BililiveRecorder does when the source has no onMetaData tag.
func NewBuilder() *Builder { return &Builder{} }

// SetBaseFromScript parses a source onMetaData script tag body (the tag's data
// payload, without the 11-byte tag header and 4-byte previous-tag-size field)
// and uses its fields as the base for the rewritten metadata. Duration and any
// existing keyframes fields are replaced by the builder. It returns false when
// the payload is not usable or the builder has already been frozen.
func (b *Builder) SetBaseFromScript(payload []byte) bool {
	props, err := parseScriptData(payload)
	if err != nil {
		return false
	}
	filtered := make([]amfProperty, 0, len(props))
	for _, prop := range props {
		switch prop.key {
		case "duration", "keyframes":
			continue
		}
		filtered = append(filtered, prop)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.frozen {
		return false
	}
	b.base = filtered
	b.hasBase = true
	return true
}

// SetRecordingInfo stores the room/stream information written as Title,
// Artist, Comment and the BililiveRecorder object. It must be called before
// the first BuildTag.
func (b *Builder) SetRecordingInfo(info RecordingInfo) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.frozen {
		return
	}
	b.info = info
}

// SetDuration updates the duration field in milliseconds. It is serialized as
// seconds, matching the FLV onMetaData convention.
func (b *Builder) SetDuration(ms int64) {
	if ms < 0 {
		ms = 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.durationMS == ms {
		return
	}
	b.durationMS = ms
	b.version++
}

// AddKeyframe appends a keyframe entry, enforcing MaxKeyframes and
// MinKeyframeIntervalMS like BililiveRecorder. It returns true when the entry
// was accepted.
func (b *Builder) AddKeyframe(timeMS, filePosition int64) bool {
	if timeMS < 0 || filePosition < 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.keyframes) >= MaxKeyframes {
		return false
	}
	if len(b.keyframes) > 0 {
		last := b.keyframes[len(b.keyframes)-1]
		if timeMS-last.TimeMS <= MinKeyframeIntervalMS {
			return false
		}
	}
	b.keyframes = append(b.keyframes, Keyframe{TimeMS: timeMS, FilePosition: filePosition})
	b.version++
	return true
}

// KeyframeCount returns the number of indexed keyframes.
func (b *Builder) KeyframeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.keyframes)
}

// Version returns a counter that changes whenever duration or the keyframe
// list changes. The writer uses it to avoid rewriting unchanged metadata.
func (b *Builder) Version() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.version
}

// BuildTag returns a complete FLV script tag (header + AMF body + previous-tag
// size) for the current state. The returned slice is newly allocated and its
// length never changes after the first call.
func (b *Builder) BuildTag() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	body := make([]byte, 0, 1024+len(b.base)*24+2*MaxKeyframes*18)
	body = appendString(body, "onMetaData")
	info := b.info
	includeInfo := info.hasContent()
	base := b.base
	if includeInfo {
		base = filterKeys(base, "Title", "Artist", "Comment", "BililiveRecorder")
	}
	extra := 2
	if includeInfo {
		extra += 4
	}
	body = append(body, amfECMAArray)
	body = binary.BigEndian.AppendUint32(body, uint32(len(base)+extra))
	body = appendProperties(body, base)
	if includeInfo {
		body = appendPropertyKey(body, "Title")
		body = appendValue(body, newStringValue(info.Title))
		body = appendPropertyKey(body, "Artist")
		body = appendValue(body, newStringValue(info.artist()))
		body = appendPropertyKey(body, "Comment")
		body = appendValue(body, newStringValue(info.comment()))
		body = appendPropertyKey(body, "BililiveRecorder")
		body = appendValue(body, newObjectValue(bililiveRecorderProps(info)))
	}
	body = appendPropertyKey(body, "duration")
	body = appendNumber(body, float64(b.durationMS)/1000.0)

	body = appendPropertyKey(body, "keyframes")
	body = append(body, amfObject)
	body = appendPropertyKey(body, "times")
	times := make([]amfValue, 0, len(b.keyframes))
	for _, keyframe := range b.keyframes {
		times = append(times, newNumberValue(float64(keyframe.TimeMS)/1000.0))
	}
	body = appendValue(body, newStrictArrayValue(times))
	body = appendPropertyKey(body, "filepositions")
	positions := make([]amfValue, 0, len(b.keyframes))
	for _, keyframe := range b.keyframes {
		positions = append(positions, newNumberValue(float64(keyframe.FilePosition)))
	}
	body = appendValue(body, newStrictArrayValue(positions))
	body = appendPropertyKey(body, "spacer")
	spacer := make([]amfValue, 0, 2*(MaxKeyframes-len(b.keyframes)))
	for i := 0; i < 2*(MaxKeyframes-len(b.keyframes)); i++ {
		spacer = append(spacer, newNumberValue(math.NaN()))
	}
	body = appendValue(body, newStrictArrayValue(spacer))
	body = append(body, 0x00, 0x00, 0x09) // end of keyframes object
	body = append(body, 0x00, 0x00, 0x09) // end of ECMA array

	size := len(body)
	if size > 0xFFFFFF {
		return nil, fmt.Errorf("flvmeta: metadata body too large: %d bytes", size)
	}
	tag := make([]byte, 11+size+4)
	tag[0] = 18 // script tag
	tag[1] = byte(size >> 16)
	tag[2] = byte(size >> 8)
	tag[3] = byte(size)
	// timestamp and StreamID are zero for onMetaData.
	copy(tag[11:11+size], body)
	binary.BigEndian.PutUint32(tag[11+size:], uint32(11+size))

	if b.fixedLen == 0 {
		b.fixedLen = len(tag)
		b.frozen = true
	} else if len(tag) != b.fixedLen {
		return nil, fmt.Errorf("flvmeta: metadata tag length changed from %d to %d", b.fixedLen, len(tag))
	}
	return tag, nil
}

// ParseMetadataTagBody extracts the top-level properties from a script tag body
// and converts AMF values to plain Go values (float64, bool, string, map, slice,
// nil). It is used by tests and by diagnostics that read written metadata.
func ParseMetadataTagBody(payload []byte) (map[string]any, error) {
	props, err := parseScriptData(payload)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(props))
	for _, prop := range props {
		out[prop.key] = toAny(prop.value)
	}
	return out, nil
}

func filterKeys(props []amfProperty, keys ...string) []amfProperty {
	out := make([]amfProperty, 0, len(props))
	for _, prop := range props {
		skip := false
		for _, key := range keys {
			if prop.key == key {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, prop)
		}
	}
	return out
}

func bililiveRecorderProps(info RecordingInfo) []amfProperty {
	start := info.startTime()
	return []amfProperty{
		{key: "RecordedBy", value: newStringValue(info.recordedBy())},
		{key: "RecordedFrom", value: newStringValue(info.StreamHost)},
		{key: "StreamServers", value: newStringValue(info.StreamHostFull)},
		{key: "RecorderVersion", value: newStringValue(info.version())},
		{key: "StartTime", value: newDateValue(start)},
		{key: "RoomId", value: newStringValue(strconv.FormatInt(info.RoomID, 10))},
		{key: "ShortId", value: newStringValue(strconv.FormatInt(info.ShortID, 10))},
		{key: "Name", value: newStringValue(info.Name)},
		{key: "StreamTitle", value: newStringValue(info.streamTitle())},
		{key: "AreaNameParent", value: newStringValue(info.AreaNameParent)},
		{key: "AreaNameChild", value: newStringValue(info.AreaNameChild)},
	}
}

func newDateValue(t time.Time) amfValue {
	_, offset := t.Zone()
	return amfValue{kind: amfDate, dateMS: float64(t.UnixNano()) / 1e6, tz: int16(offset / 60)}
}

func toAny(v amfValue) any {
	switch v.kind {
	case amfNumber:
		return v.num
	case amfBoolean:
		return v.truth
	case amfString, amfLongString:
		return v.str
	case amfObject, amfECMAArray:
		out := make(map[string]any, len(v.props))
		for _, prop := range v.props {
			out[prop.key] = toAny(prop.value)
		}
		return out
	case amfStrictArray:
		out := make([]any, 0, len(v.values))
		for _, item := range v.values {
			out = append(out, toAny(item))
		}
		return out
	case amfDate:
		return v.dateMS
	default:
		return nil
	}
}
