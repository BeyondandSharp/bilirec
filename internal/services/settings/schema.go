// SPDX-License-Identifier: GPL-3.0-only

// Code generated from docs/CONFIG-V2.md managed env keys; keep in sync with the os.Getenv keys.

package settings

import (
	"fmt"
	"strconv"
	"strings"
)

// LangText is a three-language label/description object.
type LangText struct {
	ZhCN string `json:"zh-CN"`
	ZhTW string `json:"zh-TW"`
	En   string `json:"en"`
}

// Field is one managed configuration key shown by /config or /settings.
type Field struct {
	Key             string   `json:"key"`
	Group           string   `json:"-"`
	Kind            string   `json:"kind"`
	Default         string   `json:"default"`
	Min             *int64   `json:"min"`
	Max             *int64   `json:"max"`
	Enum            []string `json:"enum"`
	RestartRequired bool     `json:"restart_required"`
	Sensitive       bool     `json:"sensitive"`
	Requires        string   `json:"requires"`
	DisabledReason  string   `json:"disabled_reason"`
	Label           LangText `json:"label"`
	Description     LangText `json:"description"`
}

// Group bundles schema fields by UI section.
type Group struct {
	ID          string   `json:"id"`
	Label       LangText `json:"label"`
	Description LangText `json:"description"`
	Fields      []Field  `json:"fields"`
}

// Schema is the full /config schema document (secrets excluded).
type Schema struct {
	Groups []Group `json:"groups"`
}

func ptrInt(v int64) *int64 { return &v }

var schemaGroups = []Group{

	{ID: "server", Label: LangText{ZhCN: "服务", ZhTW: "服務", En: "Server"}, Description: LangText{ZhCN: "监听地址与端口、认证账号、前端来源、TLS、运行模式等。", ZhTW: "監聽位址與連接埠、認證帳號、前端來源、TLS、執行模式等。", En: "Listen address/port, accounts, frontend origins, TLS and runtime mode."}},
	{ID: "storage", Label: LangText{ZhCN: "存储", ZhTW: "儲存", En: "Storage"}, Description: LangText{ZhCN: "输出目录、密钥目录、数据库目录与磁盘空间保护。", ZhTW: "輸出目錄、密鑰目錄、資料庫目錄與磁碟空間保護。", En: "Output/secrets/database directories and disk-space protection."}},
	{ID: "recording", Label: LangText{ZhCN: "录制", ZhTW: "錄製", En: "Recording"}, Description: LangText{ZhCN: "并发录制、分段轮转、恢复、流缓冲与空闲超时。", ZhTW: "並發錄製、分段輪轉、恢復、串流緩衝與閒置逾時。", En: "Concurrency, rotation, recovery, stream buffers and idle timeout."}},
	{ID: "danmaku", Label: LangText{ZhCN: "弹幕", ZhTW: "彈幕", En: "Danmaku"}, Description: LangText{ZhCN: "弹幕文件格式、写入缓冲、溢出策略与刷新周期。", ZhTW: "彈幕檔案格式、寫入緩衝、溢出策略與刷新週期。", En: "Danmaku output format, writer buffers, overflow policy and flush periods."}},
	{ID: "logging", Label: LangText{ZhCN: "日志", ZhTW: "日誌", En: "Logging"}, Description: LangText{ZhCN: "本地文件日志与 VictoriaLogs 输出。", ZhTW: "本機檔案日誌與 VictoriaLogs 輸出。", En: "Local file logging and VictoriaLogs output."}},
	{ID: "metrics", Label: LangText{ZhCN: "指标", ZhTW: "指標", En: "Metrics"}, Description: LangText{ZhCN: "Prometheus 指标端点。", ZhTW: "Prometheus 指標端點。", En: "Prometheus metrics endpoint."}},
	{ID: "frp", Label: LangText{ZhCN: "内网穿透", ZhTW: "內網穿透", En: "FRP"}, Description: LangText{ZhCN: "frp 内网穿透服务配置。", ZhTW: "frp 內網穿透服務設定。", En: "frp tunnel service configuration."}},
	{ID: "convert", Label: LangText{ZhCN: "转码", ZhTW: "轉碼", En: "Convert"}, Description: LangText{ZhCN: "FFmpeg / CloudConvert 转码与并发任务控制。", ZhTW: "FFmpeg / CloudConvert 轉碼與並發任務控制。", En: "FFmpeg / CloudConvert conversion and task concurrency."}},
	{ID: "subcheck", Label: LangText{ZhCN: "订阅检查", ZhTW: "訂閱檢查", En: "Subscription check"}, Description: LangText{ZhCN: "订阅自动开播检测的分片、间隔与抖动。", ZhTW: "訂閱自動開播偵測的分片、間隔與抖動。", En: "Sharding, intervals and jitter for auto-start subscription checks."}},
	{ID: "downloader", Label: LangText{ZhCN: "下载器", ZhTW: "下載器", En: "Downloader"}, Description: LangText{ZhCN: "外部流下载器选择与可执行文件路径。", ZhTW: "外部串流下載器選擇與可執行檔路徑。", En: "External stream downloader selection and binary paths."}},
	{ID: "repair", Label: LangText{ZhCN: "FLV 修复", ZhTW: "FLV 修復", En: "FLV repair"}, Description: LangText{ZhCN: "FLV 流修复 sidecar 的开关、模式与缓冲。", ZhTW: "FLV 串流修復 sidecar 的開關、模式與緩衝。", En: "FLV repair sidecar switch, mode and buffer."}},
	{ID: "update", Label: LangText{ZhCN: "更新检查", ZhTW: "更新檢查", En: "Update check"}, Description: LangText{ZhCN: "版本更新检查的仓库与 API 地址。", ZhTW: "版本更新檢查的儲存庫與 API 位址。", En: "Repository and API endpoints for version update checks."}},
	{ID: "features", Label: LangText{ZhCN: "录制特性", ZhTW: "錄製特性", En: "Recording features"}, Description: LangText{ZhCN: "FEATURES-V2 录制、弹幕与源选择特性（在 /settings 中编辑）。", ZhTW: "FEATURES-V2 錄製、彈幕與來源選擇特性（在 /settings 中編輯）。", En: "FEATURES-V2 recording, danmaku and source-selection features (edited in /settings)."}},
}

