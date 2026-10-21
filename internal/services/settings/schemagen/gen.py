# Generates internal/services/settings/schema.go (CONFIG-V2 managed-key metadata).
# Run from repo root:  python3 internal/services/settings/schemagen/gen.py
# meta.py holds the curated zh-CN/zh-TW/en labels/descriptions; edit it when keys
# change, then regenerate and run the settings/schema tests.
import re, json, os
ns={}
exec(open(os.path.join(os.path.dirname(__file__), 'meta.py')).read(), ns)
META = ns['META']

src = open('internal/modules/config/config.go').read()
keys = {}
def add(k, kind='string', default=''):
    keys.setdefault(k, {'kind':kind,'default':default})

for m in re.finditer(r'os\.Getenv\("([A-Z0-9_]+)"\)\s*==\s*"true"', src):
    add(m.group(1), 'bool', 'false')
for m in re.finditer(r'EmptyOrElse\(\s*(?:strings\.(?:TrimSpace|ToLower)\(\s*)?os\.Getenv\("([A-Z0-9_]+)"\)\s*\)?\s*,\s*"([^"]*)"\s*\)', src):
    add(m.group(1), 'string', m.group(2))
for pat in [r'MustAtoi\(\s*EmptyOrElse\(\s*os\.Getenv\("([A-Z0-9_]+)"\)\s*,\s*"([^"]*)"\s*\)\s*\)',
            r'MustAtoi64\(\s*EmptyOrElse\(\s*os\.Getenv\("([A-Z0-9_]+)"\)\s*,\s*"([^"]*)"\s*\)\s*\)']:
    for m in re.finditer(pat, src):
        add(m.group(1), 'number', m.group(2))
for m in re.finditer(r'parseDurationOrDefault\(\s*os\.Getenv\("([A-Z0-9_]+)"\)\s*,\s*(?:([0-9]+)\*)?time\.(Second|Minute|Hour|Millisecond)\)', src):
    n, unit = int(m.group(2) or '1'), m.group(3)
    d = f'{n}ms' if unit=='Millisecond' else f'{n*{"Second":1,"Minute":60,"Hour":3600}[unit]}s'
    add(m.group(1), 'duration', d)
for m in re.finditer(r'parseCommaSeparatedValues\(\s*EmptyOrElse\(\s*os\.Getenv\("([A-Z0-9_]+)"\)\s*,\s*"([^"]*)"\s*\)', src):
    add(m.group(1), 'csv', m.group(2))
for m in re.finditer(r'os\.Getenv\("([A-Z0-9_]+)"\)', src):
    add(m.group(1))
for m in re.finditer(r'LookupEnv\("([A-Z0-9_]+)"\)', src):
    add(m.group(1))

# special keys read via helper variables / dynamic keys
special = {
 'USERNAME': ('string',''), 'VIEWER_USERNAME': ('string',''), 'TZ': ('string','Asia/Hong_Kong'),
 'GITHUB_API_URL': ('string','https://api.github.com'),
 'UPDATE_CHECK_REPO': ('string','BeyondandSharp/bilirec'),
 'UPDATE_CHECK_RELEASE_URL': ('string','https://github.com/BeyondandSharp/bilirec/releases/latest'),
 'UPDATE_CHECK_API_URL': ('string',''),
 'CHECK_UPDATE': ('bool','true'),
 'MAX_RETRY_SECONDS': ('number','30'),
 'LOCAL_LOGS_SPLIT': ('bool','true'),
}
keys.update({k:{'kind':v[0],'default':v[1]} for k,v in special.items()})

bootstrap = {'CONFIG_FILE','SECRETS_FILE','ENV_FILE','PUID','PGID','MAX_RETRY_MINUTES','CI','KEEP_TEST_FILES',
             'BILIREC_WRITE_SIM_ARTIFACTS','CLOUDCONVERT_IMPORT_TASK_ID','CLOUDCONVERT_JOB_ID',
             'CLOUDCONVERT_TASK_ID','CLOUDCONVERT_RECOVER_SOURCE_URL'}
