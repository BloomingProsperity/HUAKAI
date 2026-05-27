# 2026-05-27 信任链 A+B 合一切片 Codex Lane Plan

## 0. 元信息

| 字段 | 内容 |
| --- | --- |
| Lane | codex |
| Time | 2026-05-27T12:07:39Z |
| Owner directive | Owner 2026-05-27 拍板: A+B 合一; C 保 Mandatory Roadmap; D 不做 |
| 本文件范围 | 只写 Codex lane plan, 不执行实现, 不 commit |
| Parallel-draft 约束 | 未读取 Claude lane plan; 工作区中若存在 Claude lane 文件, 本 lane 只忽略不打开 |
| Clean-room lane | specifier lane, 只输出行为观察和 HUAKAI-fit 计划, 不复制参考项目源码/结构/注释/实现 |
| 禁读范围 | sub2api / new-api / all-api-hub / one-api 未读取 |
| Freshness note | sandbox 禁止网络 socket, `git ls-remote` 刷新 LiteLLM/Portkey/LLMGateway 失败; 本计划依赖本地 refs。LiteLLM/Helicone/LLMGateway 本地 HEAD 在 30 天内, Portkey 和 CLIProxyAPI 作为补充证据, 实现前需再刷新 |

Metadata:

- Observed regions: 56 source/doc line regions, listed in §8 Source Coverage Proof.
- Inferences: 12 HUAKAI-fit inferences, each marked as "推断".
- Open questions: 9 Owner decision points in §6.

本计划把现有 `F-TRUST-001` 视为完整信任链目标, 把 Owner 采纳的 A+B 定义为 `F-TRUST-001 Phase 1 Lite`: 先让用户看见上游 provider/model 和可验证签名状态, 并用 lite ed25519 detached receipt 覆盖 "商家不能在用户可见账单上随意改写 provider/model/cost"。Merkle 完整链、chain-head、pubkey rotation 保留为 C Mandatory Roadmap, 不从规格中删除。

## 1. 现状盘点

### 1.1 F-TRUST-001 现有规格

已观察: 当前 `docs/specs/trust-chain-user-verifiable-ledger.md` 是完整 user-verifiable ledger 设计, 目标是把请求路径、模型链、计费事实和签名证明绑定到每个计费请求, 并把 "透明、反掺水、商家不能做假、用户消费透明" 写成核心差异化卖点 [docs/specs/trust-chain-user-verifiable-ledger.md:16](../../specs/trust-chain-user-verifiable-ledger.md:16) [docs/specs/trust-chain-user-verifiable-ledger.md:26](../../specs/trust-chain-user-verifiable-ledger.md:26)。

已观察: 该规格当前要求 `audit_ledger_entries` 记录 `hop_chain`、`model_chain`、前后 Merkle root、公钥指纹和签名, 并给出 schema 对应关系 [docs/specs/trust-chain-user-verifiable-ledger.md:30](../../specs/trust-chain-user-verifiable-ledger.md:30)。它还定义 canonical entry hash 和脱敏 guard, 明确上游响应 body、headers、错误栈、credential material 不进入 canonical evidence [docs/specs/trust-chain-user-verifiable-ledger.md:57](../../specs/trust-chain-user-verifiable-ledger.md:57) [docs/specs/trust-chain-user-verifiable-ledger.md:72](../../specs/trust-chain-user-verifiable-ledger.md:72)。

已观察: 当前规格已有 model chain verdict, 要求 requested/routed/upstream/delivered 一致性必须可解释, 不允许把缺失或不一致悄悄当成功 [docs/specs/trust-chain-user-verifiable-ledger.md:97](../../specs/trust-chain-user-verifiable-ledger.md:97) [docs/specs/trust-chain-user-verifiable-ledger.md:111](../../specs/trust-chain-user-verifiable-ledger.md:111)。它还定义 verify/pubkey/Merkle tree endpoints、append-only enforcement、pubkey rotation 和 acceptance tests [docs/specs/trust-chain-user-verifiable-ledger.md:117](../../specs/trust-chain-user-verifiable-ledger.md:117) [docs/specs/trust-chain-user-verifiable-ledger.md:138](../../specs/trust-chain-user-verifiable-ledger.md:138) [docs/specs/trust-chain-user-verifiable-ledger.md:176](../../specs/trust-chain-user-verifiable-ledger.md:176) [docs/specs/trust-chain-user-verifiable-ledger.md:222](../../specs/trust-chain-user-verifiable-ledger.md:222)。

推断: A+B 不需要推翻该规格; 它应成为 Phase 1 Lite 的验收层。具体做法是把 provider/model 可见字段和 lite detached signature 作为 F-TRUST-001 的先交付子集, 让 C 继续承担 "不可删除/不可重排/全局链头" 的完整账本证明。

### 1.2 数据库与后端现状

已观察: migration `0013` 已有 `audit_ledger_entries` 表, 字段包括请求、租户 scope、计费事件、hop/model chain、前后 Merkle root、公钥指纹和签名 [backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql:19](../../../backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql:19)。同一 migration 的注释把 hop chain、model chain、Merkle root、signature 的用途写成账本证明语义 [backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql:42](../../../backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql:42)。`0027` 已加 update/delete 拒绝触发器, 保护 ledger append-only [backend/sql/migrations/0027_ledger_append_only_trigger.up.sql:3](../../../backend/sql/migrations/0027_ledger_append_only_trigger.up.sql:3)。