var fieldSpecs = []Field{

	{Key: "BILIBILI_LOGIN_MODE", Group: "server", Kind: "enum", Default: "controller", Min: nil, Max: nil, Enum: []string{"startup", "controller", "anonymous"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "哔哩哔哩登录模式", ZhTW: "嗶哩嗶哩登入模式", En: "Bilibili login mode"}, Description: LangText{ZhCN: "anonymous、controller 或 startup；决定启动时如何登录 B 站。", ZhTW: "anonymous、controller 或 startup；決定啟動時如何登入 B 站。", En: "anonymous, controller or startup; controls how Bilibili login is performed at boot."}},

	{Key: "BILI_CDN", Group: "features", Kind: "csv", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "B 站 CDN 列表", ZhTW: "B 站 CDN 清單", En: "Bilibili CDN list"}, Description: LangText{ZhCN: "强制使用的 CDN 名称，按顺序尝试。", ZhTW: "強制使用的 CDN 名稱，依序嘗試。", En: "Forced CDN names, tried in order."}},

	{Key: "BILI_CDN_FALLBACK", Group: "features", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "B 站 CDN 回退", ZhTW: "B 站 CDN 回退", En: "Bilibili CDN fallback"}, Description: LangText{ZhCN: "收集所有成功的 CDN 地址而不在第一个停止。", ZhTW: "收集所有成功的 CDN 位址而不在第一個停止。", En: "Collect all successful CDN URL sets instead of stopping at the first."}},

	{Key: "BILI_FORCE_SOURCE", Group: "features", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "强制源类型", ZhTW: "強制來源類型", En: "Force source profile"}, Description: LangText{ZhCN: "优先选择 TS/fMP4 源（qn ≥ 10000）。", ZhTW: "優先選擇 TS/fMP4 來源（qn ≥ 10000）。", En: "Prefer TS/fMP4 source profiles (qn >= 10000)."}},

	{Key: "BILI_LIVE_API_FALLBACK_HOST", Group: "features", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "B 站直播 API 备用主机", ZhTW: "B 站直播 API 備用主機", En: "Bilibili live API fallback host"}, Description: LangText{ZhCN: "网络错误或地理限制时回退使用。", ZhTW: "網路錯誤或地理限制時回退使用。", En: "Fallback host used on network errors or geo restrictions."}},

	{Key: "BILI_LIVE_API_HOST", Group: "features", Kind: "string", Default: "https://api.live.bilibili.com", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "B 站直播 API 主机", ZhTW: "B 站直播 API 主機", En: "Bilibili live API host"}, Description: LangText{ZhCN: "主直播 API 地址（需反代官方 API，含 WBI）。", ZhTW: "主要直播 API 位址（需反向代理官方 API，含 WBI）。", En: "Primary live API host (must reverse-proxy the official API including WBI)."}},

	{Key: "BILI_REPLACE_CN01", Group: "features", Kind: "csv", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "B 站 CN01 替换主机", ZhTW: "B 站 CN01 替換主機", En: "Bilibili CN01 replacement hosts"}, Description: LangText{ZhCN: "替换 *-cn-gotcha01* 的候选主机，逗号分隔。", ZhTW: "替換 *-cn-gotcha01* 的候選主機，以逗號分隔。", En: "Comma-separated candidate hosts replacing *-cn-gotcha01*."}},

	{Key: "CHECK_UPDATE", Group: "update", Kind: "bool", Default: "true", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "检查更新", ZhTW: "檢查更新", En: "Check for updates"}, Description: LangText{ZhCN: "启动时检查新版本。", ZhTW: "啟動時檢查新版本。", En: "Check for a newer release at startup."}},

	{Key: "CLOUDCONVERT_ALLOW_DURING_RECORDING", Group: "convert", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "录制期间允许 CloudConvert", ZhTW: "錄製期間允許 CloudConvert", En: "Allow CloudConvert during recording"}, Description: LangText{ZhCN: "允许在录制进行时运行 CloudConvert 任务。", ZhTW: "允許在錄製進行時執行 CloudConvert 任務。", En: "Allow CloudConvert jobs while recordings are running."}},

	{Key: "CLOUDCONVERT_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS", Group: "convert", Kind: "number", Default: "1", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "录制期间 CloudConvert 最大活跃录制数", ZhTW: "錄製期間 CloudConvert 最大活躍錄製數", En: "CloudConvert max active recordings"}, Description: LangText{ZhCN: "录制期间允许的 CloudConvert 任务对应的最大活跃录制数。", ZhTW: "錄製期間允許的 CloudConvert 任務對應的最大活躍錄製數。", En: "Maximum active recordings allowed when running CloudConvert during recording."}},

	{Key: "CLOUDCONVERT_CHECK_INTERVAL_SECS", Group: "convert", Kind: "number", Default: "180", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "CloudConvert 检查间隔（秒）", ZhTW: "CloudConvert 檢查間隔（秒）", En: "CloudConvert check interval"}, Description: LangText{ZhCN: "轮询 CloudConvert 任务状态的间隔（秒）。", ZhTW: "輪詢 CloudConvert 任務狀態的間隔（秒）。", En: "CloudConvert job polling interval in seconds."}},

	{Key: "CLOUDCONVERT_MAX_CONCURRENT_DOWNLOADS", Group: "convert", Kind: "number", Default: "1", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "CloudConvert 最大并发下载数", ZhTW: "CloudConvert 最大並發下載數", En: "CloudConvert max concurrent downloads"}, Description: LangText{ZhCN: "CloudConvert 任务结果的并发下载上限。", ZhTW: "CloudConvert 任務結果的並發下載上限。", En: "Concurrent download limit for CloudConvert job results."}},

	{Key: "CLOUDCONVERT_THRESHOLD", Group: "convert", Kind: "number", Default: "1073741824", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "CloudConvert 转码阈值（字节）", ZhTW: "CloudConvert 轉碼閾值（位元組）", En: "CloudConvert threshold (bytes)"}, Description: LangText{ZhCN: "文件大于该字节数时使用 CloudConvert。", ZhTW: "檔案大於該位元組數時使用 CloudConvert。", En: "Use CloudConvert when the file is larger than this many bytes."}},

	{Key: "CONVERT_TO_MP4", Group: "convert", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "ffmpeg", Label: LangText{ZhCN: "录制后转 MP4", ZhTW: "錄製後轉 MP4", En: "Convert to MP4"}, Description: LangText{ZhCN: "录制完成后自动转换为 MP4。", ZhTW: "錄製完成後自動轉換為 MP4。", En: "Automatically convert finished recordings to MP4."}},

	{Key: "DANMAKU_BYTES_POOL_SIZE", Group: "danmaku", Kind: "number", Default: "4096", Min: ptrInt(0), Max: ptrInt(1073741824), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕字节池大小", ZhTW: "彈幕位元組池大小", En: "Danmaku bytes pool"}, Description: LangText{ZhCN: "弹幕写入字节池的初始容量。", ZhTW: "彈幕寫入位元組池的初始容量。", En: "Initial capacity of the danmaku writer bytes pool."}},

	{Key: "DANMAKU_CHAN_BUFFER_SIZE", Group: "danmaku", Kind: "number", Default: "256", Min: ptrInt(1), Max: ptrInt(1048576), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕通道缓冲", ZhTW: "彈幕通道緩衝", En: "Danmaku channel buffer"}, Description: LangText{ZhCN: "弹幕消息通道的缓冲条目数。", ZhTW: "彈幕訊息通道的緩衝條目數。", En: "Number of buffered entries in the danmaku message channel."}},

	{Key: "DANMAKU_OUTPUT_FORMAT", Group: "danmaku", Kind: "enum", Default: "jsonl", Min: nil, Max: nil, Enum: []string{"jsonl", "xml"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕输出格式", ZhTW: "彈幕輸出格式", En: "Danmaku output format"}, Description: LangText{ZhCN: "jsonl 或 xml；xml 忽略原始事件。", ZhTW: "jsonl 或 xml；xml 忽略原始事件。", En: "jsonl or xml; xml ignores raw events."}},

	{Key: "DANMAKU_OVERFLOW_POLICY", Group: "danmaku", Kind: "enum", Default: "drop", Min: nil, Max: nil, Enum: []string{"drop", "block"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕溢出策略", ZhTW: "彈幕溢出策略", En: "Danmaku overflow policy"}, Description: LangText{ZhCN: "队列满时丢弃（drop）或阻塞（block）。", ZhTW: "佇列滿時丟棄（drop）或阻塞（block）。", En: "Drop or block when the danmaku queue is full."}},

	{Key: "DANMAKU_RECORD_TYPES", Group: "features", Kind: "csv", Default: "raw_events,super_chat,gift,guard", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕记录类型", ZhTW: "彈幕記錄類型", En: "Danmaku record types"}, Description: LangText{ZhCN: "raw_events、super_chat、gift、guard 的逗号分隔列表。", ZhTW: "raw_events、super_chat、gift、guard 的逗號分隔清單。", En: "Comma-separated list of raw_events, super_chat, gift and guard."}},

	{Key: "DANMAKU_TRANSPORT", Group: "features", Kind: "enum", Default: "auto", Min: nil, Max: nil, Enum: []string{"auto", "ws", "wss"}, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕传输方式", ZhTW: "彈幕傳輸方式", En: "Danmaku transport"}, Description: LangText{ZhCN: "auto（wss + 一次 ws 回退）、ws 或 wss。", ZhTW: "auto（wss + 一次 ws 回退）、ws 或 wss。", En: "auto (wss with one ws fallback), ws or wss."}},

	{Key: "DANMAKU_WRITER_BUFFER_SIZE", Group: "danmaku", Kind: "number", Default: "262144", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕写入缓冲大小", ZhTW: "彈幕寫入緩衝大小", En: "Danmaku writer buffer size"}, Description: LangText{ZhCN: "弹幕写入器缓冲区大小（字节）。", ZhTW: "彈幕寫入器緩衝區大小（位元組）。", En: "Danmaku writer buffer size in bytes."}},

	{Key: "DANMAKU_WRITER_FLUSH_PERIOD_SECS", Group: "danmaku", Kind: "number", Default: "15", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕刷新周期（秒）", ZhTW: "彈幕刷新週期（秒）", En: "Danmaku flush period"}, Description: LangText{ZhCN: "弹幕写入器刷新周期（秒）。", ZhTW: "彈幕寫入器刷新週期（秒）。", En: "Danmaku writer flush period in seconds."}},

	{Key: "DANMAKU_WRITER_MIN_PERIODIC_FLUSH_BYTES", Group: "danmaku", Kind: "number", Default: "16384", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕最小周期刷新字节", ZhTW: "彈幕最小週期刷新位元組", En: "Danmaku min periodic flush bytes"}, Description: LangText{ZhCN: "低于该字节数时不进行周期刷新。", ZhTW: "低於該位元組數時不進行週期刷新。", En: "Minimum bytes before a periodic danmaku flush happens."}},

	{Key: "DANMAKU_WRITER_SYNC_PERIOD_SECS", Group: "danmaku", Kind: "number", Default: "0", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "弹幕同步周期（秒）", ZhTW: "彈幕同步週期（秒）", En: "Danmaku sync period"}, Description: LangText{ZhCN: "弹幕写入器 fsync 周期（秒）。", ZhTW: "彈幕寫入器 fsync 週期（秒）。", En: "Danmaku writer fsync period in seconds."}},

	{Key: "DATABASE_DIR", Group: "storage", Kind: "string", Default: "database", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "数据库目录", ZhTW: "資料庫目錄", En: "Database directory"}, Description: LangText{ZhCN: "bbolt 数据库文件所在目录。", ZhTW: "bbolt 資料庫檔案所在目錄。", En: "Directory holding the bbolt database files."}},

	{Key: "DEBUG", Group: "server", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "调试日志", ZhTW: "偵錯日誌", En: "Debug logging"}, Description: LangText{ZhCN: "输出更详细的调试日志，用于排查问题。", ZhTW: "輸出更詳細的偵錯日誌，用於排查問題。", En: "Emit verbose debug logs for troubleshooting."}},

	{Key: "DELETE_SOURCE_AFTER_CONVERT", Group: "convert", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "ffmpeg", Label: LangText{ZhCN: "转换后删除源文件", ZhTW: "轉換後刪除來源檔", En: "Delete source after convert"}, Description: LangText{ZhCN: "转换成功后删除原始文件。", ZhTW: "轉換成功後刪除原始檔案。", En: "Delete the source file after a successful conversion."}},

	{Key: "DOWNLOADER", Group: "downloader", Kind: "enum", Default: "", Min: nil, Max: nil, Enum: []string{"auto", "native", "ffmpeg", "streamlink"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "外部下载器", ZhTW: "外部下載器", En: "External downloader"}, Description: LangText{ZhCN: "auto、native、ffmpeg 或 streamlink。", ZhTW: "auto、native、ffmpeg 或 streamlink。", En: "auto, native, ffmpeg or streamlink."}},

	{Key: "DOWNLOADER_FALLBACK", Group: "downloader", Kind: "enum", Default: "native", Min: nil, Max: nil, Enum: []string{"auto", "native", "ffmpeg", "streamlink"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "下载器回退顺序", ZhTW: "下載器回退順序", En: "Downloader fallback order"}, Description: LangText{ZhCN: "主下载器失败时的回退顺序。", ZhTW: "主下載器失敗時的回退順序。", En: "Fallback order used when the primary downloader fails."}},

	{Key: "DOWNLOAD_BUFFER_SIZE", Group: "recording", Kind: "number", Default: "5242880", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "下载缓冲大小", ZhTW: "下載緩衝大小", En: "Download buffer size"}, Description: LangText{ZhCN: "下载数据缓冲区大小（字节）。", ZhTW: "下載資料緩衝區大小（位元組）。", En: "Download buffer size in bytes."}},

	{Key: "DROP_FILE_PAGE_CACHE", Group: "recording", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "释放文件页缓存", ZhTW: "釋放檔案頁快取", En: "Drop file page cache"}, Description: LangText{ZhCN: "写入后释放文件页缓存以降低内存占用。", ZhTW: "寫入後釋放檔案頁快取以降低記憶體用量。", En: "Drop file page cache after writes to reduce memory usage."}},

	{Key: "EXTERNAL_START_TIMEOUT_SECS", Group: "downloader", Kind: "number", Default: "20", Min: ptrInt(1), Max: ptrInt(3600), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "外部下载器启动超时（秒）", ZhTW: "外部下載器啟動逾時（秒）", En: "External start timeout"}, Description: LangText{ZhCN: "等待外部下载器输出首字节的超时（秒）。", ZhTW: "等待外部下載器輸出首個位元組的逾時（秒）。", En: "Seconds to wait for the external downloader to emit its first byte."}},

	{Key: "FFMPEG_ALLOW_DURING_RECORDING", Group: "convert", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "ffmpeg", Label: LangText{ZhCN: "录制期间允许 FFmpeg", ZhTW: "錄製期間允許 FFmpeg", En: "Allow FFmpeg during recording"}, Description: LangText{ZhCN: "允许在录制进行时运行 FFmpeg 任务。", ZhTW: "允許在錄製進行時執行 FFmpeg 任務。", En: "Allow FFmpeg jobs while recordings are running."}},

	{Key: "FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS", Group: "convert", Kind: "number", Default: "1", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "ffmpeg", Label: LangText{ZhCN: "录制期间 FFmpeg 最大活跃录制数", ZhTW: "錄製期間 FFmpeg 最大活躍錄製數", En: "FFmpeg max active recordings"}, Description: LangText{ZhCN: "录制期间允许的 FFmpeg 任务对应的最大活跃录制数。", ZhTW: "錄製期間允許的 FFmpeg 任務對應的最大活躍錄製數。", En: "Maximum active recordings allowed when running FFmpeg during recording."}},

	{Key: "FFMPEG_BIN", Group: "convert", Kind: "string", Default: "ffmpeg", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "ffmpeg", Label: LangText{ZhCN: "FFmpeg 可执行文件", ZhTW: "FFmpeg 執行檔", En: "FFmpeg binary"}, Description: LangText{ZhCN: "FFmpeg 可执行文件路径。", ZhTW: "FFmpeg 執行檔路徑。", En: "Path to the FFmpeg binary."}},

	{Key: "FFMPEG_CHECK_INTERVAL_SECS", Group: "convert", Kind: "number", Default: "60", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "ffmpeg", Label: LangText{ZhCN: "FFmpeg 检查间隔（秒）", ZhTW: "FFmpeg 檢查間隔（秒）", En: "FFmpeg check interval"}, Description: LangText{ZhCN: "检查 FFmpeg 任务状态的间隔（秒）。", ZhTW: "檢查 FFmpeg 任務狀態的間隔（秒）。", En: "FFmpeg job check interval in seconds."}},

	{Key: "FFMPEG_MAX_CONCURRENT_TASKS", Group: "convert", Kind: "number", Default: "1", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "ffmpeg", Label: LangText{ZhCN: "FFmpeg 最大并发任务数", ZhTW: "FFmpeg 最大並發任務數", En: "FFmpeg max concurrent tasks"}, Description: LangText{ZhCN: "同时运行的 FFmpeg 任务上限。", ZhTW: "同時執行的 FFmpeg 任務上限。", En: "Upper bound of simultaneous FFmpeg jobs."}},

	{Key: "FILENAME_TEMPLATE", Group: "features", Kind: "string", Default: "{roomid}-{uname}/{date}_{time}_{title}", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "文件名模板", ZhTW: "檔名模板", En: "Filename template"}, Description: LangText{ZhCN: "相对输出目录的路径模板，可用 / 建子目录；留空使用旧版命名。", ZhTW: "相對於輸出目錄的路徑模板，可用 / 建立子目錄；留空使用舊版命名。", En: "Path template relative to OUTPUT_DIR; '/' creates subdirectories; empty keeps legacy naming."}},

	{Key: "FLV_DISABLE_SPLIT_ON_ANNEXB", Group: "features", Kind: "bool", Default: "true", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "禁用 Annex-B 切段", ZhTW: "停用 Annex-B 切段", En: "Disable Annex-B split"}, Description: LangText{ZhCN: "true 容忍 Annex-B NALU；false 时按 Annex-B 切段。", ZhTW: "true 容忍 Annex-B NALU；false 時依 Annex-B 切段。", En: "true tolerates Annex-B NALUs; false rotates on Annex-B."}},

	{Key: "FLV_REPAIR", Group: "repair", Kind: "enum", Default: "", Min: nil, Max: nil, Enum: []string{"on", "off", "auto"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "FLV 修复开关", ZhTW: "FLV 修復開關", En: "FLV repair switch"}, Description: LangText{ZhCN: "on、off 或 auto（auto 在 flvrepair 可用时启用）。", ZhTW: "on、off 或 auto（auto 在 flvrepair 可用時啟用）。", En: "on, off or auto (auto enables when flvrepair resolves)."}},

	{Key: "FLV_REPAIR_BIN", Group: "repair", Kind: "string", Default: "flvrepair", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "FLV 修复程序路径", ZhTW: "FLV 修復程式路徑", En: "FLV repair binary"}, Description: LangText{ZhCN: "flvrepair sidecar 可执行文件路径。", ZhTW: "flvrepair sidecar 執行檔路徑。", En: "Path to the flvrepair sidecar binary."}},

	{Key: "FLV_REPAIR_MAX_BUFFER_MB", Group: "repair", Kind: "number", Default: "64", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "FLV 修复最大缓冲（MB）", ZhTW: "FLV 修復最大緩衝（MB）", En: "FLV repair max buffer (MB)"}, Description: LangText{ZhCN: "flvrepair sidecar 的输入缓冲上限（MB）。", ZhTW: "flvrepair sidecar 的輸入緩衝上限（MB）。", En: "Input buffer cap of the flvrepair sidecar in megabytes."}},

	{Key: "FLV_REPAIR_MODE", Group: "repair", Kind: "enum", Default: "", Min: nil, Max: nil, Enum: []string{"strict", "best-effort"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "FLV 修复模式", ZhTW: "FLV 修復模式", En: "FLV repair mode"}, Description: LangText{ZhCN: "strict 或 best-effort。", ZhTW: "strict 或 best-effort。", En: "strict or best-effort."}},

	{Key: "FLV_SPLIT_ON_SCRIPT_TAG", Group: "features", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "按 Script 标签切段", ZhTW: "依 Script 標籤切段", En: "Split on script tag"}, Description: LangText{ZhCN: "媒体数据开始后出现 Script 标签时切段。", ZhTW: "媒體資料開始後出現 Script 標籤時切段。", En: "Rotate when a Script tag appears after media data started."}},

	{Key: "FLV_WRITE_METADATA", Group: "features", Kind: "bool", Default: "true", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "保留 FLV 元数据", ZhTW: "保留 FLV 中繼資料", En: "FLV write metadata"}, Description: LangText{ZhCN: "keep 保留、drop 移除 onMetaData 等脚本标签。", ZhTW: "keep 保留、drop 移除 onMetaData 等腳本標籤。", En: "keep preserves metadata; drop strips onMetaData script tags."}},

	{Key: "FRONTEND_URL", Group: "server", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "旧版前端地址", ZhTW: "舊版前端位址", En: "Legacy frontend URL"}, Description: LangText{ZhCN: "单个旧版前端的访问地址。", ZhTW: "單一舊版前端的存取位址。", En: "Base URL of the legacy single frontend."}},

	{Key: "FRONTEND_URLS", Group: "server", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "前端来源列表", ZhTW: "前端來源清單", En: "Frontend origins"}, Description: LangText{ZhCN: "允许跨域访问 API 的前端来源，逗号分隔。", ZhTW: "允許跨網域存取 API 的前端來源，以逗號分隔。", En: "Comma-separated origins allowed to call the API (CORS)."}},

	{Key: "FRP_BASE_DOMAIN", Group: "frp", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "FRP 基础域名", ZhTW: "FRP 基礎網域", En: "FRP base domain"}, Description: LangText{ZhCN: "frp 隧道使用的基础域名。", ZhTW: "frp 通道使用的基礎網域。", En: "Base domain used by frp tunnels."}},

	{Key: "FRP_ENABLED", Group: "frp", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "启用 FRP", ZhTW: "啟用 FRP", En: "Enable FRP"}, Description: LangText{ZhCN: "启用 frp 内网穿透客户端。", ZhTW: "啟用 frp 內網穿透用戶端。", En: "Enable the frp tunnel client."}},

	{Key: "FRP_HTTPS", Group: "frp", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "启用 FRP HTTPS", ZhTW: "啟用 FRP HTTPS", En: "FRP HTTPS"}, Description: LangText{ZhCN: "frp 隧道启用 HTTPS。", ZhTW: "frp 通道啟用 HTTPS。", En: "Enable HTTPS for frp tunnels."}},

	{Key: "FRP_SCHEME_HTTPS", Group: "frp", Kind: "string", Default: "true", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "FRP HTTPS 协议", ZhTW: "FRP HTTPS 協定", En: "FRP HTTPS scheme"}, Description: LangText{ZhCN: "frp 隧道协议是否使用 HTTPS。", ZhTW: "frp 通道協定是否使用 HTTPS。", En: "Whether the frp tunnel scheme uses HTTPS."}},

	{Key: "FRP_SERVER", Group: "frp", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "FRP 服务器地址", ZhTW: "FRP 伺服器位址", En: "FRP server"}, Description: LangText{ZhCN: "frp 服务端地址。", ZhTW: "frp 伺服器端位址。", En: "frp server address."}},

	{Key: "GITHUB_API_URL", Group: "update", Kind: "string", Default: "https://api.github.com", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "GitHub API 基础地址", ZhTW: "GitHub API 基礎位址", En: "GitHub API base URL"}, Description: LangText{ZhCN: "GitHub API 基础地址，官方地址表示不覆盖。", ZhTW: "GitHub API 基礎位址，官方位址表示不覆寫。", En: "GitHub API base URL; the official URL means no override."}},

	{Key: "HOST", Group: "server", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "监听地址", ZhTW: "監聽位址", En: "Listen host"}, Description: LangText{ZhCN: "HTTP 服务绑定地址，留空表示所有网卡。", ZhTW: "HTTP 服務綁定位址，留空表示所有網路卡。", En: "Bind address for the HTTP server; empty means all interfaces."}},

	{Key: "LIVE_STREAM_WRITER_BUFFER_SIZE", Group: "recording", Kind: "number", Default: "8388608", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "直播写入缓冲大小", ZhTW: "直播寫入緩衝大小", En: "Live writer buffer size"}, Description: LangText{ZhCN: "直播流写入缓冲区大小（字节）。", ZhTW: "直播串流寫入緩衝區大小（位元組）。", En: "Live stream writer buffer size in bytes."}},

	{Key: "LIVE_STREAM_WRITER_BYTES_POOL_SIZE", Group: "recording", Kind: "number", Default: "524288", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "直播写入字节池大小", ZhTW: "直播寫入位元組池大小", En: "Live writer bytes pool"}, Description: LangText{ZhCN: "直播写入器字节池的初始容量。", ZhTW: "直播寫入器位元組池的初始容量。", En: "Initial capacity of the live writer bytes pool."}},

	{Key: "LIVE_STREAM_WRITER_BYTES_POOL_SIZE_HIGH", Group: "recording", Kind: "number", Default: "1048576", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "直播写入字节池上限", ZhTW: "直播寫入位元組池上限", En: "Live writer bytes pool (high)"}, Description: LangText{ZhCN: "直播写入器字节池的最大容量。", ZhTW: "直播寫入器位元組池的最大容量。", En: "Maximum capacity of the live writer bytes pool."}},

	{Key: "LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE", Group: "recording", Kind: "number", Default: "64", Min: ptrInt(1), Max: ptrInt(1048576), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "直播写入通道缓冲", ZhTW: "直播寫入通道緩衝", En: "Live writer channel buffer"}, Description: LangText{ZhCN: "直播写入器通道的缓冲条目数。", ZhTW: "直播寫入器通道的緩衝條目數。", En: "Number of buffered entries in the live writer channel."}},

	{Key: "LIVE_STREAM_WRITER_COLD_CACHE_RELEASE_SECS", Group: "recording", Kind: "number", Default: "60", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "冷缓存释放间隔（秒）", ZhTW: "冷快取釋放間隔（秒）", En: "Cold cache release interval"}, Description: LangText{ZhCN: "释放直播写入器冷缓存的时间间隔（秒）。", ZhTW: "釋放直播寫入器冷快取的時間間隔（秒）。", En: "Interval in seconds between releasing the live writer cold cache."}},

	{Key: "LIVE_STREAM_WRITER_FLUSH_PERIOD_SECS", Group: "recording", Kind: "number", Default: "15", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "写入刷新周期（秒）", ZhTW: "寫入刷新週期（秒）", En: "Writer flush period"}, Description: LangText{ZhCN: "直播写入器刷新周期（秒）。", ZhTW: "直播寫入器刷新週期（秒）。", En: "Live writer flush period in seconds."}},

	{Key: "LIVE_STREAM_WRITER_SYNC_PERIOD_SECS", Group: "recording", Kind: "number", Default: "0", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "写入同步周期（秒）", ZhTW: "寫入同步週期（秒）", En: "Writer sync period"}, Description: LangText{ZhCN: "直播写入器 fsync 周期（秒）。", ZhTW: "直播寫入器 fsync 週期（秒）。", En: "Live writer fsync period in seconds."}},

	{Key: "LOCAL_LOGS_BATCH_BYTES", Group: "logging", Kind: "number", Default: "65536", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "日志批量字节数", ZhTW: "日誌批次位元組數", En: "Log batch bytes"}, Description: LangText{ZhCN: "日志批量写入的字节阈值。", ZhTW: "日誌批次寫入的位元組閾值。", En: "Byte threshold for batched log writes."}},

	{Key: "LOCAL_LOGS_BUFFER_SIZE", Group: "logging", Kind: "number", Default: "4096", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "日志缓冲大小", ZhTW: "日誌緩衝大小", En: "Log buffer size"}, Description: LangText{ZhCN: "日志写入缓冲区大小（字节）。", ZhTW: "日誌寫入緩衝區大小（位元組）。", En: "Log writer buffer size in bytes."}},

	{Key: "LOCAL_LOGS_COMPRESS", Group: "logging", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "压缩日志", ZhTW: "壓縮日誌", En: "Compress logs"}, Description: LangText{ZhCN: "压缩轮转后的日志文件。", ZhTW: "壓縮輪替後的日誌檔案。", En: "Compress rotated log files."}},

	{Key: "LOCAL_LOGS_ENABLED", Group: "logging", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "启用本地日志", ZhTW: "啟用本機日誌", En: "Enable local logs"}, Description: LangText{ZhCN: "把日志写入本地文件（带轮转）。", ZhTW: "把日誌寫入本機檔案（含輪替）。", En: "Write logs to local rotating files."}},

	{Key: "LOCAL_LOGS_FLUSH_INTERVAL", Group: "logging", Kind: "duration", Default: "1s", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "日志刷新间隔", ZhTW: "日誌刷新間隔", En: "Log flush interval"}, Description: LangText{ZhCN: "日志写入器的刷新间隔（Go duration，如 1s）。", ZhTW: "日誌寫入器的刷新間隔（Go duration，如 1s）。", En: "Log writer flush interval as a Go duration (e.g. 1s)."}},

	{Key: "LOCAL_LOGS_FORMAT", Group: "logging", Kind: "enum", Default: "pretty", Min: nil, Max: nil, Enum: []string{"pretty", "json"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "本地日志格式", ZhTW: "本機日誌格式", En: "Local log format"}, Description: LangText{ZhCN: "pretty 或 json。", ZhTW: "pretty 或 json。", En: "pretty or json."}},

	{Key: "LOCAL_LOGS_GROUPS", Group: "logging", Kind: "csv", Default: "live,system,recorder,access,update", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "日志类别", ZhTW: "日誌類別", En: "Log groups"}, Description: LangText{ZhCN: "选择写入本地文件的日志类别；按 live/system/recorder/access/update 分组。", ZhTW: "選擇寫入本機檔案的日誌類別；依 live/system/recorder/access/update 分組。", En: "Select which log groups are written to local files (live/system/recorder/access/update)."}},

	{Key: "LOCAL_LOGS_MAX_AGE_DAYS", Group: "logging", Kind: "number", Default: "7", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "日志保留天数", ZhTW: "日誌保留天數", En: "Log retention days"}, Description: LangText{ZhCN: "日志文件保留的最大天数。", ZhTW: "日誌檔案保留的最大天數。", En: "Maximum age of retained log files in days."}},

	{Key: "LOCAL_LOGS_MAX_BACKUPS", Group: "logging", Kind: "number", Default: "3", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "日志备份数", ZhTW: "日誌備份數", En: "Log backups"}, Description: LangText{ZhCN: "保留的轮转日志文件数。", ZhTW: "保留的輪替日誌檔案數。", En: "Number of rotated log files to keep."}},

	{Key: "LOCAL_LOGS_MAX_SIZE_MB", Group: "logging", Kind: "number", Default: "20", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "单文件最大大小（MB）", ZhTW: "單檔最大大小（MB）", En: "Max log file size (MB)"}, Description: LangText{ZhCN: "单个日志文件轮转前的大小上限（MB）。", ZhTW: "單一日誌檔案輪替前的大小上限（MB）。", En: "Rotate a log file after this many megabytes."}},

	{Key: "LOCAL_LOGS_OVERFLOW", Group: "logging", Kind: "enum", Default: "", Min: nil, Max: nil, Enum: []string{"block", "drop"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "日志溢出策略", ZhTW: "日誌溢出策略", En: "Log overflow policy"}, Description: LangText{ZhCN: "日志队列满时阻塞（block）或丢弃（drop）。", ZhTW: "日誌佇列滿時阻塞（block）或丟棄（drop）。", En: "Block or drop when the log queue is full."}},

	{Key: "LOCAL_LOGS_PATH", Group: "logging", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "本地日志路径", ZhTW: "本機日誌路徑", En: "Local log path"}, Description: LangText{ZhCN: "本地日志文件路径。", ZhTW: "本機日誌檔案路徑。", En: "Path of the local log file."}},

	{Key: "LOCAL_LOGS_SPLIT", Group: "logging", Kind: "bool", Default: "true", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "按来源拆分日志", ZhTW: "依來源拆分日誌", En: "Split logs by source"}, Description: LangText{ZhCN: "按 logger 来源拆分本地日志；网络请求日志单独存放。", ZhTW: "依 logger 來源拆分本機日誌；網路請求日誌單獨存放。", En: "Split local logs by logger source; network request logs go to separate files."}},

	{Key: "MAX_CONCURRENT_RECORDINGS", Group: "recording", Kind: "number", Default: "3", Min: ptrInt(1), Max: ptrInt(1000), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "最大并发录制数", ZhTW: "最大並發錄製數", En: "Max concurrent recordings"}, Description: LangText{ZhCN: "同时进行的录制任务上限。", ZhTW: "同時進行的錄製任務上限。", En: "Upper bound of simultaneous recordings."}},

	{Key: "MAX_RECORDING_HOURS", Group: "recording", Kind: "number", Default: "0", Min: ptrInt(0), Max: ptrInt(100000), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "单次录制最长小时", ZhTW: "單次錄製最長小時", En: "Max recording hours"}, Description: LangText{ZhCN: "单个录制会话的最长时长（小时）；0 表示不限时长。", ZhTW: "單一錄製工作階段的最長時長（小時）；0 表示不限時長。", En: "Maximum duration of a single recording session in hours; 0 means unlimited."}},

	{Key: "MAX_RECOVERY_ATTEMPTS", Group: "recording", Kind: "number", Default: "15", Min: ptrInt(0), Max: ptrInt(1000), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "最大恢复尝试次数", ZhTW: "最大恢復嘗試次數", En: "Max recovery attempts"}, Description: LangText{ZhCN: "录制中断后的最大恢复尝试次数。", ZhTW: "錄製中斷後的最大恢復嘗試次數。", En: "Maximum recovery attempts after a recording interruption."}},

	{Key: "MAX_RETRY_SECONDS", Group: "recording", Kind: "number", Default: "30", Min: ptrInt(0), Max: ptrInt(86400), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "最大恢复时长（秒）", ZhTW: "最大恢復時長（秒）", En: "Max recovery window"}, Description: LangText{ZhCN: "断流但直播仍在时的恢复尝试窗口（秒）；0 表示不重试。", ZhTW: "斷流但直播仍在時的恢復嘗試視窗（秒）；0 表示不重試。", En: "Recovery retry window in seconds while the room is still live; 0 disables retries."}},

	{Key: "METRICS_ENABLED", Group: "metrics", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "启用 Prometheus 指标", ZhTW: "啟用 Prometheus 指標", En: "Enable Prometheus metrics"}, Description: LangText{ZhCN: "暴露 Prometheus 指标端点。", ZhTW: "暴露 Prometheus 指標端點。", En: "Expose the Prometheus metrics endpoint."}},

	{Key: "METRICS_HOST", Group: "metrics", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "指标监听地址", ZhTW: "指標監聽位址", En: "Metrics listen host"}, Description: LangText{ZhCN: "指标端点的监听地址。", ZhTW: "指標端點的監聽位址。", En: "Listen address of the metrics endpoint."}},

	{Key: "METRICS_PORT", Group: "metrics", Kind: "number", Default: "9090", Min: ptrInt(1), Max: ptrInt(65535), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "指标监听端口", ZhTW: "指標監聽連接埠", En: "Metrics listen port"}, Description: LangText{ZhCN: "指标端点的监听端口，默认 9090。", ZhTW: "指標端點的監聽連接埠，預設 9090。", En: "Listen port of the metrics endpoint, default 9090."}},

	{Key: "MIN_DISK_SPACE_BYTES", Group: "storage", Kind: "number", Default: "5368709120", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "最小剩余磁盘空间", ZhTW: "最小剩餘磁碟空間", En: "Minimum free disk space"}, Description: LangText{ZhCN: "剩余空间低于该字节数时停止录制。", ZhTW: "剩餘空間低於該位元組數時停止錄製。", En: "Stop recording when free space drops below this many bytes."}},

	{Key: "MIN_FILE_SIZE_MB", Group: "features", Kind: "number", Default: "0", Min: ptrInt(0), Max: ptrInt(1024), Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "最小文件大小（MB）", ZhTW: "最小檔案大小（MB）", En: "Min file size (MB)"}, Description: LangText{ZhCN: "小于该大小的录制文件会被删除。", ZhTW: "小於該大小的錄製檔案會被刪除。", En: "Recordings smaller than this are removed."}},

	{Key: "NO_CONVERT_IF_INVALID", Group: "convert", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "ffmpeg", Label: LangText{ZhCN: "无效文件不转换", ZhTW: "無效檔案不轉換", En: "Skip invalid files"}, Description: LangText{ZhCN: "输入文件无效时跳过转换。", ZhTW: "輸入檔案無效時跳過轉換。", En: "Skip conversion when the input file is invalid."}},

	{Key: "OUTPUT_DIR", Group: "storage", Kind: "string", Default: "records", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "输出目录", ZhTW: "輸出目錄", En: "Output directory"}, Description: LangText{ZhCN: "录制文件根目录，路径模板相对此目录。", ZhTW: "錄製檔案根目錄，路徑模板相對於此目錄。", En: "Recording root directory; path templates are relative to it."}},

	{Key: "PORT", Group: "server", Kind: "number", Default: "8080", Min: ptrInt(1), Max: ptrInt(65535), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "监听端口", ZhTW: "監聽連接埠", En: "Listen port"}, Description: LangText{ZhCN: "HTTP 服务监听端口，默认 8080。", ZhTW: "HTTP 服務監聽連接埠，預設 8080。", En: "HTTP listen port, default 8080."}},

	{Key: "PRODUCTION_MODE", Group: "server", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "生产模式", ZhTW: "生產模式", En: "Production mode"}, Description: LangText{ZhCN: "启用面向生产环境的发布行为。", ZhTW: "啟用面向生產環境的發布行為。", En: "Enable production-oriented release behavior."}},

	{Key: "PUBLIC_BASE_URL", Group: "server", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "公开访问地址", ZhTW: "公開存取位址", En: "Public base URL"}, Description: LangText{ZhCN: "对外访问地址，用于生成外部链接。", ZhTW: "對外存取位址，用於產生外部連結。", En: "Externally visible base URL used in generated links."}},

	{Key: "READ_STREAM_BYTES_POOL_SIZE", Group: "recording", Kind: "number", Default: "524288", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "读取字节池大小", ZhTW: "讀取位元組池大小", En: "Read stream bytes pool"}, Description: LangText{ZhCN: "读取流字节池的初始容量。", ZhTW: "讀取串流位元組池的初始容量。", En: "Initial capacity of the read stream bytes pool."}},

	{Key: "READ_STREAM_BYTES_POOL_SIZE_HIGH", Group: "recording", Kind: "number", Default: "1048576", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "读取字节池上限", ZhTW: "讀取位元組池上限", En: "Read stream bytes pool (high)"}, Description: LangText{ZhCN: "读取流字节池的最大容量。", ZhTW: "讀取串流位元組池的最大容量。", En: "Maximum capacity of the read stream bytes pool."}},

	{Key: "READ_STREAM_CHAN_BUFFER_SIZE", Group: "recording", Kind: "number", Default: "16", Min: ptrInt(1), Max: ptrInt(1048576), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "读取通道缓冲", ZhTW: "讀取通道緩衝", En: "Read stream channel buffer"}, Description: LangText{ZhCN: "读取流通道的缓冲条目数。", ZhTW: "讀取串流通道的緩衝條目數。", En: "Number of buffered entries in the read stream channel."}},

	{Key: "READ_STREAM_CHAN_BUFFER_SIZE_HIGH", Group: "recording", Kind: "number", Default: "48", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "读取通道缓冲上限", ZhTW: "讀取通道緩衝上限", En: "Read stream channel buffer (high)"}, Description: LangText{ZhCN: "读取流通道的最大缓冲条目数。", ZhTW: "讀取串流通道的最大緩衝條目數。", En: "Maximum buffered entries in the read stream channel."}},

	{Key: "RECORDING_RECOVERY_DURATION", Group: "recording", Kind: "enum", Default: "preserve", Min: nil, Max: nil, Enum: []string{"preserve", "reset"}, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "恢复时长策略", ZhTW: "恢復時長策略", En: "Recovery duration policy"}, Description: LangText{ZhCN: "preserve 保留已录时长，reset 重新计时。", ZhTW: "preserve 保留已錄時長，reset 重新計時。", En: "preserve keeps the recorded duration, reset restarts the timer."}},

	{Key: "SAVE_STREAM_COVER", Group: "features", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "保存直播封面", ZhTW: "儲存直播封面", En: "Save stream cover"}, Description: LangText{ZhCN: "每个会话保存一次直播封面图。", ZhTW: "每個工作階段儲存一次直播封面圖。", En: "Save the live cover image once per session."}},

	{Key: "SECRET_DIR", Group: "storage", Kind: "string", Default: "secrets", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "密钥目录", ZhTW: "密鑰目錄", En: "Secret directory"}, Description: LangText{ZhCN: "JWT、Cookie 等运行密钥的存储目录。", ZhTW: "JWT、Cookie 等執行階段密鑰的儲存目錄。", En: "Storage directory for runtime secrets such as JWT and cookies."}},

	{Key: "SEGMENT_BY_TITLE", Group: "features", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "按标题分段", ZhTW: "依標題分段", En: "Rotate by title"}, Description: LangText{ZhCN: "直播标题变化时切段。", ZhTW: "直播標題變化時切段。", En: "Rotate when the live room title changes."}},

	{Key: "SEGMENT_MINUTES", Group: "features", Kind: "number", Default: "120", Min: ptrInt(1), Max: ptrInt(1440), Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "分段时长（分钟）", ZhTW: "分段時長（分鐘）", En: "Segment minutes"}, Description: LangText{ZhCN: "按时间切段的阈值（分钟，1–1440）。", ZhTW: "依時間切段的閾值（分鐘，1–1440）。", En: "Time threshold in minutes for time-based rotation (1-1440)."}},

	{Key: "SEGMENT_MODE", Group: "features", Kind: "enum", Default: "off", Min: nil, Max: nil, Enum: []string{"off", "time", "size"}, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "分段模式", ZhTW: "分段模式", En: "Segment mode"}, Description: LangText{ZhCN: "off、time 或 size；按时间或大小自动切段。", ZhTW: "off、time 或 size；依時間或大小自動切段。", En: "off, time or size; rotate automatically by duration or size."}},

	{Key: "SEGMENT_SIZE_MB", Group: "features", Kind: "number", Default: "2048", Min: ptrInt(16), Max: ptrInt(65536), Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "分段大小（MB）", ZhTW: "分段大小（MB）", En: "Segment size (MB)"}, Description: LangText{ZhCN: "按大小切段的阈值（MB，16–65536）。", ZhTW: "依大小切段的閾值（MB，16–65536）。", En: "Size threshold in MB for size-based rotation (16-65536)."}},

	{Key: "SEQUENTIAL_WRITE", Group: "recording", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "顺序写入", ZhTW: "順序寫入", En: "Sequential write"}, Description: LangText{ZhCN: "按顺序写入数据块以减少磁盘寻道。", ZhTW: "依序寫入資料塊以減少磁碟尋道。", En: "Write chunks sequentially to reduce disk seeks."}},

	{Key: "SERVER_CRT", Group: "server", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "TLS 证书路径", ZhTW: "TLS 憑證路徑", En: "TLS certificate path"}, Description: LangText{ZhCN: "HTTPS 证书文件路径（PEM）。", ZhTW: "HTTPS 憑證檔案路徑（PEM）。", En: "Path to the PEM TLS certificate."}},

	{Key: "SERVER_KEY", Group: "server", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "TLS 私钥路径", ZhTW: "TLS 私鑰路徑", En: "TLS private key path"}, Description: LangText{ZhCN: "HTTPS 私钥文件路径（PEM）。", ZhTW: "HTTPS 私鑰檔案路徑（PEM）。", En: "Path to the PEM TLS private key."}},

	{Key: "SKIP_SMALL_FLUSH", Group: "recording", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "跳过小块刷新", ZhTW: "跳過小塊刷新", En: "Skip small flush"}, Description: LangText{ZhCN: "小数据块写入时不立即刷新。", ZhTW: "小資料塊寫入時不立即刷新。", En: "Do not flush immediately for small chunks."}},

	{Key: "SKIP_SMALL_FLUSH_THRESHOLD", Group: "recording", Kind: "number", Default: "1048576", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "小块刷新阈值", ZhTW: "小塊刷新閾值", En: "Skip small flush threshold"}, Description: LangText{ZhCN: "低于该字节数的写入不立即刷新。", ZhTW: "低於該位元組數的寫入不立即刷新。", En: "Writes smaller than this many bytes are not flushed immediately."}},

	{Key: "STREAMLINK_BIN", Group: "downloader", Kind: "string", Default: "streamlink", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "Streamlink 可执行文件", ZhTW: "Streamlink 執行檔", En: "Streamlink binary"}, Description: LangText{ZhCN: "Streamlink 可执行文件路径。", ZhTW: "Streamlink 執行檔路徑。", En: "Path to the Streamlink binary."}},

	{Key: "STREAM_IDLE_TIMEOUT", Group: "recording", Kind: "number", Default: "20", Min: ptrInt(1), Max: ptrInt(100000), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "流空闲超时（秒）", ZhTW: "串流閒置逾時（秒）", En: "Stream idle timeout"}, Description: LangText{ZhCN: "直播流无数据超过该秒数时判定断开。", ZhTW: "直播串流無資料超過該秒數時判定中斷。", En: "Seconds without stream data before the connection is considered dead."}},

	{Key: "STREAM_WRITER_BUFFER_SIZE", Group: "recording", Kind: "number", Default: "1048576", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "流写入缓冲大小", ZhTW: "串流寫入緩衝大小", En: "Stream writer buffer size"}, Description: LangText{ZhCN: "流写入器缓冲区大小（字节）。", ZhTW: "串流寫入器緩衝區大小（位元組）。", En: "Stream writer buffer size in bytes."}},

	{Key: "SUBCHECK_JITTER_SECS", Group: "subcheck", Kind: "number", Default: "2", Min: ptrInt(0), Max: ptrInt(3600), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "检查抖动（秒）", ZhTW: "檢查抖動（秒）", En: "Check jitter"}, Description: LangText{ZhCN: "检查时间的随机抖动范围（秒）。", ZhTW: "檢查時間的隨機抖動範圍（秒）。", En: "Random jitter applied to check scheduling, in seconds."}},

	{Key: "SUBCHECK_MAX_INTERVAL_SECS", Group: "subcheck", Kind: "number", Default: "300", Min: ptrInt(1), Max: ptrInt(86400), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "最大检查间隔（秒）", ZhTW: "最大檢查間隔（秒）", En: "Max check interval"}, Description: LangText{ZhCN: "同一房间两次检查之间的最大间隔（秒）。", ZhTW: "同一房間兩次檢查之間的最大間隔（秒）。", En: "Maximum interval between two checks of the same room, in seconds."}},

	{Key: "SUBCHECK_MAX_SHARDS", Group: "subcheck", Kind: "number", Default: "32", Min: ptrInt(1), Max: ptrInt(1024), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "最大分片数", ZhTW: "最大分片數", En: "Max shards"}, Description: LangText{ZhCN: "订阅检查分片数的上限。", ZhTW: "訂閱檢查分片數的上限。", En: "Upper bound of subscription-check shards."}},

	{Key: "SUBCHECK_MIN_INTERVAL_SECS", Group: "subcheck", Kind: "number", Default: "60", Min: ptrInt(1), Max: ptrInt(86400), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "最小检查间隔（秒）", ZhTW: "最小檢查間隔（秒）", En: "Min check interval"}, Description: LangText{ZhCN: "同一房间两次检查之间的最小间隔（秒）。", ZhTW: "同一房間兩次檢查之間的最小間隔（秒）。", En: "Minimum interval between two checks of the same room, in seconds."}},

	{Key: "SUBCHECK_ROOMS_PER_SHARD", Group: "subcheck", Kind: "number", Default: "50", Min: ptrInt(1), Max: ptrInt(10000), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "每分片房间数", ZhTW: "每分片房間數", En: "Rooms per shard"}, Description: LangText{ZhCN: "每个分片承载的房间数量。", ZhTW: "每個分片承載的房間數量。", En: "Number of rooms handled by each shard."}},

	{Key: "SUBCHECK_TICK_SECS", Group: "subcheck", Kind: "number", Default: "10", Min: ptrInt(1), Max: ptrInt(86400), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "订阅检查节拍（秒）", ZhTW: "訂閱檢查節拍（秒）", En: "Subcheck tick"}, Description: LangText{ZhCN: "订阅检查调度器的节拍间隔（秒）。", ZhTW: "訂閱檢查排程器的節拍間隔（秒）。", En: "Subscription-check scheduler tick interval in seconds."}},

	{Key: "TITLE_FILTER_PATTERNS", Group: "features", Kind: "textarea", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "标题过滤规则", ZhTW: "標題過濾規則", En: "Title filter patterns"}, Description: LangText{ZhCN: "每行一个 RE2 正则；匹配时跳过自动录制。", ZhTW: "每行一個 RE2 正規表示式；符合時略過自動錄製。", En: "One RE2 regex per line; matching rooms are skipped for auto-record."}},

	{Key: "TITLE_POLL_SECS", Group: "recording", Kind: "number", Default: "20", Min: ptrInt(5), Max: ptrInt(86400), Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "标题轮询间隔（秒）", ZhTW: "標題輪詢間隔（秒）", En: "Title poll interval"}, Description: LangText{ZhCN: "按标题切段时的标题检查间隔（秒）。", ZhTW: "依標題切段時的標題檢查間隔（秒）。", En: "Title check interval in seconds when rotating by title."}},

	{Key: "TRUSTED_PROXIES", Group: "server", Kind: "string", Default: "161.33.159.26", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "可信代理列表", ZhTW: "可信代理清單", En: "Trusted proxies"}, Description: LangText{ZhCN: "可信反向代理地址，逗号分隔，用于获取真实客户端 IP。", ZhTW: "可信反向代理位址，以逗號分隔，用於取得真實用戶端 IP。", En: "Comma-separated trusted proxy addresses for the real client IP."}},

	{Key: "TZ", Group: "server", Kind: "string", Default: "Asia/Hong_Kong", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "时区", ZhTW: "時區", En: "Timezone"}, Description: LangText{ZhCN: "IANA 时区名称，例如 Asia/Hong_Kong。", ZhTW: "IANA 時區名稱，例如 Asia/Hong_Kong。", En: "IANA timezone name, e.g. Asia/Hong_Kong."}},

	{Key: "UPDATE_CHECK_API_URL", Group: "update", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "GitHub API 地址（可选）", ZhTW: "GitHub API 位址（可選）", En: "GitHub API URL (optional)"}, Description: LangText{ZhCN: "可选，优先于 GITHUB_API_URL。", ZhTW: "可選，優先於 GITHUB_API_URL。", En: "Optional; takes precedence over GITHUB_API_URL."}},

	{Key: "UPDATE_CHECK_RELEASE_URL", Group: "update", Kind: "string", Default: "https://github.com/BeyondandSharp/bilirec/releases/latest", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "发布页地址", ZhTW: "發布頁位址", En: "Release page URL"}, Description: LangText{ZhCN: "新版本链接使用的绝对 http(s) 发布页地址。", ZhTW: "新版本連結使用的絕對 http(s) 發布頁位址。", En: "Absolute http(s) release-page URL used for new-version links."}},

	{Key: "UPDATE_CHECK_REPO", Group: "update", Kind: "string", Default: "BeyondandSharp/bilirec", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "更新检查仓库", ZhTW: "更新檢查儲存庫", En: "Update check repository"}, Description: LangText{ZhCN: "owner/repo 或 GitHub 仓库地址。", ZhTW: "owner/repo 或 GitHub 儲存庫位址。", En: "owner/repo or a GitHub repository URL."}},

	{Key: "UPLOAD_BUFFER_SIZE", Group: "recording", Kind: "number", Default: "5242880", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "上传缓冲大小", ZhTW: "上傳緩衝大小", En: "Upload buffer size"}, Description: LangText{ZhCN: "上传数据缓冲区大小（字节）。", ZhTW: "上傳資料緩衝區大小（位元組）。", En: "Upload buffer size in bytes."}},

	{Key: "USERNAME", Group: "server", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "管理员用户名", ZhTW: "管理員使用者名稱", En: "Admin username"}, Description: LangText{ZhCN: "与密码一起启用 JWT 认证并授予管理员权限。", ZhTW: "與密碼一起啟用 JWT 認證並授予管理員權限。", En: "Enables JWT auth together with the password and grants admin rights."}},

	{Key: "VICTORIALOGS_ACCOUNT_ID", Group: "logging", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "VictoriaLogs 账户 ID", ZhTW: "VictoriaLogs 帳戶 ID", En: "VictoriaLogs account ID"}, Description: LangText{ZhCN: "VictoriaLogs 多租户账户 ID。", ZhTW: "VictoriaLogs 多租戶帳戶 ID。", En: "VictoriaLogs tenant account ID."}},

	{Key: "VICTORIALOGS_ENABLED", Group: "logging", Kind: "bool", Default: "false", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "启用 VictoriaLogs", ZhTW: "啟用 VictoriaLogs", En: "Enable VictoriaLogs"}, Description: LangText{ZhCN: "把日志发送到 VictoriaLogs。", ZhTW: "把日誌傳送到 VictoriaLogs。", En: "Ship logs to VictoriaLogs."}},

	{Key: "VICTORIALOGS_PROJECT_ID", Group: "logging", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "VictoriaLogs 项目 ID", ZhTW: "VictoriaLogs 專案 ID", En: "VictoriaLogs project ID"}, Description: LangText{ZhCN: "VictoriaLogs 多租户项目 ID。", ZhTW: "VictoriaLogs 多租戶專案 ID。", En: "VictoriaLogs tenant project ID."}},

	{Key: "VICTORIALOGS_RETRY_MAX", Group: "logging", Kind: "number", Default: "2", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "VictoriaLogs 最大重试次数", ZhTW: "VictoriaLogs 最大重試次數", En: "VictoriaLogs max retries"}, Description: LangText{ZhCN: "发送失败时的最大重试次数。", ZhTW: "傳送失敗時的最大重試次數。", En: "Maximum retries when shipping to VictoriaLogs fails."}},

	{Key: "VICTORIALOGS_STREAM_FIELDS", Group: "logging", Kind: "string", Default: "app,logger", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "VictoriaLogs 流字段", ZhTW: "VictoriaLogs 串流欄位", En: "VictoriaLogs stream fields"}, Description: LangText{ZhCN: "作为流标签的字段，逗号分隔。", ZhTW: "作為串流標籤的欄位，以逗號分隔。", En: "Comma-separated fields used as stream labels."}},

	{Key: "VICTORIALOGS_TIMEOUT", Group: "logging", Kind: "duration", Default: "10s", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "VictoriaLogs 请求超时", ZhTW: "VictoriaLogs 請求逾時", En: "VictoriaLogs timeout"}, Description: LangText{ZhCN: "请求 VictoriaLogs 的超时时间。", ZhTW: "請求 VictoriaLogs 的逾時時間。", En: "Request timeout for VictoriaLogs."}},

	{Key: "VICTORIALOGS_URL", Group: "logging", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "VictoriaLogs 地址", ZhTW: "VictoriaLogs 位址", En: "VictoriaLogs URL"}, Description: LangText{ZhCN: "VictoriaLogs 摄入接口地址。", ZhTW: "VictoriaLogs 擷取介面位址。", En: "VictoriaLogs ingestion endpoint URL."}},

	{Key: "VIEWER_USERNAME", Group: "server", Kind: "string", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "访客用户名", ZhTW: "訪客使用者名稱", En: "Viewer username"}, Description: LangText{ZhCN: "只读访客账号的用户名。", ZhTW: "唯讀訪客帳號的使用者名稱。", En: "Username of the read-only viewer account."}},

	{Key: "WEBHOOK_URLS", Group: "server", Kind: "textarea", Default: "", Min: nil, Max: nil, Enum: nil, RestartRequired: false, Sensitive: false, Requires: "", Label: LangText{ZhCN: "Webhook 地址列表", ZhTW: "Webhook 位址清單", En: "Webhook URLs"}, Description: LangText{ZhCN: "录制事件通知的 Webhook 地址，每行一个。", ZhTW: "錄製事件通知的 Webhook 位址，每行一個。", En: "Webhook URLs notified for recording events, one per line."}},

	{Key: "WEBPUSH_SUBSCRIBER", Group: "server", Kind: "string", Default: "mailto:webpush@example.com", Min: nil, Max: nil, Enum: nil, RestartRequired: true, Sensitive: false, Requires: "", Label: LangText{ZhCN: "WebPush 订阅者", ZhTW: "WebPush 訂閱者", En: "WebPush subscriber"}, Description: LangText{ZhCN: "WebPush VAPID 订阅者标识（mailto）。", ZhTW: "WebPush VAPID 訂閱者識別（mailto）。", En: "WebPush VAPID subscriber identity (mailto)."}},
}

