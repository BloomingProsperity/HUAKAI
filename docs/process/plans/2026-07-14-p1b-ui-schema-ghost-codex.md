# 2026-07-14 P1-b UI 欺骗控件摘除与 schema 幽灵标注（Codex 独立计划）

> 独立性声明：本计划仅依据 Owner 本轮工作单、HUAKAI 内部规则与当前仓库事实独立起草；起草前未读取任何同主题 Claude 计划。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “【HUAKAI P1-b · 摘除 UI 欺骗控件 + 无消费 schema 幽灵标注(零 schema 动作)】……改完停下等审查。” |
| REFERENCE PROJECTS IN SCOPE | `CLIProxyAPI`、`sub2api`、`new-api`。本工作单是 HUAKAI 内部清理，Owner 明确无需读取镜像；计划与实现不作任何参考项目行为断言。 |
| Scope | 先对指定字段、列族与整表做全仓消费者核验；只对确认无运行时消费的前端控件、列表列和 payload 做摘除；保留前端兼容类型与后端 DTO/SQL；补中文兼容注释、处理器兼容测试；新增 `docs/architecture/deprecated-schema.md`，记录经核验的 schema 幽灵及启用缺链。 |
| Out of scope | 不改 schema、迁移、SQL 查询、生成码、OpenAPI、后端运行时语义、`Sidebar.tsx`；不运行 `sqlc generate`；不执行 `git add`、`git commit`、`git checkout`；不删除任何字段、列、表或既有功能。 |
| Success criteria | 每个负向结论有可复核的 `rg` 证据；若发现消费者则跳过并记录；死控件不在 DOM 和 payload 中，有效字段仍提交；旧客户端携带死字段时两个后端处理器均不返回 400；指定定向测试、`tsc --noEmit`、Go 定向测试、`go build`、`go vet` 全绿；最终差异未触碰禁止范围。 |
| Time estimate | 约 90–150 分钟墙钟时间；单 agent 约 2–4 工程小时，主要不确定性是 26 个 `routes` 列的逐列迁移溯源和“无消费”证伪。 |
| Blast radius | 前端模型绑定与渠道管理表单/列表；registry 与渠道 admin DTO 附近注释及对应处理器测试；架构文档。失败可能造成前端合法字段漏交、旧客户端请求被拒或文档误标仍在消费的列。 |
| Risk level | 中风险 UI/API 兼容清理。数据库结构、鉴权、计费、配额与迁移均保持只读，因此不进入高风险写路径。 |

## 事实核验与处置原则

1. 对每个候选字段区分四类命中：迁移/schema、生成码/SQL CRUD、前端类型/表单、真正运行时决策链。
2. 仅有存储、序列化、管理 CRUD、测试夹具或文档命中，不算运行时消费；但必须在最终证据中说明分类依据。
3. 任一候选若被选号、gate、failover、计费、配额、moderation、affiliate 结算或其它生产决策读取，则从本次摘除/幽灵清单移出，保留现状并报告消费者位置。
4. 不修改生成码；业务注释只落在非生成 Go 类型/DTO 或真实业务引用点。

## 判别性测试契约

| 场景 | 前置条件与动作 | 必须断言 | Mutation 自检 |
| --- | --- | --- | --- |
| 模型绑定创建/编辑 | 打开表单并提交仍有效字段 | `weight`、`max_parallel_requests`、`fallback_class` 不在 DOM 且不在请求 body；`priority`、`selection_mode` 等现存有效字段仍按原值提交 | 把任一死控件和 payload 映射加回，DOM 或 body 断言必须变红 |
| 渠道创建/编辑 | 打开渠道表单并提交有效字段 | `failover_status_codes` 不在 DOM 且不在请求 body；名称、池组、启用状态等有效字段照常提交 | 把死控件和 payload 映射加回，DOM 或 body 断言必须变红 |
| 模型绑定 API 兼容 | 老客户端请求显式携带三个历史字段 | 处理器通过解码/校验，不因这些字段返回 400，并走到预期 store 分支 | 删除 DTO 字段或改为拒绝未知字段，测试必须变红 |
| 渠道 API 兼容 | 老客户端请求显式携带 `failover_status_codes` | 处理器不返回 400，并走到预期 store 分支 | 删除 DTO 字段或改为拒绝未知字段，测试必须变红 |

