# 2026-07-05 deadcode cleanup

| Owner directive | “清理本轮引入的新增死代码(quality-gate 复绿,不许 rebaseline 蒙混)” |
| --- | --- |
| Scope | 仅处理 quality-gate 报出的新增 unreachable 死代码；逐项 `rg` 核生产与测试调用者；删除确认无调用者的残留包装器、废弃 PO-1 接线、未用 fixture 与私有 helper；更新同批死代码清理涉及的测试注释；不做 commit/push。 |
| Out of scope | 不修改 `scripts/deadcode-baseline.txt`、`scripts/staticcheck-baseline.txt`；不改业务路径、不新增运行依赖、不做 schema/auth/billing/quota 高风险改动。 |
| Success criteria | `go build ./...`、`go vet ./...`、`GOFLAGS=-buildvcs=false bash scripts/quality-gate.sh` 通过；指定包 `go test ... -count=1` 通过；报告逐项说明删/留决策、grep 证据、连带清理与 quality-gate PASS 尾部。 |
| Time estimate | 约 45-90 分钟墙钟时间；主要耗时在全仓调用者确认、编译与 quality gate。 |
| Blast radius | 目标文件集中在 HTTP response 包、gateway/chatpipe、observability、subscription、panelauth、officialclient、audit、mimicry；风险是误删接口实现或测试 fixture 导致编译失败。 |
| Failure modes | 误删仍由接口/反射/测试使用的符号：删除前全仓 `rg`，删除后 `go test` 和 `go build` 验证；连带孤儿函数遗漏：删除后跑 quality-gate deadcode；baseline 被误改：变更前后核 `git diff -- scripts/*baseline*.txt`。 |
| Decision points | 若发现某符号存在真实生产或测试调用者，不删除并在报告解释 quality-gate 误报依据；若清理牵涉高风险文件或行为变更，停止并请 Owner 确认。 |
| Pre-execution checklist | 1. 记录当前 git 状态；2. 对每个清单符号执行全仓 `rg` 调用者确认；3. 阅读待删函数附近实现，确认真实现保留；4. 用 `apply_patch` 做最小删除；5. 运行格式化与指定门禁；6. 确认 baseline 文件未被修改。 |
| Concrete execution order | 先清理 response 包死包装器与注释引用；再清理 gateway/chatpipe 导出转发；再核 observability PO-1 残留；再判定 subscription/panelauth fixture 是否整型删除；最后逐项处理 officialclient/audit/mimicry，运行完整门禁并整理中文报告。 |

说明：这是 Codex 独立计划。当前 Owner 指令已明确要求执行并禁止 commit/push，因此本轮按该计划直接进入清理；若后续需要 Claude/Codex 合成计划，可基于此文件补交叉讨论记录。
