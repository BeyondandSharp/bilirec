package config

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/size"
	"github.com/bilirec/bilirec/utils"
	"go.uber.org/fx"
	"golang.org/x/crypto/bcrypt"
)

// FRP token injected at build-time via ldflags; defaults to empty.
// When empty, FRPToken remains empty unless overridden by FRP_TOKEN env var.
var frpTokenInjected = ""

const (
	officialFRPServer = "tunnel.bilirec.org:7000"
	officialFRPDomain = "tunnel.bilirec.org"
)

// all config will be loaded from environment variables
type Config struct {
	BilibiliLoginMode string // startup, controller, or anonymous (default: controller)
	Host              string
	Port              string
	TrustedProxies    []string

	MetricsEnabled bool
	MetricsHost    string
	MetricsPort    string

	VictoriaLogsEnabled      bool
	VictoriaLogsURL          string
	VictoriaLogsStreamFields string
	VictoriaLogsAccountID    string
	VictoriaLogsProjectID    string
	VictoriaLogsTimeout      time.Duration
	VictoriaLogsRetryMax     int

	LocalLogsEnabled       bool
	LocalLogsSplit         bool
	LocalLogsPath          string
	LocalLogsGroups        []string
	LocalLogsFormat        string
	LocalLogsMaxSizeMB     int
	LocalLogsMaxAgeDays    int
	LocalLogsMaxBackups    int
	LocalLogsCompress      bool
	LocalLogsBatchBytes    int
	LocalLogsFlushInterval time.Duration
	LocalLogsBufferSize    int
	LocalLogsOverflow      string

	FRPEnabled     bool
	FRPServer      string
	FRPToken       string
	FRPBaseDomain  string
	FRPHttps       bool
	FRPSchemeHttps bool

	MaxConcurrentRecordings   int
	MaxRecordingHours         int
	MaxRecoveryAttempts       int
	MaxRetrySeconds           int
	RecordingRecoveryDuration string // preserve or reset (default: preserve)

	OutputDir   string
	SecretDir   string
	DatabaseDir string

	ConvertToMp4             bool
	DeleteSourceAfterConvert bool
	NoConvertIfInvalid       bool

	// DanmakuOutputFormat selects the sidecar file format: jsonl or xml (default: jsonl).
	// Whether to record danmaku is per start option / room config (RecordDanmaku),
	// not a global switch; the bilibili danmaku protocol capability itself is always
	// available for other consumers (e.g. future websocket-based subcheck).
	DanmakuOutputFormat string
	// DanmakuOverflowPolicy controls the per-room message channel when full:
	// "drop" (default; never blocks the websocket) or "block" (wait for space;
	// prefer completeness, but a stalled disk may stall the danmaku connection).
	DanmakuOverflowPolicy string

	CloudConvertThreshold int64
	CloudConvertApiKey    string

	PublicBaseUrl      string
	FrontendURLs       []string // CORS AllowOrigins (normalized origins)
	WebPushSubscriber  string
	NotifySSEToken     string
	WebhookURLs        string // newline-separated BililiveRecorder v2 webhook targets
	Username           string
	PasswordHash       string
	ViewerUsername     string
	ViewerPasswordHash string
	JwtSecret          string

	ServerCrt string
	ServerKey string

	Debug          bool
	ProductionMode bool

	MinDiskSpaceBytes int64

	CloudConvertCheckIntervalSecs                       int
	CloudConvertMaxConcurrentDownloads                  int
	CloudConvertAllowDuringRecording                    bool
	CloudConvertAllowDuringRecordingMaxActiveRecordings int
	FFmpegCheckIntervalSecs                             int
	FFmpegMaxConcurrentTasks                            int
	FFmpegAllowDuringRecording                          bool
	FFmpegAllowDuringRecordingMaxActiveRecordings       int
	SubcheckRoomsPerShard                               int
	SubcheckTickSecs                                    int
	SubcheckMinIntervalSecs                             int
	SubcheckMaxIntervalSecs                             int
	SubcheckMaxShards                                   int
	SubcheckJitterSecs                                  int

	// StreamIdleTimeoutSecs: stream Read idle limit (seconds); 0 = off.
	StreamIdleTimeoutSecs int

	// External stream downloader (docs/INTERFACES.md §2.3).
	// Downloader is auto/native/ffmpeg/streamlink; auto behaves like native in
	// v1 (no external process is spawned).
	Downloader               string
	DownloaderFallback       []string
	FFmpegBin                string
	StreamlinkBin            string
	ExternalStartTimeoutSecs int

	// TitlePollSecs is the live room-title poll interval for SegmentByTitle
	// auto-split sessions (docs/FEATURES-V2.md §3.1); minimum 5s.
	TitlePollSecs int

	// FLV repair sidecar (docs/INTERFACES.md §2.3), consumed by services/repair.
	FLVRepair            string // auto/on/off
	FLVRepairBin         string
	FLVRepairMode        string // best-effort/strict
	FLVRepairMaxBufferMB int

	// configurable performances
	ReadStreamBytesPoolSize      int
	ReadStreamChanBufferSize     int
	readStreamBytesPoolSizeHigh  int
	readStreamChanBufferSizeHigh int

	// configurable global performances
	uploadBufferSize                       int
	downloadBufferSize                     int
	streamWriterBufferSize                 int
	liveStreamWriterBufferSize             int
	liveStreamWriterSyncPeriod             int
	liveStreamWriterColdCacheReleasePeriod int
	liveStreamWriterFlushPeriod            int
	liveStreamWriterChanBufferSize         int
	liveStreamWriterBytesPoolSize          int
	liveStreamWriterBytesPoolSizeHigh      int
	skipSmallFlushThreshold                int
	skipSmallFlush                         bool
	sequentialWrite                        bool
	dropFilePageCache                      bool

	// danmaku recording performances
	danmakuWriterBufferSize            int
	danmakuWriterMinPeriodicFlushBytes int
	danmakuWriterFlushPeriod           int
	danmakuWriterSyncPeriod            int
	danmakuChanBufferSize              int
	danmakuBytesPoolSize               int
}

