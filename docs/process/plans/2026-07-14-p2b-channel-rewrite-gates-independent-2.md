# 2026-07-14 P2-b 渠道三门写口通电独立计划（Codex independent-2）

| 项目 | 内容 |
| --- | --- |
| Owner directive | Owner 已授权“HUAKAI P2-b 渠道三门写口通电”，要求贯通渠道 catalog 的 `body_param_strips`、`param_override`、`sensitive_words` 写入、读取与真实消费链。 |
| 本轮性质 | 独立计划，只规划，不执行实现；未读取任何同主题 Claude/Codex 计划。 |
| Scope | 渠道 create/update/list/get、OpenAPI、前端表单与 API、审计最小披露、真实 PostgreSQL 写入到消费验证。 |
| Success criteria | 三字段可稳定往返；非法格式在存储前返回 400；真实 PostgreSQL 写入后 registry 与运行时消费者立即使用新值；后端、前端和契约门全绿。 |
| Time estimate | 约 6–9 agent 小时、5–7 小时墙钟；真实 PostgreSQL 环境排障另预留 1–2 小时。 |
| Blast radius | 管理端渠道 API、admin 查询层、OpenAPI、catalog 前端、registry 集成测试；不改数据库结构、鉴权核心、账务、配额与部署。 |

## 已观察的 HUAKAI 内部事实

- 现有 migration 已为 `channels` 提供三个字段，因此本工单不新增 migration、不改 schema。
- admin 渠道 list/create/update 查询、响应模型及 mutation 请求目前没有贯通三个字段，且缺少详情 GET。
- registry 查询、`BindingMetadata`、请求 override/strip 与敏感词消费逻辑已经存在；缺口主要在管理写口、读口和端到端证明。
- registry 当前没有有效 L0 进程缓存；测试仍应使用同一 registry 实例在更新后再次解析，防止未来引入陈旧读。
- 现有 registry 集成测试通过直接 INSERT 覆盖字段，不能证明 admin 写口可用。
- `internal/adminhttp` 已到 20 个非测试文件；不得新增该包生产文件，且现有 mutation 文件接近单文件预算。
- 现有 admin 生成查询文件接近 codebudget 基线；继续堆入会触发增长门。
- Hermes 投影当前不暴露三字段原文；审计应保持只记录数量，不记录键值或敏感词。

## Scope

- 为 admin create/update/list/get 完整传输三个字段，并保持 tenant 隔离、软删除和启用状态既有约束。
- 新增 `GET /admin/v1/channels/{id}`，由前端编辑动作先取详情再回填。
- OpenAPI 同步 collection/detail 操作、请求/响应 schema、默认值及 400/404 语义。
- 前端提供两个逐行数组输入框和一个 JSON object 输入框；无效 JSON 不得发起请求。
- 用真实 PostgreSQL 证明 admin HTTP 写入、数据库读取、registry 解析、override/strip 与敏感词消费整条链。
- 为审计、跨 tenant、禁用/删除、缓存新鲜度和 OpenAPI 一致性增加判别性测试。

## Out of scope

- 不新增或修改 migration，不重做 registry 聚合优先级，不引入新的运行时依赖。
- 不改 auth、billing、quota、部署脚本、`LICENSE`、`Sidebar.tsx` 或导航结构。
- 不运行 `sqlc generate`；如拆分 SQL 查询，按仓库既有生成形状手工同步对应 Go 文件并明确记录。
- 不读取 `/home/ubuntu/refs`，不作任何借鉴项目行为断言，不执行 git add/commit/checkout。

## 决策点

1. 推荐 PUT 对省略的三字段保持旧值，显式 `[]` 或 `{}` 才清空，避免旧客户端静默抹除配置。
2. POST 省略值分别规范化为 `[]`、`{}`、`[]`；显式 `null`、类型错误或非 object 的 `param_override` 返回 400。
3. 数组采用逐行语义，服务端 trim、稳定去重并拒绝空白项；不按逗号拆分敏感词。
4. `param_override` 只接受非 null、非数组的 JSON object；响应使用 JSON object，禁止把 `[]byte` 编码成 base64。
5. 审计仅写三个 count 与既有元数据；原始路径、覆盖值、敏感词均不得进入审计 payload。
6. 上述均为中低风险工程默认；若合并计划选择 PUT 全量替换语义，应由 Owner 明确确认兼容性变化。

## 具体执行顺序

