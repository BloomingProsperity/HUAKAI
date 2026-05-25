# Owner Cloud Review Source Verification
## 0. 验证说明
- 验证人: GPT-5 Codex (verifier lane)
- 验证基线: 本仓当前 `HEAD = 6e76d59c319127b82dba5a6fcaf78a6c36f1c9da`。注意:任务描述写“HEAD = c1aced8 之后”,但本地 `git log` 显示 `6e76d59` 在 `c1aced8` 之后,且当前分支正停在 `6e76d59`。
- Owner 原文: `docs/process/research/2026-05-23-owner-cloud-review.md`
- 验证依据: `CLAUDE.md` §12 source-must-read;本次核验 Owner external AI claim,逐条读 HUAKAI 当前源码/文档。
- 判定口径: `confirmed`=行号和描述基本准确;`refined`=方向正确但行号/影响需校准;`refuted`=现 HEAD 不成立;`partial`=部分成立或环境差异无法由源码完全证明。

## 1. 细节 Bug 验证 (12 条)
### Bug #1 非流式 DLQ ref 透传
- Owner cite: `backend/internal/gatewayhttp/chat_completions_handler_headers.go:55`
- 实际代码(current HEAD): `WriteHuakaiHeaders` 先写模型头,随后仅当 ledger result 是 `Persisted` 才写 audit ledger headers;非 `Persisted` 直接返回,没有把 `DLQRef` 写到普通响应头(`backend/internal/gatewayhttp/chat_completions_handler_headers.go:55-60`)。流式路径则在 `Deferred` 时把 DLQ ref 写入 trailer(`backend/internal/gatewayhttp/chat_completions_stream.go:569-593`)。
- 现状判定: confirmed
- W5 scope? yes;这是 W5 synthesis 需新增的 finding,不在现有 W5 原始 finding 表里。
- 评论: Owner 描述准确;W4 修了流式 trailer,非流式仍沉默。

### Bug #2 CredentialStore 审计大量 `_ = InsertAuditEvent`
- Owner cite: `backend/internal/credentialstore/postgres_store.go:229`
- 实际代码(current HEAD): create 路径在写入凭据后忽略 audit insert 错误(`backend/internal/credentialstore/postgres_store.go:229-235`);同文件还在 rotate/state/delete/refresh success/refresh failure 多处忽略 audit 写入(`backend/internal/credentialstore/postgres_store.go:308-314`,`backend/internal/credentialstore/postgres_store.go:427-433`,`backend/internal/credentialstore/postgres_store.go:462-467`,`backend/internal/credentialstore/postgres_store.go:617-622`,`backend/internal/credentialstore/postgres_store.go:655-660`)。
- 现状判定: confirmed
- W5 scope? yes;W5 Codex plan 已列 C-04(`docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:17`)。
- 评论: 业务 mutation 已提交后 audit 失败可被吞,与 W5 “同成功、同失败”目标冲突。

### Bug #3 CredentialStore SetState 审计语义不准确
- Owner cite: `backend/internal/credentialstore/postgres_store.go:398`
- 实际代码(current HEAD): `SetState` 接受 normalize 后的任意合法状态(`backend/internal/credentialstore/postgres_store.go:398-405`),但 audit event type 固定为 `credential_disabled`,仅在 payload 里放目标 state(`backend/internal/credentialstore/postgres_store.go:427-432`)。
- 现状判定: confirmed
- W5 scope? yes;W5 计划已列 C-05 并要求状态迁移审计改语义(`docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:18`)。
- 评论: revoked -> active / active -> operator_attention 都会被固定写成 disabled 类事件。

### Bug #4 Antigravity token/refresh 审计失败被忽略
- Owner cite: `backend/internal/auth/antigravity_token_provider.go:145`
- 实际代码(current HEAD): cache hit 直接忽略 `writeAudit` 错误(`backend/internal/auth/antigravity_token_provider.go:141-146`);刷新成功、lock-held、DB 版本冲突、malformed、failure 等路径也都用 `_ = p.writeAudit(...)`(`backend/internal/auth/antigravity_token_provider.go:225-231`,`backend/internal/auth/antigravity_token_provider.go:273-283`,`backend/internal/auth/antigravity_token_provider.go:523-524`,`backend/internal/auth/antigravity_token_provider.go:539-552`)。
- 现状判定: confirmed
- W5 scope? yes;W5 计划已列 C-03(`docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:19`)。
- 评论: audit writer nil 时 `writeAudit` 也返回 nil(`backend/internal/auth/antigravity_token_provider.go:557-560`),生产 gate 需要补。

