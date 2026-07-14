# 2026-07-14 P2-b 渠道三门写口通电（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI P2-b · 渠道三门写口通电——反封锁/合规改写字段能配了”；“本工作单属已批修复 arc P2，直接实现”；“改完停下等审查”。 |
| Scope | 先以 HUAKAI migration、查询、registry 与运行时消费代码核实三列归属和格式，再为渠道 catalog 的 create/update/list/get 接通 `body_param_strips`、`param_override`、`sensitive_words`；同步 SQL 源与手改生成 Go 码、后端 presence-aware 校验、响应、审计与缓存结论、OpenAPI、前端 create/edit 控件与类型/API、后端/真 PostgreSQL/消费联动/Vitest 判别性测试。 |
| Out of scope | 不新增或修改迁移，不执行 `sqlc generate`，不修改 `Sidebar.tsx`，不改变运行时改写算法，不新造审计或缓存机制，不新增运行时依赖，不触碰认证/计费/配额核心，不读取借鉴项目源码，不执行 `git add` / `git commit` / `git checkout`。 |
| Success criteria | 三字段在 create 与 update 均可配置；create 缺省为空/透传，update 缺省保持原值；合法值精确落库并在 create/update/get/list 回显；非法 JSON、错误 JSON 类型、非法数组项返回 400 且不调用写库；OpenAPI 与前端一致；真 PostgreSQL 证明写库值经 registry 进入请求参数剥离/覆盖和输出敏感词改写消费链；指定 Go、codebudget、PG、Vitest、tsc 门全绿。 |
| Time estimate | 墙钟约 5–8 小时；单 agent 工时约 7–10 小时，主要不确定性来自现有 SQL 扫描面、真 PG fixture 与前端页面测试结构。 |
| Blast radius | 管理端渠道 catalog 创建、部分更新、列表/详情响应；admin sqlc 参数和扫描顺序；registry 渠道绑定配置刷新；网关请求/响应改写配置；catalog create/edit 前端和 OpenAPI 契约。 |
| REFERENCE PROJECTS IN SCOPE | `CLIProxyAPI` + `sub2api` + `new-api`。本轮为 implementer 车道，仅使用 Owner 已给行为要求和 HUAKAI 内部事实从零接线；不读取三者源码，不新增关于三者的行为断言。 |

## 独立设计判断

1. 三字段不是自由形态透传：后端必须在 handler 边界区分“字段缺席”和“字段出现”，并按消费侧实际接受的结构完成严格校验，不能把错误留到 registry 静默降级。
2. create 与 update 应复用同一套解码/规范化逻辑；update 为每个字段保留出现性，避免仅更新一门时把另外两门清空。
3. 响应应扫描数据库真实值，不使用请求体拼装成功回显；这既锁定 SQL 映射，也使变异测试能抓住少列、错位和未更新。
4. 真 PostgreSQL 测试必须跨过管理写口、数据库、registry 解析和运行时消费边界；仅测试 helper 或 mock store 不足以证明“半死开关”已通电。
5. 前端可以沿用现有表单局部状态和控件：字符串数组以逗号分隔输入并在提交时去空白、拒绝空项/重复项的处理以既有后端规范为准；`param_override` 用文本域，提交前解析并要求 JSON object。
6. 若 registry 在 mutation 后依赖已有渠道 catalog 失效机制，则复用并以测试/代码证据记录；若没有缓存，不添加伪失效调用。审计同样只扩展既有资源快照或 metadata，不引入新事件体系。

## 失败模式与缓解

| 失败模式 | 缓解 |
| --- | --- |
| `sensitive_words` 实际不在 `channels` 或类型与工单假设不同 | 在任何业务修改前核实 0109/0129 migration、registry SELECT/join 和生成 row；以事实决定写 SQL，若必须跨表或改 schema 则暂停。 |
| Go 的零值无法区分 update 缺席与显式清空 | 使用指针或仓库既有 optional/presence 类型；分别测试缺席、空数组/空 object 和非空值。 |
| JSON 可解析但结构不符合消费侧预期 | `param_override` 明确要求顶层 object，数组字段明确要求字符串数组及元素约束；类型错误在写库前 400。 |
| INSERT/UPDATE/RETURNING/SELECT 与生成码字段顺序漂移 | 同步源 SQL、查询常量、Params、Row、Scan 顺序；使用每门不同 sentinel 的真 PG round-trip 断言。 |
| update 使用 `COALESCE` 吞掉显式清空或误清未提交字段 | 为“是否提交”和值分别建参数，或沿用当前动态 SET 机制；测试“改一保二”及显式清空。 |
| handler 审计记录请求成功却不含新配置变更 | 先核实现有审计取 mutation 请求、DB 响应还是固定 metadata；只沿用同一机制扩展可审计字段，并避免记录敏感凭据。 |
| registry 读缓存导致更新后短期仍消费旧配置 | 追踪 catalog mutation 后的失效/重载调用；若存在缓存则复用并增加更新后读取断言，若每次从 DB 构建则记录无额外失效需求。 |
| 真 PG 测试只断言数据库值，没有打到消费代码 | fixture 使用不同 sentinel：`body_param_strips=["foo"]` 后断言请求体 `foo` 消失且其他字段保留；`param_override` 断言值被覆盖；`sensitive_words` 断言输出词被实际改写。 |
| 前端文本框产生“看似合法”的错误 payload | codec 单测精确断言三字段 payload；非法 JSON、非 object JSON 在调用 API 前被拦截。 |
| admin 包已超 codebudget，新增生产文件继续膨胀 | 优先在现有内聚 handler/前端文件做最小接线；若文件或包超过增长余量，按现有职责子包放置 helper，不重写 baseline。 |
| 并行会话覆盖相同文件 | 修改前逐文件执行 `.coordination/check.sh` 并认领完整目标集；发现活锁即等待或避让，不覆盖。 |