1. 先补 handler/store 捕获测试，锁定 create、update、get 的输入、返回与 400/404/tenant 契约。
2. 将渠道 catalog SQL 从临近预算的 provider 查询文件按职责拆到独立查询源，补 list/get/create/update 三字段。
3. 在 `internal/db/admin` 新的职责文件手工同步查询类型与方法，并更新 querier；不得提高 codebudget baseline。
4. 在现有 channel catalog handler 文件中扩展响应转换和详情 GET；不新增 `adminhttp` 生产文件。
5. 在现有 mutation handler 中加入 presence 跟踪、数组规范化和 JSON object 校验，控制文件不超过预算。
6. 保持数据库 mutation 与 audit 同一事务；补 count-only 审计及 sentinel 原文不泄漏断言。
7. 注册详情 GET 路由，同步 OpenAPI path/schema 与实现一致性 tripwire。
8. 更新前端 types、validator、API；create/update payload 始终精确携带三字段，edit 使用详情 GET。
9. 更新页面表单：两个逐行 textarea、一个默认 `{}` 的 JSON textarea；客户端 object 校验失败时拦截提交。
10. 增加真实 PostgreSQL 集成：POST 后解析并消费，再 PUT 后用同一 registry 实例验证旧值消失、新值生效。
11. 覆盖 disabled/deleted 不参与消费、跨 tenant 不可见、override 后 strip 且 strip 冲突时获胜。
12. 运行格式化、定向测试、全量构建/vet、codebudget、真实 PostgreSQL、Vitest 与 TypeScript 门。
13. 实现者仅交付未暂存 diff；主协调者按项目规则负责暂存、Codex per-commit review 与提交。

## 判别性 mutation contracts

- 删除任一 SQL 列或参数时，create/list/get/update 精确往返测试必须失败。
- 将 JSONB 响应退化为 base64 字符串时，响应对象断言必须失败。
- 接受 `param_override` 的 array/string/null 或错误数组元素时，400 且 store 零调用断言必须失败。
- PUT 省略字段却清空旧值时，presence 兼容测试必须失败；显式空值不清空时，清除测试也必须失败。
- 调换 override 与 strip 顺序时，同键冲突用例必须失败。
- 只写库而不传播到 `BindingMetadata` 或消费者时，真实 PostgreSQL 写到消费测试必须失败。
- 更新后返回旧配置时，同一 registry 实例二次解析测试必须失败。
- audit 或 Hermes 输出出现 sentinel 原文时，隐私断言必须失败。
- 前端漏字段、改字段名或将 object 序列化成字符串时，精确 payload 测试必须失败。
- 非法 JSON 仍调用 API 时，Vitest 的 API 零调用断言必须失败。
- 详情 GET 绕过 tenant/软删除条件时，隔离与 404 测试必须失败。
- 路由或 schema 漂移时，OpenAPI consistency 测试必须失败。

## 失败模式与缓解

- 生成文件继续膨胀：拆分渠道查询职责，不改大基线；运行 `internal/codebudget`。
- PUT 兼容性破坏：使用 presence 语义，显式空值才清除，并写双向测试。
- 审计泄漏敏感配置：只记录数量，以 sentinel 搜索整份 audit/Hermes 输出。
- 列表、详情和 mutation 模型漂移：复用统一响应转换，并用 OpenAPI/精确响应测试锁定。
- 错把“无 L0 缓存”当永久事实：同实例更新测试作为未来缓存实现的失效契约。
- 多渠道 override 键冲突：本工单不改变既有聚合规则；单独记录为后续决策，不在通电中暗改语义。
- 真实数据库测试被 mock 替代：要求 `integration_pg` 标签、真实 DSN、HTTP 写入后直接观察消费结果。

## Pre-execution checklist

1. 确认 Owner 合并计划已批准，且 PUT 省略语义没有相反决定。
2. 确认工作树中同文件无他人占用，保留用户已有修改。
3. 记录变更前 codebudget 与定向测试结果，不修改 baseline 放宽门槛。
4. 确认真实 PostgreSQL DSN 可用且仅指向测试库。
5. 先写失败测试，再逐层实现 SQL、handler、OpenAPI、前端与集成链。

## 验证门

- 后端：`go test` 覆盖 `internal/adminhttp`、`internal/hermesops`、`internal/registry`、`internal/gateway`、`internal/gatewayhttp`、`cmd/gateway`。
- 数据库：带 `integration_pg` 运行 admin 写入到 gateway 消费的真实 PostgreSQL 用例。
- 静态门：`go test ./internal/codebudget`、`go build ./...`、`go vet ./...`。
- 前端：catalog validator/API/page 定向 Vitest、必要的全量 Vitest、`npm run typecheck`。
- 契约：OpenAPI implementation consistency 与渠道路由/schema tripwire 全绿。

## Clean-room 与功能保全

- `REFERENCE PROJECTS IN SCOPE: CLIProxyAPI + sub2api + new-api` 仅为 Owner 提供的背景标签。
- 本计划只读取 HUAKAI 内部规则、规格与代码；未读参考项目源码，未提出参考项目能力或机制断言，因此不产生新的 clean-room 证据链风险。
- 三门均被完整贯通，无功能删除；风险通过验证、审计最小披露与兼容语义处理，不以关闭字段或隐藏入口规避。
