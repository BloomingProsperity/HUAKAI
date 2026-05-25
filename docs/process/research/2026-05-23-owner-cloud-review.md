---
source: "Owner chat 2026-05-23"
base: "claude/phase-1 @ 6e76d59"
scope: "W5+ ground truth review"
lane: "owner-evidence"
---

我又按"最新云端代码"抓了一轮,结论是:大功能推进很快,但细节 bug 和功能遗失主要集中在 **审计原子性、协议契约、Rust 网关、模型能力范围、测试/发布流程** 这几块。

**当前基线**

远端最新是 claude/phase-1 @ 6e76d59, 但服务器上还有两个未提交文档:
/home/codex/HUAKAI/docs/process/plans/2026-05-23-ref-prestudy-codex.md
/home/codex/HUAKAI/docs/process/plans/2026-05-23-w5-audit-atomicity-claude.md
这意味着:GitHub 上的人看不到这部分 W5 计划和问题清单。

**细节 Bug**

1. 非流式请求的审计 DLQ 结果没有透给客户端
   流式路径会写 X-HUAKAI-Ledger-DLQ-Ref, 但非流式只在 Persisted 时写审计头, Deferred 直接沉默。
   位置: backend/internal/gatewayhttp/chat_completions_handler_headers.go:55
   影响: 账务可以进入 DLQ, 但用户侧不知道, 透明账单链断一截。

2. CredentialStore 审计大量 _ = InsertAuditEvent, 失败被吞
   创建、轮换、删除、刷新等操作里, 业务状态可能已经改了, 但审计失败不会阻止。
   位置: backend/internal/credentialstore/postgres_store.go:229
   影响: 凭证变更不是强审计, 和"信任链"目标冲突。

3. CredentialStore SetState 审计语义不准确
   SetState 接受任意状态, 但审计事件固定成 credential_disabled。
   位置: backend/internal/credentialstore/postgres_store.go:398
   影响: 恢复、撤销、禁用会混成同一种事件, 后续审计追责会乱。

4. Antigravity token/refresh 审计失败被忽略
   多处 _ = p.writeAudit(...)。
   位置: backend/internal/auth/antigravity_token_provider.go:145
   影响: token 刷新、轮换、失败记录可能没有可靠审计。

5. Admin pool / provider account 变更和审计不是同事务
   先改库, 再写审计。审计失败时, 业务变更已经提交。
   位置: backend/internal/gatewayhttp/admin_pools_handler.go:162 / :173 / backend/internal/gatewayhttp/admin_pool_accounts_handler.go:759
   影响: 管理面板可能返回失败, 但池子/账号已经变了。

6. Channel health 审计 signer 为空时静默成功
   signer nil 直接 return nil。
   位置: backend/internal/channelhealth/store_postgres.go:295
   影响: 如果生产误配, 通道健康变更会没有签名审计。

7. Invitation schema 还有隔离问题
   code 仍是全局唯一, 不是 tenant 内唯一; referee_user_id 也是全局唯一。
   位置: backend/sql/migrations/0034_community_invitation_referral.up.sql:7
   影响: 多租户之间可能互相撞码、撞用户约束。

8. Invitation 外键仍不完整
   invitation/referral/reward 有部分 FK, 但 tenant_id、inviter_user_id、referrer_user_id、referee_user_id 没完整绑 tenants/users。
   位置: backend/sql/migrations/0034_community_invitation_referral.up.sql:34
   影响: 删租户、删用户后可能留下孤儿邀请/返佣数据。

9. OpenAPI 声明严格, handler 实际不严格
   很多 schema 是 additionalProperties: false, 但 handler 没 DisallowUnknownFields()。
   位置: backend/internal/gatewayhttp/invitation_handler.go:81 / backend/internal/gatewayhttp/cost_receipt_handler.go:148
   影响: 客户端按 spec 会拒绝的字段, 服务端却接受, 后面扩字段容易出兼容事故。

10. Rust 网关 Windows 下直接编不过部分路径
    tokio::net::UnixStream 没有完整平台隔离。
    位置: exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:682
    影响: 开发/CI 如果跑 Windows 会失败; 跨平台状态不健康。

