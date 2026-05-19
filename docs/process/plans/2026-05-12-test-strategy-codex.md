# 2026-05-12 HUAKAI 测试策略（Codex）

| Owner directive | “读取整个 HUAKAI 项目（backend / docs / frontend 关键路径），起草完整测试方案文档” |
|---|---|
| Scope | HUAKAI 内部 backend、docs、frontend 关键路径；测试策略，不写测试代码 |
| Out of scope | 非 MIT reference project 源码；production code/config/LICENSE；真实 AWS/Bedrock 凭据假设 |
| Success criteria | 10 个测试维度均给出现状、P0-P2 优先级、engineer-day、依赖、风险；末尾给 Owner 可立即启动的 P0 工作 |
| Truth-first note | 本文只写已观察到的 HUAKAI 内部事实和由此得出的测试策略推断；未读 reference project 源码 |

## 0. 总体结论

当前 HUAKAI 已有相当多 Go 单测骨架和局部契约测试，但测试金字塔的底部正在被一个 P0 编译问题卡住：`backend/internal/proto` 当前无法通过空跑编译，错误为 `cross_ref.go` 引用未定义的 `validateDataRetentionConsistency` 和 `validateProtocolLossEntriesAll`。在这个问题修复前，所有更高层测试统计都只能作为策略输入，不能作为 release 信号。

项目测试现状可以概括为：

- 后端测试文件密集：本地 grep 观察到 `backend/internal` 与 `backend/cmd` 下约 108 个 `*_test.go` 文件、约 959 个 `Test*` 函数；其中 `gateway`、`pool`、`proto` 是测试最集中的包。
- HCSF fixture 已有 35 个，覆盖 envelope、event、response、edge_case、regression 五类；目前偏正向合同验证，negative 和 vendor variant 仍不足。
- `backend/internal/proto/envelope_test.go` 已有 54 个 `TestINV*`，但 INV-19/28/30-33/38/40/44-50 仍需要明确补测、弱覆盖标注或正式 disposition。
- PASR D1-D5 方向已有 cache-aware selector、segment table、bitmap、shadow/canary dispatcher、claim/slot 组件，但跨包一致性测试和稳定性测试还没有形成发布门槛。
- SSE adapters 已覆盖 Anthropic/OpenAI/Gemini/Bedrock-on-Anthropic 的不同深度；Bedrock 只能走 mock/fixture，不做真实 AWS smoke。
- `backend/cmd/gateway` 是当前仓库实际单体入口；任务描述中的 `backend/cmd/huakai` 与仓库现状不一致，应作为命名漂移处理。
- Admin 路由当前实际可用面与“admin endpoints M1..M5”目标不完全一致：API Key 路由存在，pools/provider-accounts/usage/billing/audit/DLQ 等仍有 501 或未完成路径，需要测试策略区分“已实现”“计划中”“mock UI”。
- Frontend P1 dashboard 已有 mock 默认、真后端开关、SSE client 和 OpenAPI-derived types；但缺少 frontend test/a11y/hydration/SSR 测试脚手架。
- 仓库未观察到 `.github` CI workflow；`backend/Makefile` 有 `test`、`vet`、`test-integration` 等入口，但 CI/CD 门槛尚未固化。

建议测试路线：P0 先让 backend 编译恢复绿色，并补齐 HCSF validator 的 release-blocking gap；P1 建立 mock upstream E2E 与 PASR 跨包一致性；P2 再推进 load、chaos、security 深水区和 frontend 自动化质量门。

## 1. Unit per file

### 现状

重点路径：

- `backend/internal/proto/capability_*.go`、`envelope_*.go`：HCSF v0.4 协议层和 validator。
- `backend/internal/proto/envelope_test.go`：约 2094 行，54 个 `TestINV*`。
- `backend/internal/provider/*` 与 `backend/internal/gateway/*sse*`：Anthropic/OpenAI/Gemini/Bedrock SSE adapters、scanner、buffered helper。
- `backend/internal/pool/*`：PASR selector、claim gate、slot manager、shadow/canary dispatcher。
- `backend/internal/cache_routing/*`、`backend/internal/cachemetrics/*`、`backend/internal/pool/prefix_segment.go`：prompt hash、auto-inject、segment table、bitmap、cache metrics feedback。

`envelope_test.go` 已观察覆盖的 INV：

