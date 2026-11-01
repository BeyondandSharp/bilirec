package subscribe

import (
	"fmt"

	"github.com/bilirec/bilirec/pkg/pool"
	"go.etcd.io/bbolt"
)

// DanmakuTypes selects which danmaku message types are recorded. It is defined
// here (not in services/settings) so room overrides and global settings can
// share the type without an import cycle; services/settings aliases it.
type DanmakuTypes struct {
	RawEvents bool `json:"raw_events"`
	SuperChat bool `json:"super_chat"`
	Gift      bool `json:"gift"`
	Guard     bool `json:"guard"`
}

type RoomConfig struct {
	// AddedAt is the Unix timestamp (seconds) when the room was subscribed.
	// Legacy records have 0; callers must treat it as unknown.
	AddedAt               int64
	AutoRecord            bool
	Notify                bool
	RecordDurationMinutes int      // 0 = system default, -1 = unlimited, >0 = custom minutes
	Qn                    int      // 0 = backend default (原画); see bilibili.Quality
	OnlyAudio             bool     // request audio-only stream when starting recording
	RecordDanmaku         bool     // record live chat sidecar alongside video when starting recording
	RecordSubtitle        bool     // extract AI subtitle VTT sidecar when the room provides subtitles
	StreamProfiles        []string // empty/nil = auto (all formats); allow-list of http-flv|hls-ts|hls-fmp4
	DeleteOldestOnLowDisk bool     // when starting record, purge this room's oldest file by filename if below MIN_DISK_SPACE_BYTES

	// v2 per-room feature overrides (docs/FEATURES-V2.md §1.3).
	// nil = inherit the global setting. Old DB records decode with nil fields.
	SegmentMode             *string
	SegmentMinutes          *int
	SegmentSizeMB           *int
	SegmentByTitle          *bool
	MinFileSizeMB           *int
	FilenameTemplate        *string
	TitleFilterPatterns     *string
	SaveStreamCover         *bool
	DanmakuTypes            *DanmakuTypes
	DanmakuTransport        *string
	FlvWriteMetadata        *bool
	FlvSplitOnScriptTag     *bool
	FlvDisableSplitOnAnnexB *bool
	BiliLiveAPIHost         *string
	BiliLiveAPIFallbackHost *string
	BiliCDN                 *[]string
	BiliCDNFallback         *bool
	BiliForceSource         *bool
	BiliReplaceCN01         *[]string
}

var roomConfigSerializer = pool.NewSerializer()
var defaultRoomConfigBytes = mustSerializeRoomConfig(defaultRoomConfig())

func mustSerializeRoomConfig(cfg *RoomConfig) []byte {
	data, err := roomConfigSerializer.Serialize(cfg)
	if err != nil {
		panic(err)
	}
	return data
}

func defaultRoomConfig() *RoomConfig {
	return &RoomConfig{AutoRecord: false, Notify: false, RecordDurationMinutes: 0}
}

func (s *Service) UpdateConfig(roomID int, cfg *RoomConfig) error {
	key := fmt.Append(nil, roomID)
	s.roomsMu.Lock()
	defer s.roomsMu.Unlock()

	// Preserve the subscription creation time: config updates do not carry it.
	if cfg != nil {
		if existing := s.rooms[roomID]; existing != nil && cfg.AddedAt == 0 {
			cfg.AddedAt = existing.AddedAt
		}
	}

	var data []byte
	if cfg == nil {
		data = defaultRoomConfigBytes
	} else {
		encoded, err := roomConfigSerializer.Serialize(cfg)
		if err != nil {
			return err
		}
		data = encoded
	}
	if err := s.bucket.Update(func(bucket *bbolt.Bucket) error {
		exists := bucket.Get(key)
		if exists == nil {
			return ErrRoomNotSubscribed
		}
		return bucket.Put(key, data)
	}); err != nil {
		return err
	}
	s.rooms[roomID] = cloneRoomConfig(cfg)
	return nil
}

func (s *Service) ListSubscribedRoomsWithConfig() (map[int]*RoomConfig, error) {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	return cloneRooms(s.rooms), nil
}

func parseRoomConfig(raw []byte) (*RoomConfig, error) {
	if len(raw) == 0 {
		return defaultRoomConfig(), nil
	}

	cfg := &RoomConfig{}
	if err := roomConfigSerializer.Deserialize(raw, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *Service) GetConfig(roomID int) (*RoomConfig, error) {
	s.roomsMu.RLock()
	defer s.roomsMu.RUnlock()
	cfg, ok := s.rooms[roomID]
	if !ok {
		return nil, ErrRoomNotSubscribed
	}
	return cloneRoomConfig(cfg), nil
}