已观察: `backend/internal/auditledger/postgres.go` 已有 Postgres ledger writer, 会在事务内拿租户 scope lock、读取上一条 root、计算 entry/root、调用 signer、插入账本 [backend/internal/auditledger/postgres.go:108](../../../backend/internal/auditledger/postgres.go:108) [backend/internal/auditledger/postgres.go:177](../../../backend/internal/auditledger/postgres.go:177)。同文件也支持按 request/tenant scope 查询和读取最新 root/size [backend/internal/auditledger/postgres.go:226](../../../backend/internal/auditledger/postgres.go:226) [backend/internal/auditledger/postgres.go:270](../../../backend/internal/auditledger/postgres.go:270)。

已观察: `backend/internal/auditledger/signer.go` 已有 ed25519 signer interface、本地 key provider、公钥指纹和 verify helper [backend/internal/auditledger/signer.go:33](../../../backend/internal/auditledger/signer.go:33) [backend/internal/auditledger/signer.go:58](../../../backend/internal/auditledger/signer.go:58) [backend/internal/auditledger/signer.go:125](../../../backend/internal/auditledger/signer.go:125)。这能复用到 B 的 lite receipt, 但现有签名对象是账本 entry hash, 不是 Owner 指定的 `provider + model + request_id + cost + redacted metadata` detached payload。

已观察: gateway 已挂载 audit verify 和 pubkey routes [backend/cmd/gateway/routes.go:44](../../../backend/cmd/gateway/routes.go:44), lifecycle 会构造 ledger service 和 pubkey registry [backend/cmd/gateway/lifecycle.go:231](../../../backend/cmd/gateway/lifecycle.go:231)。`audit_verify_handler` 要求 `request_id` 和 `tenant_scope_ref`, 返回 ledger entry 与 chain proof, 并能校验签名 [backend/internal/gatewayhttp/audit_verify_handler.go:92](../../../backend/internal/gatewayhttp/audit_verify_handler.go:92) [backend/internal/gatewayhttp/audit_verify_handler.go:255](../../../backend/internal/gatewayhttp/audit_verify_handler.go:255) [backend/internal/gatewayhttp/audit_verify_handler.go:279](../../../backend/internal/gatewayhttp/audit_verify_handler.go:279)。`audit_pubkey_handler` 暴露 active/list/by-fingerprint 公钥 JSON [backend/internal/gatewayhttp/audit_pubkey_handler.go:34](../../../backend/internal/gatewayhttp/audit_pubkey_handler.go:34) [backend/internal/gatewayhttp/audit_pubkey_handler.go:117](../../../backend/internal/gatewayhttp/audit_pubkey_handler.go:117)。

已观察: response header helper 目前只写 requested/delivered model 和 audit ledger 链接/指纹; 没有明确写 `upstream_provider` / `upstream_model` 字段 [backend/internal/gatewayhttp/chat_completions_handler_headers.go:24](../../../backend/internal/gatewayhttp/chat_completions_handler_headers.go:24) [backend/internal/gatewayhttp/chat_completions_handler_headers.go:55](../../../backend/internal/gatewayhttp/chat_completions_handler_headers.go:55)。同文件在 L2/cache/finalize 路径能拿到 route/upstream provider/model 和结算事实, 说明 A 的字段来源已部分存在 [backend/internal/gatewayhttp/chat_completions_handler_headers.go:147](../../../backend/internal/gatewayhttp/chat_completions_handler_headers.go:147) [backend/internal/gatewayhttp/chat_completions_handler_headers.go:194](../../../backend/internal/gatewayhttp/chat_completions_handler_headers.go:194)。

已观察: cost receipt handler 已有 receipt verify 路径和 signed receipt 字段, 但用户可见 cost 结构只有 `model`, 没有 provider/upstream model 分离字段 [backend/internal/gatewayhttp/cost_receipt_handler.go:55](../../../backend/internal/gatewayhttp/cost_receipt_handler.go:55) [backend/internal/gatewayhttp/cost_receipt_handler.go:71](../../../backend/internal/gatewayhttp/cost_receipt_handler.go:71)。receipt canonical payload v2 也包含 model/cost/validation/verdict, 但不包含 provider/upstream model [backend/internal/audit/receipt_formatter.go:61](../../../backend/internal/audit/receipt_formatter.go:61) [backend/internal/audit/receipt_formatter.go:112](../../../backend/internal/audit/receipt_formatter.go:112)。

### 1.3 CLI 与前端现状

已观察: `huakai-verify` CLI 已能通过 gateway audit verify、公钥 URL 和 Merkle tree 验证账本 proof; 它也已有 `.well-known/huakai-pubkey.json` 形态的解析逻辑 [backend/cmd/huakai-verify/main.go:26](../../../backend/cmd/huakai-verify/main.go:26) [backend/cmd/huakai-verify/main.go:45](../../../backend/cmd/huakai-verify/main.go:45) [backend/cmd/huakai-verify/main.go:98](../../../backend/cmd/huakai-verify/main.go:98) [backend/cmd/huakai-verify/main.go:170](../../../backend/cmd/huakai-verify/main.go:170)。但当前 backend routes 没有观察到 `/.well-known/huakai-pubkey.json` mount, 前端却尝试读该路径 [frontend/lib/audit-api.ts:146](../../../frontend/lib/audit-api.ts:146)。

