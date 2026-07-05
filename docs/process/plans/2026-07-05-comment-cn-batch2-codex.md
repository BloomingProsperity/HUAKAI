# 2026-07-05 存量英文注释转中文批2

| Owner directive | “§7 存量英文注释转中文——批2(收尾批)” |
| --- | --- |
| Scope | 仅处理 `internal/clienterr`、`internal/proto`、`internal/provider`、`internal/pool`、`internal/gateway` 及 `/home/ubuntu/HUAKAI/frontend/src` 中的英文注释；生产代码和测试代码都包含。 |
| Out of scope | 不修改代码逻辑、标识符、字符串字面量、struct tag、生成码、第三方码、编译/工具指令、版权/SPDX 头、URL、代码示例内英文。 |
| Success criteria | 范围内残余英文说明性注释转为中文；剩余未转行均能说明排除原因；指定 Go 与前端门禁运行并记录结果。 |
| Time estimate | 约 30-60 分钟；主要时间在逐包扫描、手工核对排除项和门禁。 |
| Blast radius | 低。修改目标是注释文本；风险主要是误改生成码、工具指令或让含 CRLF/BOM 文件被整体重排。 |
| Failure modes | 误把代码示例/URL/版权头翻译；漏掉块注释续行；测试耗时或受环境依赖失败。缓解方式是先扫描候选行、按文件小补丁修改、最后复扫并运行指定门禁。 |
| Decision points | 无需 Owner 中途确认；若发现需要改代码逻辑、生成码、许可证、schema、auth/billing/quota 核心或新增依赖，则停止确认。 |
| Pre-execution checklist | 1. 确认工作树状态；2. 扫描候选英文注释；3. 排除生成码/工具指令/版权/URL/代码示例；4. 小范围应用注释翻译；5. 复扫残留；6. 运行指定门禁；7. 中文报告统计与风险。 |
| Concrete execution order | 先按 Go 包扫描，再处理前端 `src` 注释；每批修改后复扫同一路径；最后运行 `go build ./...`、`go vet ./...`、指定 `go test` 与前端 `npx vitest run`。 |
