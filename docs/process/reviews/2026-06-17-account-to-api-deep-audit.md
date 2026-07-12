# 账号→API 转换流水线 深度审计（account→API conversion pipeline）

- **日期**：2026-06-17
- **范围**：凭证获取 → 凭证存储/轮换 → 账号选择/池 → 协议转换 → 上游派发/传输 → relay 计费结算（即"把存储的账号凭证转换为对外 API 服务"的整条核心链路）
- **方法**：6 阶流水线 deep-read（每阶 effort:high，逐函数追控制流/数据流），其后一轮**对抗复核（refute-by-default，effort:high）**对每条 S1/S2 逐项设法证伪，只保留无法证伪的发现。17 个 agent，~1.53M token，~10.8 分钟。
- **对照镜像**：CLIProxyAPI（`~/refs/CLIProxyAPI`）作为 relay 行为参考；sub2api / new-api 作为契约旁证。

## 范围与局限（必读，勿当"已证完备"）

- 这是**第二遍深审**（比 2026-06-17 后端完整性审计那遍 breadth-first 更深：逐函数追不变式、查竞态/静默回退/计费泄漏，不只是"缺端点"）。但**仍非穷尽证明**。
- 每条 S1/S2 都过了一轮对抗 refute（设法证伪失败才保留）；这降低了误报率，但**不等于零漏报**——未覆盖的代码路径、未触发的并发交织、外部供应商真实行为的部分前提，仍可能藏有问题。
- "确认 S1/S2" = **本轮深读 + 对抗复核后正面确认的真缺陷**，含完整 in-repo 链路引用；**不代表穷举了全部缺陷**。
- 几条发现的"灾难级"影响是**有门槛的**（dormant/unwired 路径、需要运维选非默认模式、需要特定 race 窗口）——已在每条内逐一标注真实触发条件，请按门槛而非最坏措辞排期。
- **本文是审计/评审产物，非代码修复。** 计费/认证核心改动属 Owner 部署门禁，所有 fix-hint 仅供决策，未自动落地。

## 阶段裁决

| 阶段 | 裁决 | 发现数 |
|---|---|---|
| 凭证获取 credential-acquisition | solid | 8 |
| 凭证存储/轮换 credential-storage-rotation | minor-gaps | 6 |
| 账号选择/池 account-selection-pool | minor-gaps | 5 |
| **协议转换 proto-conversion** | **real-issues** | 5 |
| 上游派发/传输 upstream-dispatch-transport | minor-gaps | 7 |
| **relay 计费结算 relay-billing-settlement** | **real-issues** | 6 |

**合计**：37 项发现 → 对抗复核后确认 **S1 × 3、S2 × 7**，另 20 条正面确认（健康路径）。

---

## 确认 S1（3 项，全在 account→API 热路径上）

### S1-1　OpenAI 兼容上游的 tool-call ID 被丢成空串（跨协议工具调用断裂）

- **类型**：correctness-bug
- **位置**：`internal/proto/openai/sse.go:513-523`（`canonicalOpenAICallID`）→ `internal/proto/tool_call_id.go:12-18,44-66`（`ToCanonicalCallID`/`stripCallPrefix`）
- **缺陷**：响应侧 OpenAI tool-call ID 走 `proto.ToCanonicalCallID(id, UpstreamProtocolOpenAI)`，该函数**强制要求 id 以字面 `call_` 前缀开头**（`stripCallPrefix`）。但大量经 `openai_chat` marshal 族路由的供应商——Mistral（9 字符 ID）、Qwen、GLM、Kimi 等（`provider/registrydefault/default.go` 注册）——发出的 tool_call ID **不带 `call_` 前缀**。前缀不匹配时 `canonicalOpenAICallID` 返回 `CanonicalID=""` + 一条 loss——**真实 ID 被丢成空串**。buffered 路径（`sse.go:621-624`）与 streaming 路径（`sse.go:383-386`）都中招。
- **影响**：
  - **Anthropic buffered 客户端**：`anthropic_messages_response.go:108` 硬报错 `tool_use missing call_id`，整条响应失败。
  - **OpenAI buffered 客户端**：`openai_chat_response.go:101-102` **也**对空 CallID 硬报错——即 OpenAI→OpenAI 走 HCSF 路径同样失败，爆炸半径超出跨协议。
  - **Anthropic streaming 客户端**：`anthropic_messages_stream.go:255` 发出 `content_block_start` 带 `"id":""`，客户端收到一个永远无法关联 tool_result 的 tool_use——**静默工具调用损坏**。