test_prefix = ('BILIBILI_TEST_',)
secrets = ['PASSWORD','VIEWER_PASSWORD','JWT_SECRET','CLOUDCONVERT_API_KEY','FRP_TOKEN','NOTIFY_SSE_TOKEN']

# managed computed after features are defined below

# numeric-ish refinement by name
num_suffixes = ('_SECS','_MB','_BYTES','_SIZE','_HOURS','_MINUTES','_MIN','_MAX','_DAYS','_BACKUPS',
                '_SHARD','_SHARDS','_TASKS','_RECORDINGS','_ATTEMPTS','_RETRIES','_COUNT','_THRESHOLD',
                '_SIZE_MB','_BUFFER','_BUFFER_SIZE','_PERIOD_SECS','_TIME','_PORT')
def refine(k, spec):
    kind, default = spec['kind'], spec['default']
    if kind != 'string':
        return kind, default
    upper = k.upper()
    if upper in ('PORT','METRICS_PORT'):
        return 'number', default
    if any(tok in upper for tok in ('MAX_','_MIN_','_SIZE','_SECS','_BYTES','_HOURS','_MINUTES','_DAYS',
                                     '_BACKUPS','_SHARDS','_TASKS','_RECORDINGS','_ATTEMPTS','_RETRIES',
                                     '_COUNT','_THRESHOLD','_BUFFER','_INTERVAL','_TIMEOUT','_LIMIT')):
        if re.fullmatch(r'-?[0-9]+', default or '0'):
            return 'number', default
    if re.fullmatch(r'-?[0-9]+', default or '') and upper not in ('PORT','METRICS_PORT'):
        return 'number', default
    return 'string', default

enums = {
 'DOWNLOADER': ['auto','native','ffmpeg','streamlink'],
 'DOWNLOADER_FALLBACK': ['auto','native','ffmpeg','streamlink'],
 'FLV_REPAIR': ['on','off','auto'],
 'FLV_REPAIR_MODE': ['strict','best-effort'],
 'LOCAL_LOGS_OVERFLOW': ['block','drop'],
 'LOCAL_LOGS_FORMAT': ['pretty','json'],
 'RECORDING_RECOVERY_DURATION': ['preserve','reset'],
 'DANMAKU_OUTPUT_FORMAT': ['jsonl','xml'],
 'DANMAKU_OVERFLOW_POLICY': ['drop','block'],
 'BILIBILI_LOGIN_MODE': ['startup','controller','anonymous'],
 'SEGMENT_MODE': ['off','time','size'],
 'DANMAKU_TRANSPORT': ['auto','ws','wss'],
}
minmax = {
 'PORT': (1,65535), 'METRICS_PORT': (1,65535), 'SEGMENT_MINUTES': (1,1440),
 'SEGMENT_SIZE_MB': (16,65536), 'MIN_FILE_SIZE_MB': (0,1024),
 'MAX_CONCURRENT_RECORDINGS': (1,1000), 'MAX_RECORDING_HOURS': (1,100000),
 'MAX_RECOVERY_ATTEMPTS': (0,1000), 'MAX_RETRY_SECONDS': (0,86400),
 'STREAM_IDLE_TIMEOUT': (1,100000), 'TITLE_POLL_SECS': (5,86400),
 'DANMAKU_BYTES_POOL_SIZE': (0,1<<30), 'DANMAKU_CHAN_BUFFER_SIZE': (1,1<<20),
 'LIVE_STREAM_WRITER_CHAN_BUFFER_SIZE': (1,1<<20), 'READ_STREAM_CHAN_BUFFER_SIZE': (1,1<<20),
 'EXTERNAL_START_TIMEOUT_SECS': (1,3600), 'SUBCHECK_TICK_SECS': (1,86400),
 'SUBCHECK_MIN_INTERVAL_SECS': (1,86400), 'SUBCHECK_MAX_INTERVAL_SECS': (1,86400),
 'SUBCHECK_JITTER_SECS': (0,3600), 'SUBCHECK_ROOMS_PER_SHARD': (1,10000),
 'SUBCHECK_MAX_SHARDS': (1,1024),
}
requires_ffmpeg = {'CONVERT_TO_MP4','NO_CONVERT_IF_INVALID','DELETE_SOURCE_AFTER_CONVERT',
 'FFMPEG_BIN','FFMPEG_CHECK_INTERVAL_SECS','FFMPEG_MAX_CONCURRENT_TASKS',
 'FFMPEG_ALLOW_DURING_RECORDING','FFMPEG_ALLOW_DURING_RECORDING_MAX_ACTIVE_RECORDINGS'}

