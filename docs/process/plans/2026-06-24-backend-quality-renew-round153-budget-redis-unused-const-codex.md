# 2026-06-24 backend quality renew round153 budget redis unused const

| Owner directive | “做完了？ 这么快？ 这么大的项目你这么快？” |
| Scope | 仅处理 `backend/internal/budget/redis_store.go` 中已核实只在定义处出现的 `redisCounterTTLSeconds` 常量，以及对应 `backend/scripts/staticcheck-baseline.txt` U1000 条目；不改 Redis Lua 脚本语义、不改预算/配额运行行为。 |
| Success criteria | 删除孤立 unused const；baseline 中不再保留该 U1000 条目；`rg` 确认无残留引用或基线项。 |
| Time estimate | 约 10 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 低：删除未使用 Go 常量，不改变脚本文本中的 TTL、不改变 Redis key 过期策略。 |
| Failure modes | 若误改脚本文本会改变预算窗口 TTL；本轮禁止改 Lua 脚本文本，只删除 Go const 定义。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若需要调整 Redis TTL 策略或预算 fail-open/fail-closed 行为，停止并请求 Owner 确认；本轮不做。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已用 `rg` 证明常量只在定义与 baseline 出现；3. 不改 Lua 脚本；4. 清理单条 baseline；5. 运行可用静态检查。 |

## 执行顺序

1. 删除 `redisCounterTTLSeconds` 常量定义。
2. 删除 `backend/scripts/staticcheck-baseline.txt` 中对应 U1000 条目。
3. 用 `rg`、`git diff --check` 与 clean-room 词扫描核验。
4. 尝试 `gofmt` 与 scoped `go test`，如工具链缺失则如实记录。
