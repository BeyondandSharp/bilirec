export interface LoginRequest {
  user: string
  pass: string
}

export interface LoginResponse {
  user: string
  role: string
}

export interface RoomInfo {
  room_id: number
  uid: number
  area_id: number
  live_status: number
  live_url: string
  parent_area_id: number
  title: string
  parent_area_name: string
  area_name: string
  live_time: string
  description: string
  tags: string
  attention: number
  online: number
  short_id: number
  uname: string
  cover: string
  background: string
  join_slide: number
  live_id: number
  live_id_str: string
  lock_status: number
  hidden_status: number
  is_encrypted: boolean
}

export interface RecordTask {
  roomId: number
  status: 'recording' | 'recovering' | 'idle'
  fileName?: string
  fileSize?: number
  /** Cumulative bytes downloaded from the stream in this session (backend >= with bytes_read). */
  bytesRead?: number
  recordDanmaku?: boolean
  danmakuFileSize?: number
  recordedTime?: number
  startTime?: string | number
  actualQn?: number
  actualStreamFormat?: string
  /** Active downloader for this session (`native` / `ffmpeg` / `streamlink`). */
  downloader?: string
  /** Upstream source format before an external downloader remuxes it (`flv` / `ts` / `fmp4`). */
  sourceFormat?: string
  /** FLV repair state for this session (`on` / `degraded` / `off`). */
  repair?: string
  isAudioOnly?: boolean
  sessionTitle?: string
  /** Recent download/write rates sampled between polls; absent until the second sample. */
  ioRate?: StreamIoRate
  error?: string
  roomInfo?: RoomInfo
}

/** Stream write health derived from cumulative lag and recent write/download rates. */
export type StreamIoHealth = 'healthy' | 'warning' | 'unhealthy'

/**
 * Recent stream I/O rates derived from consecutive stats polls (delta bytes / delta time).
 * Never derived from elapsed_seconds: both byte counters are session-cumulative and
 * survive segment rotation, so deltas stay correct across rotates.
 */
export interface StreamIoRate {
  /** Recent download rate in bytes/sec. */
  readBps: number
  /** Recent disk write rate in bytes/sec. */
  writeBps: number
  /** Cumulative in-session lag: bytes_read - bytes_written. */
  lagBytes: number
  /** True when the write rate has lagged behind the read rate for consecutive windows. */
  backpressure: boolean
  /** healthy → warning → unhealthy, from sustained write lag — not absolute buffer size. */
  health: StreamIoHealth
  /** Recent write/read rate ratio (0–100), aligned with the 75% lag threshold. */
  healthScorePercent: number
}

export interface RecorderStats {
  /** Cumulative bytes downloaded from the stream. Absent on older backends. */
  bytes_read?: number
  bytes_written: number
  danmaku_bytes_written?: number
  record_danmaku?: boolean
  elapsed_seconds: number
  start_time: number
  status: string
  output_path: string
  actual_qn?: number
  actual_stream_format?: string
  /** Active downloader (`native` / `ffmpeg` / `streamlink`); absent on older backends. */
  downloader?: string
  /** Upstream source format before an external downloader remuxes it (`flv` / `ts` / `fmp4`). */
  source_format?: string
  /** FLV repair state (`on` / `degraded` / `off`); absent on older backends. */
  repair?: string
  is_audio_only?: boolean
  room_title?: string
}

export type RecordStatus = 'recording' | 'recovering' | 'idle'
// Response from /record/{room}/info
export interface RecordInfo {
  room_id: number
  status: RecordStatus
}

export interface RecordFile {
  name: string
  size: number
  path: string
  is_dir: boolean
  is_recording: boolean
}

export interface RecordFileListResponse {
  total: number
  items: RecordFile[]
}

export interface ShareFileInfo {
  url: string
  expires_in: number
}

/** Presigned media URL minted by POST /files/presigned. */
export interface PresignedMediaUrl {
  /** Fully-resolvable URL (relative paths are prefixed with the API origin). */
  url: string
  /** Epoch ms when the token expires (best effort; 0 when unknown). */
  expiresAtMs: number
}

/** Per-recording downloader; 'auto' leaves the choice to the server default. */
export type DownloaderKind = 'auto' | 'native' | 'ffmpeg' | 'streamlink'

export interface StartRecordRequest {
  roomId: number
  durationMinutes?: number
  qn?: number
  onlyAudio?: boolean
  recordDanmaku?: boolean
  streamProfiles?: string[]
  deleteOldestOnLowDisk?: boolean
  downloader?: DownloaderKind
}

