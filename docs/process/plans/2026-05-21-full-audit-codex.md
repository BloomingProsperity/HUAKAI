# 2026-05-21 HUAKAI 全面自查独立评估与执行计划 - Codex

> 本文件是 Codex 独立起草稿，用于与 Claude 独立草案后续交叉讨论。执行本轮时未运行 git、未修改业务代码、未直接读取外部参考项目源码；HUAKAI 内部代码和 docs 为主要依据。
>
> 路径说明：Owner 要求写到 `docs/process/plans/2026-05-21-full-audit-codex.md`。当前 sandbox 只允许写 `/home/codex/HUAKAI/backend`，对 repo 根 `../docs/process/plans/...` 写入被拒绝；因此本文件落在当前工作区相同相对路径 `backend/docs/process/plans/...`。内容可用于后续交叉讨论，是否搬入 repo 根 docs 需 Owner 或有权限的执行者处理。
>
> 独立性风险披露：一次全仓 `rg` 排除规则未拦住绝对路径，输出中带出 `docs/process/plans/2026-05-21-full-audit-claude.md` 的两行摘要。发现后已停止读取任何 Claude 草案；本文件后续判断只使用我自己读取的 HUAKAI 代码、HUAKAI docs、以及已存在的 source-cited research 文件。Owner 可决定是否要求 Codex 重做一份完全隔离稿。

## 1. 树结构独立评估

### 1.1 总体结论

Owner 给的 16-section 树作为「项目功能状态树」的一级骨架是基本准确的，覆盖了 HUAKAI 的主要产品域：身份、模型/协议、账号凭据、账号池、Go 网关、Rust 数据面、路由调度、计费、信任链、观测、安全、网络/反封禁、juice、商业增长、前端、文档测试发布。

但它现在混合了三类不同状态：生产主链、已落代码但未宣称生产、以及 spec/research/roadmap。后续全面自查必须把「树上有这个方向」和「代码已经可发布」拆开，否则会误导 Owner 以为 §6 Rust、§12 反封禁、§13 Juice、§15 前端都和 Go gateway 主链一样成熟。

我建议把这棵树保留为审计总目录，但给每个 leaf 增加两条轴：

- **HUAKAI 状态轴**：代码已接线 / 代码局部 / spec only / research only / UI mock / mandatory roadmap / 未发现证据。
- **Parity 处置轴**：沿用 `Implemented`、`Implemented Better`、`Merged Equivalent`、`Safe Equivalent`、`Plugin`、`Feature Flag`、`Mandatory Roadmap`。

### 1.2 关键证据锚点

