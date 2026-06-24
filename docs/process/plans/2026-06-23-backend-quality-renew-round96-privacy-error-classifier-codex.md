# 2026-06-23 backend quality renew round96 privacy error classifier

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 仅处理 `backend/internal/privacy` 中 `AllowlistRedactor.SanitizeError` 的错误分类脆弱点；不改认证、账本、配额、数据库 schema、部署脚本或另一个目标的计划文件。 |
| Success criteria | 错误分类不再依赖任意子串命中；常见 timeout/rate limit/forbidden/invalid/credential/upstream 分类保持；新增判别测试覆盖误判词与 wrap 后的隐私错误。 |
| Time estimate | 约 20-30 分钟；单个 Codex 小切片。 |
| Blast radius | 影响隐私日志和故障分类的公开 error class；若分类过窄可能把部分错误归为 `internal_error`，若过宽会继续误判。 |
| Failure modes | 误删现有分类语义；新增 helper 过复杂；Go 工具缺失导致无法真实运行单测。缓解：保持原有类别集合，补直接判别测试，运行可用静态检查并诚实记录工具缺失。 |
| Decision points | 若需要引入新公开错误类别或改变外部响应语义，则停止请求 Owner；本轮预期不需要。 |
| Pre-execution checklist | 1. 已重新读取目标 objective；2. 已检查当前 worktree；3. 已读取 `privacy` 实现和现有测试；4. 不触碰 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`；5. 修改后运行 `git diff --check`、clean-room 注释扫描，并尝试 Go 测试/格式化。 |