- 强覆盖或明确测试：INV-0, INV-1, INV-2, INV-3, INV-4, INV-5, INV-6, INV-7, INV-8, INV-9, INV-10, INV-11, INV-12, INV-13, INV-14, INV-15, INV-16, INV-17, INV-18, INV-23, INV-25, INV-26, INV-27, INV-29, INV-34, INV-35, INV-36, INV-37, INV-39, INV-41, INV-42, INV-43, INV-46。
- 弱覆盖：INV-7 对 protocol_loss silent-drop 有覆盖，但未等价覆盖 INV-44/45 的 v0.4 全量严格性；INV-33 相关 cross-ref helper 已被调用但当前未定义，属于编译失败而非有效覆盖。
- 缺口或需 disposition：INV-19, INV-20, INV-21, INV-22, INV-24, INV-28, INV-30, INV-31, INV-32, INV-33, INV-38, INV-40, INV-44, INV-45, INV-47, INV-48, INV-49, INV-50。

当前 P0 阻塞：

- `go test ./internal/proto -run '^$'` 无法编译，`cross_ref.go` 引用未定义 helper。
- 在此修复前，`envelope_validate.go` 中 “ValidateEnvelope covers INV-1..50” 不能作为真实测试通过信号。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | 修复 `backend/internal/proto` 编译红树，并把 `go test ./internal/proto -run '^$'` 作为最小门槛 | 0.5-1 ed | HCSF D4 helper 实现或调用边界确认 | 如果只删除调用而不补 validator，可能制造虚假绿色 |
| P0 | 为 INV-30/31/32/33/40/45 增加 validator unit 设计；其中 INV-33/45 先解决 helper 缺口 | 1-1.5 ed | P1 payload refinement 最终 INV 编号 | 数据保留和 protocol_loss 是 release-blocking 合同，弱测会导致 silent-drop |
| P0 | 对 INV-19/28/38/44/47/48/49/50 写明确 disposition：Implemented、Audit-only、Deferred、Reserved，不允许“消失” | 0.5 ed | `docs/process/plans/2026-05-12-p1-capability-payload-refinement-synthesis.md` | 编号漂移会让矩阵无法交叉审查 |
| P1 | SSE adapter unit matrix：Anthropic/OpenAI/Gemini/Bedrock 分别覆盖 normal、unknown event、usage、synthetic terminal、partial/truncated stream | 2-3 ed | mock SSE/eventstream fixture | OpenAI/Gemini buffered 或 canonical conversion 未完成时需标注 expected gap |
| P1 | PASR per-file unit：segment aging、K=3 bitmap、miss demote、LRU tie、shadow read-only、canary sample | 1.5-2 ed | 现有 pool/cachemetrics helper | cachemetrics 0/0 miss 是否触发 observer 需先澄清，否则 demote 测试可能测试错路径 |
| P2 | HCSF JSON fuzz/property：RawMessage、nil/empty slice、extension prefix、round-trip deterministic marshal | 2 ed | P0 validator 稳定 | fuzz 失败面大，不能在红树状态启动 |

## 2. Integration multi-package

### 现状

已观察到的集成面：

- PASR 由 selector、segment table、cache feedback、slot manager、claim gate、dispatcher 共同组成。
- DB slot acquire/release 有表侧计数和 acquisition token；claim gate 会写入 claim；selector dispatcher 支持 default/shadow/canary/pasr-primary/pasr-strict。
- gateway HTTP handler 串联 API key resolver、model resolver、router plan、claim reservation、PASR/account selection、credential vault、dispatcher、forwarder、settler。
- 多个 `integration_pg` build tag 测试存在，但依赖 `HUAKAI_DATABASE_URL`。

关键缺口：

