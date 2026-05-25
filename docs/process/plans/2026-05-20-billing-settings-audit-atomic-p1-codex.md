# 2026-05-20 billing settings audit atomic P1

| Owner directive | "修复 codex review 在 case C 管理 API(Phase 1C)发现的 P1(更新与审计非原子)" |
| Scope | In: HUAKAI Go backend 内部代码, billing setting PUT 的事务化更新+审计, `FOR UPDATE` 查询, wiring, focused tests. Out: 参考项目源码, git 操作, `admin_pool_accounts_handler.go`, 计费热路径, `billing/state.go`, 0046/0047 迁移. |
| Success criteria | PUT 的旧值读取、`billing_settings` upsert、`admin_audit_events` insert 在同一 pgx 事务内; 任一步失败整体回滚; 同一 `(tenant_id, setting_key)` 已有行用 `FOR UPDATE` 串行化; 现有 HTTP 行为不变; build 和指定 test 命令 0 FAIL. |
| Time estimate | 约 60-90 分钟墙钟; 1 个 Codex 实现/验证回合. |
| Blast radius | 管理端 `/admin/v1/billing/settings` PUT 和启动 wiring; sqlc billing 查询接口新增方法; 单元测试 fake 需要跟随接口变更. |
| Failure modes | 事务入口依赖没注入导致生产 503: wiring 注入 `pgxpool.Pool`; sqlc 生成不可用: 同步维护 SQL 查询文件和生成代码; fake 测试无法证明回滚: 用事务 runner fake 模拟 commit/rollback 语义和 audit failure; HTTP 错误码漂移: 保留原错误映射. |
| Decision points | 无高风险 Owner sign-off 点: 不改 schema/迁移/认证/热路径/真实密钥/支付账本. 若必须新增运行时依赖或触碰迁移则停止. |
| Pre-execution checklist | 1. 读 `docs/RULES.md` 与现有 handler/wiring/sqlc/test 形态; 2. 不执行任何 git 命令; 3. 不读参考项目源码; 4. 编辑前确认只触碰内部代码; 5. 运行指定 build/test 并报告真实结果. |

## Concrete Execution Order

1. 在 `sql/queries/billing_settings.sql` 增加 `GetBillingSettingForUpdate`，同步 `internal/db/billing` 生成代码与接口。
2. 在 `internal/billing` 增加事务化服务入口，使用 `pgxpool.Pool.BeginTx`，在同一 tx 内构造 `dbbilling.New(tx)` 和 `admindb.New(tx)`，按 `FOR UPDATE -> upsert -> audit -> commit` 执行。
3. 调整 `internal/gatewayhttp/admin_billing_settings_handler.go`，handler 校验后调用事务服务，保留响应和错误码。
4. 调整 `cmd/gateway` deps/wiring/routes，把 pgx pool 注入 billing settings deps。
5. 更新/新增 focused tests 覆盖成功写入审计、audit failure 回滚、非法值拒绝。
6. 运行 gofmt、build、指定 tests，记录真实结果。