- **复核（refute 失败）**：① 路由确认：mistral/qwen/glm/kimi 等均无条件 `MustRegister` 到共享 `&openai.Adapter{}`；非流式默认走 HCSF（默认开）。② 规范化在 HCSF 路径上强制执行（`upstream_dispatcher_hcsf.go:150`、`chat_completions_handler.go:810`），与客户端协议无关（canonical-bus 架构）。③ 缺陷机制确认：`stripCallPrefix`（`tool_call_id.go:44-66`）硬要求 `call_` 前缀，不匹配返回 `""` 无 fallback。④ 下游影响逐点确认（见上）。⑤ 镜像对照：CLIProxyAPI `internal/util/claude_tool_id.go` **原样保留**上游 id（仅清非法字符、空时才合成 fallback），无此故障。⑥ **自相矛盾（最强佐证此为 bug 而非有意）**：HUAKAI **自己**的 Anthropic 适配器（`anthropic/sse.go:486-495`）与 Gemini 适配器（`gemini/sse.go:392-400`）在同样失败时都合成确定性 `call_...` fallback 仅记 loss——唯独 OpenAI-compat 路径丢空。这种不对称表明 OpenAI 路径只是漏掉了别处已实现的 fallback。
- **唯一无法从 repo 内证实的前提**：供应商真实发出无前缀 ID（Mistral 9 字符无前缀已是公开行为）；现有测试 `sse_test.go:253` 只测带 `call_` 前缀的 id，**无任何用例覆盖无前缀场景**，故代码对此无保护。综合 in-repo 全链路成立 + 前提扎实，default real=true。
- **fix-hint**：在 `canonicalOpenAICallID` 里，`ToCanonicalCallID` 失败时不要返回空——镜像现有 Anthropic/Gemini 行为：把原始上游 id 清洗到 `[A-Za-z0-9_-]` 并包成 `call_<sanitized>`（或空时合成确定性 fallback），仅记一条 loss。等价方案：放宽 `ToCanonicalCallID` 对 `UpstreamProtocolOpenAI` 的处理，bare id 接受并加前缀而非拒绝。补判别性测试：无 `call_` 前缀的 id（如 9 字符 Mistral 式）走 buffered + streaming 两条路径，断言 canonical CallID 非空且下游不硬报错。

### S1-2　/v1/completions 流式 relay 在可取消请求 ctx 上结算（已交付但断连→计费泄漏）

- **类型**：billing-leak
- **位置**：`backend/internal/completionshttp/attempt.go:171`
- **缺陷**：`/v1/completions` 流式 relay 在**原始可取消请求 ctx** 上结算。`finishStreamingResponse()` 经 `streamAndCapture`（`attempt.go:150`）把上游 SSE 字节写给客户端，**整流完成后才**调 `ex.d.Settler.Settle(ex.ctx,...)`（`attempt.go:171`）。`ex.ctx` 是 `context.WithValue(r.Context(),...)`（`handler.go:109,115`）——net/http 在客户端断连时取消它。`DefaultSettler.Settle` 开头即 `s.pool.BeginTx(ctx,...)`（`settler.go:82`），ctx 已取消则立即出错。**一条已完整交付给客户端、但客户端在上游 EOF 之后、Tx2 开始前/中断连的流，其结算被永久丢弃：内容已交付，claim 从未提交，不计费。**
- **影响**：已完成但断连的流式 `/v1/completions` 请求**完全逃过计费**（交付的 token 永不收费；claim 一直占位直到 lease sweep 归零中止）。活 relay 路径上的收入泄漏，客户端在流末断连即可触发。
- **fix-hint**：`context.WithoutCancel` 脱钩结算 ctx + 加 DLQ 恢复（详见 S1-3）。