export interface ConvertProviders {
  providers: string[]
  enabled: boolean
}

export interface ConvertQueue {
  provider?: 'ffmpeg' | 'cloudconvert'
  task_id: string
  input_path: string
  input_file_size?: number
  output_path: string
  input_format: string
  output_format: string
  delete_source: boolean
}

export interface SubscriptionStatus {
  room_id: number
  is_subscribed: boolean
}

export interface SubscribedRooms {
  room_ids: number[]
}

export interface LiveStatus {
  live_status: number
}

/** Recording segmentation mode (FEATURES-V2 R02). */
export type SegmentMode = 'off' | 'time' | 'size'

/** Danmaku websocket transport (FEATURES-V2 R06). */
export type DanmakuTransport = 'auto' | 'ws' | 'wss'

/** Which interactive events are persisted to the danmaku sidecar (FEATURES-V2 R06). */
export interface DanmakuTypes {
  /** Persist unknown commands as raw JSONL records (XML format ignores this). */
  raw_events: boolean
  super_chat: boolean
  gift: boolean
  guard: boolean
}

/**
 * Global settings (FEATURES-V2 §1.1). `GET /settings` returns three copies:
 * `defaults` from env, `overrides` persisted values (partial), `values` merged.
 * Changes only apply to new recording sessions.
 */
export interface GlobalSettings {
  segment_mode: SegmentMode
  segment_minutes: number
  segment_size_mb: number
  segment_by_title: boolean
  min_file_size_mb: number
  filename_template: string
  title_filter_patterns: string
  save_stream_cover: boolean
  danmaku_types: DanmakuTypes
  danmaku_transport: DanmakuTransport
  flv_write_metadata: boolean
  flv_split_on_script_tag: boolean
  flv_disable_split_on_annexb: boolean
  bili_live_api_host: string
  bili_live_api_fallback_host: string
  bili_cdn: string[]
  bili_cdn_fallback: boolean
  bili_force_source: boolean
  bili_replace_cn01: string[]
}

/** Response of GET/PUT /settings and payload of POST /settings/reset. */
export interface SettingsResponse {
  /** Values parsed from env at startup (immutable). */
  defaults: GlobalSettings
  /** Only fields explicitly saved; absent = follow env default. */
  overrides: Partial<GlobalSettings>
  /** Defaults merged with overrides: the effective global settings. */
  values: GlobalSettings
  /** Where each value came from (CONFIG-V2 §1): env|file|dotenv|default. */
  sources?: Record<string, ConfigSource>
  /** Absolute path of the managed config file. */
  config_file?: string
  /** Absolute path of the managed secrets file. */
  secrets_file?: string
  /** Keys whose new value only applies after a backend restart. */
  restart_required?: string[]
}

/** Source of a managed config value (CONFIG-V2 §1). */
export type ConfigSource = 'env' | 'file' | 'secret-file' | 'dotenv' | 'default'

/** Three-language metadata string provided by the backend config schema. */
export interface LocalizedText {
  'zh-CN': string
  'zh-TW': string
  en: string
}

export type ConfigFieldKind =
  | 'number'
  | 'bool'
  | 'string'
  | 'textarea'
  | 'enum'
  | 'csv'
  | 'duration'
  | 'secret'

/** One managed env key as described by GET /config schema (CONFIG-V2 §5). */
export interface ConfigField {
  key: string
  kind: ConfigFieldKind
  default?: string
  min?: number | null
  max?: number | null
  enum?: string[] | null
  restart_required?: boolean
  sensitive?: boolean
  /** Tool dependency (e.g. "ffmpeg"); the field is disabled when unavailable. */
  requires?: string
  disabled_reason?: string
  label: LocalizedText
  description: LocalizedText
}

export interface ConfigGroup {
  id: string
  label: LocalizedText
  description: LocalizedText
  fields: ConfigField[]
}

export interface ConfigSchema {
  groups: ConfigGroup[]
}

/** Response of GET/PUT /config (CONFIG-V2 §4). */
export interface SystemConfigResponse {
  config_file?: string
  secrets_file?: string
  schema: ConfigSchema
  values: Record<string, string>
  overrides: Record<string, string>
  defaults: Record<string, string>
  sources: Record<string, ConfigSource>
  restart_required: string[]
}

/** GET /config/secrets: only whether each secret is set, never its value. */
export interface SecretStatusResponse {
  set: Record<string, boolean>
  /**
   * Sensitive-field metadata (3-language labels). The backend may return it as
   * a `schema.groups` document or as a flat `fields` array; both are handled.
   */
  schema?: ConfigSchema
  fields?: ConfigField[]
}

