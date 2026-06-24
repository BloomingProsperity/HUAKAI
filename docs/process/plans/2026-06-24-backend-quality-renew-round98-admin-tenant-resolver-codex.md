# 2026-06-24 backend quality renew round98 admin tenant resolver

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 收敛 `adminuserhttp`、`alertinghttp`、`moderationhttp`、`usernoticehttp` 中重复且语义不一致的 admin tenant 解析逻辑；不改认证核心、账本、配额、数据库 schema、部署脚本或另一个目标计划文件。 |
| Success criteria | 新增小型纯逻辑 helper 包处理 query/body tenant_id 解析与 `AdminIdentity.CanIssueForTenant` 校验；四个 HTTP 包复用 helper 并保留各自响应写法；新增 helper 判别测试覆盖平台管理员缺 tenant、租户操作员 scope fallback、非法 tenant、越权。 |
| Time estimate | 约 30-45 分钟；单个 Codex 小切片。 |
| Blast radius | 影响四个 admin HTTP 面的 tenant_id 解析错误映射；若 helper 映射错误，可能导致合法租户操作员请求被拒或非法 tenant_id 被放过。 |
| Failure modes | 误改 HTTP 错误码/错误码字符串；遗漏 `CanIssueForTenant`；把 package 做成新 god helper。缓解：helper 只返回稳定错误，由调用方保留原有 writeJSON/writeAdminError；补纯逻辑测试。 |
| Decision points | 若需要改变 RBAC 角色定义、admin token 认证、数据库 schema 或跨租户授权规则，停止请求 Owner；本轮预期不需要。 |
| Pre-execution checklist | 1. 已重新读取 objective；2. 已读取四处现有 tenant 解析逻辑；3. 已识别 signed-envelope 触及认证核心并暂停；4. 不读取不编辑另一个目标计划文件；5. 修改后运行 `git diff --check`、禁词扫描、文件体量检查，并尝试 `gofmt` / `go test`。 |