var featureKeys = []string{"BILI_CDN", "BILI_CDN_FALLBACK", "BILI_FORCE_SOURCE", "BILI_LIVE_API_FALLBACK_HOST", "BILI_LIVE_API_HOST", "BILI_REPLACE_CN01", "DANMAKU_RECORD_TYPES", "DANMAKU_TRANSPORT", "FILENAME_TEMPLATE", "FLV_DISABLE_SPLIT_ON_ANNEXB", "FLV_SPLIT_ON_SCRIPT_TAG", "FLV_WRITE_METADATA", "MIN_FILE_SIZE_MB", "SAVE_STREAM_COVER", "SEGMENT_BY_TITLE", "SEGMENT_MINUTES", "SEGMENT_MODE", "SEGMENT_SIZE_MB", "TITLE_FILTER_PATTERNS"}

// secretFields are the /config/secrets metadata (masked, never returned as values).
var secretFields = []Field{

	{Key: "PASSWORD", Group: "secrets", Kind: "secret", Default: "", RestartRequired: true, Sensitive: true, Label: LangText{ZhCN: "密码", ZhTW: "密碼", En: "Password"}, Description: LangText{ZhCN: "敏感配置：密码", ZhTW: "敏感設定：密碼", En: "Secret: Password."}},

	{Key: "VIEWER_PASSWORD", Group: "secrets", Kind: "secret", Default: "", RestartRequired: true, Sensitive: true, Label: LangText{ZhCN: "访客密码", ZhTW: "訪客密碼", En: "Viewer password"}, Description: LangText{ZhCN: "敏感配置：访客密码", ZhTW: "敏感設定：訪客密碼", En: "Secret: Viewer password."}},

	{Key: "JWT_SECRET", Group: "secrets", Kind: "secret", Default: "", RestartRequired: true, Sensitive: true, Label: LangText{ZhCN: "JWT 密钥", ZhTW: "JWT 密鑰", En: "JWT secret"}, Description: LangText{ZhCN: "敏感配置：JWT 密钥", ZhTW: "敏感設定：JWT 密鑰", En: "Secret: JWT secret."}},

	{Key: "CLOUDCONVERT_API_KEY", Group: "secrets", Kind: "secret", Default: "", RestartRequired: true, Sensitive: true, Label: LangText{ZhCN: "CloudConvert API 密钥", ZhTW: "CloudConvert API 密鑰", En: "CloudConvert API key"}, Description: LangText{ZhCN: "敏感配置：CloudConvert API 密钥", ZhTW: "敏感設定：CloudConvert API 密鑰", En: "Secret: CloudConvert API key."}},

	{Key: "FRP_TOKEN", Group: "secrets", Kind: "secret", Default: "", RestartRequired: true, Sensitive: true, Label: LangText{ZhCN: "FRP 令牌", ZhTW: "FRP 令牌", En: "FRP token"}, Description: LangText{ZhCN: "敏感配置：FRP 令牌", ZhTW: "敏感設定：FRP 令牌", En: "Secret: FRP token."}},

	{Key: "NOTIFY_SSE_TOKEN", Group: "secrets", Kind: "secret", Default: "", RestartRequired: true, Sensitive: true, Label: LangText{ZhCN: "通知 SSE 令牌", ZhTW: "通知 SSE 令牌", En: "Notify SSE token"}, Description: LangText{ZhCN: "敏感配置：通知 SSE 令牌", ZhTW: "敏感設定：通知 SSE 令牌", En: "Secret: Notify SSE token."}},
}

