# FLV 重封装修复（flvreframe）

`flvreframe` 是 **fork 自有**的修复工具，用于把旧版切分 bug 产生的“tag 框架损坏、但大部分媒体数据仍在”的 FLV 重新整理成合法文件。

> 它**不是** BililiveRecorder 的移植代码：算法、代码位置和维护方式都与
> `pkg/flv` / `rust/crates/flv-repair`（录播姬语义的移植）分离，只依赖 Go 标准库。
> 录播姬规则（R1–R8 异常、metadata duration、Annex-B 等）仍由 `flvrepair` 负责，
> 两者可以串联使用。

## 1. 背景与根因

旧版 recorder（`d7c2408` 之前）在“按配置切段”时，`rev()` 在 `RotationPending`
状态下直接丢弃当前读到的数据块并返回：

```go
if info.RotationPending() {
    chunkPool.Put(data[:cap(data)])
    return rs.ErrRotationRequested
}
```

数据块没有进 `pipe.Process`，所以**既不在上一段，也不在下一段**。结果是：

- 每次切段在文件里留下一个“丢块”签名：前一个 tag 完整，后面一段无法解析的字节，
  之后重新同步到下一个 tag；
- 切多少次，整个时间轴上就分布多少个断点（不是只在文件开头）；
- 未切段的录制（例如 `test_2.flv`）完全干净；
- 首个非法 tag type（如 `99`/`249`）会让 `flvrepair` / 录播姬解析器直接失败。

缺失的数据块无法从本文件恢复；`flvreframe` 能恢复的是**仍然存在的媒体 tag**，
并把容器整理到可解析、可播放、时间轴连续。

## 2. 算法

1. 读取 FLV 头（`FLV` + version 1 + data offset），跳过第一个 previous-tag-size。
2. 顺序解析 tag；**信任 tag 自身的 24-bit data size**，不依赖 previous-tag-size
   （旧 bug 经常把后者写错）。
3. 遇到非法 header 时向前扫描，找到“自身结构合法、且后面紧跟的下一个 tag 也合法”
   的位置（双 tag 校验，降低把随机媒体字节误判为 header 的概率），跳过中间的不可解析
   字节并记录 gap。
4. 退出条件：EOF、或超过 `--resync-size`（默认 16 MiB）仍未找到下一个 tag，此时剩余
   字节记为 trailing 并丢弃。
5. 写新文件：重算 FLV header flags、逐 tag 重算 previous-tag-size、按需把时间戳整体
   平移到第一个媒体时间戳并做单调钳制。
6. 可选 `--trim-broken-video-gops`：对每个 gap，丢弃其后的视频 tag 直到下一个视频
   关键帧（音频 tag 全部保留），得到“无花屏但有短暂冻结/跳帧”的版本。

## 3. 用法

修复只坏框架的文件（推荐先做这一步）：

```bash
make build
./dist/flvreframe --input test/test.flv --output test/test_fixed.flv
```

对已经重封装的产物再跑录播姬语义修复（补 metadata、duration、异常规则）：

```bash
./dist/flvrepair file --input test/test_fixed.flv --output test/test_fixed_repair.flv
```

需要“无花屏”版本（断点处视频短冻结，音频连续）：

```bash
./dist/flvreframe --input test/test_3.flv --output test/test_3_clean.flv --trim-broken-video-gops
```

### 参数

| 参数 | 说明 |
|---|---|
| `--input` / `--output` | 输入/输出路径（输入不会被修改） |
| `--trim-broken-video-gops` | 丢弃 gap 后到下一关键帧之间的视频帧；音频保留 |
| `--keep-timestamps` | 不平移时间戳（默认会平移到第一个媒体时间戳） |
| `--resync-size` | gap 后搜索下一个 tag 的最大字节数，默认 16 MiB |
| `--json` | 以 JSON 输出摘要（便于脚本处理） |

退出码：`0` 成功；`1` 读取/解析/写入失败；`2` 参数用法错误。

### 摘要字段

`tags_in/tags_out`、`gaps`（每处 offset/bytes/前后时间戳）、
`dropped_bytes`（gap + trailing）、`trailing_bytes`、`prev_size_mismatches`、
`first_timestamp_ms/last_timestamp_ms`、`trim` 模式下的 `dropped_video_tags`。

## 4. 验证过的样例

| 文件 | 结果 |
|---|---|
| `test.flv`（3.0 GB） | 242,068 个合法 tag；118 处断点，丢弃 gap 2,303,988 字节 + 45,685 trailing；重建后 37:50.7、可解码（断点处有短暂花屏）；双 tag 重同步比早期探索脚本多恢复 14 个真实 tag |
| `test_3.flv`（110 MB） | 88,873 个合法 tag；6 处断点，丢弃 28,715 字节（含 trailing 51）；重建后 13:53.6，**全量解码 0 错误**；`--trim-broken-video-gops` 版本无花屏、音频连续 |

## 5. 限制

- **不能恢复已经丢失的数据块**。`flvreframe` 只能把现有字节重新组织成合法 tag；
  旧版丢块造成的内容缺失（`test.flv` 约 7 秒、`test_3` 约 2.1 秒的等效媒体数据）不会
  凭空出现。要真正补全，需要另一份覆盖相同时段的录像/原片。
- 默认保留断点处的花屏（更完整的画面内容）；`--trim-broken-video-gops` 以丢少量视频帧
  换取无花屏。两种模式都不会改动音频。
- 输出不生成 `keyframes` 索引（录播姬的 `KeyframesScriptDataValue` 属于可选项，
  本 fork 未移植；见 `docs/PORTING.md` §7.5）。如需 metadata，请再跑 `flvrepair file`。
- 对于“连 tag 头都大面积损坏”的文件，搜索窗口可能找不到可重同步的位置；此时只保留
  找到的 tag，其余丢弃并计入 trailing。

## 6. 代码与测试

- 包：`internal/flvreframe`（纯标准库，无 `pkg/flv` / Rust 依赖）
- CLI：`cmd/flvreframe`
- 测试：`go test ./internal/flvreframe`（无文件重写、使用临时目录）
- 构建：`make build` 会同时产出 `dist/flvreframe`
