# 2026-05-26 Cursor Vendor 集成方案 — Codex Lane

> 过程完整性说明：本 lane 起草过程中，一次宽泛 `rg` 意外匹配并输出了 Claude parallel draft 的部分内容；后续未再打开、引用或依赖该文件。本稿事实依据限定为文末 `Source files read` 中列出的 HUAKAI 代码与允许的 MIT/Apache 参考项目。Owner 合并 synthesis 时建议把本稿标记为“独立性受一次误读影响”，或要求重跑 Codex lane。

## 1. 当前状态 (盘点)

| 文件 | 当前评价 |
| --- | --- |
| `backend/internal/provider/cursor/bootstrap.go` | 已定义 `cursor` vendor、OAuth mode、PKCE S256 与默认 loopback redirect；`ValidateCursorOAuthConfig` 要求 `auth_url` / `token_url` / `client_id` / `redirect_uri` 全由 operator 配置提供，当前正确 fail-closed。 |
| `backend/internal/provider/cursor/bootstrap_test.go` | 覆盖“不能硬编码猜测 OAuth 端点”和“授权 URL 必须带 PKCE/state/scope”等判别性条件。 |
| `backend/internal/provider/cursor/cursor_session.go` | 已能把调用方准备好的 body 透传到 Cursor Connect/proto endpoint，并设置 session/upstream token、`application/connect+proto`、UA、client-version 与可选 checksum/cookie；但 OpenAI JSON 到 Cursor wire 协议转换、Cursor chunk 到 OpenAI SSE 转换、checksum/trace/request-id 生成仍缺失。 |
| `backend/internal/provider/cursor/cursor_session_test.go` | 覆盖 endpoint、body 透传和 header 形状；但 expired session reauth 与 upstream failure DLQ 测试仍是 `t.Skip`，不能证明真实恢复路径。 |
| `backend/internal/provider/cursor/refresher.go` | 已有 refresh lock、operator-config-only token URL/client/scope、HTTP failure 分类、refresh token 轮换和 outcome 持久化；但 `access_token` 同步为 `session_token` 只是当前假设，尚未被真实 Cursor flow 证明。 |
| `backend/internal/provider/cursor/refresher_test.go` | 覆盖 SSRF 防护、失败分类、lock 内 outcome 写入和成功 token merge；质量较好，但仍只验证通用 OAuth refresh 形状。 |
| `backend/internal/provider/cursor/credential_store_adapter.go` | 仅是 `credentialstore` 到 Cursor refresher 的薄桥接，职责清晰。 |
| `backend/internal/provider/registrydefault/default.go` | `cursor_session` 仍属于 placeholder session adapter，只有 `HUAKAI_ENABLE_PLACEHOLDER_SESSION_ADAPTERS=true` 时注册，默认生产面正确关闭。 |
| `backend/internal/credentialacq/vendor_exchangers.go` | 已注册 `cursor/oauth`，但当前走 `NewPKCEFakeExchanger(TokenShapeSession)`；这不能作为真实 Cursor OAuth exchange。 |
| `backend/internal/credentialacq/oauth.go` | PKCE state/verifier/challenge、encrypted verifier、callback state 校验和 exchanger 调用已经存在，可复用为 Cursor OAuth 接入骨架。 |
| `backend/internal/credentialacq/types.go` | `DefaultModePlans` 未包含 Cursor，账号获取 UI/计划层目前看不到 Cursor mode。 |
| `backend/internal/credentialacq/refresh_lock.go` | 已有 credential refresh advisory lock，可继续用于 Cursor refresh storm 控制。 |
| `backend/internal/credentialstore/types.go` | 默认 vendor 常量和 `DefaultVendorHandlers` 未包含 Cursor；runtime material 也未把 Cursor checksum/cookie/client-version 作为一等字段暴露。 |
| `backend/internal/credentialworker/refresh_adapter.go` | `MockOnlyProviders` 仍包含 `cursor`，所以即便 Cursor refresher 存在，默认 worker 语义仍是 mock-only。 |
| `backend/internal/credentialworker/scheduler.go` | provider-name based refresher route 已支持把 `cursor` 交给 vendor refresher，并带 storm control / audit outcome。 |
| `backend/internal/credentialworker/scheduler_test.go` | 已覆盖 Cursor vendor refresher route 和 Cursor 401 `auth_expired` outcome。 |
| `backend/internal/transport/policy.go` | 已定义 `cursor` provider 与 `mimicry_cursor` transport mode，并有 fail-closed 校验。 |
| `backend/internal/transport/factory.go` | mimicry/template registry 缺失时会 fail-closed；Cursor 当前没有可用模板时不会静默降级。 |
| `backend/internal/transport/mimicry/registry.go` | 能把 `cursor` / `cursor-cli` stem 映射到 Cursor mode；但默认 template registry 和 sidecar profile 只有 Claude Code，没有 Cursor profile。 |
| `docs/10_RISK_REGISTER.md` | 已记录 Cursor/Windsurf 等 OAuth 端点/client/scope 待 Owner 捕获配置，以及 refresher SSRF/token exfil 风险已通过 operator config 缓解。 |
| `docs/03_FEATURE_PARITY_MATRIX.md` / `docs/specs/request-pacing-mimicry.md` | 已把 `cursor_ide` pacing/mimicry 放入路线图/规格上下文，但尚未形成可执行 Cursor transport profile。 |

