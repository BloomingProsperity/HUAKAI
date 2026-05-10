# P-0c Follow-up Plan — Claude × Codex Synthesis

**日期**: 2026-05-09
**前置 lanes**:
- `docs/plans/2026-05-09-p0c-followup-plan-claude.md`（Sonnet ~210 行）
- `docs/plans/2026-05-09-p0c-followup-plan-codex.md`（Codex ~22KB）
**触发**: P-0b sonnet review 4 MED 收尾；Owner 已批 commit 三 commits（`0f3d9a8` / `4ad7fc0` / `b7d9079`）

## TL;DR

两 lane 在 INV-13 编号 + 立即执行 sequencing 共识。M4 关键分歧：
- Sonnet 推 (c) runtime feature-flag at canonical entry — 但 forwarder.Forward 当前**没有 HCSF envelope 对象**，方案落不了地
- **采纳 Codex (b) 主方案**：`*HCSF` adapter 边界加 Version guard（轻量）+ `-tags debug` 全量 ValidateEnvelope helper

Codex 多抓一个：**OpenAI/Gemini non-streaming 返回零值 `&HCSF{}` 改 fail-loud**——M4 alias sunset 真落地的唯一路径，Sonnet 漏了。

3 phase 切分（取 Codex 粒度）：P-0c-A (M1+M2) → P-0c-B (M3) → P-0c-C (M4 + 顺修 fake success)。立即执行 ≤ P-1 commit #1。

## 1. 共识（直接采纳）

| 项 | 共识 |
|---|---|
| **INV-13** 新编号给 StreamPlan required/enum validity | 两 lane 推荐 |
| 不复用 INV-6（已是 BufferedResponse/StreamEvents 互斥） | 两 lane 推荐 |
| `ValidateEnvelope` 注释从 `INV-1..12` 改 `INV-1..13` | 两 lane 推荐 |
| P-0c 立即执行 ≤ P-1 commit #1 | 两 lane 推荐 |
| M1 `validateProviderProjection` 加 `Capability`/`Verdict` 空值守卫 | 两 lane 推荐 |
| M3 `TestINV1_RoundTripDeepEqual` 改 table-driven 跨 15 capability | 两 lane 推荐 |
| 不写新 ValidateEnvelope 调用进 forwarder hot path | 两 lane 推荐（forwarder 不构造 envelope） |
| **Owner 注释中文 + 标识符英文** | 两 lane 共识 |

## 2. 分歧 → 决策

### 2.1 M4 主方案：采纳 Codex (b) + (a) stacked，拒绝 Sonnet (c)

