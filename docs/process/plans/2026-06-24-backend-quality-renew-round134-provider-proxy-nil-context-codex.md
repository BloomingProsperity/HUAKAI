# 2026-06-24 backend quality renew round134 provider proxy nil context

| 项目 | 内容 |
| --- | --- |
| Owner directive | “继续刚刚未完成的 renew”；并遵守“不要触碰到另一个目标，你做你的，他做他的”。 |
| Scope | 仅处理 `backend/internal/provider/postgres_proxy_resolver_unit_test.go` 中有意传入 nil context 触发的 SA1012 baseline 项，以及对应 `backend/scripts/staticcheck-baseline.txt` 记录。 |
| Out of scope | 不改 provider resolver 生产逻辑；不改数据库 schema、auth、billing、quota、部署脚本；不触碰另一个目标的计划文件。 |
| Success criteria | 测试仍明确覆盖 nil receiver / nil pool 的 fail-loud 短路；SA1012 例外收敛为局部 `//lint:ignore`；全局 staticcheck baseline 删除该文件对应条目；scoped 文本检查通过。 |
| Time estimate | 10-15 分钟；单 agent 小切片。 |
| Blast radius | 低。只影响两个单测中的静态检查注释和 baseline 文本，不改变运行时行为。 |
| Failure modes | 若误把 nil context 改成 `context.TODO()`，测试将不再覆盖 nil context 在短路路径下不会被使用；若 `//lint:ignore` 位置不正确，staticcheck 仍会报 SA1012。 |
| Mitigation | 保留 `Resolve(nil, 1)` 入参；把 `//lint:ignore SA1012` 放在调用前一行；用 `rg` 核对 baseline 与调用点。 |
| Decision points | 无需 Owner 额外确认；不涉及高风险文件。 |
| Pre-execution checklist | 1. 已读取目标文件；2. 已核实 `Resolve` 的 nil receiver / nil pool 检查先于 DB context 使用；3. 写计划后再编辑；4. 编辑后跑 scoped 检查并记录 Go 工具链限制。 |

## 执行顺序

1. 将两个 `//nolint:staticcheck` 改为紧邻调用的 `//lint:ignore SA1012` 中文说明。
2. 从 `backend/scripts/staticcheck-baseline.txt` 删除对应 SA1012 全局豁免。
3. 运行 `rg`、`git diff --check`、尾随空白与 clean-room 词检查。
4. 尝试 `gofmt` 与 scoped `go test`，若本机缺 Go 工具链则如实记录。