## 2. 缺口分类

1. **OCAW 反封禁层**：`cursor_session.go` 只读取可选 `x-cursor-checksum`、client-version、cookie，`x-amzn-trace-id` / timezone / request-id 仍是 TODO；`transport/mimicry` 也没有 Cursor sidecar profile 或 template。缺口不是“补几个 header”这么简单，而是 checksum 来源、trace 形状、timezone/device profile 稳定性、请求 pacing 和失败后的账号隔离策略都未闭环。
2. **协议转换层**：当前 adapter 是 body passthrough。没有 OpenAI Chat/Responses JSON 到 Cursor Connect/proto payload 的 encoder，也没有 Cursor streaming chunk 到 OpenAI-compatible SSE/JSON 的 decoder。禁止把 schema 塞进冻结包 `backend/internal/proto`；若做，应新建 Cursor 专属 wire 包。
3. **OAuth flow 接通**：PKCE 框架存在，但 `cursor/oauth` 当前是 fake exchanger；`DefaultModePlans` 没有 Cursor；operator 端真实 `auth_url` / `token_url` / `client_id` / `scope` 未被 Owner 验证。真实 exchange 前必须继续 fail-closed。
4. **refresher 真实性**：refresher 已具备通用 refresh_token 表单交换、安全 endpoint 来源和失败分类，但真实 Cursor 是否返回可直接作为 `session_token` 的 access token、是否要求 cookie 或额外 device fields，仍未验证。
5. **credentialstore/runtime material**：默认 vendor handler 没有 Cursor；checksum/cookie/client-version 等 Cursor runtime material 没有一等字段或明确 `extra` contract。若直接接入，会出现 adapter 需要字段但 store 不保证供应的断层。
6. **默认注册与 rollout**：placeholder adapter 默认不注册是正确安全状态；生产接入必须新增 Cursor 专属 feature flag / canary 条件，而不是打开全部 placeholder session adapters。
7. **测试真实性**：现有单测覆盖 fail-closed 与 SSRF 风险，但还没有真实协议 fixture、真实 OAuth exchange fixture、OCAW header 判别性 fixture，也没有 end-to-end canary。

## 3. Slice 切片建议

### C0 — 证据、法律与真实流量红线