已观察: audit 前端当前是 request_id 查询和 Merkle/签名验证页面, 不是用户消费面板里的每条 API response/provider/model/status 列表 [frontend/app/audit/page.tsx:60](../../../frontend/app/audit/page.tsx:60) [frontend/app/audit/page.tsx:159](../../../frontend/app/audit/page.tsx:159)。前端 status 类型只有 `verified | partial | tampered`, 与 Owner 要求的 `verified / signed-only / unverified / missing / mismatch` 不一致 [frontend/lib/audit-api.ts:1](../../../frontend/lib/audit-api.ts:1) [frontend/components/audit/VerifyStatusBadge.tsx:5](../../../frontend/components/audit/VerifyStatusBadge.tsx:5)。前端 audit verify 请求目前只带 request_id, 后端已观察到需要 tenant scope, 这会导致真实使用路径不完整 [frontend/lib/audit-api.ts:53](../../../frontend/lib/audit-api.ts:53) [backend/internal/gatewayhttp/audit_verify_handler.go:103](../../../backend/internal/gatewayhttp/audit_verify_handler.go:103)。

### 1.4 与 A+B 的主要 gap

A gap:

- API response wire contract 未稳定暴露 `upstream_provider` + `upstream_model`。现有 headers 有 requested/delivered model, 但没有 provider 和 upstream model 的明确字段。
- User panel 未形成每条消费记录的 provider/model/status 扫描界面。
- Status vocabulary 与 Owner 目标不一致, 缺字段/不一致的可见策略还没有落到 UI 与 tests。

B gap:

- 已有 ed25519 基建, 但签名对象偏完整账本 entry; 还没有 "lite detached receipt payload = provider + model + request_id + cost + redacted metadata" 的稳定 canonical contract。
- 公钥分发存在 `/v1/audit/pubkey(s)`、CLI `.well-known`、前端 `.well-known` 三种形态, 未统一。
- Verify 现在偏完整 audit/Merkle flow; B 需要用户一键 detached verify, 即使 C 未完成也能验证签名 payload。

二者协同 gap:

- A 的 provider/model 可见字段如果没有 B 的签名, 只是 UX hint, 不能支撑 "商家不能做假"。
- B 的签名如果不回显到 A 的消费面板, 用户无法发现 missing/mismatch, 仍会退化成后台审计工具。
- Phase 1 Lite 必须把同一个 canonical fact set 同时用于 response fields、user panel row、signed receipt、verifier input, 否则容易出现 response 说一套、账单签另一套。

## 2. 缺口分类

### 2.1 A: UX 面板 + response 字段

A-Wire:

- 定义稳定字段名和位置。建议默认 response headers: `X-HUAKAI-Upstream-Provider`, `X-HUAKAI-Upstream-Model`, `X-HUAKAI-Trust-Status`, `X-HUAKAI-Trust-Request-Id`, `X-HUAKAI-Trust-Signature`。如果 body extension 不破坏 OpenAI-compatible clients, 可同时在 HUAKAI extension object 中回显。
- streaming、non-streaming、cache-hit、provider-error、ledger-deferred 路径都必须走同一 status derivation, 不能只覆盖 happy path。

A-Panel:

- User panel 每条记录显示 provider/model、request_id、cost、签名指纹、验证状态、verify action。
- Status badge 必须支持 Owner 要求的五态:
  - `verified`: signed lite receipt 有效, provider/model/cost 与服务器保存事实一致。
  - `signed-only`: detached signature cryptographically valid, 但当前环境没有服务端 fact lookup 或 Merkle proof, 所以只能说明 "这份 payload 被对应 key 签过"。
  - `unverified`: provider/model 可见, 但没有可验证签名、签名服务不可用或 verify 尚未运行。
  - `missing`: 必填字段缺失, 例如 provider/model/request_id/signature 之一缺失。
  - `mismatch`: response fields、signed payload、ledger/receipt facts 或 model-chain verdict 出现冲突。

A-Tests:

- 每个 status 测试必须是判别性 fixture。特别是 missing/mismatch 不能用天然失败或天然成功样本; 要构造 "若 body/headers 被忽略则测试会误绿" 的对照。

### 2.2 B: lite ed25519 detached signature

B-Payload:

- 建议引入 Phase 1 canonical payload version, 包含: `schema_version`, `request_id`, `tenant_scope_ref` 或其不可逆 scope reference, `occurred_at`, `upstream_provider`, `upstream_model`, `requested_model`, `routed_model`, `delivered_model`, cost total, token counts, price snapshot identifier, validation state/verdict, redacted metadata, payload hash。
- 推断: `cost` 应进入签名范围, 否则用户无法用签名 receipt 证明 "这笔钱对应哪个 provider/model 和价格事实"。如果 Owner 担心最终 cost 在 response 时尚未 settled, 可用 provisional inline signature + final receipt signature 双轨。

B-Key Distribution:

- 复用现有 pubkey registry, 但补齐 `.well-known/huakai-pubkey.json` alias, fingerprint, algorithm, effective time, cache headers。
- Phase 1 不做 rotation policy 的完整验收, 但 wire format 必须保留 `status/effective_from/effective_until` 等 forward-compatible 字段。

B-Verify:

- 内置 detached verify endpoint 接收 signed payload/signature/fingerprint, 输出五态中的 cryptographic verdict 与 fact-match verdict。
- CLI 支持第三方模式: 只拿 payload + signature + pubkey 即可验证 `signed-only`; 若再给 gateway verify URL/request_id/tenant scope, 才升级为 `verified`。

### 2.3 A+B 协同

- 同一 trust status derivation 应服务 response headers、panel badge、receipt verify API, 避免前后端各自推断。
- A 不应等 C 完成才显示价值; B 也不应隐藏在 Merkle proof 页面里。Phase 1 的产品面应是 "用户立即看到本次调用被哪个 provider/model 承接、有没有签名、有没有不一致"。
- 推断: 若签名不可用, API 可 fail-open 但必须显式 `unverified`, 这是可用性与透明度的折中; 是否对计费结算 fail-closed 由 D-8 决定。

## 3. Slice 切片

所有切片默认 0.5-1 天。Go 新文件不得落入冻结包 `backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto`; 冻结包只允许小范围修改既有文件。若需要新后端职责, 建议新包 `backend/internal/trustlite` 或扩展非冻结的 `backend/internal/audit` 既有文件。

### TRUST-A-1: Phase 1 wire contract + status vocabulary

- 输出: 更新 F-TRUST-001 Phase 1 Lite 小节, 定义 response header/body extension、五态 status、missing/mismatch 判定。
- 测试: 添加 contract/scenario tests 先红, 覆盖 missing/mismatch/signed-only。
- 风险: 若先写 UI 文案而没有 contract, 前端会和 backend 二次分叉。
- 预计: 0.5 天。

### TRUST-A-2: Gateway response fields

- 输出: 在既有 gateway response header helper/finalization 路径回显 `upstream_provider` 和 `upstream_model`, 并输出 trust status/request id/fingerprint/verify URL。
- 范围: streaming、non-streaming、cache-hit、ledger-deferred 至少各有一条判别性测试。
- 文件纪律: 只改 `backend/internal/gatewayhttp` 既有文件; 不新增冻结包文件。
- 预计: 1 天。

### TRUST-A-3: User panel provider/model/status

- 输出: 用户侧消费/审计面板显示 provider/model/cost/request id/fingerprint/status/action。现有 audit page 可复用, 但必须支持每条记录扫描; 若没有真实 list API, 先用已存在 receipt/audit endpoints 做 detail flow 并把 list API 标为下一小步。
- 状态: `missing` 和 `mismatch` 必须红色/高可见; `unverified` 不能用绿色或成功语义。
- 预计: 0.75-1 天。

### TRUST-A-4: A 的验收测试与弱测清理

- 输出: 前端类型、badge、API client、backend headers 的组合测试。
- 必测: 缺 provider 字段时显示 `missing`; provider/model 与 signed payload 不一致时显示 `mismatch`; 只有未签名字段时显示 `unverified`。
- 预计: 0.5-0.75 天。

### TRUST-B-1: Lite signed payload canonical contract

- 输出: `trust_lite_receipt_v1` canonical JSON 或等价结构, 明确字段排序/类型/脱敏规则/禁止字段。
- 推荐: 不在本切片新增 DB schema; 先从 existing audit ledger + cost receipt facts 派生 payload。若实现发现必须持久化 signed payload, 触发 D-11 Owner 确认。
- 预计: 0.75-1 天。

### TRUST-B-2: Signer integration and receipt derivation

- 输出: 复用现有 ed25519 signer, 对 lite payload 签名, 并把 provider/model/cost 写入用户可见 receipt。
- 范围: final billing/receipt path 必须有最终签名; response inline 可以是 provisional。
- 测试: 改 cost 后签名验证失败; 改 provider/model 后签名验证失败; redacted metadata 不能包含 prompt/body/credentials。
- 预计: 1 天。

### TRUST-B-3: Public key well-known distribution

- 输出: 增加 `.well-known/huakai-pubkey.json` compatibility route 或明确 rewrite, 返回 active public key、fingerprint、algorithm、cache metadata。
- 兼容: 保留 `/v1/audit/pubkey(s)` 现有路线, 避免破坏 CLI/audit UI。
- 测试: frontend/CLI 能用同一 well-known document 缓存并按 fingerprint 取 key。
- 预计: 0.5-0.75 天。

### TRUST-B-4: Detached verify endpoint + CLI mode

- 输出: endpoint 支持 payload/signature/fingerprint detached verify; CLI 支持只验签的第三方模式和带 gateway fact lookup 的 verified 模式。
- 结果映射: signature valid 但无法比对服务器事实 -> `signed-only`; signature valid 且 facts match -> `verified`; signature invalid 或 facts conflict -> `mismatch`。
- 预计: 0.75-1 天。

### TRUST-B-5: Docs, acceptance tests, release gate update

- 输出: F-TRUST-001 更新为 Phase 1 Lite + Phase 2 Merkle; docs/process/decisions 或 release gate 记录 C Mandatory Roadmap, D 不做。
- 检查: backend tests, frontend tests/build, focused CLI verifier test, per-commit Codex review。
- 预计: 0.5-1 天。

