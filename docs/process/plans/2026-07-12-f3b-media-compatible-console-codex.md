# 2026-07-12 F3b 媒体兼容端点专属控制台（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “切片 F3b——MJ / Suno / 视频兼容端点专属控制台……先逐个读后端 router+handler 确认真实路径/body/响应（禁臆猜）……做全，不留缺口。” |
| Scope | 仅修改 `frontend/src/features/mediatasks/`，并新增本计划；只读 HUAKAI 后端路由、转换 handler、媒体任务响应类型及统一前端 API。实现任务总览 / Midjourney / Suno / 视频四段式页面、三族提交与查询操作、MJ 换脸/种子/条件列表、Suno 动作、媒体结果展示与 5 秒轮询。明确不改后端、OpenAPI、全局样式、依赖、路由、认证核心、计费或数据库。 |
| Success criteria | 12 个已挂载兼容路由均有真实 API 封装与测试锁定；三族表单按 handler 字段组装 body，空必填与坏 base64 在前端拒绝；提交/查询结果可展示状态、进度、错误及 URL/data URL/裸 base64 媒体；活跃任务每 5 秒轮询且单项失败不覆盖成功项；`npx tsc --noEmit`、`npx vitest run`、`npm run build` 全绿。 |
| Time estimate | 墙钟约 90–150 分钟；单 agent 工时约 2–3 小时，主要耗时在三族交互、结果归一化与判别性测试。 |
| Blast radius | 仅媒体任务页面。失败可能导致该页无法编译、兼容端点 body 失真、轮询泄漏或不安全媒体 URL 被渲染；不会改变后端行为、数据结构、账务或鉴权。 |
| Failure modes | 端点形态臆测：逐项用 router/translate/Task 类型引用校验；OpenAPI 当前片段互相串行错位：不把该片段作为 handler 之外的事实来源；动作路径注入：纯函数白名单 MJ action、正则校验 Suno action；base64 垃圾进入请求/DOM：限制为合法 data URL 或可解码裸 base64；轮询竞态：AbortController + in-flight 防重入 + `Promise.allSettled` 部分成功合并；结果字段形态不固定：递归、限深提取已知 URL/base64 字段并保留 JSON 摘要；并行覆盖：已通过 `.coordination` 独占目标文件。 |
| Decision points | 无需中途 Owner 决策。Owner 已锁定页面聚合、session 鉴权、端点范围、5 秒轮询和只改前端；若 handler 证实端点恒错才跳过。当前只读证据表明三族均调用真实 `mediatask.Service`，没有恒错/占位端点。 |
| Pre-execution checklist | 1. 核对分支与工作区，仅保留他人后端改动；2. 读取 `AGENTS.md`、`docs/RULES.md`、适用技能；3. 独立起草本计划，不读同主题 Claude 草案；4. 协调锁覆盖所有预计文件；5. 逐端点读 router、translate、Task 响应与 session 挂载；6. 建立 shape inventory；7. 先写纯函数/API 契约测试再接 UI；8. 运行定向测试、全量三门禁；9. 检查 diff 范围并生成 `/tmp` 报告；10. 释放协调锁。 |

## 车道与参考范围

`REFERENCE PROJECTS IN SCOPE: CLIProxyAPI + sub2api + new-api`。

本任务是实现车道，只接线 HUAKAI 自身已落地兼容端点；依照 CR-R-001，不读取上述非 MIT 参考源码，也不在本计划作任何外部项目行为断言。这里登记默认三镜仅用于满足计划范围治理，不把它们当实现来源。Owner 本次没有提出架构分叉，故没有需要外部项目对照的 A/B 决策；契约事实全部引用 HUAKAI 内部代码。

## 已观察契约与 shape inventory

### Midjourney

- `POST /mj/submit/{action}`：11 个 action 白名单；请求字段来自 `Request`，含 `request_id/requestId`、`prompt`、`customId`、`botType`、`notifyHook`、`action`、`state`、`base64Array`、`index`、`maskBase64`、`sourceBase64`、`targetBase64`（`backend/internal/mjclient/router.go:40-45`；`backend/internal/mjclient/translate.go:15-43`）。
- `POST /mj/insight-face/swap`：同一请求结构，UI 明确组装 `sourceBase64` 与 `targetBase64`（`backend/internal/mjclient/router.go:42`；`backend/internal/mjclient/translate.go:54-56`）。
- `GET /mj/task/{id}/fetch` 与 `GET /mj/task/{id}/image-seed`：均以正整数 path id 调 `Status`，返回完整 `mediatask.Task`（`backend/internal/mjclient/router.go:43-44,99-119`）。
- `POST /mj/task/list-by-condition`：body 只读取可选 `limit`，范围 1–200，返回 `{items: Task[]}`（`backend/internal/mjclient/router.go:45,122-142,169-192`）。底层列表只按 tenant/user/limit 查询、不按 provider 过滤（`backend/internal/mediatask/store.go:89-112`），因此专属台需如实显示端点总数并只呈现 `midjourney` 项。

### Suno

