# 2026-07-14 P2-a 账号高级配置通用化（合成执行计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “账号高级配置通用化通电——create/update 合并统一字段集 + 前端自动渲染（治本骨架）”；“本工作单属已批修复 arc P2 首片，直接实现”。 |
| 独立输入 | `2026-07-14-p2a-account-advanced-config-codex.md` 与 `2026-07-14-p2a-account-advanced-config-independent-2.md`。外部 Claude CLI 在限定等待内没有产生输出或文件，已安全中止；第二份来自隔离规划会话，本文不把它伪称为 Claude 产物。 |
| Scope | 既有账号列的统一高级字段契约、create/update presence-aware 写入、SQL 源与手工生成码同步、list/get/create/update 回显、OpenAPI、create/edit 共用数据驱动折叠区、跨层一致性与判别性测试。 |
| Out of scope | 不新增迁移，不执行 `sqlc generate`，不改运行时 gate/选号/刷新/代理/TLS 算法，不改认证/计费/配额核心，不改 `Sidebar.tsx`，不读借鉴项目源码，不执行 `git add` / `git commit` / `git checkout`。 |
| Success criteria | 14 个字段在后端规格、create/update、持久化、四类响应、OpenAPI 和前端形成闭环；缺席与显式 `0`/`false`/`[]`/`null` 可区分；create 默认不翻转、update 不误清；字段集守卫和指定 Go/PG/Vitest/tsc/codebudget 门通过。 |
| Time estimate | 墙钟约 6–10 小时，取决于手工 sqlc 同步、现有前端测试扩散和本地 PostgreSQL 可用性。 |
| Blast radius | 管理端 provider account 创建/更新/读取、账号写 SQL 和扫描顺序、OpenAPI、账号 create/edit UI；运行时仅做既有消费链验证。 |
| REFERENCE PROJECTS IN SCOPE | `CLIProxyAPI` + `sub2api` + `new-api`。本轮 implementer 车道只使用 Owner 行为要求及 HUAKAI 内部事实，不对借鉴项目作新行为断言。 |

## 双计划交叉讨论结果

### 一致意见

1. 统一字段集固定为 `rpm_limit`、`tpm_limit`、`window_cost_limit_cents`、`max_sessions`、`disable_cooling`、`refresh_lead_seconds`、`expires_at`、`tls_fingerprint_rotate`、`custom_error_codes_enabled`、`custom_error_codes`、`pool_mode`、`temp_unschedulable_enabled`、`temp_unschedulable_rules`、`proxy_binding`。
2. `tls_fingerprint_rotate` 已在 HUAKAI 内部确认列和消费点均存在，因此纳入，不作待定项。
3. 输入必须记录字段是否出现：缺席时 create 沿用 DB 默认等价语义、update 保持旧值；显式 `0`、`false`、`[]` 必须生效；`expires_at` 和 `refresh_lead_seconds` 允许显式 `null` 清空。
4. create/update 共用规格、解析、校验和映射；响应必须来自 DB 重读，不用请求值伪装落库结果。
5. `gatewayhttp` 已超软预算常规线，统一逻辑必须进入内聚子包；主 handler 目标净缩，禁止扩大 baseline。
6. 前端 create/edit 共用一个折叠区，由静态 mirror 驱动；代理和临时停调规则保留结构化交互，不能降级为裸 JSON。
7. 必须有逐字段 create、update 改一保余、边界 400、跨层 key-set、真 PG 过期过滤/RPM 链路和前端精确 payload 测试。

### 分歧与裁决

1. **后端规格载体**：一份草案偏向三份 Go 文件，另一份偏向可执行单一规格。裁决为新子包 `gatewayhttp/accountadvanced`，以一份 `fields.json` 表达跨语言元数据、`fields.go` 负责嵌入、presence 解码、校验与 DB 映射；生产文件均控制在 600 行内。前端保留静态 mirror，并由测试与 JSON 深比较，而非运行时读取后端文件。
2. **create 默认实现**：一份草案建议 CTE 先插入再条件更新；另一份建议沿用当前单语句 `COALESCE`。裁决为保持现有 Insert 形态，以 nullable/presence 参数和 SQL `COALESCE` 对齐既有列默认，并用真 PG 默认等价测试锁住。原因是所有默认均已确认，CTE 会增加 SQL/手工生成码风险而没有新增用户收益。
3. **数值上界**：后端以数据库真实类型为准（bigint 为 `MaxInt64`、integer 为 `MaxInt32`）；前端拒绝超过 `Number.MAX_SAFE_INTEGER` 的 bigint 输入，避免 JavaScript 精度损失。所有负值和非整数均拒绝。
4. **代理回显**：新增规范化 `proxy_binding`，同时保留 `proxy_id` / `proxy_group_id` 兼容字段；direct/proxy/group 三态构造性互斥。
5. **规则回显**：`temp_unschedulable_rules` 在 list/get/create/update 全部返回，满足 Owner 的统一回显要求，不保留“仅详情”例外。
6. **过期时间校验**：允许过去时间，用于立即停用和 Owner 指定的真 PG 判别测试；只校验 RFC3339/可空语义，不强制未来时间。

### 单边发现并纳入的缺口

- 将 update named query 放回 `backend/sql/queries/admin_provider_account_mutations.sql`，手工同步生成 Go，并从手写 admin read 文件移除重复 update SQL，减少下一次漂移面。
- list/get/create/update 统一 DTO；数组规范为非 nil 空数组，nullable 保留 `null`。
- 主 handler 当前仅有约 5% 基线余量，不能在根包再加生产 Go 文件；测试文件不计预算。
- 前端新折叠区必须显示留空/沿用语义、当前值和单位，错误就地反馈；不引入依赖、不复制外部 UI。

