# 2026-07-05 前端鉴权刷新接线修复 Codex 计划

| Owner directive | “前端三处鉴权/刷新接线 bug 修(FE-1/FE-2/FE-3)…禁止 git commit/push…禁止改任何页面级设计…每处配 vitest 判别测试 + §14 变异证红(cp 备份还原)” |
| --- | --- |
| Scope | 仅修改 `/home/ubuntu/HUAKAI/frontend` 中鉴权 token 选择、会话 token 跨标签同步、裸下载前主动刷新接线及对应 vitest；必要时新增本计划文档。不改页面布局、导航、配色、信息架构，不提交、不推送。 |
| Success criteria | FE-1 admin 路径在无手贴 admin token 时回落 session token，且 admin token 优先；FE-2 `setSessionTokens` 能把新 token 广播给同源其它标签并更新内存快照，SSR/无 `window` 时 no-op；FE-3 审计与用量下载在 session 临近到期时先调用统一刷新入口；三处都有判别测试和 cp 备份还原变异证红；指定 typecheck 与 vitest 通过。 |
| Time estimate | 约 45-75 分钟：读上下文 10 分钟，实现 20 分钟，测试与变异证红 20-40 分钟。 |
| Blast radius | 前端所有 API 鉴权头选择、登录态内存 store、审计/用量下载。若失败，可能造成 admin UI 401、跨标签 session 轮换冲突或下载仍误 401。 |
| Failure modes | BroadcastChannel 在测试/旧浏览器不可用：用 storage 事件回退并在无浏览器环境降级；主动刷新判断若复制实现会漂移：从 `lib/api` 导出复用函数；测试污染全局 `fetch`/`localStorage`/DOM：每例清理 mock 和 auth store。 |
| Decision points | 无新增运行时依赖；无页面设计改动；无数据库、后端、认证核心或账务修改。若发现必须改页面或后端才能闭环，则停止请求 Owner 确认。 |
| Pre-execution checklist | 1. 已读 `tokenForPath.ts`、`store.ts`、`refreshClient.ts`、`lib/api.ts`、审计/用量下载 API 与既有测试。2. 先做最小代码改动。3. 添加判别测试。4. 运行定向测试。5. 用 cp 备份还原做三处变异证红。6. 运行指定门禁。 |
| Concrete execution order | 1. 修 FE-1 token fallback 与测试。2. 修 FE-2 store 广播/接收与测试。3. 导出并复用主动刷新前置，修 FE-3 两个下载器与测试。4. 跑 vitest 定向。5. 三处逐项变异、确认红、还原。6. 跑 `npm run typecheck` 和指定 vitest。 |