**理由（Codex 证据）**：
- forwarder.Forward 现状是 `SSEEvent → CanonicalEvent → client chunks` 流处理，**没有 HCSF envelope 对象**可在 hot path entry validate（[`backend/internal/gateway/forwarder.go:185-242`](backend/internal/gateway/forwarder.go#L185-L242), [`forwarder.go:293-298`](backend/internal/gateway/forwarder.go#L293-L298)）
- Sonnet 的 (c) 要在"canonical entry point" validate，但实际不存在这种 entry point
- Codex 的 (b) 在 `*HCSF` adapter 边界（`openai_sse.go:148-155`、`gemini_sse.go:103-110`、`anthropic_sse.go` 等）加 `Version != HCSFVersion` 检查——开销仅字符串比较级别，能直接修复 `&HCSF{}` 假成功
- 配 (a) `-tags debug` 给 CI/local 跑完整 ValidateEnvelope（不进 production 二进制）

**采纳**: **(b) production lightweight Version guard + (a) debug full validation helper**。

### 2.2 Phase 切分：取 Codex 3 phase（更细）

| Phase | 内容 | 工作量 |
|---|---|---|
| **P-0c-A** | M1 (`validateProviderProjection` 必填) + M2 (新 INV-13) | 0.5 day |
| **P-0c-B** | M3 (INV-1 round-trip table-driven 15 capability) | 0.5 day |
| **P-0c-C** | M4 (b) Version guard + (a) -tags debug 配套 + **顺修 OpenAI/Gemini fake success** | 1-1.5 day |

总 2-2.5 day。

### 2.3 Codex 多发现 — fake success 改 fail-loud

**采纳**: 在 P-0c-C 同 commit 内修 `backend/internal/proto/openai_sse.go:148-155` + `gemini_sse.go:103-110`。当前这两处 `ProviderResponseToCanonical` non-streaming 路径返回零值 `&HCSF{}`——这是 alias sunset 的真技术债源头，单加 Version guard 不修这两处会让 guard 立即触发并阻塞所有 OpenAI/Gemini buffered response。

**改法**：返回带 `RequestMeta` + `BufferedResponse` 的合法 envelope，或返回 nil + structured error 让上游 forwarder 走错误路径（per Codex Owner Decision #4）。

具体 fail-loud 形态由 P-0c-C executor 决定（标 P-0c 内 implementation decision，不需 Owner 单独拍板）。

## 3. 三维 delta 分类（per CLAUDE.md #12）

| MED | 维度 | 理由 |
|---|---|---|
| M1 projection 必填 | 算法 | 校验函数严格化，属选择策略层 |
| M2 INV-13 新编号 | 架构 | 公共 invariant taxonomy 改动 |
| M3 round-trip 全集 | 生态 | 测试覆盖度 + 可观测性提升 |
| M4 (b) Version guard + 顺修 fake success | **架构 + 生态** | 架构（adapter 边界 contract 强化）+ 生态（alias sunset 路径打通） |

## 4. Owner 必决策的 4 点

### **D-INV13**: 是否批准新增 INV-13 给 StreamPlan required/enum validity?
- 两 lane 共识推荐 yes
- 备选：复用 INV-6（但 INV-6 已是 envelope shape 互斥语义，会污染含义）或仅改 message 不加编号（不一致）
- **推荐**: 批准 INV-13

### **D-M4**: 采纳 Codex (b)+(a) 还是 Sonnet (c)+(a)?
- (b) production lightweight Version guard at adapter edges + (a) debug full validation
- (c) runtime feature-flag at canonical entry — 但 canonical entry 不存在
- **推荐**: 采纳 (b)+(a)

### **D-FailLoud**: OpenAI/Gemini non-streaming 当前返回零值 `&HCSF{}`，改 fail-loud 还是改返回合法 envelope?
- 现状是 fake success（保留架构债）
- 选项 1: 返回 nil + structured error（上游 forwarder 走 error 路径）
- 选项 2: 返回带 RequestMeta + BufferedResponse 的合法 envelope（保持成功路径）
- **推荐**: 选项 2 优先（不破坏现有非流式调用方），P-0c-C executor 实施时确认

### **D-Sequencing**: P-0c 是否阻塞 P-1 启动？
- 两 lane 共识：P-0c-A + P-0c-B 必须 ≤ P-1 commit #1
- P-0c-C 可与 P-1 day 1-2 并行
- **推荐**: 接受双 lane 共识

## 5. P-0c-A/B/C 实施 dispatch 计划

完成 Owner 4 D 决策后，dispatch 顺序：

| 步骤 | Lane | 工作量 | 依赖 |
|---|---|---|---|
| Step 1 | sonnet executor 写 P-0c-A 代码（M1+M2） | 0.5 day | D-INV13 批准 |
| Step 2 | sonnet executor 写 P-0c-B 代码（M3） | 0.5 day | Step 1 完成 |
| Step 3 | sonnet executor 写 P-0c-C 代码（M4+fake success） | 1-1.5 day | D-M4 + D-FailLoud 批准 |
| Step 4 | sonnet code-reviewer 审 P-0c 全部 | 0.25 day | Step 3 完成 |
| Step 5 | commit P-0c | - | Step 4 verdict |

总：~2-2.5 day implementation + 0.25 day review = 2.5-3 day 全 phase 完成。

## 6. Sonnet 自评盲点已被 Codex 覆盖

- Sonnet "没确认 HCSF alias 外部 consumer" → Codex (b) 方案直接抓到 `openai_sse.go:148-155` + `gemini_sse.go:103-110` 两处真消费点
- Sonnet "没读完整 Day 10 review" + "没扫 docs/specs/ INV-13 collision" → Codex 也没扫，但两 lane 共识 INV-13 不冲突现有 INV-1..12（两 lane 都核了 INV 含义边界）
- Sonnet "没 benchmark validation cost" → Codex (b) 强调"开销仅字符串比较级别"，比 Sonnet (c) 完整 ValidateEnvelope 在 hot path 轻 100x+

## 7. 风险与盲点（综合）

- **`-tags debug` 是否长期纳入 CI**：Codex 推荐普通 CI 跑 `go test ./...`，P-0c 或 release gate 额外跑 `go test -tags debug ./internal/proto`；长期纳入待 Owner 决定（**不在 P-0c 必决策范围**）
- **fake success 修改可能让现有 forwarder 测试失败**：P-0c-C executor 实施时需跑全包测试确认；如失败需修测试 fixture
- **anthropic_sse.go 是否也有同类 fake success 路径**：Codex 没列；P-0c-C executor 应 grep `&HCSF{}` 全仓核

## 8. 决策路径

如同意 4 D 推荐（INV-13 批准 / Codex (b)+(a) / fail-loud 选项 2 / 阻塞 P-1） → 立即 dispatch P-0c-A executor。

如要 override 任意 D → 修 synthesis 后 dispatch。

## Tail block

Source files read: `docs/plans/2026-05-09-p0c-followup-plan-{claude,codex}.md` (HUAKAI internal — exempt per #12)；`backend/internal/proto/envelope_validate.go` / `envelope_test.go` / `openai_sse.go` / `gemini_sse.go` / `forwarder.go` (HUAKAI internal — exempt)
Lane: synthesizer (cross-discuss + agree/conflict/gaps + Codex (b) over Sonnet (c) tradeoff)
Agent: Claude opus-4-7 [1m]
UTC timestamp: 2026-05-10T15:15Z
