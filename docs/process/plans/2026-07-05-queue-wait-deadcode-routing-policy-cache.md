# 2026-07-05 queue_wait deadcode 与 routing policy 缓存收尾
| Owner directive | "queue_wait 实现批两处收尾修复(deadcode 门 + 热路径查库)" |
| Scope | 范围内: 删除 `queuewait.Executor` 测试专用 exported option,改同包测试直接注入私有字段;为 `cmd/gateway` 的 routing policy fallback 配置增加短 TTL 内存缓存和判别测试。范围外: SQL/sqlc/schema、等待/abort/retry 语义、git commit/push。 |
| Success criteria | 指定门禁通过; `deadcode` 不再报告测试专用 option;同 tenant/pool_group 连续 `GetRoutingPolicy` 只触发一次 `GetPool`,TTL 过期和不同 pool_group 会重新查。 |
| Time estimate | 约 45-90 分钟墙钟时间;主要耗时在 quality gate 与 e2e_concurrency。 |
| Blast radius | 影响 queue_wait 构造 API 与 gateway 选号策略读取路径;若缓存实现错误,可能导致 fallback wait 配置短时间不刷新或并发 race。 |
| Failure modes | 删除 option 后生产调用点未同步导致编译失败;缓存 key 漏 tenant 导致跨租户污染;缓存锁粒度错误导致 race;测试 stub 与 `*dbbilling.Queries` 耦合过强导致无法计数。缓解:先搜索调用点,引入最小接口,用 fake now 和 `-race` 判别。 |
| Decision points | 若发现需要改 SQL/sqlc/schema、认证/计费/额度核心或新增运行时依赖,停止请求 Owner 确认。本计划不包含这些高风险动作。 |
| Pre-execution checklist | 1. 读取 `executor.go`/`executor_test.go` 与 `routing_policy_source.go`/现有测试;2. 搜索 `NewExecutor` 和 `newBindingRoutingPolicySource` 调用;3. 小补丁实现;4. `gofmt`;5. 按 Owner 指定门禁验证;6. 输出中文总结与风险。 |

## 执行顺序
1. 删除 `Option` 与 `WithNow`/`WithSleeper`/`WithTracker`/`WithJitter`,保留 `NewExecutor` 内部默认值和 nil 兜底。
2. 将 `executor_test.go` 改为 `e := NewExecutor()` 后直接设置私有字段注入 fake。
3. 将 `bindingRoutingPolicySource` 的查询依赖收窄为本文件内接口,增加 `{tenant_id,pool_group_id}` TTL 缓存和可注入时钟。
4. 增加缓存命中、TTL 过期、不同 pool_group、并发判别测试。
5. 运行指定门禁,若失败只做与本任务直接相关的修复。
