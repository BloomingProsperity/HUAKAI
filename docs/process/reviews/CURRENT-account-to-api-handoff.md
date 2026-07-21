# 账号转 API 全链路当前交接

| 项目 | 当前事实 |
| --- | --- |
| 更新时间 | 2026-07-21 UTC |
| 当前目标 | 闭环所有已部署账号从导入、身份与订阅识别、凭据刷新、额度健康、模型同步、选号、账号转 API 出站、计费、日志到失败恢复的完整链路 |
| 工作树 | `/home/ubuntu/HUAKAI-wt-validated-fixes` |
| 分支 | `fix/reverse-account-model-pull-closure-codex` |
| 已合并 PR | `#286 修复账号转 API 全链路接线`，已于 2026-07-21 合并 |
| 已推送 HEAD | `3410e5668b8120a689cf19d4742ea9e4741acbc8` |
| 当前主线 | `145329e72bfcfd038ebe8c236bbcc303790756ee`，即 PR #286 的 merge commit |
| 当前工作树 | 180 个已跟踪文件有改动、4 个文件删除，另有 29 个未跟踪路径；约 `+7241/-2823` |
| 前端 | 本目标没有改前端；仓库里的旧前端不能作为本轮后端验收依据 |
| 后续 PR | 当前未提交改动不在 #286；收口后只开一个新的 PR，仍由 Owner 决定是否合并 |

## 一、接手者先看这里

1. **已合并基线绿色不等于当前工作树绿色。** PR #286 的四条远端检查只覆盖已推送的 `3410e566`，该基线已经合并到主线。当前大量未提交代码没有进入远端 CI，不能用旧绿灯宣称现状可上线。
2. **不要重置、回滚或覆盖当前工作树。** 未提交改动包含账号模型发现、三身份余额、结算、媒体任务、协议入口和测试等多条正在收口的实现。先逐文件读真码、按职责拆分，再决定保留、修复或删除。
3. **只保留当前工作分支。** #286 已关闭；剩余改动收口并验证后只开一个后续 PR。不把 #281/#282 合进来，它们此前只作为问题线索；不得另造平行实现。
4. **文档、搜索结果和旧记忆都不能替代源码。** 搜索只用于定位；任何“已实现、没实现、能用、不能用”的结论，都必须从生产入口、DI、数据库读写、运行时调用和判别测试中证实。
5. **真实账号结果必须分类。** 已通过、上游限额/过载、缺部署者输入和本地代码失败分开记录；没有真实响应证据的能力不宣称已上线。

## 二、已经做了什么

### 2.1 已合并并由 PR CI 验证的基线

已推送提交包括：

- `fd4651ed`：修复账号转 API 全链路接线。
- `823919cf`：补齐正式账号导入与 Rust 出口端到端测试。
- `f28008fc`：收敛账号活体测试到正式导入与 Rust 出口。
- `8b627fc6`：清理混元活体测试旧命名。
- `db73cce2`：封存 Copilot、Windsurf、Cursor 等上线后 IDE 账号模式。
- `b3447077`：收紧账号转 API 全链验收。
- `3410e566`：更新账号转 API 验收状态。

该已推送基线的远端检查均通过：

- Go 单测、竞态与 `vet`。
- PostgreSQL 分包隔离集成测试。
- Rust 格式、`clippy`、测试、压力与故障门。
- 单镜像双进程生命周期冒烟。

这些结果只能证明已经进入主线的 `3410e566`，不能自动证明下面的未提交实现。

### 2.2 当前未提交工作树中已经形成的实现

以下能力已经有真码，但尚未完成本轮最终复审、提交和远端验收：