var fieldsByKey = func() map[string]Field {
	m := make(map[string]Field, len(fieldSpecs)+len(secretFields))
	for _, f := range fieldSpecs {
		m[f.Key] = f
	}
	for _, f := range secretFields {
		m[f.Key] = f
	}
	return m
}()

// ManagedSchema returns a deep copy of the /config schema with fields grouped by
// section. Feature keys are included for completeness/metadata but are owned by
// /settings; secret keys are only exposed through SecretFields.
func ManagedSchema() Schema {
	var groups []Group
	for _, g := range schemaGroups {
		if g.ID == "features" {
			// FEATURES-V2 keys are rendered by /settings and rejected by
			// PUT /config; keep their metadata internal (tests/completeness)
			// but do not ship a duplicated group in GET /config.
			continue
		}
		ng := Group{ID: g.ID, Label: g.Label, Description: g.Description}
		for _, f := range fieldSpecs {
			if f.Group == g.ID {
				ng.Fields = append(ng.Fields, cloneField(f))
			}
		}
		if ng.Fields != nil {
			groups = append(groups, ng)
		}
	}
	return Schema{Groups: groups}
}

// SecretFields returns a copy of the secret metadata.
func SecretFields() []Field {
	out := make([]Field, 0, len(secretFields))
	for _, f := range secretFields {
		out = append(out, cloneField(f))
	}
	return out
}