- 当前主请求链仍以 Go backend 为生产主链，README 明确描述 live path 为 `POST /v1/chat/completions`，并列出 auth、registry、router、pool、gateway、billing 等已落模块，同时说明真实 provider adapters、production pricing、admin APIs、frontend console 仍在 roadmap / active work 中。证据：`README.md:150-176`。
- Go HTTP route 已挂 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`，并挂 audit verify、receipts、pricing、auth/session、voucher/invitation、provider accounts、credential acquisition、channel health、pools、billing settings、usage、DLQ、L2 cache 等 admin/user surfaces。证据：`backend/cmd/gateway/routes.go:31-210`。
- Provider catalog 的默认注册表已经覆盖 OpenAI Chat/Responses/Codex、Anthropic、Gemini、OpenRouter、Bedrock、Grok、DeepSeek、Mistral、GroqCloud、Together、Perplexity、Fireworks；同时有 Cursor/Copilot/Gemini Advanced/Antigravity/Kiro/Windsurf session adapters，但后者默认只在 env opt-in 下注册。证据：`backend/internal/provider/registrydefault/default.go:1-144`。
- Rust `core_gateway` 位于 `exploratory/rust-core-gateway/merged`，README 明确是 merged lane；READINESS 写明「探索性 fork，不接入主线」和当前「NO-GO 接入主线」。证据：`exploratory/rust-core-gateway/merged/README.md:1-31`、`exploratory/rust-core-gateway/merged/READINESS.md:1-7`、`exploratory/rust-core-gateway/merged/READINESS.md:85-107`。
- 前端现在不是完整生产 Admin Ops Console。Sidebar 只有 dashboard/audit 可点，账号池、密钥、用量、设置被禁用；mimicry 页面明确写后端 `/admin/v1/mimicry-profiles` 尚未实现。证据：`frontend/components/layout/Sidebar.tsx:23-66`、`frontend/app/mimicry/page.tsx:7-18`。
- Juice 方向同时存在「降算力检测」和「透明版模型映射/替换真相链」两条材料。透明版 research 明确把差异化定义为用户可见 `请求模型 -> HUAKAI 实际路由/映射模型 -> 上游真实返回模型`，这比树上单纯的 benchmark/probe/评分更偏「truth chain」。证据：`docs/process/research/2026-05-21-juice-transparency-refcompare.md:1-9`、`docs/process/research/2026-05-21-juice-transparency-refcompare.md:186-188`；降算力检测材料仍覆盖 reasoning tokens、fingerprint、TTFT、canary probe 等信号。证据：`docs/process/research/2026-05-21-juice-model-degradation-detection.md:10-21`、`docs/process/research/2026-05-21-juice-model-degradation-detection.md:133-180`。

### 1.3 发现的结构偏差

#### A. §6 Rust 网关与 §5 Go 网关并列会误导

§5 是当前 Go production gateway/control plane 主链；§6 是 Rust data-plane / transport hot path / shadow readiness / mimicry 实验合并线。二者可以并列为「两条工程线」，但不能并列标成「两个生产网关」。

建议改名：

- §5：Go 控制面 + 当前生产网关主链。
- §6：Rust 数据面候选 + shadow/canary + transport mimicry 热路径。

自查时 §6 必须单独标注：是否接入主线、是否有真实 upstream smoke、是否有 Go/Rust benchmark、是否有 Owner GO/NO-GO 决策。

#### B. §13 Juice 标签已不够精确

树的 §13 子项是 `Benchmark Prompt / 模型能力探针 / 输出评分 / 降智趋势 / 异常检测 / 自动告警 / 模型切换建议 / 用户可见质量状态`。这些仍对应「模型降算力检测」研究，但不完整覆盖当前「juice 透明版」方向。

当前更准确的一级名建议是：

> §13 模型真实性 / Juice 透明链 / 降算力检测

子项应拆成两组：

- 透明链 MVP：请求模型、HUAKAI 映射/路由模型、上游响应模型、兼容展示模型、替换原因、规则版本、用户可验证 receipt / audit 关联。
- 检测增强：reasoning tokens、thinking block、system fingerprint、TTFT/TPS baseline、private canary probe、输出评分、趋势告警、自动切换建议。

这样不会把「用户可见真相链」误写成只是「质量评分面板」。

#### C. §2 模型接入应区分 provider API 与 subscription/session 反转

§2 列了主流 API provider，方向正确，但项目真实代码还存在另一类 provider family：Cursor、Copilot、Gemini Advanced、Antigravity、Kiro、Windsurf 等 subscription/session paths，且默认不注册或占位。把它们和 OpenAI/Anthropic/Gemini API key 型 provider 混在一个 list 里，会掩盖安全和成熟度差异。

建议 §2 拆三层标注：

- First-class API providers：OpenAI、Anthropic、Gemini、Bedrock、OpenRouter、Grok、DeepSeek、Mistral、GroqCloud、Together、Perplexity、Fireworks。
- Native/protocol surfaces：OpenAI Chat、OpenAI Responses、Anthropic Messages、Bedrock Invoke、native passthrough。
- Subscription/session adapters：OpenAI Codex/ChatGPT、Claude Code OAuth、Cursor、Copilot、Gemini Advanced、Antigravity、Kiro、Windsurf；默认必须有 feature flag / legal guard / verified endpoint 状态。

#### D. §15 前端管理面板作为目标准确，但不能代表真实完成状态

树列出的 Dashboard、用户面板、管理员面板、账号管理、凭证续期、Pool 绑定、模型管理、用量账单、审计验证、Mimicry 配置、可观测面板、系统设置，作为目标面板合理。

真实 repo 中前端更像「联调控制台 + 若干生产化页面雏形 + mock」。自查计划必须把 `frontend/app/*` 的每个页面标为：真实 API 接线、mock API、disabled nav、仅 dashboard mock 数据、或后端端点未实现。

#### E. §12 反封禁/网络策略是高风险能力族，应显式标 feature-gated / roadmap

树包含 Proxy、出口 IP 池、IP 绑定、TLS/HTTP2 指纹、设备指纹、请求节奏、风险探测。HUAKAI docs 和 Rust exploratory 确实有这些方向，但它们不是默认安全基础设施；它们涉及 ToS/legal/security 风险。

建议 §12 审计状态必须额外带字段：

- default off?
- operator legal acknowledgement?
- production fingerprint data exists?
- no bundled third-party captured fingerprint?
- feature flag / plugin / mandatory roadmap?
- 与 §11 privacy redaction、§10 observability、§9 trust chain 的审计联动是否存在?

#### F. 树漏掉或弱化的真实模块

以下是 repo 中真实存在、但 16-section 树没有清晰一级或二级位置的模块：

- **HCSF / capability matrix / protocol loss diagnostics**：在 `backend/internal/proto` 有大量 capability、envelope、fixture、protocol loss 相关代码，树可归 §5 或 §16，但建议在 §5 下显式列 `HCSF canonical envelope / capability matrix / protocol loss`。
- **Response cache / L2 cache / cache token metrics**：Go route 已有 `ResponseCache`、admin L2 cache、cache metrics；树只在 §7 提了 `Route Cache/Token Pool`，但 response cache 是独立用户可见/计费相关模块。
- **Idempotency replay**：代码有 replay store、idempotency payload hash、replay records migration；这影响 billing correctness 和 gateway semantics，应在 §5/§8/§10 中显式列。
- **Email settings / email verification / password reset**：真实 route 存在 `/v1/admin/email` 与 auth email sender，用户与权限 §1 应纳入 email verification / reset / SMTP ops。
- **Edition / run mode / feature flags / plugin boundary**：docs 是核心产品约束，但树没有单独列。可放 §16 或新补为 §17，也可在每 section 加「edition/feature flag/plugin 状态」列。
- **Capacity graph / restock forecasting / fault-domain spillover**：`docs/specs/capacity-graph.md` 是真实 spec，但树只在 §7 间接覆盖，建议在 §7 增加 capacity forecast/restock/fault-domain。
- **Operator tools / fingerprint collector / upstream policy monitor**：`tools/` 是真实目录，和 §12/§16 相关，但树没有运维工具分支。

#### G. 树列了但项目目前名实容易不符的点

这不是逐 leaf 状态审计，只做结构级提醒：

- 「前端管理面板」不应默认理解为完整 admin console；当前有 disabled nav 和 mock 页面。
- 「Rust 高性能网关」不应默认理解为已替代 Go production gateway。
- 「Juice 降智检测」不应默认理解为已实现在线检测；目前更多是 research/specifier 方向。
- 「充值订单/支付」在 §8 中应与 voucher 区分；voucher 已有 code path，真实支付 provider/order settlement 仍需后续审计确认。
- 「代理/IP 池/设备指纹/节奏伪装」在 §12 中应默认视作 gated/roadmap/exploratory，不能标成默认上线能力。

## 2. 自查执行计划草案

| 项 | 内容 |
|---|---|
| Owner directive | "HUAKAI 全面自查 —— 独立起草任务"；背景为 Owner 要求洞③落地后做全面自查，重点查与借鉴项目相比的功能缺失模块。 |
| Scope | In: 粗粒度到 leaf 级的 HUAKAI 状态标注、16-section 树修正建议、sub2api / CLIProxyAPI / new-api parity gap 标注、输出缺失总表。Out: 修改业务代码、改 schema、执行 git、直接落实现、打开 Claude 草案继续对照。 |
| Success criteria | 每个 section 至少有状态证据；每个缺口有 HUAKAI 证据路径和 reference evidence 来源；所有 gap 都映射到合法 disposition 或 mandatory roadmap；输出树能被 Owner 用来排下一轮 implementation slice。 |
| Time estimate | 3 个 Codex specifier lane 并行：HUAKAI inventory 2-3 小时；reference gap reconciliation 3-5 小时；主 session synthesis 2-3 小时。墙钟预计 1 个工作日；若要求重新 source-read 所有 stale reference citations，扩展到 2-3 天。 |
| Blast radius | Docs-only audit，理论不影响运行系统。主要风险是错误状态标注导致 Owner 排期错误、reference claim citation 过期、clean-room 口径不严、把 roadmap/spec 误标为 implemented。 |
| Failure modes | 1. 只看 docs 不看代码导致虚假完成度；缓解：每个 CODED 状态必须有 route/package/test 证据。2. 只看代码不看 parity matrix 导致 feature preservation 断链；缓解：每个 gap 回写 Feature ID 或 NEW-GAP ID。3. reference source 读法污染 clean-room；缓解：specifier lane 只写行为摘要，reviewer 不复读源码。4. 3 lane 边界交叉导致重复/漏项；缓解：按 section ownership 固定，交叉项只在主 synthesis 合并。 |
| Decision points | 1. Owner 是否接受把 §13 重命名为「模型真实性 / Juice 透明链 / 降算力检测」。2. CLIProxyAPI 是否作为本轮正式 reference tracked project，还是只作为临时比较参考。3. Rust §6 是否按 shadow/canary 单独审计，不并入 production gateway parity。4. 输出结果是否只新增 audit 文件，还是同步更新 `03_FEATURE_PARITY_MATRIX.md` / `11_ACCEPTANCE_TEST_MATRIX.md`。 |
| Pre-execution checklist | 1. 冻结本计划与 Claude 草案交叉讨论结果。2. 确认 3 lane prompt 均带 clean-room guard，且并行数不超过 3。3. 建立统一状态分类法。4. 读取 HUAKAI code/docs first。5. 仅在 reference claim 不足或过期时读 reference source。6. 汇总前做 missing/duplicate section check。 |

### 2.1 目标

本次全面自查不是立即实现功能，而是产出一份「真实、可追责、可排期」的状态树：

1. 把 Owner 的 16-section 树转成带状态标注的 HUAKAI 功能树。
2. 标出树漏掉但 repo 已经存在的模块。
3. 标出树列了但目前只有 spec/research/mock/roadmap 的模块。
4. 对照 sub2api / CLIProxyAPI / new-api，把明显 reference gap 标成缺失或弱覆盖。
5. 每个缺失项必须给出合法处置：Implemented / Better / Merged / Safe Equivalent / Plugin / Feature Flag / Mandatory Roadmap。
6. 不因 clean-room 或安全风险删功能；只改变实现边界、默认开关、插件边界或 roadmap 状态。

### 2.2 状态分类法

建议每个 leaf 输出三列状态，而不是一个大词：

#### A. HUAKAI Evidence State

| 状态 | 含义 | 证据要求 |
|---|---|---|
| `CODED-ROUTED` | 代码已接 HTTP/worker/runtime 主链 | route/wiring + package + test 或 migration |
| `CODED-PARTIAL` | 有代码，但只覆盖局部、mock、placeholder、或未生产接线 | package/test + 限制说明 |
| `SPEC-RELEASED` | spec 已 released/可执行，但代码未完全落 | docs/specs path + AT IDs |
| `RESEARCH-ONLY` | 只有调研/分解/计划，没有可执行 spec/code | research/decomposition path |
| `UI-MOCK` | 前端页面存在但 mock 或后端端点未实现 | frontend path + API wiring 状态 |
| `ROADMAP` | parity matrix / feature lock 已记录但未落 | Feature ID + matrix row |
| `NO-EVIDENCE` | 在本轮读取范围内未发现 HUAKAI 证据 | 记录搜索范围 |
| `BLOCKED-OWNER` | 需要 Owner 选择或高风险确认 | OCAW / decision point |

#### B. Parity Disposition

沿用 HUAKAI 现有合法 disposition：`Implemented`、`Implemented Better`、`Merged Equivalent`、`Safe Equivalent`、`Plugin`、`Feature Flag`、`Mandatory Roadmap`。禁止使用 Dropped / Ignored / Not Needed / Too Risky。

#### C. Confidence

| 状态 | 含义 |
|---|---|
| `HIGH` | 有代码+测试或 released spec+AT matrix 互证 |
| `MED` | 有单侧证据，但另一个面缺失，例如有 route 无 frontend / 有 spec 无 code |
| `LOW` | 只来自命名/搜索/单文件，需后续 deeper read |

### 2.3 HUAKAI 代码状态验证方法

每个 lane 对自己 section 先做 HUAKAI-only inventory：

1. 读 `README.md`、`docs/01_PROJECT_BRIEF.md`、`docs/02_CAPABILITY_CONTRACT.md`、`docs/03_FEATURE_PARITY_MATRIX.md`、`docs/11_ACCEPTANCE_TEST_MATRIX.md`、对应 `docs/specs/*.md`。
2. 用 `rg --files` 和 package list 确认 backend/frontend/Rust 实体。
3. 对 `CODED-*` leaf 至少找三类证据之一：
   - HTTP route / worker wiring / main wiring。
   - package implementation + focused tests。
   - migration + query + handler/service tests。
4. 对 `UI-MOCK` leaf 读取 frontend page 和 API client，标真实 API / mock / disabled nav。
5. 对 Rust leaf 读取 `exploratory/rust-core-gateway/merged` README/READINESS/Cargo/tests，单独标 shadow/canary readiness，不混入 Go production。
6. 对 money/auth/quota/schema 相关 leaf 只做状态审计，不提 implementation patch。

### 2.4 Reference parity gap 方法

本轮 reference 对照范围按 Owner 指定：sub2api、CLIProxyAPI、new-api。

执行顺序：

1. 优先使用 HUAKAI repo 内已有、source-cited、日期新鲜的 research/decomposition 文件。
2. 如果某个 reference claim 没有 fresh citation，specifier lane 必须按 clean-room guard 读 reference source，输出行为摘要和 file:line citation。
3. 不复制上游代码、schema、UI、注释、内部命名；只写 user outcome、observable behavior、risk、HUAKAI equivalent。
4. 每个 gap 进入统一表：

| 字段 | 说明 |
|---|---|
| Gap ID | `GAP-<section>-NNN` |
| Section | 1-16 |
| Feature / user outcome | 行为结果，不写上游实现结构 |
| Reference evidence | source-cited research 或新 source citation |
| HUAKAI evidence | code/docs path |
| HUAKAI state | Evidence State |
| Disposition | 合法 disposition |
| Risk | security / clean-room / money / ops / UX |
| Required next action | spec / test / implementation slice / Owner decision |

### 2.5 Clean-room 口径

- HUAKAI 内部代码/docs 无 clean-room 问题，可以直接读。
- sub2api / new-api / CLIProxyAPI source claim 必须走 specifier lane guard。CLIProxyAPI 不在早期 canonical reference list 中，但 Owner 本轮点名，按同等 clean-room 标准处理。
- 如果只引用已存在 research 文件，必须确认该 research 本身有 Source Coverage Proof / citations；不能把未证实二手结论升级成事实。
- 若引用超过 30 天的 reference citation，执行 lane 需 re-fetch/verify HEAD 可达后再复用。若网络或权限不可用，则标 `REFERENCE-STALE`，不得产出强 parity verdict。
- reviewer lane 后续只验证 summary/test/matrix，不复读同一 reference source。

### 2.6 输出物形态

建议输出到一个新目录，避免直接污染现有 parity matrix：

1. `docs/process/audits/2026-05-21-full-audit/01-tree-status.md`
   - 16-section 带状态树。
   - 每个 leaf: HUAKAI Evidence State / Disposition / Confidence / Evidence path / Gap link。
2. `docs/process/audits/2026-05-21-full-audit/02-missing-gap-table.md`
   - 所有缺失、弱覆盖、mock、roadmap、reference-only gap 总表。
3. `docs/process/audits/2026-05-21-full-audit/03-reference-gap-notes.md`
   - sub2api / CLIProxyAPI / new-api 行为差异摘要；只保留行为和 citations。
4. `docs/process/audits/2026-05-21-full-audit/04-owner-decisions.md`
   - 需要 Owner 确认的问题：§13 rename、CLIProxyAPI 口径、Rust production status、payment/anti-ban defaults、是否同步更新矩阵。
5. 后续 Owner 批准后再更新：
   - `docs/03_FEATURE_PARITY_MATRIX.md`
   - `docs/11_ACCEPTANCE_TEST_MATRIX.md`
   - `docs/17_FEATURE_LEVEL_MATRIX.md`
   - 必要的 specs / roadmap。

## 3. Lane 切分建议

Codex 并行上限为 3。本轮建议 3 个 specifier lane 同时跑，主 session 负责 synthesis，不把 synthesis 当第 4 个并行 lane。

### Lane A: Gateway / Provider / Protocol / Rust / Network / Juice

| 范围 | Sections |
|---|---|
| 模型接入、Go gateway、Rust data-plane、网络/反封禁、juice/model truth | §2、§5、§6、§12、§13 |

HUAKAI 读取范围：

- `backend/cmd/gateway/`
- `backend/internal/gateway/`
- `backend/internal/gatewayhttp/`
- `backend/internal/provider/`
- `backend/internal/proto/`
- `backend/internal/transport/`
- `exploratory/rust-core-gateway/merged/`
- `tools/fingerprint-collector/`
- `docs/specs/protocol-translation.md`
- `docs/specs/streaming-forwarder.md`
- `docs/specs/request-pacing-mimicry.md`
- `docs/specs/outbound-ip-pool.md`
- `docs/specs/device-fingerprint-binding.md`
- `docs/specs/active-anti-detection.md`
- `docs/process/research/2026-05-21-juice-*.md`

Reference 对照重点：

- sub2api / CLIProxyAPI / new-api 的 provider breadth、model mapping、protocol compatibility、streaming edge cases、model truth / substitution transparency。
- 对 §13 特别输出「透明链」和「降算力检测」两个子树，不混写。

预期风险：

- 容易把 Rust exploratory 当 production；必须分开。
- 反封禁材料敏感；默认按 feature flag / roadmap 口径。
- reference protocol adapter 不能复制实现结构，只能写 behavior。

### Lane B: Identity / Credentials / Account Pool / Routing / Security

| 范围 | Sections |
|---|---|
| 用户与权限、账号凭证、账号池/资源池、路由调度、安全隐私 | §1、§3、§4、§7、§11 |

HUAKAI 读取范围：

- `backend/internal/auth/`
- `backend/internal/userauth/`
- `backend/internal/usersession/`
- `backend/internal/admin/`
- `backend/internal/adminhttp/`
- `backend/internal/credentialacq/`
- `backend/internal/credentialstore/`
- `backend/internal/credentialworker/`
- `backend/internal/pool/`
- `backend/internal/router/`
- `backend/internal/channelhealth/`
- `backend/internal/privacy/`
- `backend/internal/redact/`
- `backend/internal/rate/`
- `backend/sql/migrations/0001_*` through relevant auth/pool/credential/channel-health/privacy migrations。
- specs: user-authentication、session-management、upstream-credential-management、credential-acquisition、pool-routing、rate-limiting、privacy-no-user-data-logs、channel-health-auto-disable。

Reference 对照重点：

- sub2api account acquisition / renewal / account availability / sticky / per-account concurrency。
- new-api auth / key / group / channel / rate limit / model binding。
- CLIProxyAPI session/OAuth/account profile behavior only where Owner wants provider-session comparison。

预期风险：

- Auth / quota / database schema 是高风险实现区；本轮只审计和计划，不 patch。
- 「用户组」与「账号绑定」可能在 docs/specs 与 schema 状态不同；必须标 confidence。

### Lane C: Billing / Trust / Observability / Growth / Frontend / Docs

| 范围 | Sections |
|---|---|
| 用量计费、审计信任链、可观测运维、社区商业增长、前端、文档测试发布 | §8、§9、§10、§14、§15、§16 |

HUAKAI 读取范围：

- `backend/internal/billing/`
- `backend/internal/audit/`
- `backend/internal/auditledger/`
- `backend/internal/observability/`
- `backend/internal/obs/`
- `backend/internal/dlq/`
- `backend/internal/eventbus/`
- `backend/internal/community/`
- `backend/internal/voucher/`
- `backend/internal/email/`
- `frontend/app/`
- `frontend/components/`
- `frontend/lib/api/`
- `docs/openapi/openapi.yaml`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/11_ACCEPTANCE_TEST_MATRIX.md`
- `docs/15_RELEASE_GATES.md`
- specs: observability-billing、trust-chain-user-verifiable-ledger、user-consumption-transparency、voucher-system、community-invitation-referral。

Reference 对照重点：

- sub2api / new-api billing, voucher, invitation/referral, usage transparency, admin ops panel。
- CLIProxyAPI usage/log/model mapping transparency only as it intersects §13/§15；coordinate with Lane A to avoid duplicate claims。

预期风险：

- Money-path states must not be overclaimed from tests alone；route + migration + settler/worker + receipt evidence needed for `CODED-ROUTED`。
- Frontend has mixed production-looking components and mock/debug panels；classify honestly。
- Docs/release gates may look complete while code still partial；keep docs status separate from implementation status。

## 4. 交叉讨论前的预判分歧点

1. Claude 可能更倾向把 16-section 树保持不变，只在每节内补状态；我倾向轻改 §6、§13，并补 missing modules，否则树会误导生产状态。
2. Claude 可能把 Juice 先按 Owner 最新口径聚焦「透明版」；我建议同时保留「降算力检测」为增强层，但 MVP 子树必须是 truth chain。
3. Claude 可能把 Rust 列为独立实现线；我同意独立列，但会强标 `NO-GO production` / `shadow readiness`，避免它和 Go gateway 等价。
4. Claude 可能把 frontend 作为 §15 整节；我建议 §15 必须按真实 API / disabled nav / mock API 三类拆状态。
5. CLIProxyAPI 不在早期 canonical reference list，但 Owner 本轮点名；我建议作为本轮正式 reference 输入，执行同等 clean-room guard。

## 5. Owner 中文摘要

本次 Codex 独立评估认为 16-section 树的一级结构总体准确，但它把生产主链、实验线、spec/research 和 UI mock 混在一起，后续全面自查必须加状态轴。主要偏差是：§6 Rust 不能与 §5 Go production gateway 等价；§13 Juice 应改成「模型真实性 / 透明链 / 降算力检测」；§15 前端目标准确但真实状态是局部接线和 mock 并存；§2 provider list 应区分 API provider 与 session/subscription adapter；§12 反封禁必须按 high-risk feature flag / roadmap 处理。建议用 3 个 Codex lane 并行：A 管 gateway/provider/protocol/Rust/network/juice，B 管 identity/credential/pool/routing/security，C 管 billing/trust/ops/growth/frontend/docs。没有功能缩水；clean-room 风险通过 specifier lane guard、source citation、行为摘要和 reviewer 分离控制；安全风险主要集中在 money/auth/quota/schema/anti-ban，只审计不实现。需要 Owner 确认 §13 命名、CLIProxyAPI 是否正式纳入本轮 reference、Rust 是否按 shadow/canary 单独审计、以及审计完成后是否同步改 parity/AT matrix。
