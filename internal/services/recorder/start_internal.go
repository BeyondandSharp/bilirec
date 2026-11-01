package recorder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
	"github.com/bilirec/bilirec/internal/modules/config"
	rs "github.com/bilirec/bilirec/internal/record_strategies"
	"github.com/bilirec/bilirec/internal/services/downloader"
	"github.com/bilirec/bilirec/internal/services/repair"
	"github.com/bilirec/bilirec/internal/services/settings"
	"github.com/bilirec/bilirec/pkg/backoff"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/pool"
	"github.com/bilirec/bilirec/pkg/tx"
	"github.com/bilirec/bilirec/utils"
)

type startMode int

const (
	startModeUser startMode = iota
	startModeRecovery
)

type internalStartParams struct {
	roomId  int
	opts    RecordStartOptions
	ctx     context.Context
	cancel  context.CancelFunc // user mode only; adopted by Info on success
	mode    startMode
	session *Info // recovery only
}

func (r *Service) internalStart(p internalStartParams) error {
	l := log.With("room", p.roomId)

	r.m.StreamConnectAttempt(p.roomId)

	txn := r.reser.Begin()
	if err := txn.Reserve(p.roomId); err != nil {
		if errors.Is(err, tx.ErrAlreadyReserved) {
			return ErrRecordingPending
		}
		return err
	}
	defer txn.Abort(p.roomId)

	if err := p.ctx.Err(); err != nil {
		return err
	}

	if err := r.ensureDiskSpace(l, p); err != nil {
		return err
	}

	startTimeRoomInfo := time.Now()
	roomInfo, err := r.bilic.GetLiveRoomInfo(p.roomId)
	durationRoomInfo := time.Since(startTimeRoomInfo)
	l.Debugf("duration: function=GetLiveRoomInfo spent=%v", durationRoomInfo)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrLiveAPI, err)
	} else if roomInfo.IsEncrypted {
		return ErrRoomEncrypted
	} else if roomInfo.LockStatus != 0 {
		return ErrRoomBanned
	} else if roomInfo.LiveStatus != 1 {
		return ErrStreamNotLive
	}

	// Resolve the immutable settings snapshot once for this session start
	// (docs/FEATURES-V2.md §0/§1.2). Recovery reuses the original snapshot, so
	// settings changes only affect new sessions.
	effective := r.sessionEffective(p)
	fetchOptions := streamFetchOptions(effective)
	flvSplitOptions := rs.FlvSplitOptions{
		SplitOnScriptTag: effective.FlvSplitOnScriptTag,
		SplitOnAnnexB:    !effective.FlvDisableSplitOnAnnexB,
	}

	startTimeStreamURLs := time.Now()
	streams, playExtra, err := r.bilic.GetStreamURLsV2WithExtra(p.roomId, appendStreamOptions(p.opts.streamOptions, fetchOptions)...)
	durationStreamURLs := time.Since(startTimeStreamURLs)
	l.Debugf("duration: function=GetStreamURLsV2 spent=%v", durationStreamURLs)

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrLiveAPI, err)
	} else if len(streams) == 0 {
		return ErrEmptyStreamURLs
	}

	now := time.Now()
	ctx := p.ctx
	downloaderKind := resolveDownloaderKind(p.opts, r.cfg)

	for idx, streamInfo := range streams {
		if err := ctx.Err(); err != nil {
			return err
		}

		urlPreview := utils.TruncateString(streamInfo.URL, 160)
		l.Debugf("trying stream url [%d/%d]: protocol=%s, format=%s, codec=%s, qn=%d, url=%s",
			idx+1,
			len(streams),
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			urlPreview,
		)

		ch, strategy, chunkPool, actualFormat, usedDownloader, connected, err := r.connectStream(l, p.roomId, ctx, downloaderKind, fetchOptions, flvSplitOptions, streamInfo, urlPreview, idx, len(streams))
		if err != nil {
			return err
		}
		if !connected {
			continue
		}

		// FLV repair sidecar (docs/INTERFACES.md §2.2 item 6): wrap FLV
		// sessions after connectStream succeeds and before the session is
		// committed/prepared. HLS sessions are never wrapped.
		repairState := "off"
		if actualFormat == "flv" {
			wrapped, wrappedOK := r.wrapFlvRepair(ctx, p.roomId, ch, actualFormat, chunkPool, effective.FlvWriteMetadata)
			if wrappedOK {
				ch = wrapped
				repairState = "on"
			}
		}

		if !r.sessionReadyForConfirm(p) {
			discardStreamCh(ch, chunkPool)
			if err := ctx.Err(); err != nil {
				return err
			}
			return context.Canceled
		}

		var maxDuration time.Duration
		if p.opts.hasDuration {
			maxDuration = p.opts.duration
		} else {
			maxDuration = time.Duration(r.cfg.MaxRecordingHours) * time.Hour
		}

		info, err := r.commitSession(p, txn, roomInfo, now, maxDuration)
		if err != nil {
			discardStreamCh(ch, chunkPool)
			return err
		}
		if playExtra != nil {
			info.liveSubtitleEnabled.Store(playExtra.SubtitleEnabled)
		}
		info.chunkPool = chunkPool
		info.SetStream(streamInfo.Qn, streamInfo.IsAudioOnly, actualFormat)
		info.SetSessionSource(string(usedDownloader), streamInfo.Format, repairState)
		info.effective = effective
		l.Infof("session stream connected: downloader=%s repair=%s source_format=%s format=%s qn=%d audio_only=%t",
			usedDownloader, repairState, streamInfo.Format, actualFormat, streamInfo.Qn, streamInfo.IsAudioOnly)

		if p.mode == startModeUser {
			r.emitSessionStarted(info)
			r.startTitlePoller(p.roomId, info)
		}

		return r.prepare(p.roomId, ch, strategy, info.ctx, info, p.mode == startModeUser)
	}

	l.Warn("no more url left")
	return ErrStreamURLsUnreachable
}

