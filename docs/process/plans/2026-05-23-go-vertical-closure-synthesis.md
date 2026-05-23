# 2026-05-23 Go 后端「树向闭环补齐」synthesis

输入: Owner 2026-05-23 12:56 指令 + 第三方 AI 状态树 + Owner 12:58 收紧版 + Owner 13:02 「前端先不碰」+ Owner 13:05 「Rust 别人在跑只管 Go」

参与 lane:
- Claude lane = routes 全扫 + 模块文件清单 (本对话)
- Codex lane = 独立 evaluator-go-only,bajnq4rkd,Source files read 64 个 HUAKAI Go + 12 个 ~/refs

## §0 Owner 累积硬约束 (任何违反 = 失败)

1. **不横向扩展** — 只在已有模块内补未闭环叶子节点
2. **不新增一级方向** — embeddings / images / audio / rerank / realtime / assistants / vector store / batch / 完整支付系统 / 订阅套餐 / 多语言 / Passkey / 2FA / 移动端 / L4-L6 反封禁
3. **不碰前端** — frontend/ 整目录跳过 (后续切片再决)
4. **不管 Rust** — exploratory/rust-core-gateway 别人在跑,不评不动
5. **参考项目只作检查清单不作扩展清单** — 任何 ref claim cite ~/refs/<project>/<file>:<line>,训练记忆不算证据 (CLAUDE.md #12)
6. **8 字段标注** 每缺失项: 模块 / 当前能力引证 / 缺哪叶子 / 为什么必须 / 不补怎样 / 是否横向扩展 / P0-P1-P2 / 测试用例

## §1 Lane agreement (两 lane 共识)

两 lane 独立得出的相同结论:

| 项 | Claude lane | Codex lane | Status |
|---|---|---|---|
| 用户自助 API Key endpoint 缺 | ✓ 发现 (routes 全扫无 /v1/api-keys/*) | ✓ 强调 #2 第一个最短路径补叶子 | **共识 P0** |
| W5 C3 admin pool audit 同事务 + 非流式 DLQ ref 透传 | ✓ 既定计划 | ✓ 隐式 (channelhealth ✓ 已落) | **共识 P0** (W5 续) |
| RR-W5-002 antigravity refresh 真接入点 credentialworker.Scheduler | ✓ 已写入 risk register | ⚠️ codex 未单独提 (被 receipt P0 盖) | **Claude 单独 P0** (W5 RR 续) |
| DLQ legacy vs outbox 双面要分清 | ⚠️ 没意识到双面差异 | ✓ 强调 #3 重要 | **codex 单独 P1** |

## §2 Lane disagreement (差异)

| 项 | Claude lane | Codex lane | 决策建议 |
|---|---|---|---|
| **Receipt 租户内 owner 隔离** | ❌ 没发现 (我只 tenant 视角) | ✅ **唯一阻塞当前 Go 模块安全运行的 P0** | **采纳 codex**,已 source-verified (见 §3.1) |
| 用户用量 endpoint 优先级 | P1 | 未单独提 (盖在用户自助 key 下) | P1,同 codex |

## §3 Source-verified P0 清单 (≤ 5 条)

### §3.1 [P0-1] Receipt 租户内 owner (user) 隔离缺失

- **模块**: audit / receipt
- **当前能力**: 
  - `SessionIdentity` 含 `UserID int64` ([backend/internal/auth/session_middleware.go:15-21](backend/internal/auth/session_middleware.go#L15-L21))
  - `cost_receipt_handler.go` 拿到 session ident 后只传 `ident.TenantID` 给 GetReceipt ([backend/internal/gatewayhttp/cost_receipt_handler.go:107](backend/internal/gatewayhttp/cost_receipt_handler.go#L107))
  - 所有 receipt SQL queries 只 `WHERE request_id = $1 AND tenant_id = $2` ([backend/internal/audit/receipt_storage_pgx.go:114,146,222,251,279](backend/internal/audit/receipt_storage_pgx.go))
- **缺叶子**: receipt 表加 `user_id`/`owner_user_id` 列 + GetReceipt 接口加 userID 参数 + handler 双重 owner 校验
- **为什么必须**: 信任链差异化 ([[project_core_trust_chain_differentiator]]) 要求"用户消费透明,商家不能做假",但当前 session 校验只到租户级。同租户多用户场景下,user A 用 session cookie 可以查/verify user B 的 receipt(只要拿到 request_id)= 用户级信任链泄漏
- **不补会**: 多租户内部 user-vs-user 视为同一信任域,违差异化承诺;企业租户多员工时一员工能看其他员工的请求成本
- **是否横扩**: 否 (现有 receipt 表 + 现有 handler 内加字段)
- **P0**: 阻塞 Go audit/receipt 模块对外承诺信任链的正确性
- **测试用例**: `backend/internal/gatewayhttp/cost_receipt_handler_owner_isolation_test.go` — 同 tenant 不同 user 的 receipt,user A session 查 user B receipt → 404;verify 同 → 404;mutation 自检:去掉 owner 校验 → 测试红

### §3.2 [P0-2] 用户自助 API Key endpoint 缺失

- **模块**: auth / api key
- **当前能力**: admin 有 issuer/revoker ([backend/internal/admin/issuer.go](backend/internal/admin/issuer.go), [backend/internal/admin/revoker.go](backend/internal/admin/revoker.go)) 通过 `/admin/v1/api-keys/*` 给管理员用 ([backend/cmd/gateway/routes.go:126-133](backend/cmd/gateway/routes.go#L126-L133));用户 inbound key 解析路径已有 ([backend/internal/auth/api_key_resolver.go](backend/internal/auth/api_key_resolver.go) + [backend/internal/db/auth/auth_inbound.sql.go](backend/internal/db/auth/auth_inbound.sql.go))
- **缺叶子**: `/v1/api-keys` user-facing CRUD endpoint (POST 创建 / GET 列自己的 / DELETE revoke 自己的)
- **为什么必须**: 用户拿不到 inbound API key 用 chat-completions(只有 cookie session,不适合 SDK/curl/集成场景)
- **不补会**: 第三方 SDK / curl / 本地客户端集成断;HUAKAI 中转账号→API 核心定位 ([[reference_relay_core_path]]) 不闭环
- **是否横扩**: 否 (auth_inbound 表 + admin issuer 模式已有,user-facing endpoint 复用 binding 模块 [backend/internal/binding/binding.go:1](backend/internal/binding/binding.go#L1) 已注释 "U1-A atomic")
- **P0**: 模块「用户级 API key 闭环」是 sub2api/new-api 既有,HUAKAI binding 模块 U1-A 已铺路,缺最后一公里
- **测试用例**: `backend/internal/gatewayhttp/user_api_keys_handler_test.go` — 用户创建 key (返回明文一次,后续只能拿 fingerprint) / 用户只能列自己的 / 不能 revoke 别人的 / mutation 自检:revoke 接口去掉 owner 校验 → 测试红

### §3.3 [P0-3] W5 C3 admin pool 同事务 + 非流式 DLQ ref 透传

- **模块**: gatewayhttp admin pool + chat completions
- **当前能力**: W5 synthesis [docs/process/plans/2026-05-23-w5-audit-atomicity-synthesis.md:35-65](docs/process/plans/2026-05-23-w5-audit-atomicity-synthesis.md) 已定;Owner Bug #1 已 verified [docs/process/research/2026-05-23-owner-cloud-review-verification.md:182-188](docs/process/research/2026-05-23-owner-cloud-review-verification.md#L182-L188)
- **缺叶子**: (1) admin pool/account CRUD audit insert 包 BeginTx;(2) 非流式 chat 在 `LedgerResult.State=Deferred` 时 X-HUAKAI-Ledger-DLQ-Ref header 透传
- **不补会**: admin 渠道改动静默丢审计 (违信任链);非流式客户端拿不到 DLQ ref 无法跟进复理
- **P0**: W5 既定计划,与新方向 P0 平级
- **测试用例**: W5 synthesis §6.3 T13/T14/T15/T16 (已在 synthesis 列)

### §3.4 [P0-4] RR-W5-002 antigravity refresh 真接入点接通

- **模块**: credentialworker / auth
- **当前能力**: `credentialworker.Scheduler.recordAudit` ([backend/internal/credentialworker/audit.go:15-44](backend/internal/credentialworker/audit.go#L15-L44)) audit insert + ledger Append 独立 `errors.Join` 非同事务;`dbAuditWriter` nil queries silent return nil 不 fail-closed
- **缺叶子**: `recordAudit` BeginTx-wrapped + production queries nil 报 ErrAuditWriterMissing + cmd/gateway credentialScheduler 装配点 production-required gate
- **不补会**: 生产 audit DB 抖动时 antigravity token 仍轮换 + cache 仍填 + ledger 缺条目,W5 D1/D4 落到错位置,信任链可断
- **P0**: 已在 [docs/10_RISK_REGISTER.md RR-W5-002](docs/10_RISK_REGISTER.md) 锁定 HIGH;原 W5 C2 误改 dead code skeleton,真接入点未碰
- **测试用例**: `backend/internal/credentialworker/audit_tx_required_test.go` — audit insert 失败 / ledger append 失败 各一条 fail-closed 判别;mutation 自检:移除 tx wrapper → 红

## §4 P1 清单 (≤ 6 条)

### §4.1 [P1-1] Refund DLQ 按 claim/request 状态查询 + 关联

- **模块**: audit / refund / dlq
- **当前**: refund_worker 处理失败时进 dlq,但 admin DLQ list 是泛化 ([backend/internal/gatewayhttp/admin_dlq_handler.go](backend/internal/gatewayhttp/admin_dlq_handler.go))
- **缺叶子**: 按 claim_id / request_id 查 refund 状态 endpoint
- **测试**: `backend/internal/gatewayhttp/admin_refund_dlq_handler_test.go` (codex P1-5 已列)

### §4.2 [P1-2] outbox dead 事件恢复入口

- **模块**: observability / dlq
- **当前**: outbox worker 注册 email retry + channel alert ([backend/cmd/gateway/middleware.go:110-121](backend/cmd/gateway/middleware.go#L110-L121));dead 事件写 dlq_events ([backend/internal/obs/dlq/store_postgres.go:145-181](backend/internal/obs/dlq/store_postgres.go#L145-L181));admin DLQ 指向 legacy ([backend/cmd/gateway/routes.go:206-208](backend/cmd/gateway/routes.go#L206-L208))
- **缺叶子**: outbox dead 事件 list/replay endpoint,或桥接到现有 admin DLQ — codex 强调 DLQ 两条面要分清
- **测试**: `backend/internal/gatewayhttp/admin_outbox_dlq_handler_test.go` (codex P1-6)

### §4.3 [P1-3] 用户自助用量 endpoint

- **模块**: billing / usage
- **当前**: `/admin/v1/usage` 只 admin ([backend/cmd/gateway/routes.go:203](backend/cmd/gateway/routes.go#L203))
- **缺叶子**: `/v1/usage` user-facing (owner 隔离自动用 P0-1 同模式)
- **测试**: `backend/internal/gatewayhttp/user_usage_handler_test.go` — owner 隔离 + 时间范围 + 模型筛选

### §4.4 [P1-4] 用户自助 billing/claims 视图 + audit-events 视图

- **模块**: billing + audit
- **当前**: `/admin/v1/billing/claims` 只 admin;`/admin/v1/audit-events` 只 admin
- **缺叶子**: `/v1/billing/claims` `/v1/audit-events` user-facing + owner 隔离

### §4.5 [P1-5] Channel health 当前凭证解析 (codex P1 第三个最短路径补叶子)

- **模块**: pool / channel health
- **缺叶子**: 当前 channel-health endpoint 只读状态码,没接通凭据当前是哪个版本 / 哪个 store 的解析逻辑
- **测试**: `backend/internal/gatewayhttp/channel_health_credential_resolution_test.go`

## §5 P2 暂缓清单 (≤ 4 条)

### §5.1 [P2-1] reconciliation handler 可见产物 (codex)
- handler 当前只 record/compare 内存,重启即丢

### §5.2 [P2-2] account health probe handler 空探针 (codex)
- nil probe handler 直接 no-op,健康闭环只靠错误路径

### §5.3 [P2-3] 用户自助 receipt 列表 (codex 强调 #2 第二个)
- 当前 `/v1/cost-receipts/{request_id}` 只单条查,没 list

### §5.4 [P2-4] Refund / dispute user-facing trigger
- refund_worker 内部有,无 user/admin 触发 endpoint

## §6 明确禁止清单 (Owner 黑名单 + 新发现)

### Owner 黑名单 (绝对禁)
- /v1/embeddings, /v1/images/*, /v1/audio/*, /v1/rerank, /v1/realtime, /v1/assistants, /v1/vector-stores, /v1/batches
- 完整支付集成 (Stripe / Alipay / WeChat / EasyPay)
- 订阅套餐
- 多语言, Passkey, 2FA
- 移动端 / mobile-friendly
- L4-L6 反封禁
- 前端 / UI / Console 任何工作 (frontend/ 整目录)
- Rust 工作 (exploratory/rust-core-gateway/)

### 新发现禁止 (基于源码审查)
- 任何对 frozen package (gatewayhttp / gateway / proto) 加新文件 ([[feedback_rust_clear_structure]] / CLAUDE.md #13)
- 在 dead skeleton (auth.AntigravityTokenProvider) 上继续投资 ([RR-W5-002](docs/10_RISK_REGISTER.md))
- 任何 schema 变更没 Owner 确认 (Risk-Based Confirmation Rule)

## §7 决策点 (Owner 必决)

### D-A 顺序: P0 4 条 哪个先做?

选项:
- **A**: 先 P0-1 (receipt owner 隔离) — 信任链安全洞,可独立闭合
- **B**: 先 P0-3 (W5 C3 既定计划) + P0-4 (W5 C2 续 RR-W5-002) — 收尾 W5 完整
- **C**: 先 P0-2 (用户 API Key) — 产品定位闭环
- **D**: 并行 (但 ceremony 三档 + 单 commit 单模块原则建议串)

建议 = A (信任链是 HUAKAI 差异化承诺,owner 隔离漏洞在多租户场景下立即 exploitable) → B → D-A 后再 C。

### D-B P0-2 用户 API Key 含「用户面 UI」吗?

- Owner 已说前端不碰
- 只做 backend endpoint + curl/SDK 友好 = 可以闭环 API 层
- UI 留 frontend 后续切片

### D-C P1-2 outbox dead 事件恢复入口 — 桥接还是新 endpoint?

- 桥接到现有 `/admin/v1/dlq/{handler}` = 复用 admin 面但 handler routing 复杂
- 新 endpoint `/admin/v1/outbox-dlq/*` = 清晰但 codex 强调 "两条面要分清" 矛盾
- 建议: 新 endpoint,清晰分线 (符合 [[feedback_rust_clear_structure]] 按职责组织)

## §8 ceremony 档位 (按 [[feedback_ceremony_tiered]])

| P0 项 | 难度 | Ceremony |
|---|---|---|
| P0-1 receipt owner 隔离 | **高难度** (信任链 + schema 变更 + 跨租户安全) | plan parallel + prestudy ~/refs (sub2api/new-api receipt owner 实现) + synthesis + 双 verify + schema gate |
| P0-2 用户自助 API Key | **中难度** (既有 admin issuer 模式可镜像) | Claude 起 plan + Owner D 选项 (D-B) + 跳 plan parallel |
| P0-3 W5 C3 | **中难度** | W5 synthesis 已写 spec,直接派 codex 实施 |
| P0-4 RR-W5-002 | **中难度** | 已在 risk register,Claude 起 plan + 派 codex 接通 credentialworker |

## §9 Source files read

Claude lane (HUAKAI Go):
- backend/cmd/gateway/routes.go (全量)
- backend/internal/gatewayhttp/auth_handler.go, session_handler.go (子路由)
- backend/internal/auth/session_middleware.go (SessionIdentity 结构)
- backend/internal/audit/receipt_storage_pgx.go (SQL queries 检验)
- backend/internal/gatewayhttp/cost_receipt_handler.go (P0-1 验证)
- backend/internal/credentialworker/audit.go (RR-W5-002 真接入点)
- backend/internal/auth/, userauth/, usersession/, admin/ 目录列表
- backend/internal/binding/binding.go (U1-A binding 已注释)

Codex lane (HUAKAI Go) — 64 个文件 (见 bajnq4rkd output line 46)

Codex lane (~/refs) — 12 个文件 (见 bajnq4rkd output line 48):
- ~/refs/sub2api/backend/internal/handler/{api_key_handler.go, usage_handler.go}
- ~/refs/sub2api/backend/internal/service/{api_key_service.go, channel_monitor_service.go}
- ~/refs/one-api/controller/{token.go, log.go, channel.go}, monitor/channel.go
- ~/refs/new-api/controller/{token.go, log.go}, service/pre_consume_quota.go
- ~/refs/CLIProxyAPI/internal/access/config_access/provider.go

## §10 Lane attribution + UTC timestamp

- Claude lane = synthesizer, session 81fec8f5-b3e1-465a-95c3-26d6efee9c90
- Codex lane = evaluator-go-only, GPT-5 Codex, task bajnq4rkd, 2026-05-23T14:07:01Z
- Synthesis timestamp: 2026-05-23T14:15:00Z