## Failure modes 与缓解

| Failure mode | 后果 | 缓解 |
| --- | --- | --- |
| 把 CRUD/生成码命中误判为运行时消费 | 应摘控件被错误保留，继续误导运营 | 对命中逐处读调用上下文，记录“存储边界”与“决策读取”差异 |
| 把真实运行时消费者漏掉 | 摘除仍有效能力或把活列误标废弃 | 字段名、Go/TS 名、JSON 名、SQL 名多形态搜索；沿调用链读到决策点 |
| payload 通过对象展开继续夹带死字段 | UI 看似摘除但网络请求仍发送 | 测试精确捕获请求 body，断言 key 不存在而非值为空 |
| 测试只断言“不等于坏值” | 测试误绿 | 同时断言有效字段精确值，并让 fixture 中有效值与默认值不同 |
| 误改迁移/schema/生成码 | 违反零 schema 工作单 | 编辑前后用路径清单和 `git diff --name-only` 双重检查；禁止运行生成器 |
| 并行 agent 覆盖文件 | 丢失他人改动 | 每批编辑前按 `.coordination` 精确认领目标文件；冲突即停并报告 |

## Decision points

本工作单已由 Owner 明确给出逐项处置原则，无待选架构分支。唯一事实分支是：若核验发现运行时消费者，则该项自动跳过并进入报告，不需要擅自拆除。因为没有需要 Owner 选择的方案，本计划不引入参考项目对照决策。

## Pre-execution checklist

1. [x] 读取 `AGENTS.md`、`docs/RULES.md`、测试质量规则与并行编辑协议。
2. [x] 检查工作树和活跃编辑锁，识别既有用户/其它 agent 改动。
3. [x] 盘点模型绑定、渠道前端组件/类型/API/测试与后端 DTO/处理器测试。
4. [x] 多形态搜索 A 项字段，逐处区分 CRUD 存取与生产决策读取。
5. [x] 从迁移定义提取 B 项精确列名与建表/加列迁移，逐项搜索非 schema 消费者。
6. [x] 将预计修改的精确文件加入协调锁，发现冲突则停止对应文件编辑。
7. [x] 先补或更新会因旧行为而失败的判别性测试，再做最小实现改动。
8. [x] 新增架构文档，只收录证据支持的无消费项；有消费者的候选明确移出并报告。
9. [x] 用指定 Go 缓存运行定向测试、全量 build/vet；运行受影响 Vitest 与 TypeScript 检查。
10. [x] 审查最终 diff：无迁移/schema/生成码/`Sidebar.tsx`，无 git 写操作，中文 Go 注释合规。

## Concrete execution order

1. 建立候选字段到定义、CRUD、前端、运行时决策链、测试的证据表。
2. 确定最终可摘字段与最终幽灵清单，并锁定具体编辑文件。
3. 调整前端判别性测试，确认旧实现能被断言捕获。
4. 最小摘除表单 DOM、列表列和请求 payload 映射，保留可选类型字段。
5. 在非生成后端 DTO/业务类型旁补中文兼容注释，并各补一条老客户端处理器测试。
6. 编写 `deprecated-schema.md`，逐项记录迁移来源、当前状态和未来启用链路。
7. 运行格式化、定向测试、TypeScript、Go build/vet，并检查禁止范围与字段残留。
8. 输出中文 Owner 报告，列出每字段无消费证据、改动文件、mutation 说明、风险和需确认项；不提交，停下等审查。

## 执行结果

- 已确认 A 项字段只有存储、管理 CRUD、兼容序列化或只读诊断投影，不进入生产决策；没有候选项因发现运行时消费者而移出。
- 已按工作单摘除前端控件、列表列与 payload 映射，保留可选接口类型和后端兼容接收能力。
- 已产出 26 个 `routes` 列及其余指定表/列族的保留清单；未修改迁移、schema、SQL 生成码或 `Sidebar.tsx`。
- 定向 Go 测试、代码体量门、受影响 Vitest、TypeScript、`go build ./...` 与 `go vet ./...` 已通过。全量 `go test ./internal/adminhttp -count=1` 的一个既有用例因当前沙箱禁止本地监听而无法执行；同包渠道用例已用 `-run '^TestChannelCatalog'` 独立通过。