## 4. 参考项目对照

Clean-room note: 本节只记录已观察行为, 不复制参考项目代码/结构/注释。参考项目字段/函数名不作为 HUAKAI 命名来源。

| 项目 | 已观察行为 | 对 HUAKAI A+B 的含义 |
| --- | --- | --- |
| LiteLLM | 已观察: routing/header 区域会维护 provider/model 相关元数据并写响应 headers, 但在本 lane 读过的 routing/header 区域和 focused signature search 中, 未观察到用户可独立验证的 ed25519 detached response receipt 或 Merkle proof。证据: `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8631`, `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8951`, `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/types/utils.py:2662`。 | A 的 provider/model 可见性是行业常见能力; B 的签名 receipt 是 HUAKAI 差异化, 不能用普通 headers 替代。 |
| Helicone AI Gateway | 已观察: gateway routing/proxy 区域会推导 provider/model 并写入响应/请求展示数据; UI 请求表和 drawer 会展示 provider。Focused search 未在已读 gateway/UI regions 中观察到 Phase 1 所需 ed25519 detached receipt。证据: `Helicone/ai-gateway@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/routers/gatewayRouter.ts:134`, `Helicone/ai-gateway@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:151`, `Helicone/ai-gateway@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/components/templates/requests/initialColumns.tsx:90`, `Helicone/ai-gateway@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/components/templates/requests/RequestDrawer.tsx:241`。 | A 的 panel 不应停留在后台日志; HUAKAI 应把 provider/model 和验证状态放到用户消费透明场景里。 |
| LLMGateway | 已观察: model listing 和 response transform 会暴露 provider/model 组合, response metadata 包含 requested/used provider/model 和 request id, 并会扩展 usage/cost fields。证据: `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/models/models.ts:28`, `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/models/models.ts:184`, `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat/tools/transform-response-to-openai.ts:167`, `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat/tools/transform-response-to-openai.ts:512`。 | HUAKAI 的 response/body metadata 可以借鉴 "请求模型、实际 provider/model、usage/cost 同屏" 的产品结果, 但必须加签名状态和 detached verify。 |
| Portkey | 已观察: 本地 HEAD 2026-03-25, 超过 30 天且网络刷新被 sandbox 阻断; 只作为补充证据。该 snapshot 的 request validation/context/response service 区域显示 provider 是 request/routing/response 事实的一部分, provider header 可被回传。证据: `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/middlewares/requestValidator/index.ts:111`, `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/requestContext.ts:144`, `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/responseService.ts:99`, `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/providers/openai/chatComplete.ts:157`。 | 实现前需刷新 Portkey; 当前只支持一个低风险结论: provider/model 可见性不是签名证明, HUAKAI 不能把 header 回显误标为 verified。 |
| CLIProxyAPI | 已观察: 本地 extracted snapshot 有 `.huakai-head-sha=21fad9dbb447a2ab70d51d0ac3e3d032525a6054`; API handler/executor/usage logger 会携带 requested model、provider/model、headers、usage records, 并可把记录交给插件/日志。证据: `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/handlers.go:557`, `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/executor/types.go:10`, `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/auth/conductor.go:522`, `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/usage/manager.go:13`, `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/logging/request_logger.go:44`。 | 参考点是 "把实际 provider/model/usage 作为本地事实留存"; HUAKAI 需要把这些事实变成用户可验证 receipt, 而不是只做 operator log。 |

## 5. 风险登记

| ID | 风险 | 严重度 | 缓解 |
| --- | --- | --- | --- |
| R-TRUST-AB-001 | Provider/model 字段只是自报, 被误标为 proof。 | HIGH | 只有签名和 fact-match 都通过才标 `verified`; 仅字段存在标 `unverified`。 |
| R-TRUST-AB-002 | 缺字段或不一致仍显示成功。 | HIGH | 五态 status 强制包含 `missing`/`mismatch`; tests 使用判别性 fixtures。 |
| R-TRUST-AB-003 | Cost 不进签名范围, 用户无法证明消费事实。 | HIGH | D-1 建议 cost/token/price snapshot 进 payload; cost 未 settled 时用 provisional/final 双轨。 |
| R-TRUST-AB-004 | Redacted metadata 泄露 prompt、response、credential、provider secret。 | HIGH | Canonical payload allowlist; denylist tests; 沿用 F-TRUST redaction guard。 |
| R-TRUST-AB-005 | 公钥同源分发被认为等同第三方信任。 | MED | UI 文案区分 same-origin trust 与 third-party CLI; 支持 key fingerprint、本地缓存、未来 mirror。 |
| R-TRUST-AB-006 | `/v1/audit/pubkey(s)`、`.well-known`、frontend/CLI 路径分裂。 | MED | B-3 做兼容 alias; docs 指定 canonical path。 |
| R-TRUST-AB-007 | streaming/cache/error 路径缺签名或缺 status。 | HIGH | A-2/B-2 必测四类路径; 无签名必须可见 `unverified`。 |
| R-TRUST-AB-008 | 为赶实现给冻结 Go 包新增文件。 | S1 structure risk | 只改冻结包既有文件; 新职责进 `internal/trustlite` 或非冻结既有包。 |
| R-TRUST-AB-009 | 为 lite receipt 新增 DB schema, 触碰高风险数据库结构。 | HIGH | 默认无 migration; 若必须持久化新列/表, 先走 Owner D-11。 |
| R-TRUST-AB-010 | B 没有 Merkle, 不能证明日志未删除/未重排。 | MED | UI/文档明确 Phase 1 Lite 边界; C 保 Mandatory Roadmap。 |
| R-TRUST-AB-011 | 用户把 `signed-only` 理解成完整 verified。 | MED | Badge copy 和 tooltip 明确 "只验证签名, 未比对服务端事实/链头"。 |
| R-TRUST-AB-012 | 现有 receipt/audit verify endpoint scope 不一致导致真实用户无法验证。 | MED | D-3 统一 detached verify 与 authenticated fact lookup; frontend API client 修正 tenant scope。 |