var log = logger.Named("config")

func provider(lc fx.Lifecycle) (*Config, error) {

	// parse username and password
	username, passwordHash, err := parseUsernameAndPassword("USERNAME", "PASSWORD")
	if err != nil {
		return nil, err
	}

	// parse viewer username and password
	viewerUsername, viewerPasswordHash, viewerErr := parseUsernameAndPassword("VIEWER_USERNAME", "VIEWER_PASSWORD")
	if viewerErr != nil {
		return nil, viewerErr
	}

	frontendURLs, err := parseFrontendURLs()
	if err != nil {
		return nil, err
	}

	// parse debug

	debug := os.Getenv("DEBUG") == "true"

	if debug {
		logger.SetDebug(true)
	}

	frpServer := utils.EmptyOrElse(os.Getenv("FRP_SERVER"), officialFRPServer)
	frpBaseDomain := utils.EmptyOrElse(os.Getenv("FRP_BASE_DOMAIN"), officialFRPDomain)
	frpToken := resolveFRPToken(frpServer, frpBaseDomain)

	c := &Config{
		BilibiliLoginMode:        strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("BILIBILI_LOGIN_MODE"), "controller"))),
		Host:                     strings.TrimSpace(os.Getenv("HOST")),
		Port:                     utils.EmptyOrElse(os.Getenv("PORT"), "8080"),
		TrustedProxies:           parseCommaSeparatedValues(utils.EmptyOrElse(os.Getenv("TRUSTED_PROXIES"), "161.33.159.26")),
		MetricsEnabled:           os.Getenv("METRICS_ENABLED") == "true",
		MetricsHost:              strings.TrimSpace(os.Getenv("METRICS_HOST")),
		MetricsPort:              utils.EmptyOrElse(os.Getenv("METRICS_PORT"), "9090"),
		VictoriaLogsEnabled:      os.Getenv("VICTORIALOGS_ENABLED") == "true",
		VictoriaLogsURL:          strings.TrimSpace(os.Getenv("VICTORIALOGS_URL")),
		VictoriaLogsStreamFields: utils.EmptyOrElse(strings.TrimSpace(os.Getenv("VICTORIALOGS_STREAM_FIELDS")), "app,logger"),
		VictoriaLogsAccountID:    strings.TrimSpace(os.Getenv("VICTORIALOGS_ACCOUNT_ID")),
		VictoriaLogsProjectID:    strings.TrimSpace(os.Getenv("VICTORIALOGS_PROJECT_ID")),
		VictoriaLogsTimeout:      parseDurationOrDefault(os.Getenv("VICTORIALOGS_TIMEOUT"), 10*time.Second),
		VictoriaLogsRetryMax:     utils.MustAtoi(utils.EmptyOrElse(os.Getenv("VICTORIALOGS_RETRY_MAX"), "2")),
		LocalLogsEnabled:         os.Getenv("LOCAL_LOGS_ENABLED") == "true",
		LocalLogsSplit:           parseLocalLogsSplit(os.Getenv("LOCAL_LOGS_SPLIT")),
		LocalLogsPath:            strings.TrimSpace(os.Getenv("LOCAL_LOGS_PATH")),
		LocalLogsGroups:          parseLocalLogsGroups(),
		LocalLogsFormat:          strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("LOCAL_LOGS_FORMAT"), "pretty"))),
		LocalLogsMaxSizeMB:       parseSizeMiB(utils.EmptyOrElse(os.Getenv("LOCAL_LOGS_MAX_SIZE_MB"), "20"), 20),
		LocalLogsMaxAgeDays:      utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LOCAL_LOGS_MAX_AGE_DAYS"), "7")),
		LocalLogsMaxBackups:      utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LOCAL_LOGS_MAX_BACKUPS"), "3")),
		LocalLogsCompress:        os.Getenv("LOCAL_LOGS_COMPRESS") == "true",
		LocalLogsBatchBytes:      int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("LOCAL_LOGS_BATCH_BYTES"), "65536"), 65536)),
		LocalLogsFlushInterval:   parseDurationOrDefault(os.Getenv("LOCAL_LOGS_FLUSH_INTERVAL"), time.Second),
		LocalLogsBufferSize:      int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("LOCAL_LOGS_BUFFER_SIZE"), "4096"), 4096)),
		LocalLogsOverflow:        parseLocalLogsOverflow(os.Getenv("LOCAL_LOGS_OVERFLOW")),
		FRPEnabled:               os.Getenv("FRP_ENABLED") == "true",
		FRPServer:                frpServer,
		FRPToken:                 frpToken,
		FRPBaseDomain:            frpBaseDomain,
		FRPHttps:                 os.Getenv("FRP_HTTPS") == "true",
		FRPSchemeHttps:           utils.EmptyOrElse(os.Getenv("FRP_SCHEME_HTTPS"), "true") == "true",
		MaxConcurrentRecordings:  utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_CONCURRENT_RECORDINGS"), "3")),
		MaxRecordingHours:        utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_RECORDING_HOURS"), "0")),
		MaxRecoveryAttempts:      utils.MustAtoi(utils.EmptyOrElse(os.Getenv("MAX_RECOVERY_ATTEMPTS"), "15")),
		MaxRetrySeconds:          parseMaxRetrySeconds(),
		RecordingRecoveryDuration: strings.ToLower(strings.TrimSpace(
			utils.EmptyOrElse(os.Getenv("RECORDING_RECOVERY_DURATION"), "preserve"),
		)),
		OutputDir:                          utils.EmptyOrElse(os.Getenv("OUTPUT_DIR"), "records"),
		SecretDir:                          utils.EmptyOrElse(os.Getenv("SECRET_DIR"), "secrets"),
		DatabaseDir:                        utils.EmptyOrElse(os.Getenv("DATABASE_DIR"), "database"),
		CloudConvertThreshold:              parseSizeBytes(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_THRESHOLD"), "1073741824"), 1073741824), // 1 GB
		CloudConvertApiKey:                 os.Getenv("CLOUDCONVERT_API_KEY"),                                                                // empty to disable
		ConvertToMp4:                       os.Getenv("CONVERT_TO_MP4") == "true",
		NoConvertIfInvalid:                 os.Getenv("NO_CONVERT_IF_INVALID") == "true",
		DeleteSourceAfterConvert:           os.Getenv("DELETE_SOURCE_AFTER_CONVERT") == "true",
		DanmakuOutputFormat:                strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("DANMAKU_OUTPUT_FORMAT"), "jsonl"))),
		DanmakuOverflowPolicy:              strings.ToLower(strings.TrimSpace(utils.EmptyOrElse(os.Getenv("DANMAKU_OVERFLOW_POLICY"), "drop"))),
		FrontendURLs:                       frontendURLs,
		PublicBaseUrl:                      strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")),
		WebPushSubscriber:                  utils.EmptyOrElse(os.Getenv("WEBPUSH_SUBSCRIBER"), "mailto:webpush@example.com"),
		NotifySSEToken:                     strings.TrimSpace(os.Getenv("NOTIFY_SSE_TOKEN")),
		WebhookURLs:                        os.Getenv("WEBHOOK_URLS"),
		Username:                           username,
		PasswordHash:                       string(passwordHash),
		ViewerUsername:                     viewerUsername,
		ViewerPasswordHash:                 string(viewerPasswordHash),
		JwtSecret:                          utils.EmptyOrElse(os.Getenv("JWT_SECRET"), "bilirec_secret"),
		ServerCrt:                          os.Getenv("SERVER_CRT"),
		ServerKey:                          os.Getenv("SERVER_KEY"),
		Debug:                              debug,
		ProductionMode:                     os.Getenv("PRODUCTION_MODE") == "true",
		MinDiskSpaceBytes:                  parseSizeBytes(utils.EmptyOrElse(os.Getenv("MIN_DISK_SPACE_BYTES"), "5368709120"), 5368709120), // 5GB
		CloudConvertCheckIntervalSecs:      utils.MustAtoi(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_CHECK_INTERVAL_SECS"), "180")),
		CloudConvertMaxConcurrentDownloads: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS"), "1")),
		CloudConvertAllowDuringRecording:   os.Getenv("CLOUDCONVERT_ALLOW_DURING_RECORDING") == "true",
		CloudConvertAllowDuringRecordingMaxActiveRecordings: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("CLOUDCONVERT_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS"), "1")), // <1 = no limit; when >=1, cloudconvert during recording runs only if active recordings <= this value
		FFmpegCheckIntervalSecs:                             utils.MustAtoi(utils.EmptyOrElse(os.Getenv("FFMPEG_CHECK_INTERVAL_SECS"), "60")),
		FFmpegMaxConcurrentTasks:                            utils.MustAtoi(utils.EmptyOrElse(os.Getenv("FFMPEG_MAX_CONCURRENT_TASKS"), "1")),
		FFmpegAllowDuringRecording:                          os.Getenv("FFMPEG_ALLOW_DURING_RECORDING") == "true",
		FFmpegAllowDuringRecordingMaxActiveRecordings:       utils.MustAtoi(utils.EmptyOrElse(os.Getenv("FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS"), "1")), // <1 = no limit; when >=1, ffmpeg during recording runs only if active recordings <= this value
		SubcheckRoomsPerShard:                               utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_ROOMS_PER_SHARD"), "50")),
		SubcheckTickSecs:                                    utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_TICK_SECS"), "10")),
		SubcheckMinIntervalSecs:                             utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_MIN_INTERVAL_SECS"), "60")),
		SubcheckMaxIntervalSecs:                             utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_MAX_INTERVAL_SECS"), "300")),
		SubcheckMaxShards:                                   utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_MAX_SHARDS"), "32")),
		SubcheckJitterSecs:                                  utils.MustAtoi(utils.EmptyOrElse(os.Getenv("SUBCHECK_JITTER_SECS"), "2")),

		StreamIdleTimeoutSecs: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("STREAM_IDLE_TIMEOUT"), "20")), // 0 = off

		// external stream downloader (docs/INTERFACES.md §2.3)
		Downloader:               parseDownloader(os.Getenv("DOWNLOADER")),
		DownloaderFallback:       parseCommaSeparatedValues(utils.EmptyOrElse(os.Getenv("DOWNLOADER_FALLBACK"), "native")),
		FFmpegBin:                utils.EmptyOrElse(strings.TrimSpace(os.Getenv("FFMPEG_BIN")), "ffmpeg"),
		StreamlinkBin:            utils.EmptyOrElse(strings.TrimSpace(os.Getenv("STREAMLINK_BIN")), "streamlink"),
		ExternalStartTimeoutSecs: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("EXTERNAL_START_TIMEOUT_SECS"), "20")),

		TitlePollSecs: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("TITLE_POLL_SECS"), "20")),

		// flv repair sidecar (docs/INTERFACES.md §2.3)
		FLVRepair:            parseFLVRepair(os.Getenv("FLV_REPAIR")),
		FLVRepairBin:         utils.EmptyOrElse(strings.TrimSpace(os.Getenv("FLV_REPAIR_BIN")), "flvrepair"),
		FLVRepairMode:        parseFLVRepairMode(os.Getenv("FLV_REPAIR_MODE")),
		FLVRepairMaxBufferMB: parseSizeMiB(utils.EmptyOrElse(os.Getenv("FLV_REPAIR_MAX_BUFFER_MB"), "64"), 64),

		// stream performance configs
		ReadStreamBytesPoolSize:      int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("READ_STREAM_BYTES_POOL_SIZE"), "524288"), 524288)),        // default 512KB
		ReadStreamChanBufferSize:     utils.MustAtoi(utils.EmptyOrElse(os.Getenv("READ_STREAM_CHAN_BUFFER_SIZE"), "16")),                        // default 16
		readStreamBytesPoolSizeHigh:  int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("READ_STREAM_BYTES_POOL_SIZE_HIGH"), "1048576"), 1048576)), // default 1MB for 2K/4K
		readStreamChanBufferSizeHigh: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("READ_STREAM_CHAN_BUFFER_SIZE_HIGH"), "48")),                   // default 48 for 2K/4K

		// global performance configs
		uploadBufferSize:                       int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("UPLOAD_BUFFER_SIZE"), "5242880"), 5242880)),                      // default 5MB
		downloadBufferSize:                     int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("DOWNLOAD_BUFFER_SIZE"), "5242880"), 5242880)),                    // default 5MB
		streamWriterBufferSize:                 int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("STREAM_WRITER_BUFFER_SIZE"), "1048576"), 1048576)),               // default 1MB
		liveStreamWriterBufferSize:             int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_BUFFER_SIZE"), "8388608"), 8388608)),          // 8MB: prioritize lower flush frequency for SD card longevity
		liveStreamWriterSyncPeriod:             utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_SYNC_PERIOD_SECS"), "0")),                         // 0 = disabled; sync only on Close() to minimize SD card wear
		liveStreamWriterColdCacheReleasePeriod: utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_COLD_CACHE_RELEASE_SECS"), "60")),                 // periodic cold page-cache release during recording
		liveStreamWriterFlushPeriod:            utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS"), "15")),                       // default 15s: fewer flush operations, lower SD card wear
		liveStreamWriterChanBufferSize:         utils.MustAtoi(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE"), "64")),                        // default 64: limits in-flight memory while still tolerating write latency bursts
		liveStreamWriterBytesPoolSize:          int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_BYTES_POOL_SIZE"), "524288"), 524288)),        // 512KB per buffer
		liveStreamWriterBytesPoolSizeHigh:      int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("LIVE_STREAM_WRITER_BYTES_POOL_SIZE_HIGH"), "1048576"), 1048576)), // default 1MB for 2K/4K
		skipSmallFlushThreshold:                int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("SKIP_SMALL_FLUSH_THRESHOLD"), "1048576"), 1048576)),              // default 1MB: delay file creation until buffered bytes reach this threshold
		skipSmallFlush:                         os.Getenv("SKIP_SMALL_FLUSH") != "false",                                                                         // enabled by default; when true, SD-card protection mode is enabled
		sequentialWrite:                        os.Getenv("SEQUENTIAL_WRITE") != "false",                                                                         // enabled by default; set false to disable global flush serialization
		dropFilePageCache:                      os.Getenv("DROP_FILE_PAGE_CACHE") != "false",

		// danmaku recording performance configs (defaults tuned for Raspberry Pi 4B + SD card)
		danmakuWriterBufferSize:            int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("DANMAKU_WRITER_BUFFER_SIZE"), "262144"), 262144)),            // 256KB: absorb bursts; fewer mid-interval flushes on SD
		danmakuWriterMinPeriodicFlushBytes: int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("DANMAKU_WRITER_MIN_PERIODIC_FLUSH_BYTES"), "16384"), 16384)), // 16KB: skip tiny periodic flushes that contend with video
		danmakuWriterFlushPeriod:           utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DANMAKU_WRITER_FLUSH_PERIOD_SECS"), "15")),                       // align with video flush period to reduce SD card flush frequency
		danmakuWriterSyncPeriod:            utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DANMAKU_WRITER_SYNC_PERIOD_SECS"), "0")),                         // 0 = disabled; sync only on Close() to minimize SD card wear
		danmakuChanBufferSize:              utils.MustAtoi(utils.EmptyOrElse(os.Getenv("DANMAKU_CHAN_BUFFER_SIZE"), "256")),                              // message slots per room; overflow policy decides drop vs block
		danmakuBytesPoolSize:               int(parseSizeBytes(utils.EmptyOrElse(os.Getenv("DANMAKU_BYTES_POOL_SIZE"), "4096"), 4096)),                   // 4KB per fragment buffer
	}

	// Warn once when an external downloader/repair env value was invalid and
	// normalized back to its default (see parseDownloader / parseFLVRepair*).
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("DOWNLOADER"))); raw != "" && raw != c.Downloader {
		log.Warnf("DOWNLOADER 无效（%s），已回退为 auto（等同 native，不启用外部下载器）", raw)
	}
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("FLV_REPAIR"))); raw != "" && raw != c.FLVRepair {
		log.Warnf("FLV_REPAIR 无效（%s），已回退为 auto", raw)
	}
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("FLV_REPAIR_MODE"))); raw != "" && raw != c.FLVRepairMode {
		log.Warnf("FLV_REPAIR_MODE 无效（%s），已回退为 best-effort", raw)
	}

	ReadOnly = &GlobalReadOnly{config: c}

	lc.Append(fx.StartHook(func(context.Context) error {
		if err := ReadOnly.Validate(); err != nil {
			return err
		}
		if err := os.MkdirAll(c.OutputDir, 0755); err != nil {
			return err
		}
		if err := os.MkdirAll(c.DatabaseDir, 0755); err != nil {
			return err
		}
		if err := os.MkdirAll(c.SecretDir, 0700); err != nil {
			return err
		}
		return nil
	}))

	return c, nil
}