features = {
 'SEGMENT_MODE': ('enum','off',['off','time','size']),
 'SEGMENT_MINUTES': ('number','120',(1,1440)),
 'SEGMENT_SIZE_MB': ('number','2048',(16,65536)),
 'SEGMENT_BY_TITLE': ('bool','false',None),
 'MIN_FILE_SIZE_MB': ('number','0',(0,1024)),
 'FILENAME_TEMPLATE': ('string','{roomid}-{uname}/{date}_{time}_{title}',None),
 'TITLE_FILTER_PATTERNS': ('textarea','',None),
 'SAVE_STREAM_COVER': ('bool','false',None),
 'DANMAKU_RECORD_TYPES': ('csv','raw_events,super_chat,gift,guard',None),
 'DANMAKU_TRANSPORT': ('enum','auto',['auto','ws','wss']),
 'FLV_WRITE_METADATA': ('bool','true',None),
 'FLV_SPLIT_ON_SCRIPT_TAG': ('bool','false',None),
 'FLV_DISABLE_SPLIT_ON_ANNEXB': ('bool','true',None),
 'BILI_LIVE_API_HOST': ('string','https://api.live.bilibili.com',None),
 'BILI_LIVE_API_FALLBACK_HOST': ('string','',None),
 'BILI_CDN': ('csv','',None),
 'BILI_CDN_FALLBACK': ('bool','false',None),
 'BILI_FORCE_SOURCE': ('bool','false',None),
 'BILI_REPLACE_CN01': ('csv','',None),
}

for fk, (fkind, fdefault, _extra) in features.items():
    keys[fk] = {'kind': fkind, 'default': fdefault}
managed = [k for k in keys if k not in bootstrap and not k.startswith(test_prefix) and k not in secrets]