## 6. Owner 决策点

D-1 签名 payload 范围:

- 推荐: cost 进入签名范围, 至少包括 provider、upstream_model、request_id、cost total、token counts、price snapshot reference、validation state/verdict、redacted metadata allowlist。
- Owner 需拍: 是否把 `tenant_scope_ref`、requested/routed/delivered model、rate snapshot、currency/minor unit、occurred_at 纳入 v1。

D-2 公钥分发模式:

- 推荐: canonical `.well-known/huakai-pubkey.json` + 保留 `/v1/audit/pubkey(s)` 兼容; SDK/CLI 支持 fingerprint 本地缓存。
- Owner 需拍: 是否现在就需要第三方 mirror 文档, 还是只预留字段, C 阶段再做。

D-3 验证 endpoint 路径:

- 推荐: 不破坏现有 `/v1/audit/verify` Merkle flow; 新增或明确 `/v1/trust/verify` 作为 detached payload verify, 同时让 `/v1/receipts/{request_id}/verify` 返回同一 status 模型。
- Owner 需拍: 最终公开路径命名和 auth 边界。第三方 CLI 模式应允许离线 `signed-only`; 服务端 fact-match 模式可要求登录/session/tenant scope。

D-4 缺字段 / mismatch 显示策略:

- 推荐: 不拒绝展示记录; 用红色 `missing`/`mismatch` badge 和 warning banner 阻止误解。API response 可成功返回, 但信任状态不能成功。
- Owner 需拍: mismatch 是否需要触发用户侧通知/审计事件/运营告警。

D-5 签名时机:

- 推荐: 双轨。响应 inline 先给 provider/model/request_id/provisional status; final billing/receipt event 生成包含 cost 的最终 detached signature。
- Owner 需拍: 若 signer 不可用, API 是否 fail-open + `unverified`, 或 paid request fail-closed。

D-6 与现有 F-TRUST-001 Merkle 完整版边界:

- 推荐: 本切片只做 Phase 1 Lite; 不实现 chain-head、完整 Merkle proof UI、pubkey rotation policy。但不删除现有 Merkle schema/code, wire format 保留 chain fields。
- Owner 需拍: docs 中是否把 Merkle 标为 Phase 2/C Mandatory Roadmap, 还是保留 F-TRUST-001 原编号下的 staged acceptance tests。

D-7 Response body vs header:

- 推荐: headers 为默认兼容面; body extension 仅在 HUAKAI extension mode 或不会破坏 protocol compatibility 的响应类型启用。
- Owner 需拍: 是否强制 body 字段也出现, 以及字段名是否固定为 `upstream_provider` / `upstream_model`。

D-8 签名不可用时的产品策略:

- 推荐: 用户请求 fail-open, receipt/status fail-visible; money-path settlement 如果没有最终签名应进入 operator review 或 `unverified` 队列。
- Owner 需拍: 计费是否允许无最终签名入账。

D-9 是否允许 schema migration:

- 推荐: 本切片默认不迁移 DB。若实现过程中发现现有 ledger/receipt 无法稳定派生 lite payload, 先停下请 Owner 拍。
- Owner 需拍: 是否批准后续新增 `trust_receipts` 或 receipt payload column。

## 7. 工时估 + commit groupings

总估时: 4-7 天。建议目标 5 个实现 commit + 1 个收尾 commit。每个 commit 前按项目规则 stage intended diff, 跑测试, 执行 `codex exec review --uncommitted --full-auto --sandbox read-only`; 本计划阶段不 commit。

| Commit group | 内容 | 预计 | 主要文件/包 |
| --- | --- | --- | --- |
| G1 Contract + tests first | 更新 F-TRUST-001 Phase 1 Lite, 添加 status/payload contract tests。 | 0.5-1 天 | `docs/specs/`, backend/frontend test files |
| G2 A response fields | Gateway response headers/status, 覆盖 streaming/non-streaming/cache/deferred。 | 1 天 | 只改 `backend/internal/gatewayhttp` 既有文件; 不新增冻结包文件 |
| G3 B lite payload/signature | Canonical payload, signer reuse, receipt provider/model/cost signing。 | 1-1.5 天 | 建议 `backend/internal/trustlite` 新包或 `backend/internal/audit` 既有文件 |
| G4 Key distribution + verify | `.well-known` alias, detached verify endpoint, CLI signed-only/verified 双模式。 | 0.75-1.25 天 | routes 既有文件, verifier CLI, trustlite/audit code |
| G5 User panel | Provider/model/status/action UI, five-state badge, frontend API client 修正。 | 1 天 | `frontend/app/`, `frontend/components/`, `frontend/lib/` |
| G6 Acceptance + docs gate | 补 acceptance tests, docs risk/roadmap, C Mandatory Roadmap boundary。 | 0.5-1 天 | `docs/`, focused tests |

