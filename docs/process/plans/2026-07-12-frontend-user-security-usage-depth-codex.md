# 2026-07-12 前端用户安全与用量深度（Codex 独立计划）

> 独立性声明：本计划在未读取任何同描述符 Claude 计划的前提下形成，只依据 Owner 本轮指令、HUAKAI 后端真码与现有前端实现。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “前端点亮切片2——用户端安全与用量深度（纯前端，后端零改动）”；“先读后端 handler 确认真实字段形状（禁臆猜 DTO），再写前端。” |
| Scope | 纳入：`features/profile` 的 Passkey 注册闭环；`features/usage` 的 Key 级时间序列、逐笔记录和请求 ID 查询；真实 API 层、纯函数、组件关键分支测试；前端三道门。排除：任何后端、数据库、鉴权核心、计费/配额逻辑、依赖、路由/导航、切片 1 与切片 3 文件域。 |
| Success criteria | 注册按 begin → `navigator.credentials.create()` → finish 完成，WebAuthn 二进制字段正确转换，成功刷新列表，不支持时禁用；Key 级三端点显式携带用户临时粘贴的 API Key Bearer，图形/表格/加载更多/详情齐全；API 路径与 query 测试真实；`tsc`、全部 vitest、build 全绿。 |
| Time estimate | 墙钟约 60–100 分钟；单 agent 工时约 1.5–2.5 小时。 |
| Blast radius | 仅个人安全页和用户用量主页；失败可能造成注册 ceremony 消耗后无法完成、API Key 被错误持久化、Key 级查询误带 session 后 401、分页重复或时窗被后端拒绝。 |
| Failure modes | 见下表；通过只在内存持有密钥/step-up、同一证明贯穿两步、显式 bearer、31 天半开时窗、游标替换、判别测试和全量门禁缓解。 |
| Decision points | 无需 Owner 中途决策：入口位置、端点、样式、认证方式和禁止后端改动均已由 Owner 指令及后端挂载锁定。若发现后端 DTO 与指令不可兼容才停下；当前未发现。 |

## Clean-room 与参考范围

- `REFERENCE PROJECTS IN SCOPE` 治理清单：CLIProxyAPI、sub2api、new-api。
- 当前是 HUAKAI implementer 车道；不读取上述参考项目源码、不作任何参考项目行为断言，也不把其标识、结构或实现带入代码。
- 本切片的功能形状只取自 Owner 已授权 brief 与 HUAKAI 内部 handler；因此不触发非 MIT 源码读取，也不存在待填的参考项目对照决策。

## HUAKAI 形状清单

| Actor | Path / mode / state | 已观察契约 |
| --- | --- | --- |
| 已登录用户 | Passkey register begin | `POST /v1/me/passkeys/register/begin`；请求含 `name`、`step_up`；响应含 `session_id`、`public_key`、`expires_at`。 |
| 浏览器认证器 | WebAuthn create | `public_key` 可能包裹 `publicKey`；`challenge`、`user.id`、`excludeCredentials[].id` 从 base64url 转 `ArrayBuffer`。 |
| 已登录用户 | Passkey register finish | 请求含 `session_id`、`name`、同一 `step_up`、序列化 `credential`；响应含 `passkey`。 |
| API Key 持有者 | Key 级逐笔查询 | `GET /v1/me/usage`；显式 API Key Bearer；`limit` 1–200、`cursor`、可选 RFC3339 `from/to`、`model/provider/status`；响应 `{items,next_cursor}`。 |
| API Key 持有者 | Key 级时序 | `GET /v1/me/analytics/time-series`；`from/to` 必填且窗口不超过 31 天，粒度 `day/week/month`；响应 `{items,period}`。 |
| API Key 持有者 | 单笔查询 | `GET /v1/generation?id=`；`id` 必填；响应为单条 usage record，404 表示当前身份作用域内不存在。 |

## 失败模式与缓解

| 风险 | 缓解与判别证据 |
| --- | --- |
| begin 成功但浏览器取消或不支持 | 浏览器能力预检并禁用；取消归一为中文提示；不调用 finish。 |
| WebAuthn 注册字段少转/错转 | 复用现有 base64url 原语；纯函数测试同时验证 challenge、user.id、exclude credential id 和 attestation 回传字段。 |
| step-up 缺失导致必然 403 | UI 明示“当前密码或两步验证码二选一”，构造非空 `step_up`，begin/finish 复用同一内存证明，结束后清空。 |
| API Key 被保存或误带 session | 输入仅 React 内存态，`autoComplete=off`，API 层每条请求显式传 `bearer: apiKey.trim()`；测试断言。 |
| 时序窗口超过后端 31 天 | 默认最近 30 天、结束右界为次日 UTC 零点；本地校验最大 31×24 小时。 |
| 加载更多重复首屏 | 测试断言 cursor 原样进入 query；组件只在非空 `next_cursor` 时追加。 |
| “图表”引入重依赖 | 复用 `hk-bar`，以相对最大值绘制费用条，不新增依赖或 CSS 框架。 |

## Pre-execution checklist

1. 已确认分支 `feat/fe-wire-users-mod`，不切分支、不 commit、不 push。
2. 已检查工作区：切片 1 修改集中在 `nav/router/platformcredentials`，本切片不触碰。
3. 已读取 `docs/RULES.md`、`AGENTS.md` 与 `acceptance-test-writer` skill。
4. 已核验 Passkey handler、service/types、step-up 形状及现有登录 WebAuthn helper。
5. 已核验三个 Key 级 handler 的鉴权挂载、query、分页、时间窗口与响应 DTO。
6. 已核验 `/usage` 是导航中的用户用量主页，现有 `hk-bar`、`hk-table`、`hk-loadmore`、`hk-kv` 可直接复用。
7. 已通过 `.coordination` 声明目标文件；未与切片 3 的 playground/media 文件冲突。
8. 实施前再次检查是否出现同描述符 Claude 计划或合成计划；若出现则只做差异核对，不覆盖。

## Concrete execution order

1. 在 profile 域补注册 DTO/API；以现有 base64url 原语实现注册选项转换、attestation 序列化、支持性/表单校验纯逻辑及判别测试。
2. 把 Passkey 卡拆成职责独立组件，接通命名、step-up、浏览器 create、finish、成功刷新、不支持/取消/后端失败分支。
3. 在 usage 域补 Key 级 DTO/API，所有请求显式 API Key Bearer；补路径、参数、游标与时窗测试。
4. 新增 Key 级分析组件：内存 Key 输入、日/周/月时间序列条形、逐笔 `hk-table` + `hk-loadmore`、请求 ID `hk-kv`。
5. 将组件挂到 `/usage` 主页，不改路由/导航；补 SSR 关键渲染分支和纯逻辑测试。
6. 运行定向 vitest 与 `tsc`，修复后执行全量 `npx vitest run` 和 `npm run build`。
7. 检查最终 diff、并行锁和未触碰后端/切片 1；把改动、端点 `file:line` 与门禁摘要写入 Owner 指定 `/tmp` 报告。

