# 2026-07-14 P2-a 账号高级配置通用化（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI P2-a · 账号高级配置通用化通电——create/update 合并统一字段集 + 前端自动渲染（治本骨架）”；“本工作单属已批修复 arc P2 首片，直接实现”。 |
| Scope | 在既有列和现有 API 内，建立后端高级字段规格、create/update 共享解析校验与映射、sqlc 源 SQL 及已生成 Go 码手工同步、响应回显、OpenAPI 契约、前端静态 mirror 渲染及字段集一致性守卫、判别性单测与真 PostgreSQL 联动测试。 |
| Out of scope | 不新增迁移，不执行 `sqlc generate`，不修改 `Sidebar.tsx`，不修改计费账本/配额核心/认证核心，不读取借鉴项目源码，不执行 `git add` / `git commit` / `git checkout`。 |
| Success criteria | 高级字段清单包含 Owner 指定且经 HUAKAI 内部消费点核实的既有列；create/update 共用一套字段解析与校验；未提交字段不改变现值，create 不填沿用 DB 默认；list/get 完整回显；create/edit 共用折叠区自动渲染；后端、OpenAPI、前端 mirror 有可执行的字段集一致性守卫；指定构建、vet、Go/真 PG/Vitest/tsc/codebudget 门通过。 |
| Time estimate | 墙钟 4–7 小时；单 agent 工时 5–8 小时，取决于既有 handler/sqlc/OpenAPI 生成链的扩散面。 |
| Blast radius | 管理端 provider account 创建、更新、列表/详情响应，账号池选号可用性，前端账号创建/编辑表单，OpenAPI 一致性门。 |
| REFERENCE PROJECTS IN SCOPE | `CLIProxyAPI` + `sub2api` + `new-api`。本轮是 implementer 车道，仅依 Owner 已提供的行为要求与 HUAKAI 内部代码从零实现，不对三者行为做新的事实断言，不读取其源码。 |

## 功能形态清单

- 角色：管理员。
- 路径：创建账号时预置高级配置；编辑时部分修改/显式清空；list/get 回显；前端 create/edit 同一折叠区；选号/gate/凭据刷新消费。
- 状态：字段缺席（create 使用 DB 默认，update 保持不变）、显式设值、可清空字段的显式 `null`/空值（以现有 API 惯例为准）、负值/超范围被 400 拒绝。
- 类型：整数限制、布尔开关、数组/规则、时间点、代理绑定。
- 失败路径：非法类型/越界值不落库；任一 SQL 映射遗漏必须被回显断言抓住；前后端清单漂移必须被一致性测试抓住。

## 失败模式与缓解

| 失败模式 | 缓解 |
| --- | --- |
| Go DTO 的零值无法区分“缺席”与“显式清零/false/null” | 沿用现有 optional/null 表示法；若不足，用局部 optional 类型保留 JSON 出现性，用部分更新测试锁定。 |
| create 为缺席字段主动写零值，意外覆盖 DB 默认 | INSERT 使用 nullable/布尔出现性参数与 SQL `COALESCE`/column default 惯例；对不填的 create 增加回归断言。 |
| update 因 `COALESCE` 无法显式清空 nullable 字段 | 核对现有 clear 语义；必要时使用“是否提交 + 值”双参数，不用单纯 `COALESCE` 吞掉显式 null。 |
| sqlc 源 SQL 与手改生成码漂移 | 对查询常量、Params、Scan/Exec 顺序逐位核对；不运行 generator；相关 Go 测试和 OpenAPI 门一起运行。 |
| “单一真相源”只是文档表，无法真正驱动各层 | 后端规格用于解析/校验/响应 schema 或可执行守卫，前端 mirror 直接驱动渲染；加跨层 key-set 测试，避免手工四份清单。 |
| `gatewayhttp` 超体量或 handler 超 600 行 | 高级字段规格/帮助器、测试分离到内聚文件；优先沿用现有账号管理子包；不扩大 baseline，codebudget 红则按职责拆分。 |
| 真 PG 测试破坏并行任务数据 | 使用唯一 tenant/account 标识与事务/清理惯例，不修改 schema，不清空共享表。 |