1. **账号级模型发现与同步**
   - 新包：`backend/internal/accountmodeldiscovery/`。
   - 从凭据仓解析账号的真实加密凭据，经统一 `gateway.Dispatcher` 发起模型发现。
   - 已处理 OpenAI/Codex、Anthropic、Gemini、Antigravity、Grok、Kimi、自定义 OpenAI 兼容上游和 Azure Entra 的请求形状。
   - 带 8 MiB 响应限制、最多 16 页、最多 1000 个模型、去重和稳定排序。
   - 同步时锁账号和发现时使用的精确凭据版本；凭据被轮换后返回 `credential_changed`，避免旧凭据目录覆盖新账号。
   - 成功更新和目录不变都写管理日志。
   - 主要证据：`backend/internal/accountmodeldiscovery/service.go:39-216`、`backend/internal/accountmodeldiscovery/store.go:16-108`。

2. **正式管理入口**
   - `GET /admin/v1/provider-accounts/{id}/upstream-models`：发现但不落库。
   - `POST /admin/v1/provider-accounts/{id}/upstream-models/sync`：发现并同步账号模型白名单。
   - 有租户作用域、严格 JSON、请求体上限和稳定错误码。
   - 已接入 gateway 路由与 DI，并同步 OpenAPI。
   - 主要证据：`backend/internal/adminhttp/provider_account_upstream_models_handler.go`、`backend/cmd/gateway/routes.go`、`backend/cmd/gateway/wiring.go`、`docs/openapi/openapi.yaml`。

3. **统一出站补强**
   - Dispatcher 与 provider 输入已支持 GET 和查询参数，模型发现不另开 HTTP 旁路。
   - 官方 API Key 保持 Go standard transport。
   - 需要客户端指纹的会话/OAuth 账号继续走 Rust mimicry 唯一出口；Claude Setup Token 已补入该矩阵。
   - 修复了 Codex 图片端点被模型发现 GET 覆盖后错误跳过 Responses 转换的回归。

4. **数据库同步保护测试**
   - `backend/internal/accountmodeldiscovery/store_integration_pg_test.go` 覆盖首次更新、目录不变日志、凭据轮换冲突和旧白名单保留。
   - 专用测试数据库已清理，未留下 `huakai_model_discovery_%` 数据库。

5. **当前工作树中的其他大块改动**
   - 三身份会话映射、租户钱包和额度分发：`adminsessionauth`、`balanceledger`、管理调额 handler、迁移 `0209`。
   - 结算、退款与恢复：billing、费用日志、恢复载荷。
   - chat、completions、embeddings、rerank、images、audio 的 route、attempt、fallback、billing 和模型身份接线。
   - 图片和视频任务：OpenAI/Codex 图片、Gemini/Grok 视频、`videohttp`、迁移 `0208`。
   - 导入与账号身份：导入后刷新、Gemini/Antigravity project 补全、订阅套餐归一和标签。
   - 这些代码存在不等于已经验收；尤其资金、身份、迁移和媒体任务必须按高风险链路重新逐项核实。

### 2.3 当前未提交代码已经通过的本地检查

- `go test -race -count=1 -timeout 8m ./...`：全仓通过。
- `HUAKAI_IT_TIMEOUT=12m ./scripts/integration-pg.sh`：80 个 PostgreSQL 隔离包全部通过。
- 数据库迁移已进入 `0210`；全新库迁移、媒体任务绑定快照、绑定并发与账号 RPM 真 SQL 测试通过。
- Rust 账号转 API mimicry 工作区的 `fmt`、`clippy -D warnings`、全部普通测试和忽略测试通过。
- `scripts/quality-gate.sh`：通过；静态检查和死代码 baseline 均未放宽。
- 支付挂起订单并发上限在真 PostgreSQL 首跑发现 advisory lock 参数类型错误，已修复并重跑通过。
- 活体测试首跑发现一次性数据库 DSN 仍指向管理库，已改为显式重写数据库 URL 并从 `template0` 创建；判别测试和后续活体测试证明修复生效。
- 测试结束后 `huakai_e2e_*`、`huakai_codex_*`、`huakai_it_*` 数据库残留为 0。

当前未完成的工程门只剩本批 diff 的独立审查、提交、推送和新 PR 远端 CI。

## 三、确认存在的问题与影响面

### P0-1 当前实现已完成本地验收，待独立复审与 PR CI

**事实**：当前大批改动已通过本地全量门，但尚未提交和进入远端 CI。

