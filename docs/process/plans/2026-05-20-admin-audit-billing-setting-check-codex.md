# 2026-05-20 admin audit billing setting check

| Owner directive | "HUAKAI Go backend — 修复 codex review 在 case C 管理 API(Phase 1C)发现的 1 个 P1。" |
| Scope | 只修改 HUAKAI 内部迁移和对应测试 fake；不读参考项目源码；不执行 git 操作。 |
| Success criteria | 0047 迁移完整继承当前 admin_audit_events action / target_type 白名单并分别新增 `update_billing_settings` / `billing_setting`；测试 fake 按同一白名单拒绝未知值；指定 build 和 test 命令 0 FAIL。 |
| Time estimate | 约 20-30 分钟。 |
| Blast radius | 数据库 CHECK 约束迁移、Case C 管理 API 审计测试。 |
| Failure modes | 白名单遗漏旧值会导致既有审计写入失败；down 白名单错误会导致回滚语义不清；fake 白名单与迁移不一致会继续隐藏真实 DB 差异。 |
| Mitigation | 用 `rg` 核对 0010/0014/0016/0019 到 0046 的约束定义；0047 up/down 只改 CHECK 枚举；测试 fake 使用显式白名单并跑指定 build/test。 |
| Decision points | 已由 Owner 明确授权新增 0047 迁移；不触碰 handler action/target_type 字面值；不触碰高风险文件如 LICENSE、真实密钥、生产部署。 |
| Pre-execution checklist | 1. 确认最新 action 白名单来源；2. 确认最新 target_type 白名单来源；3. 新增 0047 up/down；4. 更新 fake audit store 校验；5. 运行指定 build/test。 |

执行顺序：

1. 读取 HUAKAI 内部迁移与测试文件，确认没有 0019 之后的 CHECK 变更。
2. 新增 `sql/migrations/0047_admin_audit_billing_setting_action.up.sql` 和 `.down.sql`。
3. 在 `internal/gatewayhttp/admin_billing_settings_handler_test.go` 的 fake audit store 中加入真实 CHECK 白名单校验。
4. 运行 Owner 指定的 build 和 test 命令并报告真实结果。