groups = [
 ('server', '服务', '服務', 'Server', '监听地址与端口、认证账号、前端来源、TLS、运行模式等。', '監聽位址與連接埠、認證帳號、前端來源、TLS、執行模式等。', 'Listen address/port, accounts, frontend origins, TLS and runtime mode.'),
 ('storage', '存储', '儲存', 'Storage', '输出目录、密钥目录、数据库目录与磁盘空间保护。', '輸出目錄、密鑰目錄、資料庫目錄與磁碟空間保護。', 'Output/secrets/database directories and disk-space protection.'),
 ('recording', '录制', '錄製', 'Recording', '并发录制、分段轮转、恢复、流缓冲与空闲超时。', '並發錄製、分段輪轉、恢復、串流緩衝與閒置逾時。', 'Concurrency, rotation, recovery, stream buffers and idle timeout.'),
 ('danmaku', '弹幕', '彈幕', 'Danmaku', '弹幕文件格式、写入缓冲、溢出策略与刷新周期。', '彈幕檔案格式、寫入緩衝、溢出策略與刷新週期。', 'Danmaku output format, writer buffers, overflow policy and flush periods.'),
 ('logging', '日志', '日誌', 'Logging', '本地文件日志与 VictoriaLogs 输出。', '本機檔案日誌與 VictoriaLogs 輸出。', 'Local file logging and VictoriaLogs output.'),
 ('metrics', '指标', '指標', 'Metrics', 'Prometheus 指标端点。', 'Prometheus 指標端點。', 'Prometheus metrics endpoint.'),
 ('frp', '内网穿透', '內網穿透', 'FRP', 'frp 内网穿透服务配置。', 'frp 內網穿透服務設定。', 'frp tunnel service configuration.'),
 ('convert', '转码', '轉碼', 'Convert', 'FFmpeg / CloudConvert 转码与并发任务控制。', 'FFmpeg / CloudConvert 轉碼與並發任務控制。', 'FFmpeg / CloudConvert conversion and task concurrency.'),
 ('subcheck', '订阅检查', '訂閱檢查', 'Subscription check', '订阅自动开播检测的分片、间隔与抖动。', '訂閱自動開播偵測的分片、間隔與抖動。', 'Sharding, intervals and jitter for auto-start subscription checks.'),
 ('downloader', '下载器', '下載器', 'Downloader', '外部流下载器选择与可执行文件路径。', '外部串流下載器選擇與可執行檔路徑。', 'External stream downloader selection and binary paths.'),
 ('repair', 'FLV 修复', 'FLV 修復', 'FLV repair', 'FLV 流修复 sidecar 的开关、模式与缓冲。', 'FLV 串流修復 sidecar 的開關、模式與緩衝。', 'FLV repair sidecar switch, mode and buffer.'),
 ('update', '更新检查', '更新檢查', 'Update check', '版本更新检查的仓库与 API 地址。', '版本更新檢查的儲存庫與 API 位址。', 'Repository and API endpoints for version update checks.'),
 ('features', '录制特性', '錄製特性', 'Recording features', 'FEATURES-V2 录制、弹幕与源选择特性（在 /settings 中编辑）。', 'FEATURES-V2 錄製、彈幕與來源選擇特性（在 /settings 中編輯）。', 'FEATURES-V2 recording, danmaku and source-selection features (edited in /settings).'),
]

def group_of(k):
    p = k
    if p in features: return 'features'
    if p.startswith('FRP_'): return 'frp'
    if p.startswith('METRICS_'): return 'metrics'
    if p.startswith('LOCAL_LOGS_') or p.startswith('VICTORIALOGS_'): return 'logging'
    if p.startswith('DANMAKU_'): return 'danmaku'
    if p.startswith('SUBCHECK_'): return 'subcheck'
    if p.startswith('FLV_REPAIR') or p in ('FLV_WRITE_METADATA','FLV_SPLIT_ON_SCRIPT_TAG','FLV_DISABLE_SPLIT_ON_ANNEXB'):
        return 'features' if p in features else 'repair'
    if p.startswith('CLOUDCONVERT_') or p.startswith('FFMPEG_') or p in ('CONVERT_TO_MP4','NO_CONVERT_IF_INVALID','DELETE_SOURCE_AFTER_CONVERT'):
        return 'convert'
    if p in ('DOWNLOADER','DOWNLOADER_FALLBACK','STREAMLINK_BIN','EXTERNAL_START_TIMEOUT_SECS'):
        return 'downloader'
    if p.startswith('UPDATE_CHECK_') or p in ('CHECK_UPDATE','GITHUB_API_URL'): return 'update'
    if p in ('OUTPUT_DIR','SECRET_DIR','DATABASE_DIR','MIN_DISK_SPACE_BYTES'): return 'storage'
    if p in ('HOST','PORT','TRUSTED_PROXIES','PUBLIC_BASE_URL','FRONTEND_URL','FRONTEND_URLS',
             'SERVER_CRT','SERVER_KEY','PRODUCTION_MODE','DEBUG','SILENT_ACCESS_LOG',
             'BILIBILI_LOGIN_MODE','TZ','USERNAME','VIEWER_USERNAME','WEBHOOK_URLS','WEBPUSH_SUBSCRIBER'):
        return 'server'
    if p.startswith('LIVE_STREAM_WRITER_') or p.startswith('READ_STREAM_') or p in ('STREAM_WRITER_BUFFER_SIZE','UPLOAD_BUFFER_SIZE','DOWNLOAD_BUFFER_SIZE','MAX_CONCURRENT_RECORDINGS','MAX_RECORDING_HOURS','MAX_RECOVERY_ATTEMPTS','MAX_RETRY_SECONDS','RECORDING_RECOVERY_DURATION','STREAM_IDLE_TIMEOUT','DROP_FILE_PAGE_CACHE','SEQUENTIAL_WRITE','SKIP_SMALL_FLUSH','SKIP_SMALL_FLUSH_THRESHOLD','TITLE_POLL_SECS','MAX_CONCURRENT_RECORDING'):
        return 'recording'
    if p in features: return 'features'
    return 'server'

