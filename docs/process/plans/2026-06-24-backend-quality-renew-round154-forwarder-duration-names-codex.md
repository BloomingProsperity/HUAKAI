# 2026-06-24 backend quality renew round154 forwarder duration names

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 处理 `backend/internal/gateway/forwarder_types.go` 中两个 `time.Duration` 字段的 ST1011 命名债，以及当前仓内对这些 Go 字段名的引用；保持 JSON tag `drain_max_seconds` / `max_seconds` 不变，不调整超时语义。 |
| Success criteria | `DrainMaxSeconds` 重命名为 `DrainMaxDuration`，`MaxSeconds` 重命名为 `MaxDuration`；所有 Go 引用同步；`backend/scripts/staticcheck-baseline.txt` 中对应两条 ST1011 清除；`rg` 无旧字段残留。 |
| Time estimate | 约 15 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 中：跨 `gateway`、`gatewayhttp` 测试与 `cmd/gateway` wiring 的字段名同步；运行时语义、JSON tag、默认值与环境变量名不变。 |
| Failure modes | 漏改引用会导致编译失败；用 `rg` 全仓核旧字段名残留。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若发现该字段作为外部 API 直接暴露给非 internal 调用方，本轮停止；当前只在仓内 Go 代码中引用。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已打开真实代码核实 ST1011；3. 已用 `rg` 列出引用；4. 保持 JSON tag 与 env wiring 不变；5. 清理对应 baseline。 |

## 执行顺序

1. 更新 `TimeoutConfig` 与 `DrainBudgets` 字段名。
2. 同步 `forwarder.go`、测试和 `cmd/gateway/middleware.go` 中的字段引用。
3. 删除 `staticcheck-baseline.txt` 中两条 ST1011。
4. 运行 `rg`、`git diff --check`、clean-room 词扫描，并尝试 `gofmt/go test`。
