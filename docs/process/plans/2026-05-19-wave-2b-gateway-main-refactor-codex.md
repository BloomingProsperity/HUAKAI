# 2026-05-19 Wave 2-B gateway main refactor

| Owner directive | "Wave 2-B 拆 cmd/gateway/main.go 大文件" |
| Scope | 仅拆分 `backend/cmd/gateway/main.go` 到同包职责文件；不改 frontend / Rust / vendor/boring / proto / pool；不改 `backend/internal/gatewayhttp/chat_completions_handler.go`；不读参考反代 source。 |
| Success criteria | `main.go` 保留入口和生命周期且不超过 100 行；新增/拆出 `wiring.go`、`middleware.go`、`routes.go`、`config.go`、可选 `lifecycle.go`；每个目标文件不超过 250 行；公开 env 和路由行为不变；指定 build/test 通过。 |
| Time estimate | 约 45-75 分钟；主要时间在机械拆分、import 收敛、Go 编译修复。 |
| Blast radius | `cmd/gateway` 启动 wiring、路由注册和 admin debug gate；若拆分时漏 import 或误改注册顺序，会导致编译失败或 OpenAPI 路由一致性回归。 |
| Failure modes | 函数移动导致循环/缺失 import；middleware 顺序被改；`mountRoutes` 在 nil deps 测试场景下提前解引用；生命周期 worker 停止顺序变化。缓解：优先移动原代码块，少量提取 helper，运行 `go build ./...` 和 `go test ./cmd/gateway -race`。 |
| Decision points | 不触碰高风险文件；若需要改数据库 schema、auth core、quota、billing ledger 或新增运行依赖则停止请求 Owner 确认。本任务预期无需这些变更。 |
| Pre-execution checklist | 1. 读取完整 `main.go`；2. 确认现有测试直接依赖的同包符号；3. 拆分文件并保持包名不变；4. `gofmt`；5. 统计行数；6. 运行指定 build/test。 |

Concrete execution order:

1. 把入口 `main()` 和 `run()` 缩到 `main.go`，把 shutdown helper 移到 `lifecycle.go`。
2. 把 env/key/audit/OAuth/credential helper 移到 `config.go`。
3. 把 `deps`、依赖构造和 worker runtime 聚合移到 `wiring.go`。
4. 把 chi 基础 middleware 和 `adminGate` 移到 `middleware.go`。
5. 把 `mountRoutes` 移到 `routes.go`，保留测试可直接调用的同包符号。
6. 运行 gofmt、行数检查和指定命令。