## 字段语义基线

| JSON key | 类型与范围 | create 缺席 | update 缺席 | 显式清空/关闭 |
| --- | --- | --- | --- | --- |
| `rpm_limit` / `tpm_limit` / `window_cost_limit_cents` | bigint，`0..MaxInt64` | DB 默认 0 | 不变 | `0` 表示不限 |
| `max_sessions` | integer，`0..MaxInt32` | DB 默认 0 | 不变 | `0` 表示不限 |
| `disable_cooling` / `tls_fingerprint_rotate` | boolean | DB 默认 false | 不变 | `false` 可关闭 |
| `refresh_lead_seconds` | nullable integer，`0..MaxInt32` | NULL | 不变 | `null` 清除账号覆盖 |
| `expires_at` | nullable RFC3339 | NULL | 不变 | `null` 清除过期时间 |
| `custom_error_codes_enabled` / `pool_mode` / `temp_unschedulable_enabled` | boolean | DB 默认 false | 不变 | `false` 可关闭 |
| `custom_error_codes` | 100..599 整数数组 | `[]` | 不变 | `[]` 清空 |
| `temp_unschedulable_rules` | 已有规则数组 | `[]` | 不变 | `[]` 清空 |
| `proxy_binding` | direct/proxy/group 联合类型 | direct | 不变 | direct 清两列 |

## 失败模式与缓解

| 失败模式 | 缓解 |
| --- | --- |
| JSON 零值被误当缺席 | 通用 presence 解码 + 缺席/0/false/[]/null 表驱动测试。 |
| Insert 默认与 migration 漂移 | SQL 默认等价写法 + 真 PG 空高级字段重读测试；不在 handler 复制隐式默认。 |
| update 错位或误清 | 每个字段独立 Set-flag；全字段不同 sentinel 的“改一保余”测试。 |
| source SQL 与手改生成码漂移 | 查询常量、Params、参数顺序逐项核对；source/generated 守卫与真 PG round-trip。 |
| 前端 mirror/renderer/payload 漂移 | 精确 key/kind/nullability/range 守卫；每个 key 唯一 `data-advanced-field`；payload deep-equal。 |
| 共享 UI 降低现有可操作性 | 复用代理加载、组校验、规则行编辑和现有样式；展示留空语义与单位。 |
| codebudget 继续膨胀 | 新内聚子包，主 handler 净缩；运行标准 codebudget 门，不重写 baseline。 |
| 真 PG 服务不可用 | 先完成所有非 PG 门；重试明确 DSN，并把连接失败作为环境阻塞如实报告，禁止用 `t.Skip` 冒充通过。 |

## 决策点

Owner 已固定本切片方案，以上技术分歧均可在已授权范围内裁决。仅当发现所需列不存在、必须新增 schema、必须改变配额/计费/认证核心或新增依赖时暂停请求 Owner；不能以风险为由静默删除字段。

## 预执行检查清单

1. 检查 `.coordination` 活锁，认领所有目标文件并避开并行任务。
2. 复核工作树，只修改本票文件，不覆盖用户或其他 agent 变更。
3. 对 14 个字段逐列核实 migration/列、读 SQL、运行时消费和现有 API/UI，最终报告保留 `file:line`。
4. 先建立规格与判别性单测，再接 SQL/handler/DTO，随后 OpenAPI/前端，最后真 PG 链路。
5. 设置 `GOCACHE=/home/ubuntu/HUAKAI/.gocache`、`GOTMPDIR=/home/ubuntu/.gotmp`；仅缺失时补 `.gitignore`。
6. 全程不执行 `sqlc generate`、`git add`、`git commit`、`git checkout`。

## 具体执行顺序

1. 新建 `accountadvanced` 规格/presence/校验/映射及其测试；测试精确覆盖 14 key 与边界。
2. 扩充 Insert/Update source SQL 和手工生成码；扩充 admin row/columns/scanner，并移除重复手写 update 定义。
3. handler 的 create/update 嵌入同一高级输入，调用共享校验/映射；统一 response/list/detail DTO。
4. 补 handler 判别测试和真 PG create/update/readback、过期筛选、RPM snapshot/gate 链路。
5. 补 OpenAPI 请求/响应与 `cmd/gateway` 跨层字段集守卫。
6. 新建前端静态 mirror、codec 与共用 `AccountAdvancedSettings`；接入 create/edit，补类型和页面 tenant 传递。
7. 增加完整渲染、精确 payload、mirror 对齐测试；保持代理和规则的结构化 UI。
8. 执行 gofmt、定向测试、真 PG 测试、`go build`、`go vet`、`go test ./cmd/gateway/`、codebudget、Vitest、tsc、`git diff --check`；记录每个门结果。
9. 自审每个 mutation 是否真正被断言杀死；不 stage、不 commit，释放协调锁并停下等审查。

## 判别性变异契约

- 删除任一 INSERT 映射：对应 create 精确回显/真 PG 重读断言必须变红。
- 删除任一 UPDATE 映射或错置 Set-flag：目标字段更新或“其他字段完全保持”断言必须变红。
- 删除负数/上界/时间/规则/代理校验：相应 400 且 store 未调用断言必须变红。
- create 忽略 `pool_mode`、`custom_error_codes` 或 `proxy_binding`：建号后立即回显与消费链断言必须变红。
- 删除 `expires_at` 候选过滤：过期高优先级账号被错误选中，真 PG 断言必须变红。
- 删除 RPM SELECT/scan/snapshot/gate 任一环：`rpm_limit=1` 的第二次 precheck 不再拒绝，真 PG 链断言必须变红。
- 前端 mirror 少一字段、renderer 漏一种 kind 或 codec 丢 0/false/[]/null：key-set、完整渲染或 payload deep-equal 必须变红。