**影响面**：账号导入、鉴权、租户隔离、钱账、协议转发、迁移、媒体任务和恢复都可能出现跨模块回归；现在不能提交后续 PR，也不能宣称“全部完成”。

**下一步**：只 stage 当前目标 diff，执行独立审查，修复 S0/S1 后提交并创建一个新 PR。

### P0-2 缺少正式 HTTP 全链模型同步测试 —— 已完成（2026-07-21）

**落地**：新增 `backend/cmd/gateway/model_sync_smoke_test.go`（smoke 标签，`TestSmoke_AccountModelSyncFullChain`），启动真实网关二进制走：

`正式管理员鉴权(admin_tokens bcrypt) -> POST /admin/v1/provider-accounts/{id}/upstream-models/sync -> 解密凭据 -> 统一 Dispatcher -> mock 上游模型目录 -> 落 model_allow_list -> 审计轨迹 -> 同一别名 chat 请求被该账号选中并完成计费闭环`

断言覆盖：同步前陈旧白名单把账号排除出池（503 pool_no_capacity、零槽记录）；跨租户管理员 404 且白名单不被动过；同步 200 且 changed/models 与上游目录一致；`admin_audit_events` 有 `sync_account_models/updated` 轨迹；同步后 chat 200 且 `pool_slot_acquisitions` 该账号 `released_success`=1。dev mock 上游补了 `GET /models` 目录响应（`backend/cmd/gateway/dev_mock_upstream.go` `mockModelCatalog`）。

**该测试首跑即抓住一个真回归**：`internal/provider/openai/passthrough.go` 的 `BuildRequest` 硬编码 POST、忽略 `HTTPMethod`，openai_chat 家族（官方 OpenAI api_key、upstream_static 自定义兼容上游、Azure）的模型发现实际发出 POST /v1/models，对真上游必 405——正是“单元测试通过，生产按钮不可用”。已按 anthropic passthrough 既有范式修复（GET 无 body、无 Content-Type、未知方法拒绝），判别单测 `TestPassthroughAdapter_BuildRequest_GetModelDiscovery` 变异（回退硬编码 POST）转红验证。辐射面已核：anthropic/gemini/grok/kimi(OpenAICompat) adapter 均已支持 HTTPMethod,仅 openai passthrough 漏。

### P0-3 模型同步失败没有形成专门的分类日志 —— 已完成（2026-07-21）

**落地**：handler 两个失败出口（GET discover / POST sync）经 `logUpstreamModelsFailure` 落 `privacy.LogSystem` 系统日志（`backend/internal/adminhttp/provider_account_upstream_models_handler.go`），字段：`error_class`（11 种发现分类）、`event_class`（`upstream_models_{discover|sync}_failed`）、`outcome/tenant_id/provider_account_id/vendor/auth_mode/upstream_status/request_id`。`DiscoveryError` 增加 `Vendor/AuthMode` 回填（`Discover`/`Sync` defer annotate），持久化失败也可按账号族辨识。

**privacy 禁写约束（重要）**：privacy 值位扫描 fail-closed 拦截含 `credential`/`refresh_token` 词根的值（整包变 privacy_guard_hit），因此日志侧用等义分类：`credential_rejected→upstream_auth_rejected`、`credential_changed→auth_rotation_conflict`、auth_mode `refresh_token→oauth_refresh`；HTTP 错误码不变。原始上游错误正文按 privacy 合同不落日志，靠 error_class+upstream_status 辨识。判别测试断言全部字段且断言绝不触发 privacy_guard_hit；三轮变异（删日志调用/回退等义映射/删 annotate defer）均转红。smoke 真进程日志已实拍到完整字段输出。

### P0-4 上游模型 ID 与选号所用模型 ID 的一致性尚未用全链测试证明 —— 已完成（2026-07-21）

