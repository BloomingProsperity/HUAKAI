# 2026-07-05 账号并发槽全链路端到端并发补测

| Owner directive | 「全链路端到端并发压测——账号并发槽打满触发排队/拒绝/释放(§17 必测并发)」 |
| Scope | 仅补测 HUAKAI 既有网关运行链路:dev mock 上游可选延迟、`cmd/gateway` 独立 build tag e2e 并发测试、测试侧断言与变异证红记录。不改 schema、不改生产槽获取/释放语义、不改账务 ledger 实现、不 commit/push。 |
| Success criteria | 延迟默认 0 且不影响现有 smoke；并发测试用单账号 `cap_concurrency=3` 同时压 8 个真实 HTTP 请求，证明账号在途峰值不超过 cap、超额按真实语义进入 `WaitPlan` 并返回 429/`queue_wait`、成功 claim committed、queue_wait claim aborted、余额 held 归零、`provider_accounts.in_flight_count` 归零、`pool_slot_acquisitions` 与 `quota_concurrency_slots` 无 acquired 残留。 |
| Time estimate | 约 1.5-2.5 小时墙钟；主要时间在 e2e 运行、PG 环境差异和变异证红。 |
| Blast radius | `cmd/gateway/dev_mock_upstream.go`、`cmd/gateway/*_test.go`、可能少量复用 smoke helper 的测试侧调整；不触碰高风险 schema/auth/billing ledger/quota enforcement 生产逻辑。 |
| Failure modes | mock 延迟误影响 smoke:延迟只从新 env 读取且默认 0；并发请求未真实重叠:在 mock doer 进入上游前记录峰值并 hold；测试被 quota per-key 并发提前挡住:测试不 seed 过低 per-key concurrency policy，重点只测账号槽；请求全部成功或全部失败:断言同时要求成功数等于 cap、拒绝数等于溢出数；清理残留污染后续测试:按租户清理所有相关表。 |
| Decision points | 若生产语义不是即时拒绝而是排队/换号，断言按源码语义调整；若发现满槽不干净拒绝、槽泄漏或账务/配额预留不回滚，停止掩盖并作为 finding 上报。执行中已确认单账号满槽会进入 selector fallback `WaitPlan`，HTTP 层返回 429/`queue_wait`。 |
| Pre-execution checklist | 1. 读 `CLAUDE.md` §14/§17 与 `AGENTS.md` 计划/测试质量规则；2. 读 `cmd/gateway/smoke_test.go`、`cmd/gateway/dev_mock_upstream.go`、`internal/pool/dispatcher/slot_manager.go`、`internal/pool/router/default_selector.go`、`internal/gatewayhttp/chat_completions_dispatch.go`、`internal/gatewayhttp/chat_completions_handler.go`、`internal/quota/service.go`；3. 写计划文档；4. 实现 mock 可选延迟与峰值记录；5. 新增 `e2e_concurrency` 测试；6. 运行目标门禁；7. 用临时变异 cp 备份验证断言转红并还原。 |
| REFERENCE PROJECTS IN SCOPE | CLIProxyAPI、sub2api、new-api。边界说明:本任务不设计新功能、不产出借鉴项目行为 claim，仅验证 HUAKAI 既有生产链路；因此本轮不读取非 MIT 参考源码，避免为一个内部回归测试引入不必要 clean-room 风险。 |

## 已读 HUAKAI 语义证据

- `internal/pool/dispatcher/slot_manager.go:68-85`:账号槽 acquire 在 Serializable 事务中先递增 `in_flight_count`；影响 0 行时返回 `ErrNoSlotAvailable`。
- `internal/pool/router/default_selector.go:217-220`:selector 遇到 `ErrNoSlotAvailable` 会继续其它候选。
- `internal/pool/router/default_selector.go:344-362`:候选存在但拿不到槽时,只要账号或策略配置了 fallback queue 参数,selector 返回 `WaitPlan`。
- `internal/gatewayhttp/chat_completions_dispatch.go:497-501`:HTTP 层看到 `WaitPlan` 后 abort 当前 claim,返回 429/`queue_wait` 并设置 Retry-After。
- `internal/gatewayhttp/chat_completions_handler.go:950-954`:无账号/无槽且未进入 `WaitPlan` 的错误路径映射为 HTTP 503 `no_capacity`，并 abort 当前 claim。
- `internal/gatewayhttp/chat_completions_dispatch.go:331-399`:请求先建立 billing claim，再建立 quota reservation，然后才进入 selector。
- `internal/quota/service.go:390-410`:quota concurrency 满时返回 deny；本补测会把 per-key concurrency policy 设得高于请求总量，避免抢走账号槽语义。

## 执行顺序

1. 给 dev mock doer 增加 `HUAKAI_DEV_MOCK_UPSTREAM_DELAY_MS`，默认 0；测试侧通过 PG 高频采样 `provider_accounts.in_flight_count` 记录账号在途峰值。
2. 新增 `cmd/gateway/account_slot_concurrency_e2e_test.go`，build tag `e2e_concurrency`；复用 smoke 的 build/start/seed helper，测试内把账号 cap 改为 3、in_flight 改为 0，并补足 quota/pricing/env。
3. 并发发 8 个真实 HTTP `POST /v1/chat/completions`，每个带唯一 `Idempotency-Key`。
4. 断言成功数为 3、`queue_wait` 数为 5、账号在途峰值为 3 且不超过 cap、成功/失败 claim 状态与 usage/事件/hold/quota 均正确、所有槽释放。
5. 运行指定门禁；若 baseline 通过,再临时变异破坏槽释放路径,确认测试转红后还原。若 baseline 已因真实账务/释放缺陷转红,停止变异并记录 finding。

## 执行后修正

- 实测语义修正:满槽不是 503/no_capacity,而是 fallback `WaitPlan` → 429/`queue_wait`。
- 测试实现修正:由于 mock 上游运行在 gateway 子进程内,测试进程不能直接读取其内存峰值;改为采样 `provider_accounts.in_flight_count` 作为账号槽峰值证据。
- 当前 finding:并发 `queue_wait` abort 与流式成功 settle 会触发 billing Tx2 失败,测试已在 HTTP 200 后观测到 claim 仍为 `reserving`;这违反本任务对 claim committed/aborted、余额/槽释放闭环的要求。 baseline 已红,因此未做额外变异。