11. Rust TLS feature 仍然强互斥
    mimicry-boring 和 mimicry-openssl 同时开会 compile_error!。
    位置: exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/mod.rs:12
    影响: 还没有真正解决 BoringSSL/OpenSSL fallback 架构问题。

12. Rate limit 细分识别还没完整实现
    仍有 TODO:多平台 429 reset 提取。
    位置: backend/internal/rate/rate.go:75
    影响: 想做到 sub2api 那种细致冷却管理, 目前还没完全对齐。

**功能遗失 / 没完全对齐**

1. 模型接入还不是"全功能模型平台"
   当前主要是 chat/responses/messages 三条主协议。OpenAPI 没看到 /v1/embeddings、/v1/images、/v1/audio、/v1/rerank、/v1/realtime 这些正式路由。
   位置: docs/openapi/openapi.yaml:89

2. Provider 注册很多, 但部分是 placeholder/session-reverse, 不等于完整生产支持
   注册表里有 OpenAI、Anthropic、Gemini、OpenRouter、Bedrock、Grok、DeepSeek、Mistral、Groq、Together、Perplexity、Fireworks, 以及 Cursor/Copilot/Gemini Advanced/Antigravity/Kiro/Windsurf session 类。
   位置: backend/internal/provider/registrydefault/default.go:91
   问题: 反向 session 类更像高级实验能力, 不是标准 API provider 的稳定接入。

3. OpenAI Responses 能力还存在协议损耗
   reasoning/image/tool/input_json/thinking 等事件仍有 pending/loss 注释。
   位置: backend/internal/proto/openai_responses_response.go:73 / backend/internal/proto/openai_responses_stream.go:118
   影响: 不能宣传为 Responses 完整兼容。

4. 多模态、实时、MCP、A2A 还是规划项
   Feature matrix 里这些还是 Phase 9/未来项。
   位置: docs/03_FEATURE_PARITY_MATRIX.md:57

5. 大请求能力偏保守
   Chat body limit 是 1MiB。
   位置: backend/internal/gatewayhttp/chat_completions_validate.go:78
   影响: 长上下文、大工具参数、图文输入、批量请求可能很快撞限制。

**测试与工程缺口**

1. 普通 go test ./... 通过, 不代表完整业务没 bug
   因为很多关键 Postgres/事务测试依赖环境变量或 build tag, 默认会跳过。
   位置: backend/internal/audit/refund_worker_tx_integration_test.go:1

2. Acceptance matrix 仍大量 Planned
   网关、认证、Key 管理、通道健康、超时、池管理等 L1 验收项还没闭环更新。
   位置: docs/11_ACCEPTANCE_TEST_MATRIX.md:15

3. 远端服务器不是可验证环境
   我连上去看了, 远端没有 go, 也没有 cargo。所以服务器能存代码, 但不能证明最新代码在服务器上能完整构建/测试。

4. 仓库没看到 GitHub Actions / CI 工作流
   当前项目这么大, 如果没有 CI 强制跑 Go、OpenAPI、一部分 integration、Rust 构建, 靠人工/AI 很容易漏。

5. OpenAPI 路由一致性大面通过, 但细节契约还缺
   cmd/gateway 的 OpenAPI consistency 测试是通过的, 但审计头、DLQ 头、具体错误码、stream trailer 这些小契约还没有完整写进 spec。

**优先级**

第一优先:审计原子性。CredentialStore、Antigravity、Admin Pool、ChannelHealth 这几个要先改成"业务变更和审计同事务/失败回滚/生产 fail-closed"。

第二优先:协议契约。把审计 header、DLQ ref、错误码、stream trailer、unknown fields 行为统一到 OpenAPI 和 handler。

第三优先:功能边界。明确哪些 provider 是生产支持, 哪些是 placeholder; 哪些模型能力已支持, 哪些只是路线图。

第四优先:Rust 网关。先别继续堆功能, 先解决平台编译、TLS 架构、依赖隔离、CI 构建, 否则维护风险会继续放大。
