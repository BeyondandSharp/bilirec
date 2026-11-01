package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// StreamFetchConfig carries the Bilibili advanced fetching options
// (docs/FEATURES-V2.md §6). The zero value is a no-op: fetching keeps the
// legacy hard-coded official API host, no `cdn` query parameter, no forced
// source profile preference and no cn-gotcha01 host replacement.
//
// APIHost / FallbackAPIHost are absolute http(s) base URLs that must
// reverse-proxy the same Bilibili live API (including WBI endpoints). When a
// custom host fails, fetching falls back to the official host, so a bad B05
// configuration can never block recording.
type StreamFetchConfig struct {
	// APIHost is the primary live API host (base URL, no API path).
	APIHost string
	// FallbackAPIHost is retried once when the primary host fails.
	FallbackAPIHost string
	// CDN, when non-empty, is sent as the `cdn` query parameter; entries are
	// tried in order.
	CDN []string
	// CDNFallback collects URL sets from every successful CDN entry instead of
	// stopping at the first success.
	CDNFallback bool
	// ForceSource prefers HLS TS/fMP4 profiles at qn >= 10000 (within the
	// caller's profile allow-list).
	ForceSource bool
	// ReplaceCN01 is the candidate host list used to replace *-cn-gotcha01*
	// stream hosts (random order, first 2xx range probe wins). It is
	// independent of ForceSource (docs/FEATURES-V2.md §6.4).
	ReplaceCN01 []string
}

const (
	// officialStreamAPIHost is the hard-coded official live API base URL.
	officialStreamAPIHost = "https://api.live.bilibili.com"
	streamAPIPathV2       = "/xlive/web-room/v2/index/getRoomPlayInfo"
	// v2StreamAPI is kept for compatibility/tests; it is the legacy URL.
	v2StreamAPI = officialStreamAPIHost + streamAPIPathV2

	// defaultStreamProbeTimeout is the §6 range probe timeout for cn-gotcha01
	// host candidates.
	defaultStreamProbeTimeout = 3 * time.Second
	// cn01HostMarker marks Bilibili stream hosts that support cn-gotcha01
	// replacement (B05).
	cn01HostMarker = "cn-gotcha01"
)

// fetchFallbackReason classifies why an attempt needed a fallback. It is
// logged as the `fallback_reason` field (§6.4).
type fetchFallbackReason string

const (
	fallbackReasonNetwork fetchFallbackReason = "network_error"
	fallbackReasonStatus  fetchFallbackReason = "http_status"
	fallbackReasonDecode  fetchFallbackReason = "decode_error"
	fallbackReasonAPI     fetchFallbackReason = "api_error"
	fallbackReasonGeo     fetchFallbackReason = "geo_restricted"
	fallbackReasonSign    fetchFallbackReason = "sign_error"
	fallbackReasonEmpty   fetchFallbackReason = "empty_urls"
)

func (c StreamFetchConfig) isEmpty() bool {
	return strings.TrimSpace(c.APIHost) == "" &&
		strings.TrimSpace(c.FallbackAPIHost) == "" &&
		len(normalizeHostList(c.CDN)) == 0 &&
		len(normalizeHostList(c.ReplaceCN01)) == 0 &&
		!c.CDNFallback &&
		!c.ForceSource
}

// normalizeStreamFetchConfig trims and de-duplicates the config and returns
// nil for an empty config so callers keep the legacy code path untouched.
func normalizeStreamFetchConfig(cfg StreamFetchConfig) *StreamFetchConfig {
	out := StreamFetchConfig{
		APIHost:         normalizeAPIHost(cfg.APIHost),
		FallbackAPIHost: normalizeAPIHost(cfg.FallbackAPIHost),
		CDN:             normalizeHostList(cfg.CDN),
		CDNFallback:     cfg.CDNFallback,
		ForceSource:     cfg.ForceSource,
		ReplaceCN01:     normalizeHostList(cfg.ReplaceCN01),
	}
	if out.isEmpty() {
		return nil
	}
	return &out
}