推荐执行顺序:

1. 先落 G1, 让 payload/status 合同先红。
2. 再落 G2, 让 A 的可见字段稳定出现在所有响应路径。
3. 再落 G3/G4, 把 B 的签名和 verify 做成可独立验证。
4. 最后落 G5/G6, 避免 UI 提前假设后端状态。

## 8. Clean-room 约束

### 8.1 Lane Guard

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: litellm / portkey / helicone ai-gateway / llmgateway / CLIProxyAPI

HARD PROHIBITIONS APPLIED:

- 不复制参考项目函数名、结构字段名、注释、schema、distinctive implementation。
- 不做 line-by-line translation。
- 不粘贴上游源码块。
- 引用只作为 file:line evidence anchor, 行为用 HUAKAI 自己的语言概括。

CITATION POLICY:

- 每个参考项目 claim 带 `<repo>@<sha>:<file>:<line>` citation。
- Portkey/CLIProxyAPI 的 freshness caveat 已在 §0/§4 标出。

=== END CLEAN-ROOM LANE GUARD ===

### 8.2 Source Coverage Proof

HUAKAI source/docs read:

- `docs/specs/trust-chain-user-verifiable-ledger.md:1-9` contributed metadata/status of current F-TRUST spec.
- `docs/specs/trust-chain-user-verifiable-ledger.md:16-26` contributed core trust-chain differentiator.
- `docs/specs/trust-chain-user-verifiable-ledger.md:30-55` contributed schema mapping.
- `docs/specs/trust-chain-user-verifiable-ledger.md:57-76` contributed canonical hash/redaction guard.
- `docs/specs/trust-chain-user-verifiable-ledger.md:97-115` contributed model-chain verdict semantics.
- `docs/specs/trust-chain-user-verifiable-ledger.md:117-151` contributed verify endpoints and append-only requirements.
- `docs/specs/trust-chain-user-verifiable-ledger.md:176-183` contributed pubkey rotation boundary.
- `docs/specs/trust-chain-user-verifiable-ledger.md:203-231` contributed existing phases and acceptance tests.
- `backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql:1-55` contributed current audit ledger schema.
- `backend/sql/migrations/0027_ledger_append_only_trigger.up.sql:3-15` contributed append-only trigger behavior.
- `backend/internal/proto/trust_chain_types.go:29-123` contributed hop/model chain type semantics.
- `backend/internal/gatewayhttp/chat_completions_handler_headers.go:24-94` contributed current HUAKAI response headers.
- `backend/internal/gatewayhttp/chat_completions_handler_headers.go:147-219` contributed route/upstream/provider and ledger event facts.
- `backend/internal/auditledger/postgres.go:20-72` contributed ledger writer design and append API.
- `backend/internal/auditledger/postgres.go:108-217` contributed transaction signing and Merkle insertion behavior.
- `backend/internal/auditledger/postgres.go:226-303` contributed lookup/latest proof behavior.
- `backend/internal/auditledger/signer.go:20-188` contributed ed25519 signer/public key behavior.
- `backend/internal/gatewayhttp/audit_verify_handler.go:49-132` contributed verify route and lookup requirements.
- `backend/internal/gatewayhttp/audit_verify_handler.go:191-303` contributed Merkle proof and signature verification response.
- `backend/internal/gatewayhttp/audit_pubkey_handler.go:20-145` contributed pubkey API shape.
- `backend/cmd/gateway/routes.go:40-70` contributed mounted route map.
- `backend/cmd/gateway/lifecycle.go:231-247` contributed service wiring.
- `backend/cmd/huakai-verify/main.go:26-121` contributed CLI verify flow.
- `backend/cmd/huakai-verify/main.go:170-235` contributed well-known pubkey parsing expectation.
- `backend/internal/gatewayhttp/cost_receipt_handler.go:55-78` contributed current user receipt fields.
- `backend/internal/gatewayhttp/cost_receipt_handler.go:140-280` contributed current receipt verification flow.
- `backend/internal/audit/receipt_formatter.go:22-128` contributed current receipt canonical payload.
- `backend/internal/audit/receipt_formatter.go:231-260` contributed receipt derivation from ledger/source facts.
- `frontend/app/audit/page.tsx:60-180` contributed current audit UI behavior.
- `frontend/components/audit/VerifyStatusBadge.tsx:5-24` contributed current status vocabulary.
- `frontend/lib/audit-api.ts:1-55` contributed current audit API client verify shape.
- `frontend/lib/audit-api.ts:146-151` contributed current frontend well-known expectation.
- `frontend/components/audit/HopChainTimeline.tsx:4-36` contributed current provider display in hop timeline.
- `frontend/components/audit/ModelChainCard.tsx:5-67` contributed current model-chain UI.

Reference source read:

- `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8631-8679` contributed provider/model routing metadata observation.
- `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/router.py:8951-9010` contributed response header observation.
- `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/types/utils.py:2662-2673` contributed provider-sent model/logging metadata observation.
- `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/middlewares/requestValidator/index.ts:111-146` contributed provider request validation observation.
- `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/requestContext.ts:144-146` contributed provider context observation.
- `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/responseService.ts:99-124` contributed response header observation.
- `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/providers/openai/chatComplete.ts:157-188` contributed stream/provider metadata observation.
- `Helicone/ai-gateway@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/routers/gatewayRouter.ts:134-167` contributed provider routing observation.
- `Helicone/ai-gateway@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:151-167` contributed response provider/model headers observation.
- `Helicone/ai-gateway@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/components/templates/requests/initialColumns.tsx:90-99` contributed UI provider column observation.
- `Helicone/ai-gateway@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:web/components/templates/requests/RequestDrawer.tsx:241-246` contributed UI request drawer observation.
- `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/models/models.ts:28-35` contributed model/provider API shape observation.
- `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/models/models.ts:184-210` contributed model listing observation.
- `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat/tools/transform-response-to-openai.ts:167-186` contributed response metadata observation.
- `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat/tools/transform-response-to-openai.ts:512-535` contributed response model/metadata/usage observation.
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/handlers.go:191-195` contributed upstream header passthrough observation.
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/handlers.go:557-602` contributed request details/provider/model flow observation.
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/executor/types.go:10-72` contributed metadata/response shape observation.
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/auth/conductor.go:522-570` contributed provider/model candidate resolution observation.
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/auth/conductor.go:1224-1415` contributed selected provider/model execution observation.
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/usage/manager.go:13-32` contributed usage record shape observation.
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/cliproxy/usage/manager.go:183-199` contributed usage publication observation.
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/logging/request_logger.go:44-67` contributed request logging field observation.
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/logging/request_logger.go:293-410` contributed log write behavior observation.

Source files read: docs/specs/trust-chain-user-verifiable-ledger.md; backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql; backend/sql/migrations/0027_ledger_append_only_trigger.up.sql; backend/internal/proto/trust_chain_types.go; backend/internal/gatewayhttp/chat_completions_handler_headers.go; backend/internal/auditledger/postgres.go; backend/internal/auditledger/signer.go; backend/internal/gatewayhttp/audit_verify_handler.go; backend/internal/gatewayhttp/audit_pubkey_handler.go; backend/cmd/gateway/routes.go; backend/cmd/gateway/lifecycle.go; backend/cmd/huakai-verify/main.go; backend/internal/gatewayhttp/cost_receipt_handler.go; backend/internal/audit/receipt_formatter.go; frontend/app/audit/page.tsx; frontend/components/audit/VerifyStatusBadge.tsx; frontend/lib/audit-api.ts; frontend/components/audit/HopChainTimeline.tsx; frontend/components/audit/ModelChainCard.tsx; /home/codex/refs/litellm/litellm/router.py; /home/codex/refs/litellm/litellm/types/utils.py; /home/codex/refs/portkey/src/middlewares/requestValidator/index.ts; /home/codex/refs/portkey/src/handlers/services/requestContext.ts; /home/codex/refs/portkey/src/handlers/services/responseService.ts; /home/codex/refs/portkey/src/providers/openai/chatComplete.ts; /home/codex/refs/helicone/worker/src/routers/gatewayRouter.ts; /home/codex/refs/helicone/worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts; /home/codex/refs/helicone/web/components/templates/requests/initialColumns.tsx; /home/codex/refs/helicone/web/components/templates/requests/RequestDrawer.tsx; /home/codex/refs/llmgateway/apps/gateway/src/models/models.ts; /home/codex/refs/llmgateway/apps/gateway/src/chat/tools/transform-response-to-openai.ts; /home/codex/refs/CLIProxyAPI/sdk/api/handlers/handlers.go; /home/codex/refs/CLIProxyAPI/sdk/cliproxy/executor/types.go; /home/codex/refs/CLIProxyAPI/sdk/cliproxy/auth/conductor.go; /home/codex/refs/CLIProxyAPI/sdk/cliproxy/usage/manager.go; /home/codex/refs/CLIProxyAPI/internal/logging/request_logger.go

Lane: specifier

Agent: GPT-5 Codex, codex lane

UTC timestamp: 2026-05-27T12:07:39Z

中文摘要: 本 lane 的真观察是: HUAKAI 已有完整 F-TRUST-001 账本/ed25519/Merkle/verify/pubkey/CLI 雏形, 但 A 要求的 response `upstream_provider/upstream_model` 与五态用户面板还没落稳, B 要求的 lite detached payload 也还没有把 provider/model/request_id/cost/redacted metadata 作为单一用户可验证对象签名; 参考项目中至少 LiteLLM、Helicone、LLMGateway 已观察到 provider/model 可见性, 但在本 lane 已读区域未观察到等价的用户 detached ed25519 receipt。合理推断是: A+B 应作为 F-TRUST-001 Phase 1 Lite, C Merkle 完整链保 Mandatory Roadmap。Open questions 共 9 个, 最高优先级是 D-1 payload 范围、D-3 verify endpoint/auth 边界、D-5 签名时机和 D-8 签名不可用时是否允许计费入账。
