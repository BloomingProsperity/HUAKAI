# 2026-07-14 P2-b 渠道三门写口通电（合并权威计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI P2-b · 渠道三门写口通电——反封锁/合规改写字段能配了”；“本工作单属已批修复 arc P2，直接实现”；“改完停下等审查”。 |
| 独立计划 | `2026-07-14-p2b-channel-rewrite-gates-codex.md` 与 `2026-07-14-p2b-channel-rewrite-gates-independent-2.md`；两份计划在互不读取对方草案的会话中形成。当前环境没有可调用的 Claude 会话，因此如实记为两份 Codex 独立草案，不冒充 Claude 草案。 |
| Scope | 核实三列归属、类型、registry 读源、运行时消费与缓存；接通渠道 catalog create/update/list/get 及写接口回包；同步 SQL 源和手工生成码、严格校验、审计、OpenAPI、前端 create/edit、真 PostgreSQL 消费联动与判别性测试。 |
| Out of scope | 不新增迁移，不运行 `sqlc generate`，不修改 `Sidebar.tsx`，不改变运行时改写算法，不新增运行时依赖，不修改认证/计费/配额/部署/`LICENSE`，不读取参考项目源码，不执行 `git add`、`git commit`、`git checkout`。 |
| Success criteria | create 缺省得到空数组/空对象/空数组；update 缺省保持旧值、显式空值可清除；三字段可精确写入并由 list/get 回显；非法结构在写库前返回 400；审计能区分提交与省略；前端能配置和回填；真 PostgreSQL 证明同一 registry 实例更新后立即驱动请求参数覆盖、字段剥离和输出敏感词混淆；指定 Go、PG、OpenAPI、codebudget、Vitest、TypeScript 门全绿。 |
| Time estimate | 约 5–8 小时墙钟、7–10 agent 小时；真实 PostgreSQL fixture 和既有测试兼容是主要不确定项。 |
| Blast radius | 管理端渠道 catalog API、admin SQL 查询层、OpenAPI、catalog 前端，以及新增的真实 PostgreSQL 消费联动测试；不改变数据库结构和网关消费算法。 |
| Reference scope | `CLIProxyAPI`、`sub2api`、`new-api` 仅为 Owner 背景标签。本轮只读取 HUAKAI 内部代码和契约，不产生关于参考项目的新事实断言。 |

## 起手事实

1. `body_param_strips text[]`、`param_override jsonb`、`sensitive_words text[]` 均已存在于 `channels`，默认值依次为 `[]`、`{}`、`[]`，无需 migration。
2. registry 从同租户、同池、启用且未删除的渠道聚合三字段；字符串数组会过滤空白并去重，覆盖对象按键聚合，随后写入 binding metadata。
3. 请求期先应用 `param_override`，再应用 `body_param_strips`；输出侧由 `sensitive_words` 驱动混淆。
4. 当前 registry cache 是 no-op，占位接口没有保存解析结果；同一 registry 实例每次解析都会访问数据库。因此本票不新增失效机制，但用更新后二次解析测试固定即时生效契约。
5. 起始代码只有 collection GET/POST 与 item PUT/DELETE；Owner 原文明确要求 list/get 回显，故补充租户隔离的单条 GET。前端编辑仍以 list item 回填。
6. `internal/adminhttp` 已达到单包 20 个非测试文件，mutation 文件接近 600 行；现有 admin catalog 生成文件也接近增长基线。校验逻辑应下沉到新职责包，mutation 查询应拆到新的 SQL/生成文件，禁止提高 codebudget baseline。

## 两份计划交叉讨论结果

### 共识

- 三字段必须同时贯通 create、update、读回、OpenAPI、前端和真实消费链，不能只证明落库。
- update 必须有 presence 语义：省略不动，显式 `[]`/`{}` 清空。
- `param_override` 只能是非 null JSON object；数组字段只能是非空字符串数组；非法输入 400 且 store 零调用。
- 真 PostgreSQL 测试必须使用各不相同的 sentinel，并在 update 后复用同一 registry 实例，证明无陈旧配置。
- 不读参考项目源码、不新增迁移、不运行生成器、不引入新依赖、不修改 codebudget baseline。
- SQL 源和手工生成码需要按职责拆分，避免把增长继续塞进临界文件。

### 分歧与裁决

1. **是否新增单条 GET**：第二份计划建议新增；第一份计划把“get”视为待核实的回显面。最终按 Owner 原文“list/get response DTO 带出三字段”执行：新增租户隔离的单条 GET，并让 list/get/create/update 共用完整 item DTO；前端编辑仍复用 list item，避免额外请求。
2. **审计记录计数还是实际值**：第二份计划偏向 count-only；Owner 要求“现有审计怎么记就怎么带上新字段”，现有渠道审计记录配置快照而非仅计数。裁决为 create 记录三字段实际值，update 同时记录值与 `set_*` 标志，准确区分省略和显式清空；不新造审计事件。
3. **校验放置**：第二份计划仍打算继续扩张 mutation handler；第一份计划允许按 codebudget 下沉。裁决为新增内聚的 `internal/channelrewriteconfig` 包，handler 只负责 HTTP 错误映射，避免单文件越线。
4. **数组输入形式**：第二份计划偏向逐行，第一份计划偏向逗号分隔。Owner 明确允许二者，现有 UI 更适合紧凑文本输入，裁决为逗号分隔；服务端仍以 JSON 字符串数组作为 API 契约。

### 单份计划捕获的缺口

- 第二份计划补充了 audit/Hermes 泄漏风险和同一 registry 实例的未来缓存契约。
- 第一份计划补充了 create/update 回包必须来自数据库真实扫描、SQL 列/参数/scan 顺序的变异契约，以及前端精确 payload 测试。
- 实现前进一步确认：单条 GET 是 Owner 明列的回显面；生成文件和 mutation handler 仍需结构性拆分，不能仅靠“尽量少加代码”规避预算。

