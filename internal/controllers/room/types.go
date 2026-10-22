package room

import "github.com/bilirec/bilirec/internal/services/subscribe"

type LiveInfo struct {
	RoomId int  `json:"room_id"`
	IsLive bool `json:"is_live"`
}

type ResolveRoomIDResponse struct {
	QueryID int `json:"query_id"`
	RoomID  int `json:"room_id"`
	ShortID int `json:"short_id"`
}

type BatchRoomIDsRequest struct {
	RoomIDs []int `json:"roomIDs"`
}

type SubscribeStatus struct {
	RoomId       int  `json:"room_id"`
	IsSubscribed bool `json:"is_subscribed"`
}

type SubscribeList struct {
	RoomIds []int            `json:"room_ids"`
	AddedAt map[string]int64 `json:"added_at,omitempty"` // Unix seconds; legacy entries may be absent/0
}

// RoomConfigResponse is the additive GET /room/{roomID}/config response
// (docs/FEATURES-V2.md §1.3). Pointer fields are omitted (nil) when the room
// inherits the global setting.
type RoomConfigResponse struct {
	RoomId                int      `json:"room_id"`
	AddedAt               int64    `json:"added_at"`
	AutoRecord            bool     `json:"auto_record"`
	Notify                bool     `json:"notify"`
	RecordDurationMinutes int      `json:"record_duration_minutes"`
	Qn                    int      `json:"qn"`
	OnlyAudio             bool     `json:"only_audio"`
	RecordDanmaku         bool     `json:"record_danmaku"`
	StreamProfiles        []string `json:"stream_profiles"`
	DeleteOldestOnLowDisk bool     `json:"delete_oldest_on_low_disk"`

	// v2 per-room feature overrides; nil = inherit global (docs/FEATURES-V2.md §1.3).
	SegmentMode             *string                 `json:"segment_mode,omitempty"`
	SegmentMinutes          *int                    `json:"segment_minutes,omitempty"`
	SegmentSizeMB           *int                    `json:"segment_size_mb,omitempty"`
	SegmentByTitle          *bool                   `json:"segment_by_title,omitempty"`
	MinFileSizeMB           *int                    `json:"min_file_size_mb,omitempty"`
	FilenameTemplate        *string                 `json:"filename_template,omitempty"`
	TitleFilterPatterns     *string                 `json:"title_filter_patterns,omitempty"`
	SaveStreamCover         *bool                   `json:"save_stream_cover,omitempty"`
	DanmakuTypes            *subscribe.DanmakuTypes `json:"danmaku_types,omitempty"`
	DanmakuTransport        *string                 `json:"danmaku_transport,omitempty"`
	FlvWriteMetadata        *bool                   `json:"flv_write_metadata,omitempty"`
	FlvSplitOnScriptTag     *bool                   `json:"flv_split_on_script_tag,omitempty"`
	FlvDisableSplitOnAnnexB *bool                   `json:"flv_disable_split_on_annexb,omitempty"`
	BiliLiveAPIHost         *string                 `json:"bili_live_api_host,omitempty"`
	BiliLiveAPIFallbackHost *string                 `json:"bili_live_api_fallback_host,omitempty"`
	BiliCDN                 *[]string               `json:"bili_cdn,omitempty"`
	BiliCDNFallback         *bool                   `json:"bili_cdn_fallback,omitempty"`
	BiliForceSource         *bool                   `json:"bili_force_source,omitempty"`
	BiliReplaceCN01         *[]string               `json:"bili_replace_cn01,omitempty"`
}

// UpdateRoomConfigRequest extends the existing room config body additively.
// New fields are pointers: null/absent = inherit global; a value stores an
// override (replacing a previous one).
type UpdateRoomConfigRequest struct {
	AutoRecord            bool     `json:"auto_record"`
	Notify                bool     `json:"notify"`
	RecordDurationMinutes int      `json:"record_duration_minutes"`
	Qn                    int      `json:"qn"`
	OnlyAudio             bool     `json:"only_audio"`
	RecordDanmaku         bool     `json:"record_danmaku"`
	StreamProfiles        []string `json:"stream_profiles"`
	DeleteOldestOnLowDisk bool     `json:"delete_oldest_on_low_disk"`

	SegmentMode             *string                 `json:"segment_mode"`
	SegmentMinutes          *int                    `json:"segment_minutes"`
	SegmentSizeMB           *int                    `json:"segment_size_mb"`
	SegmentByTitle          *bool                   `json:"segment_by_title"`
	MinFileSizeMB           *int                    `json:"min_file_size_mb"`
	FilenameTemplate        *string                 `json:"filename_template"`
	TitleFilterPatterns     *string                 `json:"title_filter_patterns"`
	SaveStreamCover         *bool                   `json:"save_stream_cover"`
	DanmakuTypes            *subscribe.DanmakuTypes `json:"danmaku_types"`
	DanmakuTransport        *string                 `json:"danmaku_transport"`
	FlvWriteMetadata        *bool                   `json:"flv_write_metadata"`
	FlvSplitOnScriptTag     *bool                   `json:"flv_split_on_script_tag"`
	FlvDisableSplitOnAnnexB *bool                   `json:"flv_disable_split_on_annexb"`
	BiliLiveAPIHost         *string                 `json:"bili_live_api_host"`
	BiliLiveAPIFallbackHost *string                 `json:"bili_live_api_fallback_host"`
	BiliCDN                 *[]string               `json:"bili_cdn"`
	BiliCDNFallback         *bool                   `json:"bili_cdn_fallback"`
	BiliForceSource         *bool                   `json:"bili_force_source"`
	BiliReplaceCN01         *[]string               `json:"bili_replace_cn01"`
}