### S1-3　completionshttp 缺 chat 路径的"交付后结算保护"（瞬态结算失败=不可恢复的钱丢失）

- **类型**：missing-error-path
- **位置**：`backend/internal/completionshttp/attempt.go:171`（及非流式 `attempt.go:117`）
- **缺陷**：`completionshttp` **完全没有** gateway chat 路径具备的交付后结算保护。chat 路径 `forwardSSEAndSettle` 用 `context.WithTimeout(context.WithoutCancel(ex.ctx),30s)`（`chat_completions_stream.go:279`）把结算从客户端取消中脱钩，**且**经 `settleCompletionWithRecovery(...SourceStream)`（`stream.go:300`）把失败结算路由到持久 DLQ。completionshttp 直接在 `ex.ctx` 结算，**无 WithoutCancel、无 recovery**：`completionshttp.Deps`（`handler.go:46-61`）无 `SettleRecoveryDLQ/CompletionBus`，`completionsHandlerDeps`（`routes.go:697-714`）一个都没接。流式结算失败（`attempt.go:171-174`）只置一个 `X-Huakai-Settle-Failed` 头就返回——**计费事件丢失，无重试。**
- **影响**：已交付 `/v1/completions` 流上任何瞬态结算失败（DB 抖动、ctx 取消、序列化错误）都是**不可恢复的钱丢失**，不像 chat 路径经 DLQ worker 持久重结算。
- **复核（refute 失败）**：`/v1/completions` 是活路由（`routes.go:92`）且服务流式响应。交付后结算（`attempt.go:171`）跑在所有 chunk flush 给客户端之后，且在 `ex.ctx`（=`r.Context()`，客户端断连可取消）上。唯一兜底 `LeaseSweeper.sweepOnce`（`lease_sweep.go:78-113`）只把孤儿 claim 以 0 token Abort，**不重结算已交付用量**——已交付未结算的流变成永久不计费。同一缺口也影响非流式结算（`attempt.go:117`），但流式交付后是最坏情形（内容已交付）。
- **fix-hint**：给 completionshttp 同等保护：① `completionshttp.Deps` 加 `SettleRecoveryDLQ`（+可选 `CompletionBus`），在 `completionsHandlerDeps`（`routes.go:697-714`）接 `d.dlqService`；② 交付后流式结算（`attempt.go:171`，理想含 JSON 结算 `attempt.go:117`）用 `context.WithTimeout(context.WithoutCancel(ex.ctx),30s)` 脱钩；③ 结算出错时路由到 settlementrecovery DLQ（镜像 `settleCompletionWithRecovery`/`SourceStream`）。补判别性测试：对已交付流注入结算错误，断言发生 DLQ 入队（变异删除入队→测试转红）。

> **S1-2 与 S1-3 是同一处（`completionshttp` 交付后结算）的两面**：S1-2 是 ctx 取消导致丢结算的触发，S1-3 是缺 DLQ 恢复导致丢了无法补救。修复应一并处理。**均为 money-adjacent，属 Owner 部署门禁。**

---

## 确认 S2（7 项）