- PASR claim/release 与 slot 计数的一致性还需要跨包场景测试：成功、上游失败、客户端断开、settle 失败、release 幂等。
- cachemetrics 到 PASR feedback 的 miss/demote 路径存在语义风险：若 0/0 observation 被提前跳过，segment miss demote 不会按预期发生。
- 上游 adapter 全链路还没有统一矩阵：客户端请求 -> canonical/HCSF -> provider request -> provider stream/buffer -> canonical event/response -> client SSE。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | PASR claim/release + slot count 集成：成功、upstream 5xx、client cancel、settler error、double release | 2 ed | Postgres test DB 或 repository fake；P0 proto 编译修复 | slot 泄漏会造成账户永久不可用或并发超卖 |
| P0 | cache consistency 集成：prefix hash -> segment -> cachemetrics observer -> bitmap/miss demote -> selector score | 1.5 ed | 明确 0/0 miss observation 语义 | 测到内部 handler 而不是生产 observer 会掩盖真实缺陷 |
| P1 | gateway upstream adapter 全链路矩阵：Anthropic/OpenAI/Gemini/Bedrock mock，chat/messages 两个 endpoint | 3-4 ed | mock upstream harness；HCSF fixtures | Bedrock 只能 mock，不得引入 AWS 凭据依赖 |
| P1 | router plan + PASR dispatcher mode 集成：default/shadow/canary/pasr-primary/pasr-strict | 2 ed | selector dispatcher 测试 helper | shadow/canary 比例不稳定会让测试 flaky |
| P2 | DB serializable retry 和 orphan acquisition reconcile 测试 | 2 ed | DB transaction isolation harness | 当前代码存在 retry/reconcile TODO，需避免把未实现行为写成通过断言 |

## 3. Fixture-based contract

### 现状

HCSF fixture 现有 35 个：

- `envelope`: audio、batch、cache_control、computer_use、data_retention、file、image、live_session、mcp_server、structured_output、text、thinking、tool_result、tool_use、video。
- `event`: Anthropic lifecycle/tool partial、Gemini text、OpenAI chunks、synthetic terminal。
- `response`: Anthropic/tool/thinking buffered、OpenAI structured/text、Gemini text。
- `edge_case`: cross_tenant_isolation、empty_graph、native_passthrough_required、single_text_only、tool_use_chain。
- `regression`: 与历史 bug 模式对应的 stream terminal、cache metadata sanitize、sentinel、cache control strip、tool args lost 等。

当前 fixture test 主要验证 fixture 可解析、validator 通过、round-trip 稳定，负向合同不足。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | 增加 negative fixture 分组：missing required、bad enum、bad edge ref、cross-tenant ref、data_retention conflict、protocol_loss illegal | 2 ed | P0 INV disposition | 只有正向 fixture 会把 validator 退化成“能读样例” |
| P0 | fixture manifest：每个 fixture 绑定 capability family、INV、vendor variant、expected pass/fail | 1 ed | 现有 fixture 命名规范 | 无 manifest 时很难证明 14 capability families 均被覆盖 |
| P1 | Vendor variant fixture：Anthropic/OpenAI/Gemini/Bedrock/Codex synthetic payload，分别覆盖 buffered/streaming/replay 目标态 | 2-3 ed | 公共协议文档或 Owner sanitized captures；不得读 reference source | Codex vendor 形态需 Owner 确认；Bedrock 只能 mock |
| P1 | Regression fixture 扩展：partial SSE truncation、duplicated terminal、usage missing、tool call partial args、cache metadata mismatch | 2 ed | bug pattern library | 负向样例若来自真实日志，必须先脱敏 |
| P2 | Golden update workflow：fixture diff、canonical JSON stable order、review checklist | 1 ed | CI skeleton | golden 过度自动更新会掩盖协议回归 |

## 4. E2E mock upstream

### 现状

当前真实 gateway handler 已挂载：

- `POST /v1/chat/completions`
- `POST /v1/messages`

当前 backend handler 只支持 streaming；non-streaming 请求会返回 `non_streaming_unsupported`。`/v1/responses` 当前返回 501。`backend/cmd/gateway` 下存在 smoke 测试 build tag，但依赖数据库，并且需要在 proto 红树修复后重新确认编译状态。

三态目标：

- streaming：当前最接近可测主路径。
- buffered：adapter 层有部分 helper，但 gateway E2E 当前不应假设已完整支持。
- replay：需要先明确 replay 源、存储、幂等键和审计语义，当前只能列为策略目标。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | Mock upstream E2E harness：chat/messages streaming，OpenAI/Anthropic/Gemini/Bedrock scanner 均有 happy path | 3 ed | gateway test app、mock credential vault、mock DB/fake repo | 没有 E2E 时 PASR、adapter、settler 只能各自“局部正确” |
| P0 | E2E 断言 claim/settle/slot release：每个请求 exactly-one reserve、release、settle outcome | 1.5 ed | PASR integration helper | 重试或 fallback 下容易出现 double settle/double billing |
| P1 | E2E failure states：429、5xx、timeout、connection drop、partial SSE truncation、no terminal | 2.5 ed | chaos mock server | mid-stream fallback 需按协议边界限制，不能伪造“无损恢复” |
| P1 | Buffered E2E：仅在 gateway buffered path 实现后纳入 release gate | 1.5 ed | buffered endpoint/handler 支持 | 当前 non-streaming unsupported，测试不能提前要求产品未实现行为 |
| P2 | Replay E2E：request idempotency、stored response replay、audit trail、quota/billing不重复 | 3 ed | replay design/spec | replay 牵涉数据保留和账务，需 Owner 决策 |