func isInsufficientDiskSpace(free uint64, minimum int64) bool {
	if minimum <= 0 {
		return false
	}
	return free < uint64(minimum)
}

func (r *Service) sessionReadyForConfirm(p internalStartParams) bool {
	if err := p.ctx.Err(); err != nil {
		return false
	}
	if p.mode != startModeRecovery {
		return true
	}
	existing, ok := r.recording.Load(p.roomId)
	return ok && existing == p.session
}

func (r *Service) commitSession(
	p internalStartParams,
	txn tx.Txn[int],
	roomInfo *bilibili.LiveRoomInfoDetail,
	now time.Time,
	maxDuration time.Duration,
) (*Info, error) {
	if p.mode == startModeRecovery {
		info := p.session
		info.room = roomInfo
		info.fileTime = nextFileTime(info.fileTime, now)
		if r.cfg.RecordingRecoveryDuration == "reset" {
			info.startTime = now
		}
		// Recovery reuses the session's existing options. Keep them immutable
		// after the session is published so concurrent stats reads stay safe.
		txn.ConfirmWith(p.roomId, func() {
			info.status.Store(recordingPtr)
		})
		if err := info.ctx.Err(); err != nil {
			return nil, err
		}
		if existing, ok := r.recording.Load(p.roomId); !ok || existing != info {
			return nil, context.Canceled
		}
		return info, nil
	}

	sessionID, err := utils.NewUUIDv4()
	if err != nil {
		return nil, fmt.Errorf("无法生成录制会话 ID：%w", err)
	}

	info := &Info{
		ctx:          p.ctx,
		cancel:       p.cancel,
		startOptions: snapshotStartOptions(p.opts),
		startTime:    now,
		fileTime:     now,
		room:         roomInfo,
		maxDuration:  maxDuration,
		sessionID:    sessionID,
		backoff: backoff.NewSequence(
			2*time.Second,
			2*time.Second,
			2*time.Second,
			5*time.Second,
			10*time.Second,
			15*time.Second,
		),
	}
	info.SetOutputPath("")

	txn.ConfirmWith(p.roomId, func() {
		info.status.Store(recordingPtr)
		r.recording.Store(p.roomId, info)
		r.m.RecordingStarted(p.roomId, roomInfo.Uname)
	})

	return info, nil
}

// nextFileTime returns a second-granularity stamp strictly after prev when now
// would otherwise collide with prev's filename second.
func nextFileTime(prev, now time.Time) time.Time {
	stamp := now.Truncate(time.Second)
	prevStamp := prev.Truncate(time.Second)
	if !stamp.After(prevStamp) {
		return prevStamp.Add(time.Second)
	}
	return stamp
}