/** PUT /config and PUT /config/secrets body. */
export interface ConfigUpdateRequest {
  values: Record<string, string>
  clear?: string[]
}

/**
 * Per-room overrides (FEATURES-V2 §1.3). New fields are nullable: `null`
 * (or absent) means "inherit global"; legacy fields keep their old shape.
 */
export interface RoomConfig {
  room_id: number
  auto_record: boolean
  notify: boolean
  record_duration_minutes?: number
  qn?: number
  only_audio?: boolean
  record_danmaku?: boolean
  stream_profiles?: string[]
  delete_oldest_on_low_disk?: boolean
  segment_mode?: SegmentMode | null
  segment_minutes?: number | null
  segment_size_mb?: number | null
  segment_by_title?: boolean | null
  min_file_size_mb?: number | null
  filename_template?: string | null
  title_filter_patterns?: string | null
  save_stream_cover?: boolean | null
  danmaku_types?: DanmakuTypes | null
  danmaku_transport?: DanmakuTransport | null
  flv_write_metadata?: boolean | null
  flv_split_on_script_tag?: boolean | null
  flv_disable_split_on_annexb?: boolean | null
  bili_live_api_host?: string | null
  bili_live_api_fallback_host?: string | null
  bili_cdn?: string[] | null
  bili_cdn_fallback?: boolean | null
  bili_force_source?: boolean | null
  bili_replace_cn01?: string[] | null
}

export interface UpdateRoomConfigRequest {
  auto_record: boolean
  notify: boolean
  record_duration_minutes?: number
  qn?: number
  only_audio?: boolean
  record_danmaku?: boolean
  stream_profiles?: string[]
  delete_oldest_on_low_disk?: boolean
  /** Nullable overrides: `null`/absent = inherit global (FEATURES-V2 §1.3). */
  segment_mode?: SegmentMode | null
  segment_minutes?: number | null
  segment_size_mb?: number | null
  segment_by_title?: boolean | null
  min_file_size_mb?: number | null
  filename_template?: string | null
  title_filter_patterns?: string | null
  save_stream_cover?: boolean | null
  danmaku_types?: DanmakuTypes | null
  danmaku_transport?: DanmakuTransport | null
  flv_write_metadata?: boolean | null
  flv_split_on_script_tag?: boolean | null
  flv_disable_split_on_annexb?: boolean | null
  bili_live_api_host?: string | null
  bili_live_api_fallback_host?: string | null
  bili_cdn?: string[] | null
  bili_cdn_fallback?: boolean | null
  bili_force_source?: boolean | null
  bili_replace_cn01?: string[] | null
}

export interface DiskUsage {
  total: number
  used: number
  free: number
}

export interface WebPushKeys {
  auth: string
  p256dh: string
}

export interface WebPushPublicKeyResponse {
  enabled: boolean
  public_key: string
}

export interface WebPushSubscriptionRequest {
  endpoint: string
  keys: WebPushKeys
}

export interface WebPushUnsubscribeRequest {
  endpoint: string
}

export interface BilibiliAuthAccount {
  mid: number
  uname: string
}

export interface BilibiliAuthQr {
  url: string
}

export type AuthState = 'idle' | 'preloaded' | 'awaiting_qr' | 'authenticating' | 'authenticated' | 'failed' | 'qr_expired'

export interface BilibiliAuthInitResponse {
  qr?: BilibiliAuthQr
  error?: string
}

export interface BilibiliAuthStatus {
  authenticated: boolean
  state: AuthState
  account?: BilibiliAuthAccount
  qr?: BilibiliAuthQr
  lastError?: string
}

/** GET /logs response: tail of the current local log file (admin only, LOGS-VIEW contract). */
export interface LogsResponse {
  /** Effective LOCAL_LOGS_ENABLED (changing it requires a backend restart). */
  enabled: boolean
  /** Absolute path of the current log file. */
  path: string
  /** Whether the current log file exists yet. */
  exists: boolean
  /** File size in bytes. */
  size: number
  /** Last modification time (RFC 3339); empty when the file is missing. */
  mtime: string
  /** Log line format reported by the backend, e.g. "pretty" or "json". */
  format: string
  /** Tail lines, oldest first. */
  lines: string[]
  /** True when the read window or requested line count cut the output. */
  truncated: boolean
}

export interface ServerVersionResult {
  current: string
  latest: string
  outdated: boolean
  checked: boolean
  url: string
  error: string
  error_code?: string
  retry_after_secs?: number
}