## 5. Real-upstream smoke

### 现状

Owner memory 限定真实 vendor account scope：只覆盖 Anthropic、OpenAI、Gemini、Codex 四类真实账号；Bedrock/AWS 不可假设存在，其他 vendor 只能 mock。真实 smoke 不应写入 CI 默认路径，也不应要求 Owner 提供 AWS 凭据。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | Owner-local real smoke SOP：Anthropic/OpenAI/Gemini/Codex，最小 prompt、最低预算、手动 opt-in env | 1 ed | Owner 提供本地真实 key；secret 不入库 | smoke 不能阻塞普通 CI，也不能泄露 key |
| P0 | 真实 smoke 结果格式：vendor、model、endpoint、stream terminal、usage、cache headers、cost estimate、redaction check | 0.5 ed | logging redaction | 输出中若包含 prompt/key/account id 会形成安全事件 |
| P1 | Vendor-sliced metrics：shadow/canary/cache hit ratio 按 vendor 单独统计，不做跨 vendor 平均 | 1 ed | metrics label 规范 | 跨 vendor 平均会掩盖某个真实 provider 的单点退化 |
| P1 | Codex real vendor contract 确认：endpoint、auth、stream 格式、允许预算 | 0.5 ed | Owner 明确 Codex 账号形态 | “Codex”不是当前 gateway adapter 的常规 provider 名，不能凭记忆假设 |
| P2 | 周期性手动 smoke checklist：release 前人工运行、保存脱敏摘要 | 0.5 ed | release process | 自动化真实调用可能产生不可控账单 |

## 6. Load / stability

### 现状

已有 pool、gateway、token bucket、singleflight、segment table、dispatch sampling 等单测/局部并发测试。还缺少把 PASR shadow/canary、cache hit ratio 收敛、并发 fanout、slot release、settler outcome 放在同一个稳定性模型中的测试。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | Race baseline：P0 包通过后运行 `go test -race` 的核心包白名单：proto/pool/gatewayhttp/gateway/cachemetrics | 1 ed | 编译绿色 | 全量 race 在红树状态没有价值 |
| P1 | PASR shadow vs canary stability：固定 seed、固定 account pool、验证 selection drift、fallback rate、segment mutation 边界 | 2 ed | selector dispatcher deterministic hooks | canary 随机采样若不可控会 flaky |
| P1 | Cache hit ratio convergence：同 tenant/session/prefix 重复请求，验证命中率上升、miss demote、bitmap 截断 | 2 ed | cachemetrics observer 语义修复/确认 | 0/0 miss skip 会让 demote 永远不发生 |
| P1 | 并发 fanout：100-1000 concurrent request mock，验证 slot 上限、claim idempotency、no leak、latency percentile | 3 ed | mock DB 或 real Postgres profile | 真实 DB 环境不稳定会造成误报 |
| P2 | Long soak：30-60 分钟 mock upstream streaming，观测 goroutine、fd、expvar cardinality、memory | 2 ed | CI/nightly runner | expvar per-account metrics 可能出现 cardinality 膨胀 |

## 7. Chaos / fault

### 现状

已有错误分类、Bedrock scanner 错误、部分 timeout/forwarder 测试；也观察到若干 forwarder 高级场景以 `t.Skip` 保留：pre-stream failover、sanitized error envelope、buffered path、mid-stream failover、orphan sweep、tenant isolation under load、tokenizer inferred usage 等。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | Upstream 429/5xx/timeout chaos：pre-stream failure 可 fallback，post-stream failure 只能安全终止并记录 loss | 2 ed | mock upstream fault injector | mid-stream fallback 若处理错误，会造成重复 token、乱序或重复计费 |
| P0 | Connection drop / partial SSE truncation：synthetic terminal、protocol_loss、settle outcome、client-visible error | 2 ed | SSE scanner harness | BUG-GW-002 类型风险，最容易生产复现 |
| P1 | Rate limit backoff and account quarantine：单账号 429 不应污染整个 pool；disabled account 不可继续 routable | 2 ed | account state fake/repo | 若 quarantine 过度会降低可用性，过弱会持续打坏账号 |
| P1 | DLQ / orphan recovery：settle 失败、slot release 失败、claim orphan sweep | 2.5 ed | DB integration harness | 账务和 quota 相关，不能只做内存 fake |
| P2 | Chaos replay suite：故障注入矩阵自动生成，覆盖 vendor-specific stream quirks | 2 ed | P1 E2E stable | 组合爆炸，需要按 bug pattern 选择最小高价值集 |