### Bug #5 Admin pool / provider account 变更和审计不是同事务
- Owner cite: `backend/internal/gatewayhttp/admin_pools_handler.go:162` / `:173` / `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:759`
- 实际代码(current HEAD): pool create 先 `InsertPool`,再 `writeAdminPoolAudit`(`backend/internal/gatewayhttp/admin_pools_handler.go:162-180`);pool update 同样先 update 再 audit(`backend/internal/gatewayhttp/admin_pools_handler.go:252-273`)。provider account create/update/enabled/clear/delete 也都是 mutation 后调用 audit helper(`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:243-247`,`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:372-383`,`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:409-425`,`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:440-452`,`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:474-486`);helper 本身只是单独 insert admin audit(`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:759-767`)。
- 现状判定: confirmed
- W5 scope? yes;W5 已列 GW-10(`docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:15-16`)。
- 评论: audit 失败时 handler 返回 503,但先前 mutation 已可能提交。

### Bug #6 Channel health 审计 signer 为空时静默成功
- Owner cite: `backend/internal/channelhealth/store_postgres.go:295`
- 实际代码(current HEAD): `AppendAudit` 先插入 `channel_health_audit_events`,然后 signer nil 时直接返回 nil,不追加 trust ledger 签名证据(`backend/internal/channelhealth/store_postgres.go:266-296`)。
- 现状判定: confirmed
- W5 scope? yes;W5 已列 C-10(`docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md:20`)。
- 评论: 这不是“完全无 audit row”,而是“无签名审计/无 trust ledger append”,Owner 影响描述成立。