func (r *Service) connectStream(
	l logger.Logger,
	roomId int,
	ctx context.Context,
	downloaderKind downloader.Kind,
	fetchOptions []bilibili.GetStreamURLsOption,
	flvSplitOptions rs.FlvSplitOptions,
	streamInfo bilibili.StreamURLInfo,
	urlPreview string,
	idx, total int,
) (<-chan []byte, rs.RecordStrategy, *pool.BucketedBytesPool, string, downloader.Kind, bool, error) {
	var ch <-chan []byte
	var strategy rs.RecordStrategy
	actualFormat := streamInfo.Format
	// usedDownloader starts as native: it is upgraded only when the external
	// downloader actually starts (fallback still records as native).
	usedDownloader := downloader.KindNative

	chunkPool, releaseChunkPool := r.st.AcquireChunkPool(streamInfo.Qn)
	connected := false
	defer func() {
		if !connected {
			releaseChunkPool()
		}
	}()

	// External downloader path (docs/INTERFACES.md §2.2 item 5): ffmpeg or
	// streamlink remux the source to FLV, so the session always records FLV
	// and reports Format="flv". The original source format is logged.
	if downloaderKind.IsExternal() && supportsExternalDownloader(streamInfo.Format) {
		if extCh, extStrategy, ok := r.connectExternalStream(l, ctx, downloaderKind, streamInfo, chunkPool, releaseChunkPool, flvSplitOptions); ok {
			connected = true
			return extCh, extStrategy, chunkPool, "flv", downloaderKind, true, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, "", "", false, err
		}
		l.Warnf("外部下载器启动失败，回退到内置下载：downloader=%s source_format=%s (protocol=%s, format=%s, codec=%s, qn=%d, url=%s)",
			downloaderKind,
			streamInfo.Format,
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			urlPreview,
		)
	}

	switch streamInfo.Format {
	case "ts", "fmp4":
		l.Debugf("stream response [%d/%d]: protocol=%s, format=%s, codec=%s, qn=%d, req=%s",
			idx+1,
			total,
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			urlPreview,
		)

		initialURL := streamInfo.URL
		profile := utils.Ternary(
			streamInfo.Format == "ts",
			bilibili.ProfileHLSTS,
			bilibili.ProfileHLSFMP4,
		)

		fetchM3u8URL := func() (string, error) {
			if initialURL != "" {
				url := initialURL
				initialURL = ""
				return url, nil
			}

			latestStreams, fetchErr := r.bilic.GetStreamURLsV2(roomId,
				appendStreamOptions([]bilibili.GetStreamURLsOption{
					bilibili.WithProfiles(profile),
					bilibili.WithQn(bilibili.Quality(streamInfo.Qn)),
					bilibili.WithOnlyAudio(streamInfo.IsAudioOnly),
				}, fetchOptions)...,
			)
			if fetchErr != nil {
				return "", fetchErr
			} else if len(latestStreams) == 0 {
				return "", nil
			}

			tryResolve := func(candidate bilibili.StreamURLInfo) (string, bool) {
				fetchCtx := ctx
				if _, ok := fetchCtx.Deadline(); !ok {
					var cancel context.CancelFunc
					fetchCtx, cancel = context.WithTimeout(fetchCtx, 3*time.Second)
					defer cancel()
				}
				m3u8Resp, fetchErr := r.bilic.GetLiveHlsPlaylistClient().R().SetContext(fetchCtx).Get(candidate.URL)
				if fetchErr != nil {
					return "", false
				}
				if body := m3u8Resp.RawBody(); body != nil {
					defer body.Close()
				}
				if m3u8Resp.StatusCode() != 200 {
					return "", false
				}

				if m3u8Resp.RawResponse != nil && m3u8Resp.RawResponse.Request != nil && m3u8Resp.RawResponse.Request.URL != nil {
					return m3u8Resp.RawResponse.Request.URL.String(), true
				}
				return candidate.URL, true
			}

			for _, candidate := range latestStreams {
				if candidate.Format != streamInfo.Format || candidate.Protocol != streamInfo.Protocol || candidate.Codec != streamInfo.Codec {
					continue
				}
				if refreshedURL, ok := tryResolve(candidate); ok {
					return refreshedURL, nil
				}
			}

			for _, candidate := range latestStreams {
				if candidate.Format != streamInfo.Format {
					continue
				}
				if refreshedURL, ok := tryResolve(candidate); ok {
					return refreshedURL, nil
				}
			}

			return "", nil
		}

		hlsCh, hlsErr := r.st.ReadHlsStream(fetchM3u8URL, r.bilic.GetLiveHlsPlaylistClient(), r.bilic.GetLiveHlsSegmentClient(), ctx, streamInfo.Qn, chunkPool, releaseChunkPool)
		if hlsErr != nil {
			if errors.Is(hlsErr, context.Canceled) || ctx.Err() != nil {
				return nil, nil, nil, "", "", false, context.Canceled
			}
			l.Errorf("cannot start HLS stream: %v, will try next url", hlsErr)
			return nil, nil, nil, "", "", false, nil
		}
		ch = hlsCh
		strategy = utils.TernaryFunc(
			streamInfo.Format == "ts",
			func() rs.RecordStrategy { return rs.NewHlsTsStrategy(streamInfo.Qn) },
			func() rs.RecordStrategy { return rs.NewHlsFmp4Strategy(streamInfo.Qn) },
		)
		connected = true
	case "flv":
		startTimeFlv := time.Now()
		resp, err := r.bilic.FetchLiveStreamUrlWithCtx(streamInfo.URL, ctx)
		durationFlv := time.Since(startTimeFlv)
		l.Debugf("duration: function=FetchLiveStreamUrlWithCtx spent=%v", durationFlv)

		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil, nil, nil, "", "", false, context.Canceled
			}
			l.Errorf("cannot fetch url: %v, will try next url (protocol=%s, format=%s, codec=%s, qn=%d, url=%s)",
				err,
				streamInfo.Protocol,
				streamInfo.Format,
				streamInfo.Codec,
				streamInfo.Qn,
				urlPreview,
			)
			return nil, nil, nil, "", "", false, nil
		} else if resp.StatusCode() != 200 {
			drainAndCloseBody(resp.RawBody())
			l.Errorf("non-200 response: %d, will try next url (protocol=%s, format=%s, codec=%s, qn=%d, url=%s)",
				resp.StatusCode(),
				streamInfo.Protocol,
				streamInfo.Format,
				streamInfo.Codec,
				streamInfo.Qn,
				urlPreview,
			)
			return nil, nil, nil, "", "", false, nil
		}

		finalURL := ""
		if resp.RawResponse != nil && resp.RawResponse.Request != nil && resp.RawResponse.Request.URL != nil {
			finalURL = resp.RawResponse.Request.URL.String()
		}

		l.Debugf("stream response [%d/%d]: status=%d, content-type=%s, protocol=%s, format=%s, codec=%s, qn=%d, req=%s, final=%s",
			idx+1,
			total,
			resp.StatusCode(),
			resp.Header().Get("Content-Type"),
			streamInfo.Protocol,
			streamInfo.Format,
			streamInfo.Codec,
			streamInfo.Qn,
			urlPreview,
			utils.TruncateString(finalURL, 160),
		)

		flvCh, flvErr := r.st.ReadFlvStream(resp, ctx, streamInfo.Qn, chunkPool, releaseChunkPool)
		if flvErr != nil {
			drainAndCloseBody(resp.RawBody())
			l.Errorf("cannot capture url stream: %v, will try next url", flvErr)
			return nil, nil, nil, "", "", false, nil
		}
		ch = flvCh
		strategy = rs.NewFlvStrategyWithOptions(streamInfo.Qn, flvSplitOptions)
		connected = true
	default:
		return nil, nil, nil, "", "", false, fmt.Errorf("unsupported format: %s", streamInfo.Format)
	}

	return ch, strategy, chunkPool, actualFormat, usedDownloader, true, nil
}

