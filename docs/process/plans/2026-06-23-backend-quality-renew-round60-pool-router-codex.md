# 2026-06-23 backend quality renew round60 pool-router

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 `backend/internal/pool`、`backend/internal/pool/router`、`affinityrules`、`circuitbreaker` 与 `cmd/gateway/selector_wiring.go` 的真实接线路径；不修改生产代码，不触碰另一个 security-scan 目标。 |
| Success criteria | 输出中文 findings，所有结论落到真实 `file:line`；量化 pool/router 体量；核对 selector/gate/fail-open/slot/affinity/circuitbreaker 的复杂度与测试覆盖；运行可用检查或诚实记录工具缺失。 |
| Time estimate | 约 35-50 分钟人工等价审查；本轮 Codex 执行 1 个审查切片。 |
| Blast radius | 审查本身只新增计划文件；后续若按建议重构，影响账号选择、路由 gate、PASR 学习、限流前置检查与 provider fallback。 |
| Failure modes | 把已有 tests 当作运行证明但未核判别力；只看 `internal/pool` 忽略 `cmd/gateway/selector_wiring.go` 的真实接线；把纯安全跨租户结论展开过深。缓解：以源码、测试和接线调用点为证据，纯安全只留指针。 |
| Decision points | 若要实际拆分 `pool/router`、改 fail-open 策略或引入统一 routing-policy 包，需要 Owner 后续确认；本轮只报告。 |
| Pre-execution checklist | 1. 量化相关包文件数/行数；2. 读取 `default_selector.go`、`gates.go`、`router/pasr.go`、`prefix_segment.go`、`selector_wiring.go`；3. 搜索 fail-open、slot、claim、affinity、circuitbreaker、tests；4. 运行可用检查。 |

## Concrete Execution Order

1. 用 `wc -l`、`find` 量化 `internal/pool` 与相关子包。
2. 打开 selector/gate/PASR/prefix segment/dispatcher 的核心实现。
3. 核对 `cmd/gateway/selector_wiring.go` 怎样把 gate 链接入生产运行时。
4. 搜索 clean-room 注释、fail-open 注释、deadcode baseline 与测试断言。
5. 运行 `go test ./internal/pool/... ./cmd/gateway` 或记录工具缺失。
6. 按 S0/S1/S2/S3 输出本轮中文审查正文。
