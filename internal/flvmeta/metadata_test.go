// SPDX-License-Identifier: GPL-3.0-only

package flvmeta

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"
)

func appendTestProperty(dst []byte, key string, value amfValue) []byte {
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(key)))
	dst = append(dst, key...)
	return appendValue(dst, value)
}

func TestBuildTagEmptyMetadata(t *testing.T) {
	builder := NewBuilder()
	tag, err := builder.BuildTag()
	if err != nil {
		t.Fatal(err)
	}
	if tag[0] != 18 {
		t.Fatalf("tag type = %d, want 18", tag[0])
	}
	size := int(tag[1])<<16 | int(tag[2])<<8 | int(tag[3])
	if size+11+4 != len(tag) {
		t.Fatalf("tag length mismatch: size=%d len=%d", size, len(tag))
	}
	prev := binary.BigEndian.Uint32(tag[len(tag)-4:])
	if prev != uint32(11+size) {
		t.Fatalf("previous tag size = %d, want %d", prev, 11+size)
	}
	body, err := ParseMetadataTagBody(tag[11 : 11+size])
	if err != nil {
		t.Fatal(err)
	}
	if body["duration"].(float64) != 0 {
		t.Fatalf("duration = %v", body["duration"])
	}
	keyframes := body["keyframes"].(map[string]any)
	if got := len(keyframes["times"].([]any)); got != 0 {
		t.Fatalf("times = %d, want 0", got)
	}
	if got := len(keyframes["filepositions"].([]any)); got != 0 {
		t.Fatalf("filepositions = %d, want 0", got)
	}
	if got := len(keyframes["spacer"].([]any)); got != 2*MaxKeyframes {
		t.Fatalf("spacer = %d, want %d", got, 2*MaxKeyframes)
	}
}

func TestTagLengthStableAndUpdates(t *testing.T) {
	builder := NewBuilder()
	first, err := builder.BuildTag()
	if err != nil {
		t.Fatal(err)
	}
	builder.SetDuration(12345)
	builder.AddKeyframe(1000, 200)
	builder.AddKeyframe(3000, 5000)
	second, err := builder.BuildTag()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("tag length changed: %d -> %d", len(first), len(second))
	}
	size := int(second[1])<<16 | int(second[2])<<8 | int(second[3])
	body, err := ParseMetadataTagBody(second[11 : 11+size])
	if err != nil {
		t.Fatal(err)
	}
	if body["duration"].(float64) != 12.345 {
		t.Fatalf("duration = %v, want 12.345", body["duration"])
	}
	keyframes := body["keyframes"].(map[string]any)
	times := keyframes["times"].([]any)
	positions := keyframes["filepositions"].([]any)
	if len(times) != 2 || times[0].(float64) != 1 || times[1].(float64) != 3 {
		t.Fatalf("times = %v", times)
	}
	if positions[0].(float64) != 200 || positions[1].(float64) != 5000 {
		t.Fatalf("filepositions = %v", positions)
	}
	if got := len(keyframes["spacer"].([]any)); got != 2*(MaxKeyframes-2) {
		t.Fatalf("spacer = %d", got)
	}
}

func TestKeyframeIntervalAndCapacity(t *testing.T) {
	builder := NewBuilder()
	if !builder.AddKeyframe(1000, 1) {
		t.Fatal("first keyframe rejected")
	}
	if builder.AddKeyframe(2000, 2) {
		t.Fatal("keyframe below MinKeyframeIntervalMS accepted")
	}
	if builder.AddKeyframe(2900, 3) {
		t.Fatal("keyframe at exactly MinKeyframeIntervalMS accepted (BLR uses strict >)")
	}
	if !builder.AddKeyframe(2901, 3) {
		t.Fatal("keyframe above MinKeyframeIntervalMS rejected")
	}
	for i := 0; i < MaxKeyframes+10; i++ {
		builder.AddKeyframe(int64(10000+i*(MinKeyframeIntervalMS+100)), int64(10000+i))
	}
	if got := len(builder.keyframes); got != MaxKeyframes {
		t.Fatalf("keyframes = %d, want %d", got, MaxKeyframes)
	}
}

func TestPreservesSourceFields(t *testing.T) {
	source := appendString(nil, "onMetaData")
	source = append(source, amfECMAArray)
	source = binary.BigEndian.AppendUint32(source, 4)
	source = appendTestProperty(source, "duration", newNumberValue(99))
	source = appendTestProperty(source, "encoder", newStringValue("obs"))
	source = appendTestProperty(source, "videocodecid", newNumberValue(7))
	source = appendTestProperty(source, "keyframes", newObjectValue([]amfProperty{
		{key: "dummy", value: newNumberValue(1)},
	}))
	source = append(source, 0x00, 0x00, 0x09)

	builder := NewBuilder()
	if !builder.SetBaseFromScript(source) {
		t.Fatal("SetBaseFromScript failed")
	}
	builder.SetDuration(5000)
	builder.AddKeyframe(2000, 1234)
	tag, err := builder.BuildTag()
	if err != nil {
		t.Fatal(err)
	}
	size := int(tag[1])<<16 | int(tag[2])<<8 | int(tag[3])
	body, err := ParseMetadataTagBody(tag[11 : 11+size])
	if err != nil {
		t.Fatal(err)
	}
	if body["encoder"].(string) != "obs" {
		t.Fatalf("encoder = %v", body["encoder"])
	}
	if body["videocodecid"].(float64) != 7 {
		t.Fatalf("videocodecid = %v", body["videocodecid"])
	}
	if body["duration"].(float64) != 5 {
		t.Fatalf("duration = %v, want 5", body["duration"])
	}
	keyframes := body["keyframes"].(map[string]any)
	if _, ok := keyframes["dummy"]; ok {
		t.Fatal("source keyframes object leaked into output")
	}
	if got := keyframes["times"].([]any); len(got) != 1 || got[0].(float64) != 2 {
		t.Fatalf("times = %v", got)
	}
}