- **范围**：只做证据计划和风险登记，不写实现代码；确认 Cursor OAuth 端点/client/scope、Cursor wire schema 来源、ToS/EULA 可接受边界、允许采集的红acted traffic 类型。
- **文件**：`docs/process/plans/`、`docs/10_RISK_REGISTER.md`、`docs/03_FEATURE_PARITY_MATRIX.md`、可新增 `docs/process/evidence/cursor-*.md`；不触碰冻结包。
- **成功标准 (testable)**：Owner 明确选择 Cursor 产品形态；每个真实端点、client id、scope、checksum 来源都有“已证实 / 禁止 / 未知”状态；证据文档不含 token、cookie、session id、raw body secret。
- **判别性 test 示例**：新增文档/脚本检查 `TestCursorEvidenceRejectsSecretMaterial`：fixture 中放入形如 `Authorization: Bearer live_*` 或 `Cookie: WorkosCursorSessionToken=...` 时必须失败；若 sanitizer 只检查文件名或空跑，该测试应变红。
- **时间估计**：0.5-1 天，外加 Owner 法务/产品确认时间。
- **风险**：误采集真实账号 secret、ToS 不允许第三方代理、证据不足导致后续实现靠猜。

### C1 — Cursor credential acquisition 与 store contract

- **范围**：把 Cursor 从 fake OAuth/acquisition gap 拉到可配置但默认关闭的真实 contract：mode plan、vendor handler、runtime material 字段、exchanger 注册边界。
- **文件**：现有 `backend/internal/credentialacq/types.go`、`backend/internal/credentialacq/vendor_exchangers.go`、`backend/internal/credentialstore/types.go` 及对应测试；如需新增 exchanger，放在 `backend/internal/credentialacq/` 或 `backend/internal/provider/cursor/`，不新建冻结包文件。
- **成功标准 (testable)**：Cursor acquisition mode 只在 operator config 完整时出现；fake exchanger 不会在生产 Cursor path 被调用；credential runtime material 能稳定提供 session/upstream token、client-version、checksum policy、cookie policy。
- **判别性 test 示例**：`TestCursorOAuthModeHiddenWhenOperatorConfigMissing`：缺少 `token_url` 或 `client_id` 时 mode plan 必须不可用；若代码回退到 fake exchanger 或硬编码默认端点，测试失败。
- **时间估计**：1-1.5 天。
- **风险**：新增 runtime material contract 可能牵涉账号管理 UI/API；若需要 schema migration，必须停下请 Owner 确认。

### C2 — 真实 OAuth exchange 与 refresher 闭环

- **范围**：用 operator-provided Cursor OAuth 配置替换 fake exchange；保持 refresh endpoint/client/scope 只来自 operator config；把 worker mock-only 状态改为 Cursor 专属 opt-in。
- **文件**：`backend/internal/provider/cursor/bootstrap.go`、`backend/internal/provider/cursor/refresher.go`、`backend/internal/provider/cursor/*_test.go`、`backend/internal/credentialworker/refresh_adapter.go`；不改 auth core、不改 billing/quota、不改数据库 schema。
- **成功标准 (testable)**：authorization_code exchange 与 refresh_token exchange 都拒绝 credential-supplied endpoint/client；`invalid_grant` 进入 `auth_expired`，429 honoring capped retry-after，5xx transient；缺少 operator config 时 worker 不尝试 refresh。
- **判别性 test 示例**：`TestCursorOAuthExchangeIgnoresCredentialSuppliedTokenURL`：credential payload 中塞恶意 `oauth_token_endpoint=http://127.0.0.1`，fake HTTP server 只能看到 operator-config URL 被调用；若实现读取 credential URL，测试失败。
- **时间估计**：1.5-2 天。
- **风险**：真实 Cursor OAuth token shape 未证实；如果 access token 不等于 session token，需要回到 C0 补证据，不能猜。

### C3 — OCAW header/profile 与 transport mimicry

