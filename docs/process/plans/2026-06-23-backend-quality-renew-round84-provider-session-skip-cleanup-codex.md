# 2026-06-23 backend quality renew round84 provider session skip cleanup

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；目标文档 §③-6 点名 copilot/cursor/gemini/kiro/windsurf/antigravity 存在纯 no-op `t.Skip` 占位测试，不得当作覆盖计数。 |
| Scope | 仅清理 `backend/internal/provider/{copilot,cursor,gemini,kiro,windsurf,antigravity}` 的 session adapter 测试占位函数；不改 provider 生产逻辑、不新增响应处理语义、不碰 auth、billing、quota、schema、迁移、部署、`LICENSE` 或另一个 security 目标。 |
| Success criteria | 这些 session adapter 测试文件不再包含仅 `t.Skip` 的 401/5xx 占位测试函数；缺口改为显式 TODO 注释，说明真实覆盖应落在 reauth / dispatcher / channel-health 层；现有请求构造判别测试保留。 |
| Time estimate | 约 10-15 分钟；一个 Codex 小闭环。 |
| Blast radius | 低。仅删除假覆盖测试函数，减少 skipped 测试噪声；不会改变运行时代码。 |
| Failure modes | 误删已有有效判别测试；TODO 表述不清导致后续误解为已覆盖；本地缺 Go 工具链无法跑包测试。缓解：只删除函数体唯一语句为 `t.Skip` 的占位函数，保留上下文 TODO，使用 `rg` 验证目标目录无残留。 |
| Decision points | 无需 Owner 中途确认；如发现需要新增真实响应处理或跨层 DLQ 语义，停止并另开计划。 |
| Pre-execution checklist | 1. 已重新读取 goal objective；2. 已确认 6 个 session 测试文件各有 2 个纯 `t.Skip` 占位函数；3. 已确认相邻测试已有请求构造判别覆盖；4. 修改后运行 `rg`、`git diff --check`、可用 Go 检查。 |
