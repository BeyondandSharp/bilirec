package subscribe

import (
	"slices"
	"strconv"
)

func cloneRoomConfig(cfg *RoomConfig) *RoomConfig {
	if cfg == nil {
		return defaultRoomConfig()
	}
	out := *cfg
	if cfg.StreamProfiles != nil {
		out.StreamProfiles = slices.Clone(cfg.StreamProfiles)
	}
	out.SegmentMode = clonePtr(cfg.SegmentMode)
	out.SegmentMinutes = clonePtr(cfg.SegmentMinutes)
	out.SegmentSizeMB = clonePtr(cfg.SegmentSizeMB)
	out.SegmentByTitle = clonePtr(cfg.SegmentByTitle)
	out.MinFileSizeMB = clonePtr(cfg.MinFileSizeMB)
	out.FilenameTemplate = clonePtr(cfg.FilenameTemplate)
	out.TitleFilterPatterns = clonePtr(cfg.TitleFilterPatterns)
	out.SaveStreamCover = clonePtr(cfg.SaveStreamCover)
	out.DanmakuTypes = clonePtr(cfg.DanmakuTypes)
	out.DanmakuTransport = clonePtr(cfg.DanmakuTransport)
	out.FlvWriteMetadata = clonePtr(cfg.FlvWriteMetadata)
	out.FlvSplitOnScriptTag = clonePtr(cfg.FlvSplitOnScriptTag)
	out.FlvDisableSplitOnAnnexB = clonePtr(cfg.FlvDisableSplitOnAnnexB)
	out.BiliLiveAPIHost = clonePtr(cfg.BiliLiveAPIHost)
	out.BiliLiveAPIFallbackHost = clonePtr(cfg.BiliLiveAPIFallbackHost)
	if cfg.BiliCDN != nil {
		v := slices.Clone(*cfg.BiliCDN)
		out.BiliCDN = &v
	}
	out.BiliCDNFallback = clonePtr(cfg.BiliCDNFallback)
	out.BiliForceSource = clonePtr(cfg.BiliForceSource)
	if cfg.BiliReplaceCN01 != nil {
		v := slices.Clone(*cfg.BiliReplaceCN01)
		out.BiliReplaceCN01 = &v
	}
	return &out
}

// clonePtr deep-copies a pointer field so callers cannot mutate the cache.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneRooms(rooms map[int]*RoomConfig) map[int]*RoomConfig {
	out := make(map[int]*RoomConfig, len(rooms))
	for id, cfg := range rooms {
		out[id] = cloneRoomConfig(cfg)
	}
	return out
}

func (s *Service) loadRooms() error {
	rooms := make(map[int]*RoomConfig)
	err := s.bucket.ForEach(func(k, v []byte) error {
		roomID, err := strconv.Atoi(string(k))
		if err != nil {
			log.Warnf("扫描条目失败：%s：%v，已忽略。", string(k), err)
			return nil
		}

		cfg, err := parseRoomConfig(v)
		if err != nil {
			log.Warnf("解析房间 %d 配置失败：%v，使用默认值", roomID, err)
			cfg = defaultRoomConfig()
		}
		rooms[roomID] = cfg
		return nil
	})
	if err != nil {
		return err
	}

	s.roomsMu.Lock()
	s.rooms = rooms
	s.roomsMu.Unlock()
	return nil
}