# token-based labels
zh_tokens = {
 'HOST':'主机','PORT':'端口','USERNAME':'用户名','PASSWORD':'密码','SECRET':'密钥','TOKEN':'令牌',
 'URL':'地址','URLS':'地址列表','DIR':'目录','PATH':'路径','FILE':'文件','FILEs':'文件','ENABLED':'启用',
 'DISABLED':'禁用','ENABLE':'启用','DEBUG':'调试','MODEL':'模式','MODE':'模式','INTERVAL':'间隔',
 'TIMEOUT':'超时','SIZE':'大小','BUFFER':'缓冲','CHANNEL':'通道','CHAN':'通道','POOL':'池','BYTES':'字节',
 'MB':'MB','SECS':'秒','SECONDS':'秒','HOURS':'小时','MINUTES':'分钟','DAYS':'天','COUNT':'数量',
 'MAX':'最大','MIN':'最小','CONCURRENT':'并发','RECORDINGS':'录制数','RECORDING':'录制','RETRY':'重试',
 'RETRIES':'重试次数','ATTEMPTS':'尝试次数','THRESHOLD':'阈值','OUTPUT':'输出','INPUT':'输入',
 'DATABASE':'数据库','STORAGE':'存储','LOG':'日志','LOGS':'日志','LEVEL':'级别','FORMAT':'格式',
 'COMPRESS':'压缩','BACKUPS':'备份数','FLUSH':'刷新','SYNC':'同步','PERIOD':'周期','BATCH':'批量',
 'OVERFLOW':'溢出','POLICY':'策略','METRICS':'指标','ENABLE_METRICS':'启用指标','FRP':'内网穿透',
 'SERVER':'服务器','SCHEME':'协议','HTTPS':'HTTPS','BASE':'基础','DOMAIN':'域名','CDN':'CDN',
 'REPLACE':'替换','FORCE':'强制','SOURCE':'来源','FALLBACK':'回退','LIVE':'直播','API':'API',
 'BILI':'B站','BILIBILI':'B站','DANMAKU':'弹幕','WRITER':'写入器','READ':'读取','STREAM':'流',
 'FRONTEND':'前端','PROXY':'代理','PROXIES':'代理','TLS':'TLS','CRT':'证书','KEY':'私钥',
 'PRODUCTION':'生产','SILENT':'静默','ACCESS':'访问','TRUSTED':'可信','PLACEHOLDER':'占位',
 'WEBHOOK':'Webhook','WEBPUSH':'WebPush','SUBSCRIBER':'订阅者','VIDEO':'视频','AUDIO':'音频',
 'CONVERT':'转码','DOWNLOAD':'下载','UPLOAD':'上传','CHECK':'检查','UPDATE':'更新','REPO':'仓库',
 'RELEASE':'发布','GITHUB':'GitHub','REPAIR':'修复','BIN':'可执行文件','STREAMLINK':'Streamlink',
 'FFMPEG':'FFmpeg','CLOUDCONVERT':'CloudConvert','ID':'ID','ACCOUNT':'账户','PROJECT':'项目',
 'TASK':'任务','TASKS':'任务数','RECOVERY':'恢复','DURATION':'时长','IDLE':'空闲','DISK':'磁盘',
 'SPACE':'空间','DROP':'丢弃','CACHE':'缓存','PAGE':'页面','SHARD':'分片','SHARDS':'分片数',
 'JITTER':'抖动','TICK':'间隔','ROOMS':'房间数','SHARD_ROOMS':'分片房间数','PER':'每','SEGMENT':'分段',
 'TITLE':'标题','FILTER':'过滤','PATTERNS':'规则','COVER':'封面','SAVE':'保存','MIN_FILE':'最小文件',
 'RECORD_TYPES':'记录类型','TRANSPORT':'传输','METADATA':'元数据','SPLIT':'切段','SCRIPT':'Script',
 'TAG':'标签','ANNEXB':'AnnexB','CN01':'CN01','HIGH':'高','LOW':'低','COLD':'冷','RELEASE_SECS':'释放秒数',
 'MIN_PERIODIC':'最小周期','FLUSH_BYTES':'刷新字节','BUFFER_SIZE':'缓冲大小','BYTES_POOL':'字节池',
 'LOGIN':'登录','ANONYMOUS':'匿名','STARTUP':'启动','CONTROLLER':'控制器','PRESERVE':'保留','RESET':'重置',
 'STRICT':'严格','BEST-EFFORT':'尽力','ON':'开','OFF':'关','AUTO':'自动','NATIVE':'原生','JSONL':'JSONL',
 'XML':'XML','PRETTY':'美化','JSON':'JSON','STREAM_IDLE':'流空闲','START':'启动','MODE':'模式',
}
def humanize(k):
    words = re.split(r'[_]', k)
    return ' '.join(w.capitalize() for w in words if w)