## 8. Security

### 现状

已观察到 API key resolver、admin API key issue/list/revoke、scope/tenant、redaction 相关测试和实现路径。当前风险点包括：quota 越权、admin endpoint authorization、data retention validator 与执行链一致性、injection、日志/metrics 泄露，以及 `/debug/vars` 暂未置于 admin auth 后。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | API key issuance chain：创建、哈希存储、前缀展示、撤销、tenant 绑定、scope enforcement | 1.5 ed | admin API key repo/test DB | API key 明文或 hash 泄露是高风险安全事故 |
| P0 | Quota 越权与 tenant isolation：用户 A 不能消费/读取用户 B quota、usage、claims、audit | 2 ed | quota/billing fake 或 DB harness | 多租户隔离是平台底线，不能依赖 UI 隔离 |
| P0 | Data retention enforcement：HCSF policy validator + logging/settle/fixture 路径不保留 forbidden payload | 2 ed | INV-30..33 修复 | 只验证 envelope 不验证执行链，会出现“合同正确、落盘泄露” |
| P1 | Injection suite：SQL、JSON schema、header、model name、SSE event/data、prompt hash extension key | 2 ed | fuzz/negative fixture | SSE 注入可能破坏客户端事件边界 |
| P1 | Secret redaction：logs、expvar/debug、audit、error responses、frontend fetch errors | 1.5 ed | logging conventions | `/debug/vars` 当前暴露面需要 Owner 决策是否 P0 关闭或保护 |
| P2 | Security regression gate：每个 bug pattern 绑定最小测试，release 前强制跑 | 1 ed | CI/CD workflow | 无门槛时安全测试容易变成手动清单 |

## 9. Frontend

### 现状

Frontend 是 Next.js 14。当前 `frontend/package.json` 只有 `dev`、`build`、`start`、`type-check`，未观察到 app 级 test/a11y 脚手架。P1 dashboard 当前默认 mock，`NEXT_PUBLIC_USE_MOCK=0` 时会尝试真后端 `/admin/v1/usage` 和 `/admin/v1/provider-accounts?limit=5`，失败后 fallback 到 mock 并显示 backend banner。SSE client 自行解析 `[DONE]`、EOF、blank-line framing。

注意：前端设计和实现 owner 是 Gemini；Codex 在此只定义测试策略、风险和 backend contract，不直接改 frontend。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | Frontend baseline gate：`npm run type-check`、`npm run build`，记录 SSR/hydration 错误 | 0.5 ed | Node/npm install 状态 | dashboard server component 与 localStorage token client helper 的边界需验证 |
| P0 | Dashboard mock contract：mock 默认、真后端失败 fallback、banner 文案、空状态、provider account 列表字段 | 1 ed | Gemini ownership；mock data manifest | mock 中非真实 vendor 需标注为 UI mock，避免被误认为真实 smoke 支持 |
| P1 | Wired backend smoke：`NEXT_PUBLIC_USE_MOCK=0` 接真 gateway admin routes，验证 501/unauthorized/empty/success 四态 | 1.5 ed | admin endpoints 实现状态 | 当前多个 admin route 仍 501，测试必须允许“计划中”而非伪造成功 |
| P1 | SSE parser unit/browser test：chunk split、multi-event、`[DONE]`、EOF no blank line、error callback | 1 ed | JS test runner 选择 | 客户端 SSE 解析错误会表现为“后端 stream 坏了”的误诊 |
| P1 | a11y + responsive + hydration：dashboard table/card、banner、loading/error | 1.5 ed | Playwright/axe 或等价工具；新增依赖需 Owner 确认 | 新增 runtime/dev dependency 属中高风险，需按规则确认 |
| P2 | Visual regression：关键 dashboard states screenshot | 1 ed | stable dev server and fixtures | 视觉测试维护成本高，先服务 release-critical screens |

## 10. CI/CD

### 现状

