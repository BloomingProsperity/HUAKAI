# 2026-06-24 backend quality renew round157 credentialworker health store deadcode

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 删除 `backend/internal/credentialworker/health_state.go` 中已核实无引用的 `providerAccountHealthDBStore` 类型及其方法，并清理 `backend/scripts/staticcheck-baseline.txt` 对应两条 U1000；保留 `providerAccountHealthStore` 接口与 `updateProviderAccountHealth` SQL 写入函数。 |
| Success criteria | `providerAccountHealthDBStore` 不再存在；`updateProviderAccountHealth` 仍被审计路径和测试使用；baseline 不再包含这两条 U1000；`rg` 无死 wrapper 残留。 |
| Time estimate | 约 10 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 低到中：文件属于 credentialworker，但删除对象是未引用 wrapper，不改变健康状态迁移 SQL、scheduler audit flow、alert delivery 或测试 spy 注入。 |
| Failure modes | 若 wrapper 被 build tag 或外部生成调用可能编译失败；当前 `rg` 在仓内未发现引用，修改后继续核验。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若需要改健康状态 SQL、刷新 outcome 分类或 worker 事务路径，则停止并请求 Owner 确认；本轮不做。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已打开真实代码；3. 已用 `rg` 核实 wrapper 只在定义与 baseline 出现；4. 保留实际 SQL helper；5. 清理两条 baseline。 |

## 执行顺序

1. 删除 `providerAccountHealthDBStore` 类型和 `UpdateProviderAccountHealth` 方法。
2. 删除对应 staticcheck baseline 条目。
3. 用 `rg`、`git diff --check`、clean-room 词扫描核验。
4. 尝试 `gofmt` 与 scoped `go test`，如工具链缺失则如实记录。