**落地**：P0-2 的 smoke 测试即判别夹具——公开别名 `gpt-4.1-mini` ≠ 注册表出站模型 `dev-mock-model`（mock 上游目录只含 `dev-mock-model*`）。证明了：同步落库值 = 上游原始 ID = 选号 `ProviderModelID` 过滤值；白名单不含出站模型时账号被排除（“目录看着正确但调用 503”场景的负向臂）；同步后同一别名请求命中该账号。无需改 schema。

### P0-5 真实厂商账号端到端验证 —— 已执行，外部限制单列

本轮真实账号验证均从正式导入入口建号，经过真实 PostgreSQL、网关和对应出站，再检查计费与清理；没有用数据库直写冒充导入成功。

- Anthropic、OpenAI、Grok、Kimi Coding 的官方 Key 完整链通过；这些标准请求走 Go standard transport。
- Codex 账号完成 Responses 与 Chat 转换矩阵，覆盖流式、非流式、工具调用、图片生成、图片输入、结构化输出和 reasoning；需要客户端指纹的账号链走 Rust mimicry。
- Antigravity 正式导入、project/tier 识别和真实调用通过。
- Claude AI OAuth 完成正式导入并抵达真实上游；本轮上游返回限流，本地正确分类、冷却账号并在无容量后停止盲重试。这是外部账号状态，不记作本地协议失败，也不冒充活体通过。
- Gemini AI Studio 本轮使用 `gemini-3.1-flash-lite` 完成正式导入、真实调用、计费与槽释放。
- Gemini Code Assist 已真实触发刷新，随后上游要求部署者提供 Google Cloud `project_id`；本机材料没有该字段，因此没有伪造项目继续调用。
- Kimi OAuth、Grok OAuth 没有完整可用的 OAuth 材料；Moonshot Key 返回 401。对应官方 Key 链已验证，但这些认证模式不宣称活体通过。
- 多账号轮询、并发和故障切换由可控多账号夹具证明，不拿单个真实账号伪装多账号验收。

测试数据均已清理。未完成的是受外部账号状态或部署者输入阻塞的认证模式，不是继续改本地代码即可消除的工程缺口。

### P0-6 三身份余额和结算高风险验收 —— 本地通过

三身份允许/拒绝路径、部署者不得越级调整下级租户用户、租户管理员只能管理本租户普通用户、双边分录、前后余额、幂等冲突、余额不足、并发锁序、反向纠错和结算失败恢复，均已进入单元与真实 PostgreSQL 测试。迁移 `0209` 已完成往返验证。

该链仍需本批独立审查和新 PR 远端 CI；在此之前不把“本地通过”写成“已合并上线”。

### P0-7 媒体任务、图片和视频高风险验收 —— 本地通过，厂商活体范围如实保留

迁移 `0208` 与 `0210`、`videohttp`、Gemini/Grok 视频 provider、OpenAI/Codex 图片、媒体任务账号与绑定快照、轮询、重试、结算与恢复已通过全量单元、竞态和 PostgreSQL 集成。任务会持久化原 `binding_id`、绑定 RPM/TPM/最大并发；worker 真正占槽时恢复原绑定合同，固定账号轮询与受保护下载再经过账号 RPM 原子准入，不会双计用户 Key/绑定预算，也不会脱离原池重新选号。

Grok 视频已用真实 Key 完成正式导入、异步提交、原账号轮询、成功终态、钱账、费用凭证、配额、槽释放和六跳日志闭环。Gemini Veo 的现有 Key 没有视频生成配额，因此不宣称真实产物通过；其协议、原账号/原绑定、429 延迟重试、受保护下载和钱账由判别测试及真实 PostgreSQL 覆盖。

### P0-8 完整本地质量门 —— 已完成

- `go test -race -count=1 -timeout 8m ./...`：通过。
- `HUAKAI_IT_TIMEOUT=12m ./scripts/integration-pg.sh`：80 个隔离包通过。
- Rust mimicry 工作区 `fmt`、`clippy -D warnings`、普通测试和忽略测试：通过。
- 数据库迁移和真 SQL：通过至 `0210`。
- `scripts/quality-gate.sh` 与代码预算：通过，baseline 未放宽。
- 所有专用测试数据库已删除，残留为 0。

