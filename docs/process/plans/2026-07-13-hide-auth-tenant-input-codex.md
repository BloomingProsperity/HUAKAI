# 2026-07-13 隐藏登录相关页面租户输入（Codex 独立计划）

> 独立计划声明：本计划由 Codex 在未读取同主题 Claude 计划的前提下独立形成；截至起草时，仓库中未发现同主题 Claude 计划或 Owner 已批准的合成计划。本文件只做规划，不代表已获准执行。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI 前端登录相关页面隐藏‘租户’输入框（Owner 拍板：单租户部署形态，不该让用户看见租户概念）。” |
| Scope | 范围内：盘点 `frontend/src` 中登录、注册、找回密码、重置密码、邮箱验证、新设备确认、OAuth 回调、首装向导等公开认证页面；隐藏其中面向用户的租户输入；请求仍固定传 `tenant_id=1`；更新直接相关 Vitest；运行完整 Vitest 与前端构建。范围外：后端、`frontend/e2e/helpers.ts`、后台运营页中的租户筛选/代管/策略配置能力、提交 commit。 |
| Success criteria | 登录与注册流程不展示租户输入，且所有登录、注册、OAuth、通行密钥、Telegram、邀请码校验请求继续传租户 1；找回密码不展示租户输入，重置邮件请求继续传租户 1；其余公开认证页和首装向导确认无租户输入；相关测试覆盖默认值和请求参数；`npx vitest run` 与 `npm run build` 均通过；不修改后端和 E2E helper。 |
| Time estimate | 执行约 30–60 分钟墙钟时间，约 45–90 分钟 agent 时间；完整测试与构建耗时以本机实际为准。 |
| Blast radius | 主要影响未登录用户的登录、注册和密码找回请求参数。默认值错误会导致认证请求落入错误租户或被后端拒绝；删除范围过宽会破坏后台平台管理员的跨租户运营能力。 |
| Failure modes | 1. 只删输入框却遗留可变 state 或传出 `0`：改为单一数字常量并加判别测试。2. 把后台运营租户字段误当作登录字段删除：限定公开认证路由范围。3. 忽略 OAuth/通行密钥等共享 `tenant_id` 路径：逐一检查 `LoginPage` 所有 `tid()` 调用。4. 测试只断言元素不存在但不验证请求参数：优先在纯逻辑/API mock 层断言租户 1。5. 触碰用户既有改动：执行前复查 `git status` 和目标文件 diff。 |
| Decision points | Owner 需先取得 Claude 独立计划，并批准双方差异后的合成计划。若 Owner 意图把“全前端”扩展到所有已登录后台运营页面的租户筛选/管理字段，需另行明确，因为那会移除平台管理员能力并造成明显功能缩水。 |

## 已观察到的现状

- `frontend/src/auth/LoginPage.tsx` 的登录与注册共用页面已经不渲染租户输入框，但仍用不可变字符串 state 保存 `"1"`，再经 `tid()` 转换。执行时应简化为合适位置的 `DEFAULT_TENANT_ID = 1`，保持所有认证分支继续透传。
- `frontend/src/auth/ForgotPasswordPage.tsx` 仍展示“租户 ID”数字输入框，是本轮已确认需要修改的页面。
- `frontend/src/features/setup/SetupWizardPage.tsx` 当前没有租户输入，也没有向安装请求体传 `tenant_id`；除非合成计划另有依据，不应凭空改变其后端契约。
- `ResetPasswordPage`、`EmailVerifyPage`、`DeviceConfirmPage` 没有租户输入；它们从链接参数读取租户并在缺省/非法时回退 1。保留现有兼容链接机制，不把 URL 内部参数误当作可见输入框。
- 后台运营页面存在大量租户筛选、代管和配置字段。这些不是“登录相关页面”，本轮不删除，避免跨租户运营功能缩水。

## Pre-execution checklist

1. 确认 Owner 已提供 start signal，并已批准 Claude/Codex 双计划合成结果。
2. 重新读取 `git status --short`，确认目标文件是否有 Owner/其他 agent 的并行改动。
3. 对公开认证路由做一次精确 grep，列出所有可见租户输入和所有请求传参点。
4. 核对现有默认租户机制；优先复用已有常量，避免同一认证域出现互相漂移的多个默认值。
5. 确认不修改后端、数据库契约、E2E helper 和后台运营租户控件。
6. 以小补丁修改页面与直接相关测试，代码注释和测试说明全部使用中文。
7. 运行目标 Vitest；失败时先判断是本次回归还是仓库既有问题并记录证据。
8. 在 `frontend/` 运行 `npx vitest run`，再运行 `npm run build`。
9. 检查最终 diff，确认请求体仍传 `tenant_id: 1`、页面不再暴露登录相关租户输入、没有生成物或无关文件进入改动。
10. 不 commit；按 Owner Summary Rule 输出中文报告。

## Concrete execution order

1. 先把 `LoginPage` 的固定字符串 state/转换函数收敛为数字默认常量，逐一替换登录、注册、邀请码校验、OAuth、通行密钥、Telegram 等内部租户参数。
2. 删除 `ForgotPasswordPage` 的租户 state 与可见字段，改用同一默认租户常量调用校验和重置邮件 API，并修正文档注释。
3. 复核首装向导和其余公开认证页面；只有发现真实可见租户输入时才修改，不为满足文件数量制造无意义变更。
4. 更新或新增最小但有判别力的 Vitest，既验证页面不暴露租户字段，也验证请求参数仍为 1；若现有测试架构不支持组件渲染，则在既有纯逻辑/API mock 边界完成判别覆盖并诚实记录限制。
5. 运行完整测试、构建和最终 grep/diff 审计，形成中文交付报告。