func drainAndCloseBody(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}

// discardStreamCh drains a stream channel so its producer can finish, returning
// buffers to the pool.
//
// Note: this path does not force-close the underlying producer. For the native
// HTTP path and the external downloader path the producer stops on EOF/session
// context cancellation (a downloader process tree is killed by its context
// watcher when the session is cancelled, or by EOF when the stream ends). This
// mirrors the pre-existing native discard behavior; see Lead review note on T3.
func discardStreamCh(ch <-chan []byte, chunkPool *pool.BucketedBytesPool) {
	if ch == nil {
		return
	}
	go func() {
		for data := range ch {
			if chunkPool != nil && data != nil && cap(data) > 0 {
				chunkPool.Put(data[:cap(data)])
			}
		}
	}()
}

// supportsExternalDownloader reports whether an external downloader can fetch
// this source format. All supported formats are remuxed to FLV by ffmpeg.
func supportsExternalDownloader(format string) bool {
	switch format {
	case "flv", "ts", "fmp4":
		return true
	default:
		return false
	}
}

// resolveDownloaderKind returns the per-session downloader kind: the request
// override when present, otherwise the global DOWNLOADER setting. "auto" and
// unknown values behave like native in v1.
func resolveDownloaderKind(o RecordStartOptions, cfg *config.Config) downloader.Kind {
	if o.hasDownloader {
		return o.downloaderKind
	}
	if cfg == nil {
		return downloader.KindNative
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Downloader)) {
	case string(downloader.KindFFmpeg):
		return downloader.KindFFmpeg
	case string(downloader.KindStreamlink):
		return downloader.KindStreamlink
	default:
		return downloader.KindNative
	}
}

