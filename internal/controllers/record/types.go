package record

import "github.com/bilirec/bilirec/internal/services/recorder"

type (
	BatchRoomIDsRequest struct {
		RoomIDs []int `json:"roomIDs"`
	}

	StartRecordingRequest struct {
		DurationMinutes       *int      `json:"duration_minutes"`
		Qn                    *int      `json:"qn"`
		OnlyAudio             *bool     `json:"only_audio"`
		RecordDanmaku         *bool     `json:"record_danmaku"`
		StreamProfiles        *[]string `json:"stream_profiles"`
		StreamProfile         *string   `json:"stream_profile"`
		DeleteOldestOnLowDisk *bool     `json:"delete_oldest_on_low_disk"`
		// Downloader overrides the global DOWNLOADER for this recording:
		// auto|native|ffmpeg|streamlink. auto (or omitted) inherits the
		// server configuration.
		Downloader *string `json:"downloader"`
	}

	Status struct {
		RoomId int                   `json:"room_id"`
		Status recorder.RecordStatus `json:"status"`
	}

	StopResult struct {
		RoomId  int  `json:"room_id"`
		Success bool `json:"success"`
	}
)