| # | 阶段 | 包 | 类型 | 一句话 |
|---|---|---|---|---|
| S2-1 | 凭证获取 | anthropicoauth | silent-fallback | Anthropic OAuth **刷新**路径用 `http.DefaultClient` 而非 mimicry uTLS（获取路径有，刷新路径漏）→ 长期高频刷新走原生 TLS 指纹，可被反 ban 标记 |
| S2-2 | 凭证存储/轮换 | credentialworker | incomplete-mode | Azure（access-token-only）登记为 refreshable 但唯一适配器是 mock，无真实 AAD 刷新→ token 过期后因 `allowGrace:true` 仍被当作有效 Bearer 服务（非镜像 parity 缺口，是 HUAKAI 自有不完整模式） |
| S2-3 | 凭证存储/轮换 | credentialworker | test-gap | rotation 测试只验"老 created_at 被标记"；`DueForRotation` 选 `created_at` 而结构字段叫 `LastRefreshAt`（`SaveRefreshSuccess` 写 `last_refresh_at`）——健康但创建久的 OAuth 凭证会被误降级。**门槛：rotation 扫描在 wiring 里未接、生产 OFF（dormant）** |
| S2-4 | 账号选择/池 | pool/dispatcher+provider | silent-fallback | `provider_accounts.expires_at` 被 migration 文档为生命周期 gate-2，但**选择→派发热路径无处强制**：热路径 eligibility 查询不 filter 也不 SELECT expires_at，Lifecycle gate 槽是 `AllowAllGate{}`→ 管理员设了过期但 enabled=true 的账号仍被选中，每请求烧一轮路由/slot/claim 直到 health 冷却降级 |
| S2-5 | 协议转换 | proto | test-gap | OpenAI tool-call ID 仅有带 `call_` 前缀的 fixture，`proto_test.go:74` 还断言保留前缀；无 bare-id 端到端用例 → **掩盖了 S1-1**（变异翻转前缀严格性不会转红） |
| S2-6 | 协议转换 | proto/gemini | correctness-bug | 与 S1-1 同型：Gemini functionCall **带 id** 时走 `ToCanonicalCallID(id,UpstreamProtocolGemini)` 要求 `func_` 前缀，真实 Gemini id 无此前缀→ provided-id 丢成 `""`+loss。空 id 分支（合成 `call_%08x`）反而处理良好，唯独 provided-id 分支误处理。降级为 S2 因 Gemini 多数 functionCall 无 id（走良好的空分支） |
| S2-7 | 上游派发/传输 | gateway | correctness-bug | `DispatchHCSF`（默认非流式 buffered 路径）以 `io.LimitReader(resp.Body,1<<20)` 读成功响应——**硬 1 MiB 截断、无溢出检测**。>1 MiB 合法响应被静默截断→`ProviderResponseToCanonical` 失败→客户端收到 502 误分类为 parse error 而非 too-large。对比 legacy 路径 `readRawBufferedUpstreamBody` 读 limit+1 检测溢出返回 `CodeUpstreamResponseTooLarge`。SSE 形 buffered >1 MiB 时还会计费一个被截断的部分响应 |

### S2 详情要点（含触发门槛）

- **S2-1**（anthropicoauth 刷新 mimicry 缺失）：`NewRefresher`（`wiring.go:1278-1281`）只传 `WithFallbackRefresher` 不传 `WithHTTPClient`，`httpClient()`（`refresher.go:408-413`）回退 `http.DefaultClient`。对 `api.anthropic.com/v1/oauth/token` 的周期性刷新走原生 Go TLS 指纹。S2（安全/反 ban 姿态退化，非正确性/计费/数据丢失）。fix：加 `WithHTTPClient(DefaultHTTPClient())` + 失败响亮自检（镜像 `assertAnthropicClaudeAIOAuthExchangerHasHTTPClient`）。

- **S2-2**（Azure 不完整模式）：access-token-only Azure 凭证被设 `refresh_before_at`，worker 选中→mock 适配器无 `mock_token_endpoint` 返回 `ErrNoRefreshRequired`（仅 throttle +30s）→token 真过期→`allowGrace:true` 绕过过期 gate（`crypto/types.go:254`）仍服务过期 Bearer。**门槛：需运维选 AAD access-token 粘贴路径而非静态 `azure_api_key`（非默认次要模式）**。注：`BuildAzureCandidate` 是死代码（0 调用方），活触发是通用 ingest plan。fix：要么 Azure 仅在有真实 AAD 刷新材料时才 refreshable，要么实现真 AAD client-credentials 适配器，要么 Azure 设 `allowGrace:false`。

- **S2-3**（rotation test-gap + created_at/last_refresh_at 语义错配）：`DueForRotation` 选 `created_at`（`rotation_pg.go:36`），但候选字段叫 `LastRefreshAt`。健康但创建久的 OAuth 凭证会被标记 `needs_rotation`，而 `needs_rotation` 同时被服务查询与刷新查询排除，无自动回服路径。**门槛：`WithRotationScan`/`NewPostgresRotationStore` 在 `wiring.go` 从未调用——扫描未接、生产 OFF**。即"真实风险但当前 dormant 路径上的缺测"，S2 不升级。fix：补判别性集成测试（老 created_at + 近 last_refresh_at 仍在服务/刷新集；needs_rotation 排除；回服转换）+ 解决字段名/列名错配。