// normalizeAPIHost validates an absolute http(s) base URL and strips a
// trailing slash. Invalid input is ignored (falls back to the official host)
// so a bad setting never blocks recording. The official host itself folds into
// the empty value: settings always seed BILI_LIVE_API_HOST with the official
// URL, and doing so must stay a byte-identical no-op.
func normalizeAPIHost(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		log.Warnf("忽略无效的 B 站直播 API Host：%q", raw)
		return ""
	}
	if strings.EqualFold(raw, officialStreamAPIHost) {
		return ""
	}
	return raw
}

// normalizeHostList trims entries, drops empty ones and de-duplicates.
func normalizeHostList(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		key := strings.ToLower(entry)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// streamAPIHostCandidates returns the ordered list of API hosts to try:
// primary → configured fallback → official host as final safety net.
// With an empty config it returns exactly the official host, which keeps the
// request sequence identical to the legacy behavior.
func streamAPIHostCandidates(cfg *StreamFetchConfig) []string {
	primary := ""
	if cfg != nil {
		primary = cfg.APIHost
	}
	if primary == "" {
		primary = officialStreamAPIHost
	}

	candidates := make([]string, 0, 3)
	add := func(host string) {
		host = strings.TrimRight(strings.TrimSpace(host), "/")
		if host == "" {
			return
		}
		for _, existing := range candidates {
			if strings.EqualFold(existing, host) {
				return
			}
		}
		candidates = append(candidates, host)
	}

	add(primary)
	if cfg != nil {
		add(cfg.FallbackAPIHost)
	}
	add(officialStreamAPIHost)
	return candidates
}

// streamAPIURL joins an API base URL with the v2 stream API path.
func streamAPIURL(host string) string {
	return strings.TrimRight(host, "/") + streamAPIPathV2
}

// streamSignError wraps WBI signing failures while keeping the legacy error
// message byte-identical.
type streamSignError struct {
	err error
}

func (e *streamSignError) Error() string {
	return fmt.Sprintf("无法签名 wbi：%v", e.err)
}

func (e *streamSignError) Unwrap() error {
	return e.err
}

// isFallbackEligible reports whether another host should be tried after err.
// Authoritative results (room not found), local signing failures and caller
// cancellation are final; everything else (network, HTTP, geo, API, proxy
// mangling) may be retried against another host.
func isFallbackEligible(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRoomNotFound) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var signErr *streamSignError
	if errors.As(err, &signErr) {
		return false
	}
	return true
}