func parseUsernameAndPassword(usernameKey, passwordKey string) (string, string, error) {
	username := os.Getenv(usernameKey)
	password := os.Getenv(passwordKey)
	if username == "" || password == "" {
		return "", "", nil
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", "", err
	}
	return username, string(passwordHash), nil
}

// parseDownloader normalizes DOWNLOADER to auto/native/ffmpeg/streamlink.
// Unknown values fall back to auto (which behaves like native in v1).
func parseDownloader(raw string) string {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "auto", "native", "ffmpeg", "streamlink":
		return value
	default:
		return "auto"
	}
}

func parseFLVRepair(raw string) string {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "on", "off", "auto":
		return value
	default:
		return "auto"
	}
}

func parseFLVRepairMode(raw string) string {
	switch value := strings.ToLower(strings.TrimSpace(raw)); value {
	case "strict":
		return "strict"
	default:
		return "best-effort"
	}
}

func parseDurationOrDefault(raw string, fallback time.Duration) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

// allLogGroups is the fixed set exposed by the local-log source selector.
var allLogGroups = []string{"live", "system", "recorder", "access", "update"}

// parseLocalLogsGroups returns the selected local-log groups. A missing key
// means all groups (nil); an explicitly empty value means no named groups.
func parseLocalLogsGroups() []string {
	raw, ok := os.LookupEnv("LOCAL_LOGS_GROUPS")
	if !ok {
		return nil
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}
	known := make(map[string]struct{}, len(allLogGroups))
	for _, group := range allLogGroups {
		known[group] = struct{}{}
	}
	seen := make(map[string]struct{}, len(allLogGroups))
	out := make([]string, 0, len(allLogGroups))
	for _, part := range strings.Split(raw, ",") {
		group := strings.ToLower(strings.TrimSpace(part))
		if group == "" {
			continue
		}
		if _, ok := known[group]; !ok {
			continue
		}
		if _, ok := seen[group]; ok {
			continue
		}
		seen[group] = struct{}{}
		out = append(out, group)
	}
	return out
}