未观察到 `.github` workflow。后端 `Makefile` 有 `test`、`vet`、`tidy`、`generate`、`test-integration`，其中 `test` 目标为 `go test ./... -race -count=1`，`test-integration` 使用 `-tags=integration_pg`。项目规则要求 commit 前运行 Codex review。任务指定 `go test -tags debug` 与 lint，但当前需要先确认 debug tag 实际覆盖哪些包。

### 策略表

| 优先级 | 工作项 | 工作量 | 依赖 | 风险 |
|---|---:|---:|---|---|
| P0 | Local preflight script/docs：`go test ./internal/proto -run '^$'`、核心包 unit、frontend type-check/build、git status audit | 1 ed | P0 编译修复 | 直接跑全量会被红树噪音淹没 |
| P0 | Codex review gate 文档化：stage 后 `codex exec review --uncommitted --full-auto`，HIGH 必修 | 0.5 ed | Owner workflow | 不跑 review 会违反项目 per-commit discipline |
| P1 | CI workflow skeleton：backend compile/unit/race subset、frontend type-check/build、artifact logs | 2 ed | Owner 确认 CI 平台；不碰 secrets | 若直接加入 GitHub Actions 属配置变更，应先确认 |
| P1 | Optional jobs：`integration_pg`、smoke、real-upstream 手动触发，默认不跑真实 vendor | 1.5 ed | test DB / Owner local env | 真实 vendor 不得进入默认 CI，避免账单和 secret 风险 |
| P1 | `go test -tags debug` 矩阵：先发现 debug-tag packages，再纳入门槛 | 0.5 ed | build tag audit | 若没有 debug tag，盲目要求会制造无效门槛 |
| P2 | Lint/static analysis：`go vet`、gofmt check、go mod verify、frontend lint/a11y | 1 ed | lint toolchain; 新依赖确认 | 新 lint 规则可能产生大量历史债，需要分阶段 |

## P0 启动建议

Owner 可立即开做的 5 件事：

1. 先恢复 backend 红树：解决 `backend/internal/proto/cross_ref.go` 未定义 helper，跑通 `go test ./internal/proto -run '^$'`，再跑 `go test ./internal/proto`。
2. 关闭 HCSF validator release gap：补测或明确 disposition INV-19/28/30/31/32/33/38/40/44/45/47/48/49/50，尤其 data retention 与 protocol_loss。
3. 建立 mock upstream E2E harness：覆盖 `/v1/chat/completions` 与 `/v1/messages` streaming，四个 adapter scanner 都走一遍，并断言 claim/settle/slot release。
4. 建立最小本地 preflight：backend proto/core packages、frontend `type-check`/`build`、Codex uncommitted review，先不引入真实 vendor。
5. 写 real-upstream smoke SOP：只允许 Anthropic/OpenAI/Gemini/Codex，Bedrock/AWS 保持 mock-only，输出必须脱敏并按 vendor 分片统计。

## Owner 决策点

- 是否把 `/debug/vars` admin auth 保护列为 P0 安全项。
- Codex real vendor 的 endpoint/auth/stream contract 由谁提供，是否进入 P0 smoke。
- 是否允许新增 frontend test/a11y dev dependencies；若允许，由 Gemini 执行。
- 是否先写 CI workflow，还是先以本地 preflight 文档作为过渡。
- Replay 三态的存储、审计、data retention、billing 幂等语义是否进入 Phase 1，还是列入 Mandatory Roadmap。

## 中文摘要

本文只基于 HUAKAI 内部代码、docs、memory 和本地命令结果起草测试策略，没有读取非 MIT reference project 源码。真实观察包括：后端测试数量不少但 `backend/internal/proto` 当前编译失败；HCSF 35 fixture 以正向合同为主；`envelope_test.go` 已覆盖大量 INV 但 data retention、protocol loss、tool/result cross-ref、batch/file ref、audit-only/warning 类 INV 仍有缺口；PASR cache-aware routing 已具备核心组件但缺少跨包一致性和稳定性门槛；Frontend dashboard 有 mock/real 开关但缺少自动化测试；CI workflow 尚未固化。合理推断是：P0 必须先恢复红树、补齐 validator release gap、建立 mock upstream E2E 和最小 preflight；真实 vendor smoke 只能覆盖 Anthropic/OpenAI/Gemini/Codex，Bedrock/AWS 必须保持 mock-only。Open questions 主要集中在 Codex vendor contract、debug vars 暴露策略、frontend test dependency、CI 平台和 replay 语义。
