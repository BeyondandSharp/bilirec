package bilibili

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"

	"github.com/bilirec/bilirec/pkg/fp"
	"github.com/bilirec/bilirec/utils"
)

type (
	StreamResponseV1 struct {
		Code    int        `json:"code"`
		Message string     `json:"message"`
		TTL     int        `json:"ttl"`
		Data    StreamData `json:"data"`
	}

	StreamData struct {
		CurrentQuality     int           `json:"current_quality"`
		AcceptQuality      []string      `json:"accept_quality"`
		CurrentQn          int           `json:"current_qn"`
		QualityDescription []QualityDesc `json:"quality_description"`
		Durl               []StreamURL   `json:"durl"`
	}

	QualityDesc struct {
		Qn   int    `json:"qn"`
		Desc string `json:"desc"`
	}

	StreamURL struct {
		URL        string `json:"url"`
		Length     int    `json:"length"`
		Order      int    `json:"order"`
		StreamType int    `json:"stream_type"`
		P2PType    int    `json:"p2p_type"`
	}
)

type (
	StreamResponseV2 struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		TTL     int    `json:"ttl"`
		Data    struct {
			PlayurlInfo *PlayurlInfo `json:"playurl_info"`
		} `json:"data"`
	}

	RoomPlayInfo struct {
		LiveStatus  int          `json:"live_status"`
		Encrypted   bool         `json:"encrypted"`
		PlayurlInfo *PlayurlInfo `json:"playurl_info"`
	}

	PlayurlInfo struct {
		Playurl *Playurl `json:"playurl"`
	}

	Playurl struct {
		Streams []StreamItem `json:"stream"`
	}

	StreamItem struct {
		ProtocolName string       `json:"protocol_name"`
		Formats      []FormatItem `json:"format"`
	}

	FormatItem struct {
		FormatName string      `json:"format_name"`
		Codecs     []CodecItem `json:"codec"`
	}

	CodecItem struct {
		CodecName string        `json:"codec_name"`
		BaseUrl   string        `json:"base_url"`
		CurrentQn int           `json:"current_qn"`
		AcceptQn  []int         `json:"accept_qn"`
		UrlInfos  []UrlInfoItem `json:"url_info"`
	}

	UrlInfoItem struct {
		Host  string `json:"host"`
		Extra string `json:"extra"`
	}

	StreamURLInfo struct {
		Protocol    string
		Format      string
		Codec       string
		URL         string
		Qn          int
		AcceptQn    []int
		IsAudioOnly bool
	}
)

var ErrInvalidStreamProfile = errors.New("无效的流配置")
var ErrInvalidStreamCodec = errors.New("无效的流编码")
var ErrInvalidStreamQuality = errors.New("无效的流画质")

const v1StreamAPI = "https://api.live.bilibili.com/room/v1/Room/playUrl"

const (
	streamAPICodeRoomNotFound   = 19002003 // playUrl v1
	streamAPICodeRoomNotFoundV2 = 60004    // getRoomPlayInfo v2
	streamAPICodeGeoRestricted  = 60005
)

func mapStreamAPIError(code int, message string) error {
	switch code {
	case streamAPICodeRoomNotFound, streamAPICodeRoomNotFoundV2:
		return ErrRoomNotFound
	case streamAPICodeGeoRestricted:
		return ErrStreamGeoRestricted
	default:
		return &APIError{Code: code, Message: message}
	}
}

func (c *Client) GetStreamURLs(roomID int) ([]string, error) {
	client := c.liveClient.R()
	client.SetQueryParams(map[string]string{
		"cid":      fmt.Sprint(roomID),
		"qn":       "10000",
		"platform": "web",
	})

	resp, err := client.Get(v1StreamAPI)
	if err != nil {
		return nil, err
	} else if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("状态码：%d", resp.StatusCode())
	}

	var sr StreamResponseV1
	if err := json.Unmarshal(resp.Body(), &sr); err != nil {
		return nil, err
	} else if sr.Code != 0 {
		return nil, mapStreamAPIError(sr.Code, sr.Message)
	}

	return fp.Map(sr.Data.Durl, func(durl StreamURL) string {
		return durl.URL
	}), nil
}

func (c *Client) GetStreamURLsV2(roomID int, opts ...GetStreamURLsOption) ([]StreamURLInfo, error) {
	options := defaultGetStreamURLsOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	for _, profile := range options.profiles {
		if !profile.IsValid() {
			return nil, ErrInvalidStreamProfile
		}
	}

	for _, codec := range options.codecs {
		if !codec.IsValid() {
			return nil, ErrInvalidStreamCodec
		}
	}

	if !options.qn.IsValid() {
		return nil, ErrInvalidStreamQuality
	}

	protocolRaw, formatRaw := profileQueryParams(options.profiles)
	protocolStr := utils.EmptyOrElse(protocolRaw, "0,1")
	formatStr := utils.EmptyOrElse(formatRaw, "0,1,2")
	codecStr := utils.EmptyOrElse(joinCodecs(options.codecs), "0,1,2")

	params := map[string]string{
		"room_id":      fmt.Sprint(roomID),
		"qn":           fmt.Sprint(int(options.qn)),
		"no_playurl":   "0",
		"mask":         "1",
		"platform":     "web",
		"protocol":     protocolStr,
		"format":       formatStr,
		"codec":        codecStr, // 0: avc, 1: hevc, 2: unknown
		"dolby":        "5",
		"panorama":     "1",
		"hdr_type":     "0,1",
		"web_location": "444.8",
		"only_audio":   utils.Ternary(options.onlyAudio, "1", "0"), // 希望可以防止錯誤拿到音頻流
	}

	fetchConfig := options.fetch
	if fetchConfig != nil {
		log.With(
			"room", roomID,
			"api_host", fetchConfig.APIHost,
			"fallback_api_host", fetchConfig.FallbackAPIHost,
			"cdn", fetchConfig.CDN,
			"force_source", fetchConfig.ForceSource,
			"replace_cn01", fetchConfig.ReplaceCN01,
		).Debug("使用 B 站高级拉流配置")
	}

	streamInfos, err := c.fetchStreamURLs(roomID, params, options, fetchConfig)
	if err != nil {
		return nil, err
	}

	sortStreams(streamInfos, int(options.qn))
	streamInfos = applyForceSourcePreference(streamInfos, options, fetchConfig)
	streamInfos = c.applyCN01HostReplacement(roomID, streamInfos, fetchConfig)
	return streamInfos, nil
}

func containFormat(profiles []StreamProfile, format string) bool {
	return slices.ContainsFunc(profiles, func(profile StreamProfile) bool {
		switch format {
		case "flv":
			return profile == ProfileHTTPFLV
		case "ts":
			return profile == ProfileHLSTS
		case "fmp4":
			return profile == ProfileHLSFMP4
		default:
			return false
		}
	})
}

func isAudioOnlyStreamURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Query().Get("ptype") == "1"
}