- **S2-4**（expires_at 生命周期 gate 未强制）：`ListEligibleAccountsByPoolGroup`（`pool_accounts.sql:148-177`）不引用 expires_at；`DefaultGateChain` 的 Lifecycle 槽是 `AllowAllGate{}`，`AuthCredentialGate` 未接；`CredentialVault.Resolve`（`postgres_vault.go:111`）只查 enabled+deleted_at。expires_at 与 credential_state 解耦（非 OAuth 类型无 refresher 触碰 credential_state）。触发：api_key/upstream_static 账号过期但 enabled=true。fix（载重修复）：`ListEligibleAccountsByPoolGroup` WHERE 加 `AND (pa.expires_at IS NULL OR pa.expires_at > NOW())`；纵深防御再在 `queryProviderAccount` SELECT + Resolve 加 `ErrAccountExpired`。

- **S2-5**（OpenAI tool-call-ID 缺测）：见上表；与 S1-1 联动，修 S1-1 时一并补 bare-id 判别 fixture。

- **S2-6**（Gemini provided-id 丢弃）：`geminiCanonicalCallID`（`gemini/sse.go:390-401`）provided-id 翻译失败返回 `""`。镜像 Anthropic fallback（`anthropic/sse.go:488-494`）或 sanitize-and-pass-through。与 S1-1 同根，可一并修。

- **S2-7**（HCSF 1 MiB 静默截断）：`DispatchHCSF`（`upstream_dispatcher_hcsf.go:127`）默认开（除非 `HUAKAI_DISPATCH_HCSF=0`）。普通单对象 JSON >1 MiB → 请求中止报 opaque 502（无计费泄漏）；SSE 形 buffered >1 MiB → `ReconstructBufferedFromSSE` 宽容地丢截断尾事件、按之前完整事件计费部分响应（窄计费泄漏）。镜像 CLIProxyAPI 读非流式 body 用**无界** `io.ReadAll`，故 HUAKAI 默认路径是相对镜像的回归。fix：读 `(1<<20)+1` 加溢出哨兵，>1<<20 返回映射到 `CodeUpstreamResponseTooLarge` 的类型化错误；把 1 MiB cap 与 `maxRawBufferedUpstreamBodyBytes` 合并为单一共享 limit 防漂移。

---

## 正面确认（20 条健康路径，摘要）

**凭证获取（solid）**：OAuth 回调先查 terminal/consumed→过期→常量时间 state-hash 比对（`subtle.ConstantTimeCompare`）→PKCE verifier 解密（AAD 绑 tenant/account/vendor/mode）；CAS `status NOT IN (terminal)` 谓词区分 replay/not-found。运维/管理员提供的 OAuth 端点 **SSRF 分层防御**（静态拒 non-https/loopback/RFC1918/link-local/169.254.169.254/metadata 名 + dial 时 IP 重校验 + redirect block，`auth.NewSSRFProtectedOAuthClient`）。`ParseImportContent` 拒绝 JSON 形畸形行，防"看似成功实则不可用"的导入。

**凭证存储/轮换**：at-rest 加密 **AES-256-GCM**（每次随机 12 字节 nonce、AAD 绑 tenant/account/vendor/mode/version/key_id、AADHash 常量时间比对、`privacy.Zeroize`、32 字节密钥强制、scheme pinning），无 nonce 复用路径；Sign（ed25519 长度校验）正确。刷新写回 race-safe（`token_version` CAS、advisory-lock tx 内 `credential_version` CAS + 同 tx 审计插入；`ErrNoRefreshRequired` 只置 `next_attempt_at` 不污染 state）。