仅剩当前 diff 的独立只读审查、新提交、新 PR 和远端 CI。

## 四、真实存在但可排在 P0 之后的差距

### P1-1 Azure API Key 账号仍缺正确专用适配

当前账号模型发现只安全支持 Azure Entra Bearer 加资源 `base_url`。普通 Azure API Key 没有正确的 Azure header、资源域名和版本化路径适配，现有凭据物化会拒绝，避免把密钥错误发往普通 OpenAI 主机。

**影响**：Azure API Key 账号不能正式服务或同步模型。

**处理**：读 Azure 官方协议与 HUAKAI 真码，建立独立 adapter 和 host/header 防泄漏测试；不能把它当普通 OpenAI key 透传。

### P1-2 其他已发布 OpenAI 兼容厂商的账号级模型同步合同未定

`servingcapability` 中 DeepSeek、Qwen、GLM、Yi、Baichuan、Doubao、Ernie、Step、Hunyuan、MiniMax 当前标为全局模型发现，见 `backend/internal/servingcapability/contracts.go:221-238`。这不等于 Bug，但与“所有账号导入后能识别自身可用模型”的目标存在颗粒度差距。

**影响**：不同 key 权限不一致时，全局目录可能展示账号实际不可用的模型。

**处理**：逐厂商读官方源码/协议或真实 API，确认是否有账号级模型目录；有就接入统一 discovery，确实没有就保留全局合同并明确“不适用”，不能伪造。

### P1-3 账号级模型发现的剩余外部前提

Grok 和 Kimi Coding 的真实模型目录已成功读取；Antigravity 的真实 project/tier 与调用也已通过。Gemini Code Assist 已触发真实刷新，但当前凭据缺少上游强制要求的 Google Cloud `project_id`，因此该模式的模型发现和生成仍待部署者补齐项目身份后复验。Grok/Kimi OAuth 因缺完整材料不冒充已通过。

### P1-4 MCP 不能宣称已经上线

原生 function/tool calling 已有协议和流式事件覆盖，但 MCP client/server 的细粒度授权、租户隔离、工具目录、调用日志、超时、恢复和运维入口没有完整闭环。

**影响**：前端或文档若把工具调用等同于 MCP，会形成错误产品承诺和权限风险。

**处理**：保留 Mandatory Roadmap；读该领域维护活跃、许可证明确的成熟项目源码形成行为合同，再结合 HUAKAI 身份、池和日志体系独立实现。

### P1-5 全局日志 30 天分类清理合同尚未证明覆盖所有模块

Owner 已定：产品中统一称“日志”，普通操作、拒绝和错误日志分类明确并保留 30 天；永久资金事实不可按 30 天删除。当前只在部分链路有清理和分类，没有证据证明所有入口、worker、DLQ、账号刷新、模型同步、媒体任务都使用同一保留合同。

**影响**：日志无限增长、重要错误无法检索，或错误地删除永久资金事实。

**处理**：按表和写入者逐一列出“普通日志/安全日志/永久资金事实”，检查清理 worker、索引、告警和 Hermes 查询入口；只删除前两类过期记录，永久事实只允许反向更正。

## 五、不是 Bug 的主动边界

- Copilot、Windsurf、Cursor 已由 Owner 指定为首次正式上线后再做，当前必须保持封存，不得因底层 adapter 存在就宣称可服务。
- 官方 API Key 没有个人订阅等级属于正常语义；能否查余额必须按厂商官方接口逐家判断，不能伪造套餐标签。
- 官方 API Key 走 Go standard，账号会话/OAuth 只有确实需要指纹时才走 Rust mimicry；“统一 Dispatcher”不等于所有请求都走 Rust。
- 前端页面、动态链路图和配额弹窗不在当前 PR；后端必须先提供同一事实源，后续 UI 才能展示池选择、真实模型、用量、预扣和最终扣款。
- 服务器探针与安全监测是后续独立模块，不得插队打断当前账号转 API 闭环。

## 六、当前剩余执行顺序