### Bug #7 Invitation schema 还有隔离问题
- Owner cite: `backend/sql/migrations/0034_community_invitation_referral.up.sql:7`
- 实际代码(current HEAD): `invitations.code` 是全局唯一,不是 `(tenant_id, code)` 唯一(`backend/sql/migrations/0034_community_invitation_referral.up.sql:4-15`);`referrals.referee_user_id` 也是全局唯一(`backend/sql/migrations/0034_community_invitation_referral.up.sql:23-34`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: 跨波 F-COMM schema isolation,需要单独立项;涉及 DB schema,属高风险需 Owner 确认。
- 评论: 多租户碰撞/串租户约束风险存在。

### Bug #8 Invitation 外键仍不完整
- Owner cite: `backend/sql/migrations/0034_community_invitation_referral.up.sql:34`
- 实际代码(current HEAD): referral 只对 `(tenant_id, invitation_id)` 绑 invitation(`backend/sql/migrations/0034_community_invitation_referral.up.sql:33-35`);reward 只对 referral 绑 tenant 组合 FK,receipt_id 单独引用 receipt(`backend/sql/migrations/0034_community_invitation_referral.up.sql:42-53`)。tenant_id / inviter_user_id / referrer_user_id / referee_user_id 没有完整绑定 tenants/users。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: 跨波 F-COMM schema integrity,同 Bug #7 一并立项。
- 评论: Owner 对“部分 FK,但关键租户/用户 FK 不完整”的描述准确。

### Bug #9 OpenAPI 声明严格,handler 实际不严格
- Owner cite: `backend/internal/gatewayhttp/invitation_handler.go:81` / `backend/internal/gatewayhttp/cost_receipt_handler.go:148`
- 实际代码(current HEAD): invitation handler 在 body limit 后直接 `json.NewDecoder(...).Decode(dst)`,未启用 unknown-field 拒绝(`backend/internal/gatewayhttp/invitation_handler.go:80-89`);receipt verify 读完整 body 后 `json.Unmarshal` 到结构体,未知字段会被 Go 默认忽略(`backend/internal/gatewayhttp/cost_receipt_handler.go:148-164`)。对应 OpenAPI schema 却是 strict: invitation create `additionalProperties: false`(`docs/openapi/openapi.yaml:2449-2452`),receipt verify body `UserCostReceipt` 也是 `additionalProperties: false`(`docs/openapi/openapi.yaml:2807-2810`)。
- 现状判定: refined
- W5 scope? no
- 当前波次归属: 跨波 API contract / strict decoding remediation。
- 评论: 方向正确;Owner cite 指到 body limit 行,真实冲突需要同时看后续 decode/unmarshal 行和 OpenAPI schema 行。

### Bug #10 Rust 网关 Windows 下直接编不过部分路径
- Owner cite: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:682`
- 实际代码(current HEAD): `UnixSocketConnector` 的 `Service<Uri>` impl 使用 `tokio::net::UnixStream` 作为 response 类型,且该 impl 没有包在 `#[cfg(unix)]` 下(`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:668-697`)。同文件顶部也没有全文件 Unix cfg(`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:1-23`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: W11/W12 Rust 预生产硬化;总波次表把 Rust security/telemetry 放 W11/W12(`docs/process/plans/2026-05-22-audit-remediation-wave.md:61-62`)。
- 评论: 未在 Windows 编译实测,但源码平台 cfg 缺失足以确认风险方向。

### Bug #11 Rust TLS feature 仍然强互斥
- Owner cite: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/mod.rs:12`
- 实际代码(current HEAD): 同时启用 `mimicry-boring` 与 `mimicry-openssl` 会触发 `compile_error!`,错误文本明确说明二者互斥(`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/mod.rs:12-16`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: W11/W12 Rust 预生产硬化。
- 评论: Owner 描述准确;这仍是架构限制,不是运行时 fallback。

### Bug #12 Rate limit 细分识别还没完整实现
- Owner cite: `backend/internal/rate/rate.go:75`
- 实际代码(current HEAD): `rate.go` 顶部声明 provider-specific classifiers 仍是后续工作(`backend/internal/rate/rate.go:1-5`),TODO 明确列多平台 429 reset 提取、403 dispatch、cascade clearing、OAuth 401 force-refresh interaction(`backend/internal/rate/rate.go:75-77`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: W7 routing/capacity/health;总波次表把 routing 容量与健康门控放 W7(`docs/process/plans/2026-05-22-audit-remediation-wave.md:57`)。
- 评论: 不能宣传为完整 sub2api 式 cooldown 管理。

## 2. 功能遗失验证 (5 条)
### Feature #1 模型接入还不是“全功能模型平台”
- Owner cite: `docs/openapi/openapi.yaml:89`
- 实际代码(current HEAD): OpenAPI `paths:` 从该行开始;客户端 gateway 正式暴露的是 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`(`docs/openapi/openapi.yaml:89-186`)。全文搜索未发现 `/v1/embeddings`、`/v1/images`、`/v1/audio`、`/v1/rerank`、`/v1/realtime`。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: 跨波 product capability roadmap。
- 评论: OpenAPI 证实当前不是“全模型 API 面”。

### Feature #2 Provider 注册很多,但部分是 placeholder/session-reverse
- Owner cite: `backend/internal/provider/registrydefault/default.go:91`
- 实际代码(current HEAD): 正式 API key passthrough adapters 注册在 lines 90-125;6 家 session 反转协议在常量注释中标为 scaffold/TODO header(`backend/internal/provider/registrydefault/default.go:74-80`),且默认不注册,需环境变量 opt-in(`backend/internal/provider/registrydefault/default.go:127-130`)。
- 现状判定: refined
- W5 scope? no
- 当前波次归属: W10 protocols 生产协议注册与投影收口;总波次表 W10 覆盖 production protocol registration(`docs/process/plans/2026-05-22-audit-remediation-wave.md:60`)。
- 评论: Owner 方向正确;引用 line 91 只指 OpenAI Chat 注册,placeholder 证据在 74-80 与 127-130。

### Feature #3 OpenAI Responses 能力还存在协议损耗
- Owner cite: `backend/internal/proto/openai_responses_response.go:73` / `backend/internal/proto/openai_responses_stream.go:118`
- 实际代码(current HEAD): non-streaming response 对 tool_result/reasoning/image 生成 loss entry 或 pending loss(`backend/internal/proto/openai_responses_response.go:70-78`);streaming 对 tool_use/input_json_delta/thinking_delta 等仍返回 pending/loss(`backend/internal/proto/openai_responses_stream.go:118-120`,`backend/internal/proto/openai_responses_stream.go:144-149`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: W9 proto / protocol-translation hardening。
- 评论: Owner “不能宣传完整兼容”成立。

### Feature #4 多模态、实时、MCP、A2A 还是规划项
- Owner cite: `docs/03_FEATURE_PARITY_MATRIX.md:57`
- 实际代码(current HEAD): feature matrix 把 multimodal 标为 Mandatory Roadmap Phase 9+(`docs/03_FEATURE_PARITY_MATRIX.md:57`),Realtime 也为 Mandatory Roadmap Phase 9+(`docs/03_FEATURE_PARITY_MATRIX.md:58`),MCP/A2A 类外部协议桥接为 Plugin Phase 9+(`docs/03_FEATURE_PARITY_MATRIX.md:59`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: Product roadmap,非 W5。
- 评论: 功能没有删除,但当前不是已完成状态。

### Feature #5 大请求能力偏保守
- Owner cite: `backend/internal/gatewayhttp/chat_completions_validate.go:78`
- 实际代码(current HEAD): chat request body 使用 `http.MaxBytesReader` 限制为 `1<<20`,即 1 MiB(`backend/internal/gatewayhttp/chat_completions_validate.go:76-84`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: API contract / large payload policy,需与 abuse/成本限制一起定。
- 评论: 对长上下文、大工具参数、图文或批量请求确有偏小风险。

## 3. 测试与工程缺口验证 (5 条)
### Test #1 普通 `go test ./...` 通过不代表完整业务没 bug
- Owner cite: `backend/internal/audit/refund_worker_tx_integration_test.go:1`
- 实际代码(current HEAD): 该文件本身无 build tag,但内部打开 PG pool 时要求 `HUAKAI_DATABASE_URL`,否则 `t.Skip`;表不存在也 skip(`backend/internal/audit/refund_worker_tx_integration_test.go:126-144`)。仓库还有多处 `integration_pg` build tag 或环境变量门控的集成测试,例如 auditledger PG test 文件声明不带 tag 不进入默认 suite(`backend/internal/auditledger/postgres_test.go:1-8`)。
- 现状判定: refined
- W5 scope? no
- 当前波次归属: 测试工程/CI gate。
- 评论: Owner 方向正确;但 cite 到 line 1 不能证明 skip,真实证据在该文件 126-144 和其他 integration tag 文件。

### Test #2 Acceptance matrix 仍大量 Planned
- Owner cite: `docs/11_ACCEPTANCE_TEST_MATRIX.md:15`
- 实际代码(current HEAD): acceptance matrix 从首批 L1 行开始大量 `Status = Planned`,例如 AT-GW-001..AT-SEC-005(`docs/11_ACCEPTANCE_TEST_MATRIX.md:13-27`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: release readiness / acceptance closure。
- 评论: 这是 release gate 缺口,不是单一代码 bug。

### Test #3 远端服务器不是可验证环境
- Owner cite: 无源码 cite;Owner 声称远端无 go/cargo。
- 实际代码/环境(current HEAD): 本仓 backend 是 Go module 且要求 Go 1.25(`backend/go.mod:1-3`);Rust merged workspace 要求 Rust 1.95(`exploratory/rust-core-gateway/merged/Cargo.toml:1-8`)。本 sandbox 实测有 `go version go1.25.0` 与 `cargo 1.95.0`,并且 `cd backend && GOCACHE=/tmp/huakai-go-cache go test ./...` exit 0;当前 `go list ./...` 为 82 个包。
- 现状判定: partial
- W5 scope? no
- 当前波次归属: 工程环境/CI。
- 评论: Owner 对“他连的远端 SSH PATH/服务器无工具”可能属实,但不能泛化到本 sandbox;本地可验证环境存在。默认 `go test ./...` 第一次因 `/home/codex/.cache/go-build` 只读导致 cache trim exit 1,换 `/tmp` GOCACHE 后通过。

### Test #4 仓库没看到 GitHub Actions / CI 工作流
- Owner cite: 无源码 cite。
- 实际代码(current HEAD): `git ls-files '.github/**'` 返回空;工作树没有 `.github` 目录。仓库只有本地 Makefile test/build/vet/generate 目标,例如 `test` 目标为 `go test ./... -race -count=1`(`backend/Makefile:18-19`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: CI/release engineering。
- 评论: absence claim 无 file:line 可 cite,以 git tracked-file 查询为证;应补 CI workflow 或等价强制 gate。

### Test #5 OpenAPI 路由一致性大面通过,但细节契约还缺
- Owner cite: cmd/gateway OpenAPI consistency test。
- 实际代码(current HEAD): consistency test 只比较 spec paths 与 mounted chi paths,并要求 spec-only/impl-only 为空(`backend/cmd/gateway/openapi_consistency_test.go:45-98`);本次实测 `GOCACHE=/tmp/huakai-go-cache go test ./internal/openapicheck ./cmd/gateway` exit 0。OpenAPI 200 headers 目前只声明 `X-HUAKAI-Protocol-Loss` 与 `X-HUAKAI-Idempotency-Hit`,未声明 audit ledger / DLQ / trailer 细节(`docs/openapi/openapi.yaml:120-132`,`docs/openapi/openapi.yaml:168-174`,`docs/openapi/openapi.yaml:206-212`)。
- 现状判定: confirmed
- W5 scope? no
- 当前波次归属: API contract / OpenAPI detail hardening。
- 评论: route coverage 通过不等于响应 header/trailer/error-code 契约完整。

## 4. 总体结论
- 22 条逐条 verdict: confirmed 18 / refined 3 / refuted 0 / partial 1。
- W5 范围新增 finding: Bug #1 非流式 DLQ ref 透传。Bug #2-#6 均已落在 W5 主题或 W5 plan 中;Bug #1 是 W4 修流式后剩下的非流式契约缺口,应加进 W5 synthesis 或紧邻 W5 的 trust-ledger contract patch。
- Owner 行号错位/需校准条目: Bug #9(真实证据跨 handler decode + OpenAPI schema),Feature #2(placeholder 证据不在 line 91),Test #1(skip 证据不在 line 1),Test #3/Test #4(Owner 未给源码 cite,只能用命令/仓库缺失验证)。
- 已知但延后的 finding: Bug #12 属 W7 routing/cooldown;Bug #10/#11 属 W11/W12 Rust 预生产硬化;Feature #3 属 W9 proto;Feature #2 属 W10 protocol registration。
- 跨波需要单独立项: Bug #7/#8 Invitation schema isolation/FK integrity;Bug #9 OpenAPI strict decoding 与 handler 行为不一致;Feature #1/#4/#5 分别是 product capability/API surface/large-payload policy,不应在 W5 静默吸收。
- 验证命令: `cd backend && GOCACHE=/tmp/huakai-go-cache go test ./...` exit 0;`go test ./internal/openapicheck ./cmd/gateway` exit 0;`go list ./... | wc -l` 输出 82。

## 5. Clean-room 声明
- Lane: verifier
- 只读 HUAKAI 内部代码与文档;无外部参考源码读取。
- Source files read: `CLAUDE.md`;`docs/process/research/2026-05-23-owner-cloud-review.md`;`docs/process/plans/2026-05-22-audit-remediation-wave.md`;`docs/process/plans/2026-05-23-w5-audit-atomicity-codex.md`;`docs/process/plans/2026-05-23-w5-audit-atomicity-claude.md`;`docs/03_FEATURE_PARITY_MATRIX.md`;`docs/11_ACCEPTANCE_TEST_MATRIX.md`;`docs/openapi/openapi.yaml`;`docs/10_RISK_REGISTER.md`;`backend/Makefile`;`backend/go.mod`;`backend/cmd/gateway/openapi_consistency_test.go`;`backend/internal/gatewayhttp/chat_completions_handler_headers.go`;`backend/internal/gatewayhttp/chat_completions_stream.go`;`backend/internal/gatewayhttp/chat_completions_validate.go`;`backend/internal/gatewayhttp/invitation_handler.go`;`backend/internal/gatewayhttp/cost_receipt_handler.go`;`backend/internal/gatewayhttp/admin_pools_handler.go`;`backend/internal/gatewayhttp/admin_pool_accounts_handler.go`;`backend/internal/credentialstore/postgres_store.go`;`backend/internal/auth/antigravity_token_provider.go`;`backend/internal/channelhealth/store_postgres.go`;`backend/internal/rate/rate.go`;`backend/internal/audit/refund_worker_tx_integration_test.go`;`backend/internal/auditledger/postgres_test.go`;`backend/internal/provider/registrydefault/default.go`;`backend/internal/proto/openai_responses_response.go`;`backend/internal/proto/openai_responses_stream.go`;`backend/sql/migrations/0034_community_invitation_referral.up.sql`;`exploratory/rust-core-gateway/merged/Cargo.toml`;`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs`;`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/mod.rs`。
- Agent: GPT-5 Codex
- UTC timestamp: 2026-05-23T11:11:31Z

中文总结: 本轮对 Owner cloud review 22 条逐条 source-verify,18 条 confirmed、3 条 refined、1 条 partial、0 条 refuted;最高优先级是把新增的非流式 DLQ ref 透传纳入 W5 synthesis,同时保持既有 W5 的 credentialstore/auth/admin/channelhealth audit 原子化不缩水;本次只读 HUAKAI 内部代码和文档,无外部参考源码读取,clean-room 风险低;需要 Owner 另行确认的是 invitation schema 修复、OpenAPI strict decoding 策略、Rust W11/W12 是否提前、以及 CI workflow/gate 的落地节奏。
