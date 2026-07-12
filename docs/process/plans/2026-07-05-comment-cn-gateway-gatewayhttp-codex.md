# 2026-07-05 存量英文注释转中文首批

| Owner directive | “存量英文注释转中文——首批 internal/gateway + internal/gatewayhttp(只改注释文字,零逻辑改动)” |
| --- | --- |
| Scope | 只处理 `backend/internal/gateway/*.go` 与 `backend/internal/gatewayhttp/*.go` 顶层手写 Go 文件中的英文散文注释；不递归子包；不改逻辑、标识符、字符串、SQL、struct tag、常量值、编译/工具指令、版权/SPDX 或生成码文件。 |
| Success criteria | `git diff internal/gateway internal/gatewayhttp` 只包含注释行变化；英文技术标识符保留；无 clean-room 新风险；指定 `go build`、`go vet`、`go test` 门禁完成并报告结果。 |
| Time estimate | 约 1.5-3 小时墙钟时间，取决于英文注释数量与门禁耗时。 |
| Blast radius | 预期仅影响注释文本；若误改代码行会破坏零逻辑改动承诺，需立即回退该处修改。 |
| Failure modes | 误改非注释代码行：用 diff 审查与脚本检查拦截；误转技术标识符：人工复核保留 `HTTP`、`SSE`、`TLS`、`RFC`、环境变量、函数/类型名等；生成码误改：先扫描生成码标记并跳过；门禁失败：记录失败命令与原因，不做无关修复。 |
| Decision points | 若发现生成码、版权头或需要改逻辑才能通过门禁，停止对应部分并报告；本任务禁止 commit/push。 |
| Pre-execution checklist | 1. 读取语言与注释规则；2. 枚举目标顶层 `.go` 文件；3. 扫描生成码和工具指令；4. 翻译英文散文注释；5. 检查 diff 只含注释；6. 运行指定 build/vet/test；7. 输出中文汇报。 |
| Concrete execution order | 先用脚本抽取候选注释行，再分批用 `apply_patch` 修改；修改后用 `git diff --check`、自定义 diff 检查和 Go 门禁验证。 |