## 实现决策

1. 请求 DTO 以 `json.RawMessage` 保留字段是否出现及显式 `null`；共享规范化包返回三门值和三个 `Set*` 标志。
2. create 对省略字段规范化为 `[]`、`{}`、`[]`；update 只在对应 `Set*` 为真时写列。
3. 字符串数组拒绝 `null`、错误元素类型和 trim 后空字符串；存储 trim 后的稳定值。`param_override` 拒绝 `null`、数组、标量、空白键，并编码为 JSON object。
4. list/get/create/update 响应统一输出非 null 数组和 JSON object，避免 nil 数组或 `[]byte` base64 回显。
5. 将 Create/Update/SoftDelete 渠道查询从旧 catalog 查询文件拆到新 mutation 查询文件；手工同步新的生成 Go 文件，并把新 SQL 源加入 `backend/sqlc.yaml`。旧 catalog 文件保留 list/get。
6. update SQL 使用 `Set*` 加 `CASE WHEN`，避免未提交字段被清空；显式空数组/空对象会真实清除。
7. 前端数组输入采用逗号分隔，JSON 使用 textarea；validator 输出 object 而非字符串，并在请求函数之前阻断非法 JSON。
8. 真 PG 测试通过 admin 查询层写入，再经 registry 与现有消费者验证；不复制消费算法，不以 mock 替代数据库链路。

## Failure modes 与缓解

| Failure mode | 缓解 |
| --- | --- |
| SQL 少列、参数错位或 scan 错位 | 三字段使用不同 sentinel；create/list/update 精确回读；增加 SQL 源/生成码契约测试。 |
| update 省略字段误清、显式空值不清 | 每字段独立 `Set*`；单字段更新保二测试与显式清除单测。 |
| JSON 可解析但顶层类型不符 | 服务端和前端都要求 non-null object；store/API 零调用断言。 |
| registry 更新后读旧值 | 同一 registry 实例先后解析，update 后直接验证新请求/输出；当前 no-op cache 结论写入交付。 |
| 审计无法复盘或泄漏机制扩大 | 沿用既有 audit snapshot，只加入本票字段和值/presence；不触碰 Hermes 或新建事件体系。 |
| admin 包或生成文件继续膨胀 | 校验下沉职责包；mutation 查询拆文件；运行 codebudget；不改 baseline。 |
| 前端表单看得到但 payload 漏字段 | 控件 SSR 断言、validator 精确对象断言、API body 精确断言。 |
| 测试只证明 helper | 真 PostgreSQL 测试跨 admin 查询层、registry 和三个现有消费者。 |

## Pre-execution checklist

1. 核对 migration、registry SQL、decode、运行时消费、现有路由、审计和缓存事实。
2. 检查并认领完整文件集；将独立规划会话提出的 GET 按 Owner 原文收敛为租户隔离的正式能力，并清理超预算内联代码。
3. 设置 `GOCACHE=/home/ubuntu/HUAKAI/.gocache` 与 `GOTMPDIR=/home/ubuntu/HUAKAI/.gotmp`。
4. 先写/调整判别性测试，再逐层接 SQL、handler、OpenAPI、前端与真 PG。
5. 不执行任何被 Owner 禁用的 git 或生成命令。

## Concrete execution order

1. 补齐 GET route/query/interface/test 与 OpenAPI，确保 tenant_id、id、软删围栏完整。
2. 新建独立校验包及单测，接入 create/update DTO、presence、审计和响应转换。
3. 拆分 mutation SQL/生成码，同步 list 三字段和 `sqlc.yaml`，写 SQL/生成码契约测试。
4. 更新 handler 单测：create 精确三门、update 改一保二、非法结构 400、list/get 精确回显、审计快照。
5. 更新 OpenAPI item/create/update schema及一致性门。
6. 更新前端 types、validator/API、表单回显、用途文案和 Vitest。
7. 增加真 PostgreSQL admin 写入到 registry/三个消费者的联动测试。
8. 运行 gofmt、定向 Go 测试、真 PG、`cmd/gateway`、codebudget、build/vet、前端 Vitest/tsc 与 `git diff --check`。
9. 不暂存、不提交；释放协调锁，汇报证据后停下等审查。

## 判别性变异契约

- 删除任一 INSERT 列/参数：create 后 list/get/数据库精确回显缺门，断言红。
- 删除任一 UPDATE 映射或 `Set*`：目标门不生效；若误清，另外两门保持断言红。
- 删除服务端 `param_override` 顶层 object 校验：非法 JSON/array/scalar 不再 400，store 零调用断言红。
- 删除数组元素校验：数字、object 或空白字段名越过 400，表驱动断言红。
- 删除 registry 解码或请求消费接线：`foo` 未剥离或覆盖值未生效，真 PG 联动断言红。
- 删除输出敏感词接线：sentinel 词未混淆，真 PG 联动断言红。
- 删除前端任一字段映射或 JSON 拦截：精确 payload 或 API 零调用断言红。
- 将响应中的 JSONB 当 `[]byte` 返回：JSON object 类型断言红。

## 风险与 Owner 决策

- 安全风险为管理员可配置项会影响真实上下游载荷；本票用严格结构校验、租户隔离和既有审计约束，不添加参数 allowlist 以免缩水既有能力。
- clean-room 风险低：未读参考源码，未复制外部标识、结构、注释或算法。
- 当前无执行中途需要 Owner 选择的高风险项。若事实迫使修改 schema、认证/计费/配额核心、部署或新增运行时依赖，立即停下请求确认。