- **范围**：新增 Cursor OCAW header/profile 生成模块，明确 strict/dev 两种策略；接入 `cursor_session.go` 和 `transport/mimicry` Cursor profile。
- **文件**：新包建议 `backend/internal/provider/cursor/ocaw/` 或同目录既有文件；`backend/internal/provider/cursor/cursor_session.go`；`backend/internal/transport/mimicry/registry.go` 及测试。禁止在 `backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto` 新增文件。
- **成功标准 (testable)**：strict mode 中缺少已验证 checksum 算法时 fail-closed；dev/canary mode 可显式允许 passthrough；request-id、timezone、trace id、client-version 从 account profile 生成且可审计；不同账号 profile 不串用。
- **判别性 test 示例**：`TestCursorOCAWStrictModeRejectsMissingChecksumSource`：strict mode + no verified checksum source 必须返回 typed error；如果代码发送空 checksum 或静默降级为 generic transport，测试失败。
- **时间估计**：2-3 天，取决于 checksum 证据。
- **风险**：反封禁行为可能触碰 ToS；checksum/trace 不能从非许可源码反推；真实流量采集必须红acted 且 Owner 批准。

### C4 — Cursor wire protocol 转换

- **范围**：实现 OpenAI Chat/Responses request 到 Cursor Connect/proto payload 的 encoder，以及 Cursor streaming response 到 OpenAI-compatible stream/JSON 的 decoder；unsupported fields 必须 typed error。
- **文件**：新包建议 `backend/internal/provider/cursor/wire/`；修改 `backend/internal/provider/cursor/cursor_session.go` 使用 encoder/decoder interface；测试 fixture 放在 Cursor provider 目录或新 wire 包。不得在冻结 `backend/internal/proto` 新增文件。
- **成功标准 (testable)**：model/messages/tools/stream/temperature 等最小字段进入 Cursor payload；Cursor chunk fixture 能输出 OpenAI SSE delta 和 finish reason；usage/error 映射可判别；缺失 schema 时不得假装支持。
- **判别性 test 示例**：`TestCursorWireEncoderUsesOpenAIModelAndMessages`：输入 OpenAI JSON 中只有 `model` 和一条 user message，断言 encoded Cursor payload 中能解出同一模型和消息；如果实现仍只是原样透传 inbound body，测试失败。
- **时间估计**：4-7 天，可能拆成 request encoder、stream decoder、error mapping 三个子 slice。
- **风险**：目前未在允许参考项目中找到 Cursor protobuf schema 的 MIT 来源；若只能靠真流量推断，必须保持 behavior-level clean-room，不复制 proprietary schema/source。

### C5 — 默认关闭的 canary wiring 与回滚

- **范围**：把 C1-C4 成果串到默认关闭的 Cursor feature flag/canary path；不启用全部 placeholder session adapters；增加可观测 failure outcome 和 rollback 文档。
- **文件**：`backend/internal/provider/registrydefault/default.go`、`backend/internal/credentialworker/*`、`backend/internal/transport/*`、Cursor provider tests、运维 docs；如需网关 handler 接入，只改既有文件，不在冻结包新建文件。
- **成功标准 (testable)**：默认环境不注册 Cursor；`HUAKAI_ENABLE_CURSOR_VENDOR=true` 且 operator config 完整时才注册；缺少 OCAW/protocol capability 时 fail-closed；canary fake upstream 可证明 request path、refresh path、error path。
- **判别性 test 示例**：`TestCursorVendorFlagDoesNotEnableOtherPlaceholderAdapters`：只打开 Cursor flag 时只能注册 Cursor adapter，不能顺带注册 Windsurf/OpenAI Codex/Kiro placeholders；如果代码复用旧 placeholder 总开关，测试失败。
- **时间估计**：1 天。
- **风险**：误开 placeholder family 会扩大攻击面；canary 若接真实账号需 Owner 明确批准。

## 4. 参考项目对照 (CLAUDE.md #15)