## 决策点

Owner 已固定字段、链路、测试和禁区，正常实现无待选方案。仅在以下事实出现时暂停请求 Owner：三列并非既有 schema、正确写口必须修改数据库结构；需要改变认证/计费/配额核心；必须新增运行时依赖；或目标文件被其他 live agent 持有且无法安全错开。字段类型、SQL 表归属、现有缓存/审计机制属于起手核实项，按仓库事实裁决并记录，不把未观察结论写成事实。

## 预执行检查清单

1. 完整读取 `AGENTS.md`、`docs/RULES.md`、测试质量规则与 `.coordination/README.md`。
2. 检查工作树和 live locks；计划合成后认领所有将修改的文件，并定时续约。
3. 用 0109/0129 migration 核实列名、表名、类型、默认值；追踪 registry SELECT/join 到三个消费点，保留最终 `file:line` 证据。
4. 读取 catalog handler/store/SQL/生成码/response/OpenAPI/前端 create/edit/tests 的现有部分更新、错误响应、审计与样式惯例。
5. 明确缓存拓扑和 mutation 后失效链；在写测试前确定可观察的消费联动入口。
6. 设置 `GOCACHE=/home/ubuntu/HUAKAI/.gocache`、`GOTMPDIR=/home/ubuntu/HUAKAI/.gotmp`；不改 `.gitignore`，除非事实显示现有条目缺失且 Owner 已说明应存在。

## 具体执行顺序

1. 完成负向证伪：列归属/类型、registry 读源、解码格式、请求/响应消费、审计和缓存链逐项形成事实表。
2. 先补后端解码/校验与 handler 判别性单测：合法三门、错误顶层类型、错误元素类型、update 缺席/显式清空语义。
3. 扩展 create/update DTO 和共享规范化逻辑；保持 handler 流程与现有错误结构，非法输入在 store 前返回 400。
4. 同步 INSERT/UPDATE/RETURNING/list/get 的 SQL 源和已生成 Go 码，逐位核对参数、列和 scan 顺序；接通 create/update/list/get response。
5. 沿用现有审计与 registry 刷新/失效机制接线；若无需附加操作，以代码和测试证据记录结论。
6. 更新 OpenAPI 两个 mutation 请求及所有实际返回 schema；运行 `go test ./cmd/gateway/` 发现并修正契约漂移。
7. 在 `frontend/src/features/catalogs/` 同步类型/API，给 create/edit 添加两组数组输入和一个 JSON object 文本域，详情/编辑加载时回显；复用已有样式和错误提示。
8. 增加前端完整渲染、初始回显、精确 payload、update 未提交保持，以及非法/非 object JSON 不调用 API 的 Vitest。
9. 增加真 PG create→get/list、update 改一保二，并从 registry 构建实际绑定后调用参数剥离/覆盖与输出改写的联动测试；fixture 使用唯一 tenant/catalog/channel 标识并按现有清理惯例回收。
10. 运行 gofmt、定向 Go 测试、真 PG、`go build`/`go vet`、`go test ./cmd/gateway/`、codebudget、Vitest、tsc、`git diff --check`；不 stage、不 commit，释放协调锁，停下等审查。

## 判别性变异契约

- 删除 `INSERT` 任一门列或参数：create 后数据库重读及 get/list 精确回显中该门恢复默认，测试必须变红。
- 删除 `UPDATE` 任一门映射或出现性标志：更新目标门的精确值/消费联动不再变化，测试必须变红；若实现误清，另外两门 sentinel 保持断言变红。
- 删除 `param_override` JSON 解析或顶层 object 校验：非法 JSON或 array/scalar 请求不再 400，store 未调用/前端 API 未调用断言变红。
- 删除数组元素类型校验：混入数字、object 或不允许空字段名的请求越过 400，测试变红。
- 删除 registry 对 `body_param_strips` 或 `param_override` 的读取/解码：真 PG fixture 经 registry 后 `foo` 未被剥离或参数未覆盖，消费断言变红。
- 删除输出改写对 `sensitive_words` 的接线：真 PG 配置的 sentinel 词保持原样，输出断言变红。
- 删除前端任一控件或 payload 映射：控件 key-set/精确 payload 深比较变红；删除前端 JSON 拦截则 API 未调用断言变红。

## 风险记录

- 安全：三门会改变真实上下游载荷；严格限制为顶层字段名数组和 JSON object，并保持管理员权限/既有审计，可避免任意结构或错误类型静默进入请求链。具体可覆盖哪些参数不在本票额外设 allowlist，因为那会缩水既有已消费能力。
- clean-room：本轮不读取参考源码，不复制外部标识、结构、注释或算法；仅从 HUAKAI 既有 schema、实现和 Owner 明示契约完成内部接线，预期无新增 clean-room 污染面。
- 功能 preservation：三门全部同时接通 create、update、readback、UI 与消费链，不因风险删除或隐藏任一门。