func parseLocalLogsSplit(raw string) bool {
	return !strings.EqualFold(strings.TrimSpace(raw), "false")
}

// parseSizeBytes parses a human-readable size with bare values interpreted as
// bytes. Invalid values fall back to the legacy default and log a warning.
func parseSizeBytes(raw string, fallback int64) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	value, err := size.ParseBytes(raw)
	if err != nil {
		log.Warnf("无效的字节大小 %q，使用默认值 %d：%v", raw, fallback, err)
		return fallback
	}
	return value
}

// parseSizeMiB parses a size for integer-MiB settings. Bare values are MiB;
// suffixed values are converted to MiB.
func parseSizeMiB(raw string, fallback int) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	value, err := size.ParseMiB(raw)
	if err != nil {
		log.Warnf("无效的大小 %q，使用默认值 %d MiB：%v", raw, fallback, err)
		return fallback
	}
	return value
}

// parseMaxRetrySeconds returns the stream-disconnect recovery window.
//
// MAX_RETRY_SECONDS is the current key (default 30). MAX_RETRY_MINUTES is kept
// as a compatibility alias for existing deployments; when both are set the
// seconds key wins. A non-positive value disables the recovery retry loop.
func parseMaxRetrySeconds() int {
	if raw := strings.TrimSpace(os.Getenv("MAX_RETRY_SECONDS")); raw != "" {
		return max(utils.MustAtoi(raw), 0)
	}
	if raw := strings.TrimSpace(os.Getenv("MAX_RETRY_MINUTES")); raw != "" {
		return max(utils.MustAtoi(raw), 0) * 60
	}
	return 30
}

