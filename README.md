<div align="center">

# 🎬 XCut

**本地优先的自动视频剪辑 / Local-first automatic video editing**

用一条确定性流水线把原始素材变成高光成片：
**探测 → 分析 → 事件 → 风格 → 时间线 → 渲染**
无云端 · 无遥测 · 不依赖 AI

[![Release](https://img.shields.io/github/v/release/xiabee/XCut?include_prereleases&label=%E6%9C%80%E6%96%B0%E7%89%88&color=blue)](https://github.com/xiabee/XCut/releases)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/Windows%20%7C%20Linux-amd64%20%7C%20arm64-lightgrey)](https://github.com/xiabee/XCut/releases)
[![Status](https://img.shields.io/badge/status-Alpha-orange)](docs/ROADMAP.md)

[English](README_EN.md) | 中文

</div>

---

## ✨ 亮点

| | |
|---|---|
| 🖱️ **双击即用** | 原生桌面窗口（WebView2）承载内嵌 Web UI——不需要 Node、没有构建步骤、没有额外文件 |
| 🎞️ **拖放导入** | 视频文件直接拖进页面，同名不覆盖；也支持本地路径导入 |
| ✂️ **可视化时间线** | 片段色块按时长比例渲染、缩略图、拖动排序、边缘手柄裁剪、转场徽标（cut / fade / xfade）、检查器编辑与快捷键 |
| 🏸 **懂球的风格引擎** | 羽毛球 rally 模式：击球驱动的打分、球场 ROI 运动分析、多样性去重；每个片段都带"为什么入选"的解释 |
| 🎤 **字幕与卡拉 OK** | 经 AI sidecar 语音转写 → SRT 或逐字填充的卡拉 OK ASS，一键烧录进成片 |
| 🔒 **本地优先** | 仅监听本机回环、无遥测；AI 是可选 sidecar 增强，永远不是地基 |
| 📦 **资源有界** | 任务、进程、缓存、临时文件、日志全部有配置上限；空闲约 15 MB 内存、约 0% CPU |

目标场景：**羽毛球 · KTV · Vlog · 舞台演出 · 体育高光**

## 🚀 快速开始

> 前置条件：**FFmpeg + ffprobe**（PATH 上有，或设置 `XCUT_FFMPEG` / `XCUT_FFPROBE`；或放在 exe 旁边）。

### 方式一：下载预编译版本（推荐）

到 [**Releases**](https://github.com/xiabee/XCut/releases) 下载：

- **Windows 安装包** `xcut-*-windows-setup.exe`（约 6 MB）—— 双击安装，
  含开始菜单/桌面快捷方式与卸载器；无人值守安装请显式选作用域：
  `xcut-...-setup.exe /CURRENTUSER /VERYSILENT /DIR=D:\XCut`
  （不给作用域时安装器会让你在"仅当前用户/所有用户"之间选，脚本会卡在那一步）；
- **Windows 免安装 zip**（含 QUICKSTART.txt）—— 解压后双击 `xcut.exe`；
- Linux/macOS 下载对应平台的静态二进制。Linux 侧 FFmpeg 需自行安装
  （一键安装目前只覆盖 Windows）。**银河麒麟 V10 SP1 注意**：该机自带的
  FFmpeg 会把海思 OMX 解码插件日志写到 stdout，污染 ffprobe 的 JSON 输出，
  导致导入失败；把 `XCUT_FFMPEG` / `XCUT_FFPROBE` 指向一个标准构建即可，
  已在真机验证全链路（导入→分析→时间线→渲染）通过。

首次启动会自动检测环境：若缺少 FFmpeg，界面顶栏提供**一键安装**
（官方构建，经校验和验证后装到 exe 旁的 bin 目录，不会静默捆绑）。
AI 字幕等能力由你自行配置本地后端（设置 `workers.ai_bin` 或把
`xcut-ai` 放到 PATH），应用只提供接口、绝不自行下载模型。

### 方式二：从源码构建

构建需要 Go 1.25+：

```sh
git clone https://github.com/xiabee/XCut.git && cd XCut
go build -o xcut ./cmd/xcut          # Windows 下产出 xcut.exe

# 环境自检
./xcut doctor
```

### 一条龙出片

```sh
./xcut auto my-video.mp4 --project first-run --style generic_highlight

# 成片落在工作区的工程目录里：
#   ~/.xcut/projects/<project-id>/render.mp4  （或你传的 --out 路径）
```

用任意播放器或 `ffprobe` 验证结果。

### 分步执行

```sh
./xcut init                                   # 创建 ~/.xcut 工作区
./xcut project create badminton-2026          # 新建工程
./xcut import badminton-2026 match.mp4        # 探测 + 指纹入库
./xcut analyze badminton-2026                 # 运动/音频特征 → 事件
./xcut timeline badminton-2026 --style badminton_highlight
#   重新生成会覆盖手动编辑——上一版文档保留为 timeline.backup.json；
#   `--restore-backup` 可以换回来
./xcut render badminton-2026 --out cut.mp4
./xcut jobs badminton-2026                    # 任务历史（崩溃安全）
```

## 🖥️ Serve（本地 Web UI + HTTP API）

```sh
./xcut client     # 原生桌面窗口（WebView2）承载内嵌 UI
./xcut serve      # 同一套 UI 跑在浏览器 http://127.0.0.1:8619
```

创建工程，把视频文件拖进页面（或用"选择文件…"按钮）或填本地路径导入，
然后带着实时任务进度跑 分析 → 时间线 → 渲染——渲染出的 MP4 直接在
页面里播放。编辑工作区是一条真正的时间线：片段按时长比例渲染成色块并带
客户端截取的缩略图，接缝显示可编辑的转场徽标（cut / fade / xfade），
色块可拖动排序，边缘手柄可裁剪，检查器编辑所选片段的裁剪、速度、音量与
转场（Delete 移除、Space 播放、Ctrl+S 保存）。逐片段预览跟随标尺播放头。
工程还能通过 AI sidecar 把语音转写成字幕并烧录进成片（普通 SRT 或
卡拉 OK 式逐字填充的 ASS），球场 ROI 直接在帧上框选。UI 是内嵌进二进制
的原生 HTML/JS（`go:embed`）：不需要 Node、没有构建步骤、没有额外文件。
设计文档：docs/CLIENT_DESIGN.md。

![xcut web UI：一个已导入素材的工程、四个成功任务，渲染的高光正在
结果面板中播放](docs/img/web-ui.png)

<details>
<summary><b>HTTP API 一览</b>（<code>/api/v1</code>，仅本机回环）</summary>

```sh
curl http://127.0.0.1:8619/api/v1/health
curl http://127.0.0.1:8619/api/v1/projects
curl -X POST http://127.0.0.1:8619/api/v1/projects -d '{"name":"new-project"}'
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/assets -d '{"path":"D:/videos/clip.mp4"}'
curl -X POST "http://127.0.0.1:8619/api/v1/projects/<id>/assets/upload?filename=clip.mp4" --data-binary @clip.mp4  # 内容上传（拖放导入的落点；8 GiB/文件）
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/render -d '{}'
curl -X POST http://127.0.0.1:8619/api/v1/jobs/<jobID>/cancel            # 取消排队/运行中的任务（202；终态 409）
curl http://127.0.0.1:8619/api/v1/projects/<id>/assets/<assetID>/file   # 片段预览（支持 range）
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/subtitles -d '{}'  # 经 AI sidecar 语音转写（202 + 任务）
curl http://127.0.0.1:8619/api/v1/projects/<id>/subtitles               # 查询已有字幕产物
curl -X POST http://127.0.0.1:8619/api/v1/projects/<id>/render -d '{"subs": true}'  # 把字幕烧录进成片
curl -X PUT  http://127.0.0.1:8619/api/v1/projects/<id>/assets/<aid>/roi -d '{"x":0.1,"y":0.1,"w":0.5,"h":0.6}'  # 每源球场 ROI
```

异步任务端点返回 `202` 与 `job_id`；轮询 `GET /api/v1/jobs/{id}`。
每个工程同时只允许一个 analyze/timeline/render 任务排队或运行——重复
触发返回 `409`（导入永不去重）。活动任务在 Web UI 里有取消按钮；
CLI 运行（在你自己的终端里同步执行）用 Ctrl+C 取消。
仅监听本机回环是设计决定：`xcut serve` **拒绝**任何非回环地址，除非你
同时显式开启 `server.listen_remote` 并配置 `server.auth_token`（见
`docs/SECURITY.md` 与 `docs/DECISIONS.md` D12）。

<details>
<summary><b>远程访问（可选）</b>：给非本机对端加一道 bearer token</summary>

```powershell
# 1. 生成一个足够强的 token（≥24 字符，太短会被直接拒绝启动）
$tok = (openssl rand -hex 24)
# 2. 写进 <workspace>/config.json（或设环境变量 XCUT_AUTH_TOKEN）
#    {"server":{"listen":"0.0.0.0:8619","listen_remote":true,"auth_token":"..."}}
./xcut serve   # 启动行会写明当前姿态：loopback only / remote bind, authentication required
curl -H "Authorization: Bearer $tok" http://<主机IP>:8619/api/v1/health
```

- 本机回环对端始终可信：桌面客户端、双击启动的 exe、`xcut client` 都不需要 token。
- token 只能来自配置文件或环境变量，**不是命令行参数**（否则进入进程列表与 shell 历史）。
- `xcut config show` 里 token 显示为 `<set>`，`xcut init` 写出的默认配置也不落盘它。
- 密码学上是常量时间比较；失败按对端计数（5 分钟 20 次后转 `429`）。
- 判定只看 socket 地址，`Host`/`X-Forwarded-For` 一律不参与。
- 传输是明文 HTTP：请放在可信内网或 Tailscale/SSH 隧道里，**不要**用同机反向代理
  套一层 TLS——那会让所有远端都变成"本机回环"从而绕过鉴权（SECURITY.md 有详细说明）。
- Web UI 可远程使用：浏览器给 `<video src>`、缩略图和下载链接挂不上请求头，
  所以首次登录（用 bearer 令牌证明）后换发一个 `HttpOnly; SameSite=Strict`
  会话 cookie 供**读取**使用；**写入**仍必须把会话 id 回显在 `X-Cut-Session`
  头里——跨站页面读不到 HttpOnly cookie，也就无法伪造这个回显，CSRF 因此在
  结构上不成立（D14）。令牌本身不落盘，刷新页面不会重复索要。
- 会话是内存态：TTL 12 小时、上限 256 个、重启即全部失效，`DELETE
  /api/v1/session` 或界面右上角的"退出登录"可立即撤销。
- 运行手册（实测过的 SSH 隧道配方与它把鉴权交给了谁、401/403/429 各代表什么、
  备份范围、轮换与撤销步骤）：**docs/OPERATIONS.md**。

</details>

</details>

## 🎨 风格（Styles）

风格是数据而非代码——`internal/style/presets/` 里的受校验 JSON 预设
（内嵌），可被 `<workspace>/styles/` 覆盖：

- `generic_highlight` —— 运动/音频均衡打分
- `generic_xfade` —— 类似 generic_highlight，但接缝用真正的交叉淡化（xfade）
- `ktv_mv` —— 音频主导（歌声/能量），按 onset 密度加权，音乐场景用更长片段
- `badminton_highlight` —— 重运动的 rally 模式：onset 密度聚类带迟滞
  （真实球场音频从不安静——环境声会让检测器在每个间隙持续触发）、
  击球驱动打分、球场 ROI 运动分析

风格里的 `target_duration` 只是**默认**：单次生成可以用
`xcut timeline/auto --duration <秒>`（或界面风格旁的"成片时长（秒）"）临时
覆盖，绝不回写预设文件。之所以值得调这个而不是继续调参数——在真实比赛的
标注上量过：60s 成片最多只能覆盖 473s 回合时间里的 12.7%，把目标放到 240s
能覆盖 18/43 个回合（召回 0.112→0.289），精度只降约 7 个点（详见
[docs/EVAL.md](docs/EVAL.md)）。

针对球场区域运动分析，Web UI 可以直接在工程素材的某一帧上框选感兴趣
区域（侧栏"框选球场 ROI…"）；保存为该素材自己的区域
（`GET/PUT/DELETE /api/v1/projects/{id}/assets/{aid}/roi`），时间线生成
时覆盖所选风格的 per-preset 区域——每个固定机位的球场位置都可以不同，
没有自己区域的素材回退到风格设置。风格级区域走
`GET/PUT/DELETE /api/v1/styles/{name}/roi`。

每个入选片段都在元数据里携带得分、分项与入选原因——Web UI 会展示每段
的"为什么"。

Web UI 支持中英文：顶栏选择器即时切换，偏好持久化，首次访问自动跟随
浏览器语言（零依赖——以英文字符串为键的纯 JSON 词典，Go 测试防漂移）。

## ✂️ 时间线与渲染

渲染器严格按校验后的时间线执行：`cut`、`fade`（经黑场）与 `xfade`
（真正交叉淡化）可在同一条时间线内自由混用；逐片段 `speed` 对视频与
音频同时生效。不支持的结构（音轨、多轨时间线、片段特效）会被响亮拒绝
而非悄悄丢弃。输出经 ffprobe 校验后原子发布；若 `--out` 会覆盖任何源
素材，渲染直接拒绝。

## 🎤 字幕（KTV/吉他弹唱）

语音转文字是 AI 能力，因此遵循 sidecar 规则：核心绝不运行或下载模型。
参考 sidecar（`scripts/xcut-ai-sidecar.py`）探测本机安装的 Whisper 后端
——`openai-whisper`、`faster-whisper`（`pip install faster-whisper`）或
whisper.cpp 的 `whisper-cli`——没有安装时如实报告不可用；装好任意一个，
能力即刻点亮，XCut 零改动。然后：

```sh
./xcut subtitles song.mp4 --ass        # 带词级时间戳的转写 → 卡拉 OK ASS（默认 SRT）
./xcut render proj --subs lyrics.ass   # 把字幕烧录进成片（音频流直拷）
```

在 Web UI 里同样的链路就是一个按钮：转写（选素材）→ 状态 + 下载链接 →
勾选"烧录字幕"→ 渲染。词级时间戳驱动卡拉 OK 填充（每个字随演唱扫过；
sidecar 文本会被转义，杂散花括号或换行无法破坏 ASS 事件）；没有词级
时间戳时只产出普通 SRT。SRT 生成后，"预览文本"开关可以内联显示字幕内容。

<details>
<summary><b>语义 / 视觉 AI（预留接口）</b></summary>

参考 sidecar 另外支持两个 env 配置的 OpenAI 兼容 HTTP 后端——不捆绑
模型、不静默下载，配置即点亮：

```sh
# 语音转写走远程 Whisper 服务器（POST /v1/audio/transcriptions）
export XCUT_SIDECAR_STT_URL="http://127.0.0.1:9000"

# 单帧语义描述（POST /v1/chat/completions，多模态模型）——
# 比赛阶段感知、内容打标等语义信号的接入面
export XCUT_SIDECAR_VISION_URL="https://your-gateway.example:8443"
export XCUT_SIDECAR_VISION_MODEL="vision"
export XCUT_SIDECAR_INSECURE_TLS=1   # 自签证书时
```

配置后 `frame_describe` 能力在 sidecar capabilities 里点亮（核心消费
它的流水线属后续工作）；`XCUT_SIDECAR_TIMEOUT`（秒）限制单次 HTTP 调用。

</details>

## ⚙️ 配置

`./xcut config show` 打印生效配置；优先级为
默认值 < 配置文件（`<workspace>/config.json`）< 环境变量（`XCUT_*`）< CLI 参数。

<details>
<summary><b>全部资源/配置旋钮</b>（展示默认值）</summary>

| 键 | 默认值 | 含义 |
|---|---|---|
| `workspace` | `~/.xcut` | 数据目录（数据库、缓存、临时、工程） |
| `resource.max_concurrent_jobs` | 2 | 并行任务硬上限 |
| `resource.max_ffmpeg_processes` | 2 | 并行 ffmpeg/ffprobe 硬上限 |
| `resource.max_render_workers` | 1 | 并发渲染任务的独立上限 |
| `resource.max_analysis_workers` | 2 | 单次分析的 ffmpeg 并发 |
| `resource.ffmpeg_threads` | 2 | 每进程 `-threads` |
| `resource.frame_sample_fps` | 2 | 分析采样率 |
| `resource.analysis_width` | 640 | 分析降采样宽度 |
| `resource.proxy_enabled` | `false` | 生成低分辨率分析代理（需主动开启） |
| `resource.max_proxy_gb` | 2 | 代理磁盘预算（LRU 逐出） |
| `resource.proxy_threads` | 继承 | 一次性代理编码线程（解码受限；调高可缩短冷启动） |
| `resource.analyzer_call_timeout` | `30m` | 单分析器 ffmpeg 预算（防挂死） |
| `resource.max_temp_gb` / `max_cache_gb` | 20 / 10 | 磁盘预算 |
| `jobs.max_history` | 500 | 保留的终态任务行数（随任务完成修剪） |
| `job.stale_running_after` | `2h` | CLI 启动孤儿任务对账的年龄门槛 |
| `log.level` / `log.max_size_mb` / `log.max_files` | info / 50 / 3 | serve 日志轮转 |
| `server.listen` | `127.0.0.1:8619` | 除 `listen_remote` 外强制本机回环 |
| `server.listen_remote` | false | 允许非回环绑定；必须同时配 `auth_token`（D12） |
| `server.auth_token` | （空） | 非本机对端的 bearer token，≥24 字符（或 `XCUT_AUTH_TOKEN`） |
| `workers.audio` | `auto` | `auto`/`ffmpeg`/`rust` 音频分析器 |
| `workers.ai_bin` | `xcut-ai-sidecar` | AI sidecar 二进制（能力自动探测） |
| `ffmpeg.bin` / `ffmpeg.ffprobe_bin` | `ffmpeg` / `ffprobe` | 工具链覆盖（或 `XCUT_FFMPEG`/`XCUT_FFPROBE`） |

</details>

没有任何东西无界运行：任务、进程、缓存、代理、临时文件与日志都有配置
上限。`xcut cleanup [--dry-run]` 回收临时空间；`xcut cache stats|clear`
检查并清理分析/代理缓存。

## 🦀 可选的 Rust worker

Rust worker 加速音频分析，并为所有未来 worker（包括 AI sidecar）验证
进程边界协议。它**永远不是必需品**：

```sh
cargo build --release -p xcut-worker-media
# 然后把 target/release/xcut-worker-media 放上 PATH，或设置：
#   {"workers": {"media_bin": "/path/to/xcut-worker-media", "audio": "auto"}}
```

`auto` 模式在 worker 缺失或遇到编解码缺口时自动回退到内置 FFmpeg 分析器。

## 🛠️ 开发

```sh
go build ./... && go vet ./... && go test ./...   # Go 侧
cargo test                                          # Rust 侧（crates/）
```

测试用 FFmpeg `lavfi` 现场生成全部媒体 fixture——仓库里没有二进制
fixture。缺少 FFmpeg 时集成测试自动跳过。

架构、决策、安全模型、性能策略都在 [`docs/`](docs/)：

| 文档 | 内容 |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | 流水线、边界、worker 协议 |
| [docs/DECISIONS.md](docs/DECISIONS.md) | ADR 日志（为什么是 Go/Rust/SQLite/JSON…） |
| [docs/SECURITY.md](docs/SECURITY.md) | 威胁模型与控制措施 |
| [docs/PERFORMANCE.md](docs/PERFORMANCE.md) | 实测基线 |
| [docs/PROJECT_STATE.md](docs/PROJECT_STATE.md) | 当前真实可用状态 |
| [docs/ROADMAP.md](docs/ROADMAP.md) | 演进方向 |
| [docs/USAGE.md](docs/USAGE.md) | 逐命令参考 |
| [docs/EVAL.md](docs/EVAL.md) | 选择质量评估（`xcut eval`） |

## 📦 打包

```sh
scripts/build-release.ps1   # Windows（PowerShell 5.1+）
scripts/build-release.sh    # Linux/macOS（bash）
```

在 `dist/` 下产出带版本号的二进制：

| 产物 | 平台 |
|---|---|
| `xcut`（核心 + 内嵌 Web UI） | windows/amd64, linux/amd64, linux/arm64 |
| `xcut-worker-media`（可选） | windows/amd64, linux/amd64（静态 musl） |

Go 二进制完全静态（无 CGO）——即拷即用。Linux 的 Rust worker 用内置
`rust-lld` 针对 musl 目标构建，无需平台工具链。

## 📄 许可证

[MIT](LICENSE)
