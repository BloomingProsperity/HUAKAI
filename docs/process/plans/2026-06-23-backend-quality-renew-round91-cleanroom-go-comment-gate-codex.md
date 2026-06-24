# 2026-06-23 backend-quality-renew-round91-cleanroom-go-comment-gate-codex

| Owner directive | “做完了？ 这么快？ 这么大的项目你这么快？” |
| --- | --- |
| Scope | 仅为 `backend/**/*.go` 增加 clean-room 注释回归测试门；不碰另一个目标计划，不读取或修改 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`。 |
| Out of scope | 不做安全专项，不改业务运行逻辑，不改 `LICENSE`，不改鉴权、计费、配额、数据库 schema、部署脚本或真实密钥。 |
| Success criteria | `codebudget` 测试能扫描 Go 注释并拒绝出现借鉴项目名或“借鉴/参考某项目”等禁词；现有仓库扫描为零违规；工作树 diff 无空白错误。 |
| Time estimate | 约 20-35 分钟；Codex 单小步实现与本地静态验证。 |
| Blast radius | 仅测试代码质量门；失败时影响测试结果，不影响生产二进制运行路径。 |
| Failure modes | 误扫字符串字面量导致假阳性：只解析 Go 注释而不是全文匹配；遗漏目录：从 `backend/` 根递归扫描 `.go`；禁词列表过宽导致后续合法技术说明被拦：范围限定为 Go 注释，文档仍允许在 clean-room 规则下说明来源。 |
| Decision points | 若需要把禁词门扩展到文档、SQL、前端或提交钩子，另开计划并请 Owner 确认；本轮不扩大范围。 |
| Pre-execution checklist | 1. 已重新读取目标文件；2. 已读取 clean-room 技能说明；3. 复核 `backend/internal/codebudget/budget_test.go` 现有测试结构；4. 写测试前先确认全仓 Go 注释当前无禁词残留。 |

## 执行顺序

1. 在 `backend/internal/codebudget` 添加或扩展测试，使用 Go 标准库解析注释。
2. 禁词列表覆盖 Owner 硬规则点名的借鉴项目名与中文违规表述。
3. 本地用 `rg` 与轻量脚本辅助确认当前 Go 文件没有残留。
4. 运行可用检查；若 `go`/`gofmt` 缺失，明确记录。