## 决策点

Owner 已固定主方案：前端静态 mirror + 一致性守卫、既有列、手改 sqlc 生成码、不新增迁移。预期无新 Owner 决策点；只有出现下列高风险事实时暂停：所需列实际不存在、必须改 schema，或必须改配额/计费/认证核心语义。因没有待 Owner 选择的 A/B/C 方案，本节不对借鉴项目做行为比较，以避免 implementer 车道产生无源事实断言。

## 预执行检查清单

1. 读取当前 `.coordination/` 锁，按确定文件集广播意图；不覆盖他人改动。
2. 检查工作树现有变更，记录并避开与本工作单无关的用户改动。
3. 逐列 `rg` 核实 migration/schema、读取点、消费点、现有 DTO/SQL/response/OpenAPI/前端表单，保留最终 `file:line` 证据。
4. 核对当前包/文件 codebudget 和前端测试约定，再锁定新文件目标。
5. 先写共享字段规格与校验的判别性测试，再接 SQL/响应，最后接前端和 OpenAPI。
6. 设置 `GOCACHE=/home/ubuntu/HUAKAI/.gocache` 与 `GOTMPDIR=/home/ubuntu/.gotmp`；仅当 `.gitignore` 缺少对应项时追加。

## 具体执行顺序

1. 盘点字段：`rpm_limit`、`tpm_limit`、`window_cost_limit_cents`、`max_sessions`、`disable_cooling`、`refresh_lead_seconds`、`expires_at`、`tls_fingerprint_rotate`、`custom_error_codes_enabled`、`custom_error_codes`、`pool_mode`、`temp_unschedulable_enabled`、`temp_unschedulable_rules`、`proxy_binding`；仅在 HUAKAI 列和消费点均存在时纳入可选条目。
2. 建立后端字段规格和 create/update 共用 optional 解析、类型/边界/组合校验，将 handler 只保留流程编排。
3. 同步 `InsertProviderAccount` / `UpdateProviderAccount` 的 `.sql` 和已生成 Go 查询码，核对列、占位符、Params 与返回 Scan 顺序。
4. 补齐 response 映射与 list/get 查询选列，保证新建/更新结果可立即回显。
5. 更新 OpenAPI 请求/响应 schema 和已生成前端类型（若仓库当前要求手工同步），运行 `go test ./cmd/gateway/` 契约门。
6. 在 `frontend/src/features/accounts/` 建静态高级字段 mirror 和共享折叠区，create/edit 同一数据驱动渲染，对数字/布尔/数组/时间/结构值复用现有控件样式。
7. 增加判别性测试：create 逐字段设值与建号残缺回归；update 改一保余；非法边界 400；真 PG 的 `expires_at` 选号过滤和可行的 RPM gate 联动；前端完整渲染、精确 payload、前后端 key-set 一致。
8. 执行 gofmt、定向 Go 测试、真 PG 测试、`go build`、`go vet`、`cmd/gateway`、codebudget、Vitest、tsc；记录任何环境性阻塞。
9. 用 `git diff --check` 和未分段 diff 做自审，不 stage、不 commit，释放协调锁，停下等待审查。

## 判别性变异契约

- 删除任意一个 INSERT 列/参数映射：对应 create 回显精确值断言变红。
- 删除任意一个 UPDATE 列/参数映射：对应 update 新值断言变红，且“其他字段保持原值”断言防止误清。
- 删除数值边界/组合校验：与正常值只差违规条件的请求不再返回 400，对应测试变红。
- create 忽略 `pool_mode` / `custom_error_codes` / `proxy_binding`：创建后立即 GET/list 的精确回显断言变红。
- 移除选号的 `expires_at` 过滤：真 PG 中只给过期号更高优先级的 fixture 会选中坏号，断言变红。
- 前端清单少一 key：字段集一致性守卫变红；提交适配器丢一值：payload 精确相等断言变红。