// fetchStreamURLs fetches stream URLs honoring the advanced fetch config:
// host fallback, `cdn` iteration and CDN collection. The returned list is
// unsorted; callers apply sortStreams/ForceSource ordering.
//
// RoomPlayExtra (subtitle metadata) is aggregated from every successful API
// response: the advanced path may issue several requests (host fallback, CDN
// iteration) and subtitle availability is sticky, so any response claiming it
// wins.
func (c *Client) fetchStreamURLs(roomID int, params map[string]string, options getStreamURLsOptions, cfg *StreamFetchConfig) ([]StreamURLInfo, *RoomPlayExtra, error) {
	hosts := streamAPIHostCandidates(cfg)

	cdns := []string{""}
	if cfg != nil && len(cfg.CDN) > 0 {
		cdns = cfg.CDN
	}
	collectAll := cfg != nil && cfg.CDNFallback

	var collected []StreamURLInfo
	var extra *RoomPlayExtra
	var lastErr error

	for _, cdn := range cdns {
		var lastFailureReason fetchFallbackReason
		cdnSucceeded := false

		for hostIdx, host := range hosts {
			infos, attemptExtra, reason, err := c.fetchStreamURLsFromHost(host, cdn, params, options)
			if err != nil {
				if !isFallbackEligible(err) {
					return nil, nil, err
				}
				lastErr = err
				lastFailureReason = reason
				if cfg != nil {
					log.With(
						"room", roomID,
						"api_host", host,
						"cdn", cdn,
						"fallback_reason", string(reason),
						"err", err,
					).Warn("B 站直播流 API 请求失败，尝试下一个地址")
				}
				continue
			}

			// A successful response is authoritative for subtitle metadata even
			// when it carries no playable URLs.
			extra = mergeRoomPlayExtra(extra, attemptExtra)

			if len(infos) == 0 {
				lastFailureReason = fallbackReasonEmpty
				if cfg != nil {
					log.With(
						"room", roomID,
						"api_host", host,
						"cdn", cdn,
						"fallback_reason", string(fallbackReasonEmpty),
					).Warn("B 站直播流 API 未返回可用地址，尝试下一个地址")
				}
				continue
			}

			if cfg != nil && hostIdx > 0 && lastFailureReason != "" {
				log.With(
					"room", roomID,
					"api_host", host,
					"cdn", cdn,
					"fallback_reason", string(lastFailureReason),
				).Info("B 站直播流 API 已切换到备用地址")
			}

			if !collectAll {
				return infos, extra, nil
			}
			collected = appendUniqueStreamURLInfos(collected, infos)
			cdnSucceeded = true
			break
		}

		_ = cdnSucceeded
	}

	if len(collected) > 0 {
		return collected, extra, nil
	}
	if lastErr != nil {
		return nil, nil, lastErr
	}
	return []StreamURLInfo{}, extra, nil
}

// mergeRoomPlayExtra folds one successful response's room-play metadata into
// the aggregate. Subtitle support is sticky: if any response reports it, the
// aggregate reports it.
func mergeRoomPlayExtra(dst, src *RoomPlayExtra) *RoomPlayExtra {
	if src == nil {
		return dst
	}
	if dst == nil {
		return &RoomPlayExtra{SubtitleEnabled: src.SubtitleEnabled}
	}
	dst.SubtitleEnabled = dst.SubtitleEnabled || src.SubtitleEnabled
	return dst
}

// fetchStreamURLsFromHost performs a single signed API request against one
// host, optionally with a `cdn` query parameter.
func (c *Client) fetchStreamURLsFromHost(host string, cdn string, params map[string]string, options getStreamURLsOptions) ([]StreamURLInfo, *RoomPlayExtra, fetchFallbackReason, error) {
	request := c.liveClient.R()

	queryParams := make(map[string]string, len(params)+1)
	for key, value := range params {
		queryParams[key] = value
	}
	if cdn != "" {
		queryParams["cdn"] = cdn
	}

	request.SetQueryParams(queryParams)
	signedQuery, err := c.wbi.SignQuery(request.QueryParam, time.Now())
	if err != nil {
		return nil, nil, fallbackReasonSign, &streamSignError{err: err}
	}
	request.QueryParam = signedQuery

	resp, err := request.Get(streamAPIURL(host))
	if err != nil {
		return nil, nil, fallbackReasonNetwork, err
	}
	if resp.StatusCode() != 200 {
		return nil, nil, fallbackReasonStatus, fmt.Errorf("状态码：%d", resp.StatusCode())
	}

	var sr StreamResponseV2
	if err := json.Unmarshal(resp.Body(), &sr); err != nil {
		return nil, nil, fallbackReasonDecode, err
	}
	if sr.Code != 0 {
		apiErr := mapStreamAPIError(sr.Code, sr.Message)
		if errors.Is(apiErr, ErrStreamGeoRestricted) {
			return nil, nil, fallbackReasonGeo, apiErr
		}
		return nil, nil, fallbackReasonAPI, apiErr
	}

	// Room-play metadata (AI subtitle availability) comes from the same
	// response that yields the play URLs.
	extra := &RoomPlayExtra{
		SubtitleEnabled: sr.Data.SubtitleCfg != nil && sr.Data.SubtitleCfg.Status == 1,
	}
	return buildStreamURLInfos(sr, options), extra, "", nil
}