**账号选择/池**：slot double-spend 正确防止（`IncrementInFlightCount` WHERE `in_flight_count<cap` + `InsertSlotAcquisition` 同 Serializable Tx，0 行→`ErrNoSlotAvailable`，40001 重试 3 次；release 幂等 + `sync.Once`）。3-ID claim/lease/slot 体系自洽（per-attempt ClaimID、`WriteAcquisitionToken` CAS WHERE `status='reserving'`、claim 无 writer 是硬错非静默跳过、PASR 后变异失败 fail-closed 不重跑 default）。model-route pinning 不静默穿透（routed 空→`ErrNoEligibleAccount`）。cache-miss 降级 atomics 并发安全。

**协议转换**：`Adapter.CanonicalToProviderRequest` 返回 `ErrNotImplemented` 确属**请求路径死代码**（活路径是 `gateway.MarshalToProviderRequest`），无供应商族经 stub 静默丢请求内容。协议 loss 记账流式正确传播（append 到 `acc.StreamProtocolLoss`→`UsageRecordDraft`），用量累积 set-to-latest 对累积型供应商正确无双计。

**上游派发/传输**：失败 attempt 的 slot/lease 释放正确（每条失败路径 `Settler.Abort`，Serializable Tx2 内 `FOR UPDATE` claim、释放余额 hold、幂等 `ReleaseSlotAndDecrementInFlight`，abort 失败经 `degradeFailureIfAbortFailed` 禁重试防双花）。**有意无 mid-stream 模型/账号 fallback**（retry/fallback 仅 pre-delivery，`!DeliveryStarted` gate，与 CLIProxyAPI 一致）。传输选择 fail-closed + proxy 隔离防泄漏（拒非法 combo、sidecar 不可用不静默降级、`Proxy=nil` 剥离 env proxy、`WrapTransportWithProxy` fail-loud 防泄漏真实出口 IP、mimicry 模板缺失 fail-close 不回退 Anthropic 指纹）。错误分类→重试决策映射自洽。

**relay 计费结算**：**非流式** `/v1/completions` buffered 路径非泄漏（`Settle` 在写任何响应字节前调用）。retry/fallback 无双计费（reserve 前先 `abort`；无 Idempotency-Key 每 attempt 新 logicalRequestID；有 key 走 `ReReserveAbortedClaim` 复活同行）。`SumWindowCost` 截断使 5h window cap 略宽松——与 windowcost fail-open 安全契约一致（非用户计费 ledger）。**主 gateway 流式结算路径 sound**（`WithoutCancel`+30s、三态结算条件防中止已交付流、失败入 settlementrecovery DLQ、ledger-fail-closed 强制 abort）——**这正是 completionshttp（S1-2/S1-3）所缺的保护**。

---

## 三联建议（triage）

1. **proto tool-call ID 族缺陷（S1-1 + S2-5 + S2-6）**：同根（`stripCallPrefix` 前缀严格 + 失败丢空），OpenAI 族（S1-1，含 Mistral/Qwen/GLM/Kimi）+ Gemini provided-id（S2-6）。修复方向单一：失败时 sanitize-and-wrap/合成 fallback，镜像本仓 Anthropic 已有行为；配 bare-id 判别 fixture（S2-5）。**correctness，非 money——但属核心 relay 转换，建议 Owner 拍板后单独切片修复 + 强变异测试。**
2. **completionshttp 交付后结算（S1-2 + S1-3）**：money-adjacent 收入泄漏 + 不可恢复钱丢失，同一处两面。修复方向单一：`WithoutCancel` 脱钩 + 接 settlementrecovery DLQ（chat 路径已有的保护移植过来）。**属 Owner 部署门禁（计费核心），需 Owner 确认后再动。**
3. **生命周期/姿态/dormant（S2-1/S2-2/S2-3/S2-4/S2-7）**：S2-4（expires_at gate）有单行 SQL 载重修复、影响活路径，性价比最高；S2-1（刷新 mimicry）安全姿态；S2-7（HCSF 截断）边界正确性；S2-2/S2-3 门槛较高（非默认模式 / dormant 扫描），可较低优先。

> 本审计为评审产物。所有计费/认证核心 fix-hint 仅供 Owner 决策与排期，**未自动落地**。