// FieldByKey returns the metadata for a managed key.
func FieldByKey(key string) (Field, bool) {
	f, ok := fieldsByKey[key]
	if !ok {
		return Field{}, false
	}
	return cloneField(f), true
}

// ManagedKeys returns every managed non-secret key (features included).
func ManagedKeys() []string {
	out := make([]string, 0, len(fieldSpecs))
	for _, f := range fieldSpecs {
		out = append(out, f.Key)
	}
	return out
}

// FeatureSchemaKeys returns the FEATURES-V2 keys (owned by /settings).
func FeatureSchemaKeys() []string {
	return append([]string(nil), featureKeys...)
}

// SecretSchemaKeys returns the secret keys (owned by /config/secrets).
func SecretSchemaKeys() []string {
	out := make([]string, 0, len(secretFields))
	for _, f := range secretFields {
		out = append(out, f.Key)
	}
	return out
}

func cloneField(f Field) Field {
	if f.Min != nil {
		v := *f.Min
		f.Min = &v
	}
	if f.Max != nil {
		v := *f.Max
		f.Max = &v
	}
	if f.Enum != nil {
		f.Enum = append([]string(nil), f.Enum...)
	}
	return f
}

// ValidateFieldValue applies the schema kind/range/enum validation rules.
func ValidateFieldValue(f Field, value string) error {
	value = strings.TrimSpace(value)
	switch f.Kind {
	case "bool":
		if value != "true" && value != "false" {
			return fmt.Errorf("%s 必须是 true 或 false", f.Key)
		}
	case "number":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("%s 必须是整数", f.Key)
		}
		if f.Min != nil && n < *f.Min {
			return fmt.Errorf("%s 不能小于 %d", f.Key, *f.Min)
		}
		if f.Max != nil && n > *f.Max {
			return fmt.Errorf("%s 不能大于 %d", f.Key, *f.Max)
		}
	case "duration":
		if value != "" {
			if _, err := parseDurationValue(value); err != nil {
				return fmt.Errorf("%s 不是合法时长（如 10s、1m）", f.Key)
			}
		}
	case "enum":
		if len(f.Enum) == 0 {
			return nil
		}
		for _, allowed := range f.Enum {
			if value == allowed {
				return nil
			}
		}
		return fmt.Errorf("%s 仅支持：%s", f.Key, strings.Join(f.Enum, "、"))
	}
	if f.Key == "FILENAME_TEMPLATE" {
		if err := validateFilenameTemplateValue(value); err != nil {
			return err
		}
	}
	return nil
}