最新两轮独立审查已完成：账号模型同步的安全会话写权限已接通；媒体任务继续明确区分上游实际成本与客户最终实扣；不支持的视频操作及缺少完整账号/池/协议/模型/绑定快照的视频任务均在创建任务和冻结余额前同步拒绝。全仓 `go test -race -count=1 -timeout 12m ./...` 与质量门通过。

1. 复核最终 diff、未跟踪文件和凭据泄漏，确保不包含前端、真实秘密、临时产物或被拒绝的旧迁移。
2. 只 stage 本目标 diff，运行项目规定的独立只读审查；S0/S1 修完，最多按规则两轮。
3. 提交正文全中文，在现有唯一分支上吸收最新 `origin/main`，重跑受影响冒烟。
4. 推送并创建一个新 PR，等待远端 CI；不得自行合并。
5. 新 PR 确认完整覆盖后关闭旧开放 PR；`#278` 明确记录为拒绝不安全实现。
6. Gemini Code Assist `project_id`、Grok/Kimi OAuth 材料、Gemini Veo 配额、Claude 限流和 Moonshot 有效 Key 作为外部前提记录，材料恢复后复验，不用伪数据涂绿。

## 七、必须读取的规则和当前记忆

### 7.1 权威规则，按顺序读取

1. `AGENTS.md`
2. `CLAUDE.md`
3. `docs/RULES.md`
4. `.claude/skills/CANONICAL.md`
5. `docs/process/plans/2026-07-20-reverse-account-model-pull-closure-codex.md`
6. `docs/architecture/egress-tls-mimicry-SSOT.md`
7. `docs/architecture/2026-07-18-global-renew-audit.md`
8. `docs/architecture/deprecated-schema.md`

### 7.2 本目标建议读取的技能

- `.agents/skills/reference-project-miner/SKILL.md`
- `.agents/skills/clean-room-license-guard/SKILL.md`
- `.agents/skills/api-gateway-risk-review/SKILL.md`
- `.agents/skills/production-scenario-review/SKILL.md`
- `.agents/skills/acceptance-test-writer/SKILL.md`
- `.agents/skills/feature-parity-auditor/SKILL.md`
- `.agents/skills/feature-merger/SKILL.md`
- `.agents/skills/release-readiness-gate/SKILL.md`

### 7.3 只作导航、不能代替当前源码的记忆

- `/home/ubuntu/.claude/projects/-home-ubuntu/memory/look-at-associated-artifacts-not-myopic.md`
- `/home/ubuntu/.claude/projects/-home-ubuntu/memory/owner-demands-multiangle-investigation.md`
- `/home/ubuntu/.claude/projects/-home-ubuntu/memory/must-read-source-cross-branch-2026-07-16.md`
- `/home/ubuntu/.claude/projects/-home-ubuntu/memory/logging-detailed-discriminating-2026-07-17.md`
- `/home/ubuntu/.claude/projects/-home-ubuntu/memory/official-contract-over-mirrors-2026-07-10.md`
- `/home/ubuntu/.claude/projects/-home-ubuntu/memory/real-accounts-location-huakai-accounts-dir.md`
- `/home/ubuntu/.claude/projects/-home-ubuntu/memory/test-all-owner-accounts-2026-07-15.md`
- `/home/ubuntu/.claude/projects/-home-ubuntu/memory/real-e2e-and-reversed-lane-2026-07-20.md`
- `/home/ubuntu/.claude/projects/-home-ubuntu/memory/egress-rust-only-go-dead-2026-07-18.md`

注意：记忆记录的是当时状态，后续代码可能已经改变。尤其旧的“单租户/下级代理/协管员”“Go uTLS 仍是生产出口”“Rust 仍停放”“迁移只到 207”等表述，不得作为当前结论。当前三身份以本文件和当前计划为准：部署者 `platform_admin`、下级租户管理员 `tenant_operator`、最终用户 `users.role='user'`。

## 八、Owner 的硬规则摘要