def en_label(k):
    special = {'PORT':'Port','HOST':'Host','DEBUG':'Debug','TZ':'Timezone','USERNAME':'Admin username',
               'VIEWER_USERNAME':'Viewer username'}
    return special.get(k, humanize(k))
def zh_label(k):
    if k in ('USERNAME',): return '管理员用户名'
    if k in ('VIEWER_USERNAME',): return '访客用户名'
    if k in ('TZ',): return '时区'
    parts = []
    for w in re.split(r'_', k):
        if w in zh_tokens: parts.append(zh_tokens[w])
        elif w.isdigit(): parts.append(w)
        else: parts.append(w if len(w)<=4 else w)
    out = ''.join(parts)
    return out if out else k

group_by_id = {g[0]: g for g in groups}

def field_lit(key):
    spec = keys.get(key)
    kind, default = refine(key, spec) if spec else ('string','')
    if key in features:
        fkind, fdefault, extra = features[key]
        kind, default, enum, mm = fkind, fdefault, None, None
        if isinstance(extra, list): enum = extra
        elif isinstance(extra, tuple): mm = extra
    else:
        enum, mm = enums.get(key), minmax.get(key)
        if enum:
            kind = 'enum'
    restart = 'false' if key in features else 'true'
    requires = '"ffmpeg"' if key in requires_ffmpeg else '""'
    sensitive = key in secrets
    enum_lit = '[]string{' + ','.join('"%s"'%e for e in enum) + '}' if enum else 'nil'
    if key in features:
        minx, maxx = (mm if mm else (None,None))
    else:
        minx, maxx = minmax.get(key, (None,None))
    min_lit = f'ptrInt({minx})' if minx is not None else 'nil'
    max_lit = f'ptrInt({maxx})' if maxx is not None else 'nil'
    gid = group_of(key)
    g = group_by_id.get(gid, group_by_id['server'])
    if key in META:
        zh, desc_zh, tw, desc_tw, en, desc_en = META[key]
        label_zh_tw = tw
    else:
        en = en_label(key); zh = zh_label(key)
        label_zh_tw = zh
        desc_zh = f'{g[1]}：{zh}'
        desc_tw = f'{g[2]}：{label_zh_tw}'
        desc_en = f'{g[5]} ({en}).'
    return (f'{{Key: "{key}", Group: "{gid}", Kind: "{kind}", Default: {json.dumps(default, ensure_ascii=False)}, '
            f'Min: {min_lit}, Max: {max_lit}, Enum: {enum_lit}, RestartRequired: {restart}, Sensitive: {str(sensitive).lower()}, '
            f'Requires: {requires}, Label: LangText{{ZhCN: {json.dumps(zh, ensure_ascii=False)}, ZhTW: {json.dumps(label_zh_tw, ensure_ascii=False)}, En: {json.dumps(en)}}}, '
            f'Description: LangText{{ZhCN: {json.dumps(desc_zh, ensure_ascii=False)}, ZhTW: {json.dumps(desc_tw, ensure_ascii=False)}, En: {json.dumps(desc_en)}}}}}')