// buildStreamURLInfos flattens the playurl response into StreamURLInfo entries
// (legacy behavior, unchanged).
func buildStreamURLInfos(sr StreamResponseV2, options getStreamURLsOptions) []StreamURLInfo {
	if sr.Data.PlayurlInfo == nil || sr.Data.PlayurlInfo.Playurl == nil {
		return []StreamURLInfo{}
	}

	streamInfos := make([]StreamURLInfo, 0)
	for _, stream := range sr.Data.PlayurlInfo.Playurl.Streams {
		for _, format := range stream.Formats {
			if !containFormat(options.profiles, format.FormatName) {
				continue
			}
			for _, codec := range format.Codecs {
				for _, urlInfo := range codec.UrlInfos {
					fullURL := urlInfo.Host + codec.BaseUrl + urlInfo.Extra
					audioOnlyStream := isAudioOnlyStreamURL(fullURL)
					// 雖然加了 only_audio=1/0 理應不會有非預期的流，但是再做一次檢測
					if options.onlyAudio != audioOnlyStream {
						continue
					}
					streamInfos = append(streamInfos, StreamURLInfo{
						Protocol:    stream.ProtocolName,
						Format:      format.FormatName,
						Codec:       codec.CodecName,
						URL:         fullURL,
						Qn:          codec.CurrentQn,
						AcceptQn:    codec.AcceptQn,
						IsAudioOnly: audioOnlyStream,
					})
				}
			}
		}
	}
	return streamInfos
}

// appendUniqueStreamURLInfos appends infos whose URL is not collected yet,
// preserving the first occurrence order.
func appendUniqueStreamURLInfos(dst []StreamURLInfo, src []StreamURLInfo) []StreamURLInfo {
	seen := make(map[string]struct{}, len(dst)+len(src))
	for _, info := range dst {
		seen[info.URL] = struct{}{}
	}
	for _, info := range src {
		if _, ok := seen[info.URL]; ok {
			continue
		}
		seen[info.URL] = struct{}{}
		dst = append(dst, info)
	}
	return dst
}

// applyForceSourcePreference moves TS/fMP4 profiles at qn>=10000 in front of
// the sorted list, but only when the caller's profile allow-list includes an
// HLS profile. Order inside each group is preserved.
func applyForceSourcePreference(streams []StreamURLInfo, options getStreamURLsOptions, cfg *StreamFetchConfig) []StreamURLInfo {
	if cfg == nil || !cfg.ForceSource || len(streams) < 2 {
		return streams
	}
	if !hlsProfilesAllowed(options.profiles) {
		return streams
	}

	preferred := make([]StreamURLInfo, 0, len(streams))
	rest := make([]StreamURLInfo, 0, len(streams))
	for _, info := range streams {
		if isForcedSourceProfile(info) {
			preferred = append(preferred, info)
		} else {
			rest = append(rest, info)
		}
	}
	if len(preferred) == 0 || len(rest) == 0 {
		return streams
	}

	out := make([]StreamURLInfo, 0, len(streams))
	out = append(out, preferred...)
	out = append(out, rest...)
	return out
}

// isForcedSourceProfile reports whether a stream is an acceptable
// force_source candidate: HLS TS/fMP4 at original quality or better.
func isForcedSourceProfile(info StreamURLInfo) bool {
	switch info.Format {
	case "ts", "fmp4":
		return info.Qn >= int(QualityOriginal)
	default:
		return false
	}
}

// hlsProfilesAllowed reports whether the caller allow-list includes TS/fMP4.
// An empty allow-list means "default (all formats)".
func hlsProfilesAllowed(profiles []StreamProfile) bool {
	if len(profiles) == 0 {
		return true
	}
	for _, profile := range profiles {
		if profile == ProfileHLSTS || profile == ProfileHLSFMP4 {
			return true
		}
	}
	return false
}