- 全中文：规则、计划、代码注释、commit 正文、交接和汇报全部中文；技术标识符保留英文。
- 真相优先：只相信当前真码、真实运行和判别测试；不能拿 grep、文档、测试 helper 或 Agent 摘要冒充生产证据。
- 全链路闭环：发现一点，要沿上游依赖、下游消费者、同构模块、并发、幂等、失败补偿、日志、告警和人工恢复一起查。
- 能力并集、实现唯一：重复或矛盾实现先取能力并集，接入唯一正式入口，测试证明后删除旧实现；不能删掉独有功能。
- 领域优先：中转站核心看 Sub2API/New API/CLIProxyAPI 等；钱账、MCP、监控等领域要选该领域维护活跃、成熟且许可证明确的头部项目。必须 clean-room 读源码，只提炼行为合同，不复制实现。
- 官方协议优先：协议、模型、端点和参数以官方文档与真实 API 实测为准；借鉴项目用于功能形态和运维逻辑，不能压过官方事实。
- 三项同时命中才暂停审批：存在分歧、成熟项目没有先例、影响很大。其他问题核实后直接修。生产秘密、部署、不可逆数据操作、`LICENSE` 和合并主线仍必须 Owner 明确批准。
- 三身份绝不能混：部署者、下级租户管理员、最终用户。部署者可调整下级租户钱包，不能越级调整该租户用户；租户管理员只能管理本租户普通用户。
- 分组策略 fail-closed：配置真相读取失败时明确 503，绝不临时越级进入更高等级账号池。
- 日志：统一称“日志”，错误分类必须可辨识；普通日志 30 天清理，永久资金事实不可删除，只能反向纠错。
- 出口：官方 Key 走 Go standard；需要指纹的会话/OAuth 走 Rust mimicry 唯一出口，禁止 mimicry 静默回退 standard。
- 真实账号：Owner 已授权用于本目标测试；最小成本、逐个模型、逐协议，秘密不进入命令输出、日志、文档或提交。
- 单线执行：完成手头链路再做后续需求；只保留一个分支、一个 PR，不碰其他目标，不自行合并。

## 九、真实凭据位置与安全边界

仅允许读取，不得把内容写进本文、日志、测试快照、shell 历史或 Git：

- `/home/ubuntu/huakai-accounts/`
- `~/.claude/.credentials.json`
- `~/.codex/auth.json`
- `~/.gemini/oauth_creds.json`
- `~/.gemini/antigravity-cli/antigravity-oauth-token`

Grok/Kimi 的历史材料可能是文本或旧临时文件，不保证仍有效。先检查存在性和权限，通过进程环境或内存注入测试，输出只显示厂商、账号指纹、测试阶段和脱敏错误分类。不得打印完整 token/key/Cookie，不得把真实凭据复制进仓库。

## 十、完成标准

只有同时满足下面条件，才能向 Owner 汇报“账号转 API 后端闭环完成”：

- 所有已发布账号类型都经过正式导入、身份/套餐识别、凭据刷新、额度/模型发现、混合池选号、正确出口、真实响应、计费结算、日志与恢复。
- 同一个用户 Key 在授权混合池中可按模型能力调用 Claude、Gemini、Kimi、GPT、Grok，且池策略失败时绝不越级。
- 三身份允许与拒绝矩阵、资金双边事实、并发、幂等和恢复在真实 PostgreSQL 通过。
- 图片、语音、视频、embeddings、rerank、Responses、Gemini 等入口没有绕过统一能力、凭据、调度、计费和恢复链。
- 每个宣称支持的厂商和模型有真实账号结果；失败明确区分账号失效、上游限制、本地协议错误和环境错误。
- 最新 HEAD 的 Go、PostgreSQL、Rust、竞态、迁移、质量门和独立复审全部通过。
- 测试数据库、租户、账号、凭据、claim、hold、日志和任务残留为零；无旧测试库或旧提示干扰。
- 剩余改动只进入一个后续 PR；该 PR 保持绿色，仍由 Owner 最终批准合并。

本文是当前目标唯一交接文件。后续接手者只更新本文，不再新建同义进度、问题或交接文档。
