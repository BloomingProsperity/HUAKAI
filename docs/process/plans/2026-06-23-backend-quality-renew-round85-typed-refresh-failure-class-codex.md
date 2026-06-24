# 2026-06-23 backend quality renew round85 typed refresh failure class

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；目标文档 §③-3 点名 `mode_refresh.go` 对 `err.Error()` 子串匹配依赖错误文案稳定，分类脆弱。 |
| Scope | 仅处理 credentialworker refresh failure class 的类型化入口与判别测试；涉及 `backend/internal/credentialworker` 与已有 provider refresher 的 `RefreshError` 方法；不改刷新 HTTP 行为、不改数据库 schema/迁移、不碰 auth core、billing、quota、部署、`LICENSE`、真实密钥或另一个 security 目标。 |
| Success criteria | provider typed `RefreshError` 能暴露 outcome；`ClassifyRefreshErrorClass` / `classifyModeRefreshError` 优先读取 typed outcome 并归一化为既有安全 failure class；旧字符串匹配保留为 fallback；新增测试证明 typed outcome 即使错误文本没有关键字也能分类。 |
| Time estimate | 约 20-30 分钟；一个 Codex 小闭环。 |
| Blast radius | 中低。失败分类更稳定，但仍保持既有 class 字符串契约：`invalid_grant`、`rate_limit_exceeded`、`risk_control_triggered`、`account_disabled`、`payload_invalid`、`operator_config_required`、`temporary`。 |
| Failure modes | typed outcome 与 failure class 命名不一致导致 dry-run 文案变化；provider 包和 credentialworker 形成 import cycle；本地缺 Go 工具链无法执行测试。缓解：provider 只新增无依赖方法；credentialworker 做归一化映射；用 `rg` 和 `git diff --check` 做可用检查。 |
| Decision points | 无需 Owner 中途确认；若需要重构 provider refresh 流程或修改持久化 schema，停止并另开计划。 |
| Pre-execution checklist | 1. 已重新读取 goal objective；2. 已读取 api-gateway-risk-review skill；3. 已定位 `classifyModeRefreshError` 和 dry-run 调用；4. 已确认多个 provider `RefreshError` 已有 `Outcome` 字段；5. 修改后跑目标 grep、diff check、可用 Go 检查。 |