// downloaderFor returns the downloader for kind, or nil (native/unknown).
func (r *Service) downloaderFor(kind downloader.Kind) downloader.Downloader {
	if r.downloaders == nil {
		return nil
	}
	return r.downloaders.Get(kind)
}

// connectExternalStream opens an external downloader process tree and starts
// pumping its FLV output. It returns ok=false (so the caller can fall back to
// the native path for the same streamInfo) on spawn failure or when no output
// byte arrives before EXTERNAL_START_TIMEOUT_SECS.
func (r *Service) connectExternalStream(
	l logger.Logger,
	ctx context.Context,
	kind downloader.Kind,
	streamInfo bilibili.StreamURLInfo,
	chunkPool *pool.BucketedBytesPool,
	releasePool func(),
	flvSplitOptions rs.FlvSplitOptions,
) (<-chan []byte, rs.RecordStrategy, bool) {
	d := r.downloaderFor(kind)
	if d == nil {
		l.Warnf("未找到外部下载器：downloader=%s", kind)
		return nil, nil, false
	}

	var headers map[string]string
	if r.bilic != nil {
		headers = r.bilic.StreamRequestHeaders()
	}
	spec := downloader.Spec{
		URL:     streamInfo.URL,
		Format:  streamInfo.Format,
		Headers: headers,
	}

	start := time.Now()
	rc, err := d.Open(ctx, spec)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return nil, nil, false
		}
		l.Warnf("外部下载器启动失败：downloader=%s source_format=%s qn=%d err=%v", kind, streamInfo.Format, streamInfo.Qn, err)
		return nil, nil, false
	}

	ch := r.st.ReadExternalStream(rc, ctx, streamInfo.Qn, chunkPool, releasePool)
	l.Infof("外部下载器已连接：downloader=%s source_format=%s qn=%d first_byte=%v", kind, streamInfo.Format, streamInfo.Qn, time.Since(start))
	return ch, rs.NewFlvStrategyWithOptions(streamInfo.Qn, flvSplitOptions), true
}

// sessionEffective resolves the immutable settings snapshot for a session
// start. Recovery reuses the snapshot taken when the session first started so
// settings changes only affect new sessions (docs/FEATURES-V2.md §0).
func (r *Service) sessionEffective(p internalStartParams) settings.Effective {
	if p.mode == startModeRecovery && p.session != nil && p.session.effective.SegmentMode != "" {
		return p.session.effective
	}
	if r.settings == nil {
		return settings.Effective{}
	}
	return r.settings.Resolve(p.roomId)
}

// streamFetchOptions maps the effective Bilibili advanced-fetch settings to a
// GetStreamURLsV2 option (docs/FEATURES-V2.md §6). Defaults are a no-op:
// WithFetchConfig folds the official host into empty and keeps the legacy
// single-host request.
func streamFetchOptions(eff settings.Effective) []bilibili.GetStreamURLsOption {
	return []bilibili.GetStreamURLsOption{
		bilibili.WithFetchConfig(bilibili.StreamFetchConfig{
			APIHost:         eff.BiliLiveAPIHost,
			FallbackAPIHost: eff.BiliLiveAPIFallbackHost,
			CDN:             eff.BiliCDN,
			CDNFallback:     eff.BiliCDNFallback,
			ForceSource:     eff.BiliForceSource,
			ReplaceCN01:     eff.BiliReplaceCN01,
		}),
	}
}

// appendStreamOptions returns base + extra without mutating base's backing
// array (callers may reuse the start options across attempts).
func appendStreamOptions(base, extra []bilibili.GetStreamURLsOption) []bilibili.GetStreamURLsOption {
	out := make([]bilibili.GetStreamURLsOption, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}

// wrapFlvRepair wraps an FLV byte channel with the flvrepair sidecar when
// repair is enabled. Non-FLV formats and repair failures return the original
// channel so a repair problem can never abort a recording (docs §2.5).
func (r *Service) wrapFlvRepair(ctx context.Context, roomID int, ch <-chan []byte, format string, chunkPool *pool.BucketedBytesPool, writeMetadata bool) (<-chan []byte, bool) {
	if format != "flv" || r.repair == nil || !r.repair.Enabled() {
		return ch, false
	}
	wrapped, err := r.repair.WithRoom(roomID).WrapStream(ctx, ch, chunkPool, repair.SessionOptions{WriteMetadata: writeMetadata})
	if err != nil {
		log.Warnf("启用 FLV 修复失败，本次会话继续录制但不修复：%v", err)
		return ch, false
	}
	if wrapped == nil {
		return ch, false
	}
	return wrapped, true
}