all_non_feature = [k for k in managed if k not in features]
feature_keys = [k for k in managed if k in features]
assert set(all_non_feature) | set(feature_keys) == set(managed), 'mismatch'
all_non_feature.sort(); feature_keys.sort()

secret_labels = {
 'PASSWORD': ('密码','密碼','Password'),
 'VIEWER_PASSWORD': ('访客密码','訪客密碼','Viewer password'),
 'JWT_SECRET': ('JWT 密钥','JWT 密鑰','JWT secret'),
 'CLOUDCONVERT_API_KEY': ('CloudConvert API 密钥','CloudConvert API 密鑰','CloudConvert API key'),
 'FRP_TOKEN': ('FRP 令牌','FRP 令牌','FRP token'),
 'NOTIFY_SSE_TOKEN': ('通知 SSE 令牌','通知 SSE 令牌','Notify SSE token'),
}

lines = []
lines.append('// SPDX-License-Identifier: GPL-3.0-only\n')
lines.append('// Code generated from docs/CONFIG-V2.md managed env keys; keep in sync with the os.Getenv keys.\n')
lines.append('package settings\n')
lines.append('\nimport (\n\t"fmt"\n\t"strconv"\n\t"strings"\n)\n')
lines.append('''// LangText is a three-language label/description object.
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
''')
for g in groups:
    lines.append(f'\t{{ID: "{g[0]}", Label: LangText{{ZhCN: {json.dumps(g[1], ensure_ascii=False)}, ZhTW: {json.dumps(g[2], ensure_ascii=False)}, En: "{g[3]}"}}, Description: LangText{{ZhCN: {json.dumps(g[4], ensure_ascii=False)}, ZhTW: {json.dumps(g[5], ensure_ascii=False)}, En: "{g[6]}"}}}},')
lines.append('}\n\nvar fieldSpecs = []Field{\n')
for k in sorted(managed):
    lines.append('\t' + field_lit(k) + ',\n')
lines.append('}\n')
lines.append('var featureKeys = []string{' + ','.join('"%s"'%k for k in feature_keys) + '}\n')
lines.append('''// secretFields are the /config/secrets metadata (masked, never returned as values).
var secretFields = []Field{
''')
for k in secrets:
    z, tw, en = secret_labels[k]
    lines.append(f'\t{{Key: "{k}", Group: "secrets", Kind: "secret", Default: "", RestartRequired: true, Sensitive: true, '
                 f'Label: LangText{{ZhCN: "{z}", ZhTW: "{tw}", En: "{en}"}}, Description: LangText{{ZhCN: "敏感配置：{z}", ZhTW: "敏感設定：{tw}", En: "Secret: {en}."}}}},\n')
lines.append('}\n')
lines.append('''var fieldsByKey = func() map[string]Field {
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
''')
os.makedirs('internal/services/settings', exist_ok=True)
open(os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'schema.go'),'w').write('\n'.join(lines))
print('keys', len(managed), 'features', len(feature_keys), 'secret', len(secrets))