func TestSpacerIsNaN(t *testing.T) {
	builder := NewBuilder()
	builder.AddKeyframe(1000, 1)
	tag, err := builder.BuildTag()
	if err != nil {
		t.Fatal(err)
	}
	size := int(tag[1])<<16 | int(tag[2])<<8 | int(tag[3])
	body, err := ParseMetadataTagBody(tag[11 : 11+size])
	if err != nil {
		t.Fatal(err)
	}
	keyframes := body["keyframes"].(map[string]any)
	spacer := keyframes["spacer"].([]any)
	if len(spacer) == 0 {
		t.Fatal("empty spacer")
	}
	if !math.IsNaN(spacer[0].(float64)) {
		t.Fatalf("spacer[0] = %v, want NaN", spacer[0])
	}
}

func TestParseMetadataRejectsOtherScript(t *testing.T) {
	body := appendString(nil, "onTextData")
	body = append(body, amfECMAArray, 0, 0, 0, 0, 0x00, 0x00, 0x09)
	if _, err := ParseMetadataTagBody(body); err == nil {
		t.Fatal("expected error for non-onMetaData script")
	}
}

func TestRecordingInfoFields(t *testing.T) {
	builder := NewBuilder()
	builder.SetRecordingInfo(RecordingInfo{
		Title:           "直播标题",
		StreamTitle:     "直播标题",
		Name:            "主播",
		RoomID:          123456,
		ShortID:         42,
		AreaNameParent:  "虚拟主播",
		AreaNameChild:   "虚拟日常",
		StreamHost:      "example.bilivideo.com",
		StreamHostFull:  "example.bilivideo.com [1.2.3.4]",
		RecorderVersion: "v1.2.3",
		RecordedBy:      "bilirec",
		StartTime:       time.Date(2026, 11, 4, 1, 2, 3, 456000000, time.FixedZone("CST", 8*3600)),
	})
	builder.SetDuration(5000)
	builder.AddKeyframe(2000, 1234)
	tag, err := builder.BuildTag()
	if err != nil {
		t.Fatal(err)
	}
	size := int(tag[1])<<16 | int(tag[2])<<8 | int(tag[3])
	body, err := ParseMetadataTagBody(tag[11 : 11+size])
	if err != nil {
		t.Fatal(err)
	}
	if body["Title"].(string) != "直播标题" {
		t.Fatalf("Title = %v", body["Title"])
	}
	if body["Artist"].(string) != "主播 (123456)" {
		t.Fatalf("Artist = %v", body["Artist"])
	}
	comment := body["Comment"].(string)
	for _, want := range []string{"bilirec", "123456", "主播", "直播标题", "虚拟主播·虚拟日常", "example.bilivideo.com"} {
		if !strings.Contains(comment, want) {
			t.Fatalf("Comment missing %q: %s", want, comment)
		}
	}
	recorder := body["BililiveRecorder"].(map[string]any)
	if recorder["RecordedBy"].(string) != "bilirec" || recorder["RecorderVersion"].(string) != "v1.2.3" {
		t.Fatalf("BililiveRecorder = %v", recorder)
	}
	if recorder["RoomId"].(string) != "123456" || recorder["ShortId"].(string) != "42" {
		t.Fatalf("BililiveRecorder ids = %v", recorder)
	}
	if recorder["StreamTitle"].(string) != "直播标题" || recorder["Name"].(string) != "主播" {
		t.Fatalf("BililiveRecorder names = %v", recorder)
	}
	start, ok := recorder["StartTime"].(float64)
	if !ok || start <= 0 {
		t.Fatalf("StartTime = %v", recorder["StartTime"])
	}
}

func TestRecordingInfoOverridesSourceFields(t *testing.T) {
	source := appendString(nil, "onMetaData")
	source = append(source, amfECMAArray)
	source = binary.BigEndian.AppendUint32(source, 3)
	source = appendTestProperty(source, "Title", newStringValue("旧标题"))
	source = appendTestProperty(source, "Comment", newStringValue("旧注释"))
	source = appendTestProperty(source, "encoder", newStringValue("obs"))
	source = append(source, 0x00, 0x00, 0x09)
	builder := NewBuilder()
	if !builder.SetBaseFromScript(source) {
		t.Fatal("SetBaseFromScript failed")
	}
	builder.SetRecordingInfo(RecordingInfo{Title: "新标题", Name: "主播", RoomID: 1})
	tag, err := builder.BuildTag()
	if err != nil {
		t.Fatal(err)
	}
	size := int(tag[1])<<16 | int(tag[2])<<8 | int(tag[3])
	body, err := ParseMetadataTagBody(tag[11 : 11+size])
	if err != nil {
		t.Fatal(err)
	}
	if body["Title"].(string) != "新标题" || body["encoder"].(string) != "obs" {
		t.Fatalf("body = %v", body)
	}
	if strings.Contains(body["Comment"].(string), "旧注释") {
		t.Fatal("source Comment leaked into output")
	}
}
