# Person Filter Roadmap (人员筛选演进路线)

Goal: 从"手动框一个颜色区域"演进到"给一张照片，自动找到这个人并剪出 TA 的高光"。
每一步都是可交付的，不依赖下一步。AI 始终走 sidecar 协议，核心零 AI 依赖。

## Phase 1 — Color Signature v2 (shipped)

- HSV 色签（18×3×3 = 162 bins，L1 归一化）
- 用户在源视频某一帧上框选自己所在的矩形（`xcut player --set x,y,w,h`）
- presence 扫描：整帧解码 → 滑动窗口 patch-max（积分图）→ 每帧 0..1 分
- **限制**：单一直方图无法区分"同色系的不同人"

## Phase 2 — Multi-Region Body Model (current)

把 spot rect 纵向分为三个水平带（头部 0–25%、躯干 25–65%、腿部 65–100%），
每个带独立建 H×S×V 直方图。评分要求多数带匹配（torso 必须匹配，head/legs 至少
一个匹配），比单直方图判别度高一个量级——白衬衫+深短裤和"全白"不再混淆。

- [x] `MultiRegionSignature`：3 带独立直方图
- [x] `ScoreFrameMultiRegion`：torso 加权 ≥ head/legs 加权
- [x] `min_player_presence` 门槛基于 multi-region 分数

## Phase 3 — Photo Reference Input (next)

用户上传一张包含目标人物的照片（或从视频截取一帧），在照片上框选人物区域：
系统用与 Phase 2 相同的 multi-region 模型从照片中采样。去掉了"必须在视频上画框"
的限制，使得冷启动更友好。

- [ ] `xcut player <project> --photo <image> --rect x,y,w,h`
- [ ] 照片与视频分辨率无关（归一化坐标即可）
- [ ] Web UI：上传照片 → 点选人物 → 自动生成 spot

## Phase 4 — ONNX Person Detector (sidecar)

在 Rust worker（或 Python sidecar）中加入轻量人形检测（YOLOv8-nano ONNX），
逐帧或低帧率检测所有人物 bounding box。检测结果是纯几何（不含身份），但
它与 Phase 2 的颜色签名结合后可以：
- 自动缩小颜色匹配的搜索范围（只在人形区域内做直方图回投影）
- 多人场景中区分"多少人匹配签名"
- 追踪同一人跨帧的位置变化

- [ ] sidecar 协议 v2：`op: "detect_people"` → `[{rect, confidence}]`
- [ ] presence 扫描改为：每帧检测 → 签名匹配在 bbox 内 → 置信度加权
- [ ] 多人场景的 identity 聚类（颜色 + 位置连续性）

## Phase 5 — Person Re-ID Embedding (long-term)

用 OSNet/TransReID 等轻量 re-ID embedding 模型替代颜色直方图，输出
128–512 维向量，余弦相似度匹配。这是学术界的标准做法，判别力远超颜色。
模型通过 Model Registry 安装（不自动下载），推理走 sidecar。

- [ ] sidecar 协议 v3：`op: "reid_embed"` → `[{embedding}]`
- [ ] Go 核存储 per-asset embedding，余弦相似度匹配
- [ ] 跨相机身份链接（同一人在不同 camera/session 中关联）

## 当前状态

Phase 2 已交付（multi-region body model）。Phase 3 是下一步。