// applyCN01HostReplacement replaces *-cn-gotcha01* stream hosts with the
// configured candidates, probing each candidate with a 3-second range request
// and keeping the first 2xx. The original URL is kept when no candidate works.
//
// ReplaceCN01 is independent of ForceSource (§6.4): it applies whenever it is
// set and a resolved URL host matches.
func (c *Client) applyCN01HostReplacement(roomID int, streams []StreamURLInfo, cfg *StreamFetchConfig) []StreamURLInfo {
	if cfg == nil || len(cfg.ReplaceCN01) == 0 || len(streams) == 0 {
		return streams
	}

	out := make([]StreamURLInfo, len(streams))
	copy(out, streams)

	for idx := range out {
		parsed, err := url.Parse(out[idx].URL)
		if err != nil {
			continue
		}
		if !strings.Contains(strings.ToLower(parsed.Hostname()), cn01HostMarker) {
			continue
		}

		replaced, ok := c.probeCN01Candidates(parsed, cfg.ReplaceCN01)
		if !ok {
			log.With(
				"room", roomID,
				"old_host", parsed.Host,
				"replace_cn01", cfg.ReplaceCN01,
				"fallback_reason", "no_candidate_responding",
			).Warn("cn-gotcha01 地址替换失败，保留原始地址")
			continue
		}

		log.With(
			"room", roomID,
			"old_host", parsed.Host,
			"new_host", mustURLHost(replaced),
			"replace_cn01", cfg.ReplaceCN01,
		).Info("cn-gotcha01 地址替换成功")
		out[idx].URL = replaced
	}

	return out
}

// probeCN01Candidates shuffles the candidates and returns the first URL that
// answers a range probe with a 2xx status.
func (c *Client) probeCN01Candidates(original *url.URL, candidates []string) (string, bool) {
	order := make([]string, len(candidates))
	copy(order, candidates)
	rand.Shuffle(len(order), func(i, j int) {
		order[i], order[j] = order[j], order[i]
	})

	for _, candidate := range order {
		candidateURL, err := urlWithReplacedHost(original, candidate)
		if err != nil {
			continue
		}
		if c.probeStreamURL(candidateURL) {
			return candidateURL, true
		}
	}
	return "", false
}

// urlWithReplacedHost replaces the host (and optional scheme) of base with
// candidate, keeping path and query intact.
func urlWithReplacedHost(base *url.URL, candidate string) (string, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", errors.New("empty host candidate")
	}

	replaced := *base
	if strings.Contains(candidate, "://") {
		parsedCandidate, err := url.Parse(candidate)
		if err != nil || parsedCandidate.Host == "" {
			return "", fmt.Errorf("invalid host candidate %q", candidate)
		}
		replaced.Scheme = parsedCandidate.Scheme
		replaced.Host = parsedCandidate.Host
	} else {
		replaced.Host = candidate
	}
	return replaced.String(), nil
}

// probeStreamURL performs the §6 range probe: GET Range: bytes=0-1023 with a
// 3-second timeout; any 2xx counts as success.
func (c *Client) probeStreamURL(rawURL string) bool {
	timeout := c.cn01ProbeTimeout
	if timeout <= 0 {
		timeout = defaultStreamProbeTimeout
	}

	ctx := context.Background()
	if c.ctx != nil {
		ctx = c.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false
	}
	request.Header.Set("Range", "bytes=0-1023")
	request.Header.Set("Referer", liveReferer)
	request.Header.Set("User-Agent", liveUserAgent)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(request)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	// Drain a bounded amount so the probe does not waste bandwidth.
	_, _ = io.CopyN(io.Discard, resp.Body, 1024)

	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
}

func mustURLHost(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Host
}
