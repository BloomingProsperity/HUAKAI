# 2026-06-24 backend quality renew round99 refresh failure classifier

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 收敛 `backend/internal/credentialworker` 的 refresh failure class 分类逻辑：从 `mode_refresh.go` 大文件迁到同包小文件，并把 raw `err.Error()` 子串匹配收紧为 token/短语匹配；不改 provider adapter 协议、不改凭据 schema、不改数据库、不碰另一个目标计划文件。 |
| Success criteria | `mode_refresh.go` 行数下降；分类仍覆盖 `invalid_grant`、rate limit、risk control、account disabled、payload invalid、operator config、temporary；新增误判词测试，证明 `decryptology/jsonify/disabled_accountant` 不会被当成真实故障类。 |
| Time estimate | 约 25-35 分钟；单个 Codex 小切片。 |
| Blast radius | 影响 credential refresh 失败写库 class 与 dry-run 展示；若分类过窄，部分未知错误会退到 `temporary`，若过宽则继续误导运维。 |
| Failure modes | 误改安全类集合；漏掉 typed outcome；增加 `mode_refresh.go` 体量；Go 工具缺失导致不能真实跑单测。缓解：迁出小文件、保留现有 public class、补判别测试、运行可用静态检查并记录工具缺失。 |
| Decision points | 若需要改变刷新状态机、credential store schema、provider adapter 返回协议或新增公开失败类，停止请求 Owner；本轮预期不需要。 |
| Pre-execution checklist | 1. 已重新读取 objective；2. 已读取当前 `mode_refresh.go` 分类实现与测试；3. 已确认 `mode_refresh.go` 超 baseline，不能继续加逻辑；4. 不读取不编辑 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`；5. 修改后运行 `git diff --check`、禁词扫描、行数检查，并尝试 `gofmt` / `go test`。 |