- `POST /suno/submit`：空 action 时 `custom_mode=false` 得到普通生成，`custom_mode=true` 得到自定义生成；body 可承载 `request_id/requestId`、`gpt_description_prompt`、`prompt`、`mv`、`title`、`tags`、`continue_at`、`continue_clip_id`、`make_instrumental`、`model_version`、`custom_mode`、`input`、`notify_hook`（`backend/internal/sunoclient/router.go:31-35`；`backend/internal/sunoclient/translate.go:15-30,60-67`）。
- `POST /suno/submit/{action}`：action 并非有限枚举；handler 接受由字母、数字、`-`、`_` 组成的非空值并规范成任务类型（`backend/internal/sunoclient/translate.go:60-83`）。UI 使用明确动作输入并做同等正则校验，不伪造动作清单。
- `GET /suno/fetch?id={id}` 与 `GET /suno/fetch/{id}`：query 还接受 `task_id` 别名，返回完整 `Task`（`backend/internal/sunoclient/router.go:34-35,70-90,117-125`）。

### 视频

- `POST /video/submit`：body 可承载 `request_id/requestId`、`model`、`prompt`、`image`、`duration`、`width`、`height`、`fps`、`seed`、`n`、`response_format`；返回 `{task_id,status}`（`backend/internal/videoclient/router.go:36-40,42-72`；`backend/internal/videoclient/translate.go:18-31`）。
- `GET /video/fetch`：有 `id/task_id/taskId` 时返回单任务；没有 id 时按 `limit`（1–200）返回 `{items}`（`backend/internal/videoclient/router.go:74-103,132-139,174-185`）。无 id 分支同样复用用户级全量列表，专属台只呈现 `video` provider 项并报告原始条数。
- `GET /video/fetch/{id}`：正整数 path id 查询单任务（`backend/internal/videoclient/router.go:105-130`）。

### 共同响应与认证

- 提交统一返回 HTTP 202 的 `{task_id,status}`；查询返回 `Task`，其媒体结果在 `result`，状态/进度/错误字段由 `backend/internal/mediatask/types.go:86-107` 定义。
- 三族全部挂在 `SessionMiddleware` 内（`backend/cmd/gateway/routes.go:342-348`），前端只调用 `src/lib/api`，不要求用户输入 Key。

## 文件职责与执行顺序

1. `types.ts`：补充三族表单/body、查询方式与兼容响应类型。
2. `api.ts`：按 5/4/3 路由添加 session API 封装，所有路径参数先由纯函数校验。
3. `compatibility.ts`：集中表单→body、base64 校验、任务合并、结果媒体提取与 JSON 脱敏；保持与 React/网络解耦。
4. `CompatibilityTaskPanel.tsx`：实现共用查询结果卡、状态/进度、图片/音频/视频渲染与 5 秒部分成功轮询。
5. `MidjourneyConsole.tsx`：实现 action 提交、换脸、任务/种子查询、条件列表。
6. `SunoConsole.tsx`：实现普通/自定义提交、自由但受约束的 action 提交、path/query 两种查询。
7. `VideoConsole.tsx`：实现视频提交、path/query 查询与列表刷新。
8. `MediaTasksPage.tsx`：在页头下加入 `hk-seg` 四段，保留现有任务总览和创建 Modal。
9. `compatibility.test.ts`：锁定每条 API 路径/方法/body；每族至少两条纯函数测试；覆盖坏 base64、空必填、动作校验、URL/base64 资源提取、恶意 scheme 排除及轮询单项失败合并。

## 判别性测试自检

- API 测试断言完整调用参数；任一方法、路径、query 或 body 键变异都会变红。
- MJ 将 `base64Array` 错拼、Suno 将 `make_instrumental=false` 错删、视频将 `duration` 变成字符串时，精确相等断言会变红。
- 轮询 fixture 同时含成功与失败 Promise；若改回 `Promise.all` 或失败清空已有项，测试会变红。
- 展示 fixture 同时含安全 URL、裸 base64、data URL 和 `javascript:`；若只处理单一形态或放开危险协议，测试会变红。

## 风险与假设记录

- 后端兼容层对多数业务字段只做 JSON 透传；UI 的必填规则限定为本任务明确要求的主要创建语义，不声称后端对所有 action 强制同一字段。
- Suno action handler 没有有限动作枚举，因此 UI 不展示臆造枚举，只展示经 handler 同等字符约束校验的动作输入。
- MJ `image-seed` 当前返回完整任务而非独立 seed DTO；UI 如实显示任务及其 `result`，不虚构 `seed` 顶层响应。
- 媒体 `result` 是 provider 透传 JSON，字段集合不封闭；展示逻辑只提取明确可识别的安全资源，其余仍保留脱敏 JSON 供排障。
- 适用技能影响：`frontend-ops-ui-review` 促使页面展示状态、进度、错误与恢复查询；`acceptance-test-writer` 促使测试同时覆盖正常、失败与轮询恢复路径。未改全局验收矩阵，因为 Owner 将可写范围锁定为本片计划与 `features/mediatasks/`。