| Slice | 参考项目对照 |
| --- | --- |
| C0 | LiteLLM 把 Cursor 表示为 BYOK/provider endpoint 能力，支持 chat/messages/responses 等 direct API 形态，而不是 IDE session/proto 反向兼容；这支持把“BYOK direct provider”列为较安全产品选项 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/provider_endpoints_support_backup.json:2286`。LLMGateway 的 Cursor 集成文案说明当前 Cursor guide 只支持 plan mode，coding agent 不适用于 external API endpoint，说明“声称完整 Cursor IDE 兼容”必须谨慎 `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:packages/shared/src/components/integration-guides-grid.tsx:52`。 |
| C1 | CLIProxyAPI 的 PKCE helper 生成 verifier/challenge，OAuth URL 携带 state、challenge 和 S256，token exchange 提交 authorization_code/client_id/code_verifier，可借鉴 PKCE acquisition 形态但不能复制实现 `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/codex/pkce.go:13`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/codex/openai_auth.go:63`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/codex/openai_auth.go:95`。LiteLLM 的 OpenAI-like provider 工具只要求 api_key/api_base 并设置 JSON content-type 与 Bearer header，说明它处理的是 direct provider contract，不覆盖 Cursor session runtime material 问题 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/llms/openai_like/common_utils.py:21`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/llms/openai_like/common_utils.py:41`。 |
| C2 | CLIProxyAPI 的 Kimi refresh flow 用 refresh_token grant、client_id、content-type/accept headers，并分类 401/403 与非 200 错误，可作为“通用 refresh shape”参考 `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/kimi/kimi.go:342`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/kimi/kimi.go:382`。同仓库 xAI flow 通过 discovery/issuer 验证得到 token endpoint，再用 code_verifier exchange，说明 endpoint 来源要有可信配置或 discovery 约束；HUAKAI Cursor 目前应继续用 operator config fail-closed `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/xai/xai.go:96`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/auth/xai/xai.go:140`。 |
| C3 | Portkey 把 provider headers、base URL、endpoint path 和 request handler 放在 provider context 层统一处理，适合借鉴“header 生成集中化”的边界 `Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/providerContext.ts:28`、`Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/providerContext.ts:43`、`Portkey-AI/gateway@351692fd9236af222168134b416924fae0bdba23:src/handlers/services/providerContext.ts:109`。Envoy AI Gateway 有 route/backend header mutation 能力，但已读文件展示的是通用 header mutation 和 model routing，不是 Cursor anti-ban/OCAW profile `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/ai_gateway_route.go:74`、`envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:api/v1beta1/ai_gateway_route.go:321`。 |
| C4 | LiteLLM OpenAI-like chat handler 直接向 `api_base` 发送 JSON，并按 OpenAI-like response/stream 处理；这不能解决 Cursor Connect/proto 编解码，但可作为 direct-provider fallback 对照 `BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/llms/openai_like/chat/handler.py:26`、`BerriAI/litellm@79b45786719778117debd57e38b9262283431ce2:litellm/llms/openai_like/chat/handler.py:111`。Envoy AI Gateway extproc 注册的是 OpenAI chat/completions/embeddings/responses 等协议路径和 Anthropic messages/cohere rerank，不是 Cursor IDE wire protocol；因此 HUAKAI 的 Cursor proto bridge 不能从 Envoy 直接获得等价实现 `envoyproxy/ai-gateway@4d3eae8b35c4ccc41643d94bb5f69280846561b0:cmd/extproc/mainlib/main.go:295`。 |
| C5 | LLMGateway custom-provider e2e 测试要求 provider key/baseUrl 缺失时报 400，成功时按 custom provider mode 路由，适合作为 HUAKAI Cursor canary 的 fail-closed 测试对照 `theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat-custom-provider.e2e.ts:100`、`theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat-custom-provider.e2e.ts:154`、`theopenco/llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043:apps/gateway/src/chat-custom-provider.e2e.ts:177`。Helicone provider helper 的默认 auth/body builder 体现 direct provider fallback，但不覆盖 Cursor OAuth/session/OCAW；它可借鉴默认 Bearer auth 与 unsupported params 过滤边界 `Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:packages/cost/models/provider-helpers.ts:216`、`Helicone/helicone@094b210b405a3dcc4887d55bfe2d4b4c37af2f20:packages/cost/models/provider-helpers.ts:289`。 |

## 5. 风险登记

- **clean-room 暴露面**：当前允许参考项目中未观察到 Cursor IDE Connect/proto schema 的 MIT 来源。C4 只能使用 HUAKAI 自己的黑盒行为证据、公开协议材料或 Owner 提供的合法资料；不得读取或搬运 LGPL/AGPL/copyleft 参考实现，也不得复制 proprietary Cursor 客户端代码、schema、字段命名或算法。
- **parallel-draft 独立性风险**：本次起草曾被宽泛搜索误触 Claude draft 输出。虽然后续未依赖该文件，Owner 合并时仍应把该风险纳入 synthesis 可信度判断。
- **反封禁敏感度**：checksum、trace id、timezone、request-id、client-version、cookie 组合可能属于 Cursor 风控信号。若没有真实许可证据，strict mode 应 fail-closed；dev mode 必须显式打标，不能进入默认生产。
- **法律/ToS**：Cursor IDE EULA/ToS 是否允许第三方代理、session token refresh、traffic mimicry、账号池复用，需要 Owner 明确决策。技术可行不等于可上线。
- **安全风险**：OAuth/token/cookie 捕获容易泄露真实账号；refresher 必须继续禁止 credential-supplied token endpoint/client；证据文档和测试 fixture 必须自动扫描 secret。
- **包结构风险**：不得在冻结包 `backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto` 新增文件；Cursor wire/proto 相关代码应放 Cursor provider 专属新包。
- **数据库/schema 风险**：如果 Cursor runtime material 需要新增表或迁移，属于高风险，必须 Owner 确认后单独计划。

## 6. Owner 决策点

1. **产品形态：A Cursor IDE session/proto 完整代理；B Cursor BYOK/direct-provider safe equivalent；C manual-first/internal canary。**  
   LiteLLM 已读文件支持 direct Cursor BYOK/provider 形态，LLMGateway 已读文案提醒 Cursor external endpoint 对 coding agent 不完整；若 Owner 选择 A，必须接受 C0/C3/C4 的额外证据和 ToS gate。
2. **OAuth 来源：A Owner 提供并维护 operator config；B 使用公开且许可证可接受的 CLI client/discovery；C 不做 OAuth，只允许 manual token import。**  
   HUAKAI 当前代码已经按 A fail-closed；CLIProxyAPI 可证明 PKCE/refresh 形态，但不能证明 Cursor endpoint/client 可直接复用。
3. **OCAW 策略：A strict fail-closed 直到 checksum/trace 证据完整；B dev/canary 允许缺 checksum；C 放弃 OCAW，仅做 BYOK direct provider。**  
   Portkey/Envoy 只提供 generic header/routing 对照，没有 Cursor anti-ban 等价实现；A 是上线前更稳妥选项。
4. **Cursor wire schema 来源：A 只接受公开/许可资料；B 允许红acted traffic 行为推断并由 HUAKAI 自建 schema；C 暂缓协议转换，保留 passthrough/manual-first。**  
   若没有合法 schema 来源，C4 不能进入 Released-spec 状态。
5. **上线开关：A 新增 Cursor 专属 feature flag；B 继续复用 placeholder session adapters 总开关；C 仅本地 canary 不接生产。**  
   推荐 A；B 会把其他 placeholder session adapters 一起暴露，扩大风险面。

## 7. 时间合计 + 推荐起步切片

- **C0**：0.5-1 天，外加 Owner 法务/产品确认。
- **C1**：1-1.5 天。
- **C2**：1.5-2 天。
- **C3**：2-3 天，checksum/trace 证据不足时会阻塞。
- **C4**：4-7 天，建议拆成 request encoder、stream decoder、error mapping 三段。
- **C5**：1 天。

合计工程时间约 **10-15 天**，不含 Owner 获取合法 Cursor 证据、ToS 决策和真实账号 canary 审批。推荐起步顺序是 **C0 → C1 → C2**：先把 legal/evidence 与 OAuth/store/refresher contract 关住，再决定是否投入 C3/C4。若 Owner 选择 BYOK/direct-provider safe equivalent，则 C3/C4 可转为 Mandatory Roadmap，而不算功能静默删除。

Source files read: `/home/codex/HUAKAI/backend/internal/provider/cursor/bootstrap.go`; `/home/codex/HUAKAI/backend/internal/provider/cursor/bootstrap_test.go`; `/home/codex/HUAKAI/backend/internal/provider/cursor/cursor_session.go`; `/home/codex/HUAKAI/backend/internal/provider/cursor/cursor_session_test.go`; `/home/codex/HUAKAI/backend/internal/provider/cursor/refresher.go`; `/home/codex/HUAKAI/backend/internal/provider/cursor/refresher_test.go`; `/home/codex/HUAKAI/backend/internal/provider/cursor/credential_store_adapter.go`; `/home/codex/HUAKAI/backend/internal/provider/registrydefault/default.go`; `/home/codex/HUAKAI/backend/internal/credentialacq/vendor_exchangers.go`; `/home/codex/HUAKAI/backend/internal/credentialacq/oauth.go`; `/home/codex/HUAKAI/backend/internal/credentialacq/types.go`; `/home/codex/HUAKAI/backend/internal/credentialacq/refresh_lock.go`; `/home/codex/HUAKAI/backend/internal/credentialstore/types.go`; `/home/codex/HUAKAI/backend/internal/credentialworker/refresh_adapter.go`; `/home/codex/HUAKAI/backend/internal/credentialworker/scheduler.go`; `/home/codex/HUAKAI/backend/internal/credentialworker/scheduler_test.go`; `/home/codex/HUAKAI/backend/internal/transport/policy.go`; `/home/codex/HUAKAI/backend/internal/transport/factory.go`; `/home/codex/HUAKAI/backend/internal/transport/mimicry/registry.go`; `/home/codex/HUAKAI/docs/10_RISK_REGISTER.md`; `/home/codex/HUAKAI/docs/03_FEATURE_PARITY_MATRIX.md`; `/home/codex/HUAKAI/docs/specs/request-pacing-mimicry.md`; `/home/codex/refs/CLIProxyAPI/.huakai-head-sha`; `/home/codex/refs/CLIProxyAPI/LICENSE`; `/home/codex/refs/CLIProxyAPI/internal/auth/codex/pkce.go`; `/home/codex/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go`; `/home/codex/refs/CLIProxyAPI/internal/auth/xai/xai.go`; `/home/codex/refs/CLIProxyAPI/internal/auth/kimi/kimi.go`; `/home/codex/refs/litellm/litellm/provider_endpoints_support_backup.json`; `/home/codex/refs/litellm/litellm/llms/openai_like/chat/handler.py`; `/home/codex/refs/litellm/litellm/llms/openai_like/common_utils.py`; `/home/codex/refs/portkey/src/handlers/services/providerContext.ts`; `/home/codex/refs/portkey/src/handlers/services/requestContext.ts`; `/home/codex/refs/portkey/src/providers/openai/api.ts`; `/home/codex/refs/helicone/packages/cost/models/provider-helpers.ts`; `/home/codex/refs/llmgateway/packages/shared/src/components/integration-guides-grid.tsx`; `/home/codex/refs/llmgateway/apps/gateway/src/chat-custom-provider.e2e.ts`; `/home/codex/refs/llmgateway/apps/gateway/src/responses/responses.ts`; `/home/codex/refs/envoy-ai-gateway/api/v1beta1/ai_gateway_route.go`; `/home/codex/refs/envoy-ai-gateway/cmd/extproc/mainlib/main.go`; Lane: codex-specifier; Time: 2026-05-26T08:34:30Z