func parseLocalLogsOverflow(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "block":
		return "block"
	default:
		return "drop"
	}
}

func parseCommaSeparatedValues(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	vals := make([]string, 0, len(parts))
	for _, part := range parts {
		v := strings.TrimSpace(part)
		if v == "" {
			continue
		}
		vals = append(vals, v)
	}
	return vals
}

const defaultFrontendOrigin = "https://app.bilirec.org"

func parseFrontendURLs() ([]string, error) {
	primary := strings.TrimSpace(os.Getenv("FRONTEND_URLS"))
	alias := strings.TrimSpace(os.Getenv("FRONTEND_URL"))

	raw := primary
	if raw == "" {
		raw = alias
	} else if alias != "" {
		log.Warn("FRONTEND_URLS 已设置，忽略 FRONTEND_URL 别名")
	}
	if raw == "" {
		raw = defaultFrontendOrigin
	}

	parts := utils.SplitAndTrim(raw, ",")
	origins := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if !utils.IsValidAbsoluteHTTPURL(part) {
			return nil, fmt.Errorf("无效的前端 URL: %s", part)
		}
		parsed, err := url.Parse(part)
		if err != nil {
			return nil, fmt.Errorf("无效的前端 URL: %s", part)
		}
		origin := parsed.Scheme + "://" + parsed.Host
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("无效的前端 URL: %s", raw)
	}
	return origins, nil
}

func resolveFRPToken(server, baseDomain string) string {
	token := os.Getenv("FRP_TOKEN")
	if token != "" {
		return token
	} else if server == officialFRPServer && baseDomain == officialFRPDomain {
		return frpTokenInjected
	} else {
		return ""
	}
}

var Module = fx.Module("config", fx.Provide(provider))
