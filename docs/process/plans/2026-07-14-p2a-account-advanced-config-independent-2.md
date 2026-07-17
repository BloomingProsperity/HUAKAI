# 2026-07-14 P2-a 账号高级配置通用化通电（独立计划 2）

| 项目 | 内容 |
| --- | --- |
| 状态 | 独立草案，仅供后续双计划对照与综合；本文件不授权直接实施 |
| 规划会话 | `codex-p2a-independent-plan-2` |
| Owner directive | “账号高级配置通用化通电——create/update 合并统一字段集 + 前端自动渲染。” |
| Scope | 账号 create/update 共用高级字段契约、解析/校验/持久化映射、读回显、OpenAPI、create/edit 共用前端高级设置、字段集一致性与判别性测试 |
| Success criteria | 明确字段在 create/update/list/get/OpenAPI/UI/运行时消费链闭环；缺席、显式 `0`/`false`/`[]`/`null` 语义可区分；所有指定 Go/PG/Vitest/TypeScript 门通过 |
| Time estimate | 约 10–14 agent-hours；单线 wall clock 约 8–12 小时，若后端与前端在综合计划批准后并行则约 6–9 小时，另留 1–2 小时处理交叉审查 S0/S1 |
| Blast radius | 管理端账号 POST/PATCH/GET/list、账号写 SQL 与扫描顺序、OpenAPI、账号 create/edit UI；运行时只验证既有消费链，不改选择/限流/刷新/代理/TLS 算法 |

REFERENCE PROJECTS IN SCOPE: CLIProxyAPI + sub2api + new-api

本轮只依据 Owner 行为要求与 HUAKAI 内部仓库现状规划，未读取 `/home/ubuntu/refs` 或任何借鉴项目源码，也不作任何借鉴项目行为断言。外部 Claude CLI 的存在、版本、能力与行为不属于本会话事实范围，本计划不涉及也不评价它。

## 1. 独立性、事实边界与不变量

1. 本会话没有读取 `docs/process/plans/2026-07-14-p2a-account-advanced-config-codex.md`；综合者应在两份独立草案均完成后再做差异对照。
2. 本轮只写本计划，不修改产品代码、不执行实现、不运行 `sqlc generate`，也不执行 `git add`、`git commit`、`git checkout`。
3. 不新增 migration，不改 `LICENSE`，不加 runtime/dev dependency，不接触真实凭据或生产数据。
4. 不动 `frontend/src/layout/Sidebar.tsx`。
5. 所有新增或修改的 `.go` 生产/测试注释、计划、审查说明使用中文；英文函数、类型、JSON key、SQL 关键字保留英文。代码注释不得出现借鉴项目名。
6. 这个切片只让已经存在的账号配置列可安全写入、读回并由 UI 操作；若实现时发现必须修改 quota/rate/session/refresh/proxy/TLS 的运行时算法，立即停在该扩 scope 决策点，不能把算法改动偷渡进控制面通电票。

## 2. HUAKAI 当前形状盘点

以下是本会话实际读取到的 HUAKAI 内部事实：

- `createProviderAccountRequest` 目前只覆盖基础调度字段；`updateProviderAccountRequest` 已覆盖 `custom_error_codes_enabled`、`custom_error_codes`、`pool_mode`、`temp_unschedulable_enabled`、`temp_unschedulable_rules`、`proxy_binding`，但没有本票的通用数值/布尔/时间字段（`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:141`、`:169`）。
- create 最终构造 `admindb.InsertProviderAccountParams`，update 则构造带 Set-flag 的 `UpdateAdminProviderAccountParams`；当前代理互斥逻辑仍在 handler 内（同文件 `:307`、`:500`、`:534`）。
- `AdminProviderAccountRow`、统一 SELECT 列表与 DTO 目前没有 `rpm_limit`、`tpm_limit`、`window_cost_limit_cents`、`max_sessions`、`disable_cooling`、`refresh_lead_seconds`、`tls_fingerprint_rotate`（`backend/internal/db/admin/admin_provider_accounts.go:9`、`:120`；handler `:210`、`:963`）。
- `InsertProviderAccount` 的源 SQL 和手工同步的 sqlc Go 文件已存在；SQL 已有 `expires_at`，但 handler create 请求没有暴露它（`backend/sql/queries/admin_provider_account_mutations.sql:1`；`backend/internal/db/admin/admin_provider_account_mutations.sql.go:14`）。update 全字段 SQL 当前是 `backend/internal/db/admin/admin_provider_accounts.go:338` 的手写查询，尚未与源 query 文件同位。
- `expires_at` 已由真 PG 候选查询过滤；`rpm_limit/tpm_limit`、窗口消费上限、会话数与 cooling 标记已从 `ListEligibleAccountsByPoolGroup` 进入 `AccountSnapshot` 和 gate 链（`backend/sql/queries/pool_accounts.sql:87`、`:125`、`:162`；`backend/internal/pool/dispatcher/account_source.go:81`；`backend/cmd/gateway/selector_wiring.go:222`）。
- `refresh_lead_seconds` 已被凭据加载/刷新路径消费；`NULL` 或 `0` 均回落到全局窗口，正数覆盖全局窗口（`backend/internal/credentialstore/postgres_store.go:775`、`:895`）。
- `tls_fingerprint_rotate` 同时存在于列与真实消费点：迁移 `backend/sql/migrations/0122_provider_account_tls_rotate.up.sql:6`，解析消费 `backend/internal/tlsfpresolve/resolver.go:100`。因此本计划把它纳入统一字段集，不再保留为待定项。
- OpenAPI 的 `ProviderAccountCreate`/`ProviderAccountUpdate`/`ProviderAccount` 只声明了部分现有字段，尚未覆盖本票字段和完整代理读回（`docs/openapi/openapi.yaml:17818`、`:17894`、`:17965`）。
- create/edit 当前各自维护高级区与 payload 逻辑，create 只显示基础调度参数，edit 手写了代理与错误策略区（`frontend/src/features/accounts/CreateAccountModal.tsx:186`；`frontend/src/features/accounts/EditAccountModal.tsx:138`）。
- codebudget 当前事实：`gatewayhttp` 基线为 13,835 行/33 非测试文件，5% 余量；当前约 14,093 行/34 非测试文件，新增同包非测试文件会把文件数推到 35 并越过 `33×1.05=34.65`。`admin_pool_accounts_handler.go` 基线 1,068 行，当前约 1,082 行，最多仅剩约 39 行余量。因此统一逻辑必须进新子包，并让主 handler 净缩减，严禁继续喂大包或重写放大基线（`backend/internal/codebudget/baseline.json`、`backend/internal/codebudget/budget_test.go`）。

## 3. Scope

### 3.1 In scope

1. 建立一个 HUAKAI 自有的账号高级字段规格表，作为后端字段 key、输入类型、可空性、数值范围、create/update 缺席策略和响应策略的唯一 Go 端定义。
2. create/update 嵌入同一个高级输入结构，并共用 presence-aware 解码、标准化、校验和 DB 参数映射。
3. 把以下字段纳入统一高级字段集：
   - `rpm_limit`
   - `tpm_limit`
   - `window_cost_limit_cents`
   - `max_sessions`
   - `disable_cooling`
   - `refresh_lead_seconds`
   - `expires_at`
   - `tls_fingerprint_rotate`（已确认列与消费点均存在）
   - `custom_error_codes_enabled`
   - `custom_error_codes`
   - `pool_mode`
   - `temp_unschedulable_enabled`
   - `temp_unschedulable_rules`
   - `proxy_binding`
4. create 补齐 update 已有的错误策略、pool 模式、临时停调规则和代理绑定；create 缺席沿用 DB 默认等价语义，update 缺席保持旧值。
5. source SQL、手工同步的 sqlc Go、admin 读模型/扫描/DTO、list/get/create/update 响应与 OpenAPI 同步。
6. create/edit 共用一个“高级设置”折叠组件；前端静态 mirror 驱动 number/boolean/number-array/rule-array/datetime/custom 输入和 payload 构建。
7. 增加 Go ↔ frontend mirror ↔ OpenAPI 的 key-set 一致性门，以及逐字段、缺席/清零、真 PG 运行时链路和完整渲染测试。
8. 保留现有租户 scope、混合渠道风险确认、凭据写入、审计和代理租户校验行为。

### 3.2 Out of scope

1. 新 migration、DB 列/约束/index 变更、`sqlc generate` 或全仓 sqlc 重生成。
2. 改动 account selection、RPM/TPM counter、window cost、session cap、cooldown、refresh lead、proxy resolver、TLS resolver 的业务算法或默认开关。
3. 新的高级字段、新的代理资源 CRUD、TLS profile CRUD、凭据格式、账户批量编辑或 Sidebar 导航改造。
4. 参考项目源码读取、参考行为对比、功能 parity 结论。
5. 加测试库。完整 React 渲染使用仓库已有 `react-dom/server` + Vitest，避免引入 Testing Library。

## 4. 统一字段语义

### 4.1 Presence 模型

后端用一个可记录 `Present`、`Null`、`Value` 的通用 JSON 包装类型解码高级字段，不能只依赖 Go 零值或普通指针：

- 字段缺席：`Present=false`。
- 显式 `0`、`false`、`[]`：`Present=true` 且完整保留 Value。
- 允许清空的 nullable 字段显式 `null`：`Present=true, Null=true`。
- 不允许 `null` 的字段收到 `null`：400，而不是静默当作缺席。
- create：`Present=false` 不覆盖插入行刚取得的 DB 默认；update：`Present=false` 不修改旧列。
- update 中 `expires_at:null`、`refresh_lead_seconds:null` 明确清回 `NULL`；数组清空使用 `[]`；代理清回直连使用 `{"mode":"direct"}`。

### 4.2 字段规格表

| JSON key | DB 形状/当前默认 | 输入/范围 | create 缺席 | update 缺席 | 显式状态与消费点 |
| --- | --- | --- | --- | --- | --- |
| `rpm_limit` | `bigint NOT NULL DEFAULT 0` | int64，`0..MaxInt64` | 保持 0 默认 | 保持旧值 | `0`=不限；正数进入 per-account RPM gate |
| `tpm_limit` | `bigint NOT NULL DEFAULT 0` | int64，`0..MaxInt64` | 保持 0 默认 | 保持旧值 | `0`=不限；正数进入 per-account TPM gate |
| `window_cost_limit_cents` | `bigint NOT NULL DEFAULT 0` | int64，`0..MaxInt64` | 保持 0 默认 | 保持旧值 | `0`=不限；正数进入窗口成本 gate |
| `max_sessions` | `integer NOT NULL DEFAULT 0` | int32，`0..MaxInt32` | 保持 0 默认 | 保持旧值 | `0`=不限；正数进入 session cap |
| `disable_cooling` | `boolean NOT NULL DEFAULT false` | bool | 保持 false | 保持旧值 | 显式 false 必须可把 true 关掉；健康 gate 消费 |
| `refresh_lead_seconds` | nullable integer | int32/null，`0..MaxInt32` | 保持 NULL | 保持旧值 | null/0 走全局窗口，正数为账号覆盖；两种存储态仍需原样回显 |
| `expires_at` | nullable timestamptz | RFC3339/null | 保持 NULL | 保持旧值 | null=永久；时间值由候选 SQL 以 `> NOW()` 判可调度。过去时间允许表达“立即过期”，不另造未来时间约束 |
| `tls_fingerprint_rotate` | `boolean NOT NULL DEFAULT false` | bool | 保持 false | 保持旧值 | 显式 false 可关闭；`tlsfpresolve` 消费 |
| `custom_error_codes_enabled` | bool，默认 false | bool | 保持 false | 保持旧值 | 显式 false 可关闭 |
| `custom_error_codes` | integer[]，默认 `[]` | 100..599 整数数组 | 保持 `[]` | 保持旧值 | `[]` 明确清空；后端也校验每个元素，不把前端当安全边界 |
| `pool_mode` | bool，默认 false | bool | 保持 false | 保持旧值 | 显式 false 可关闭；错误策略 provider 消费 |
| `temp_unschedulable_enabled` | bool，默认 false | bool | 保持 false | 保持旧值 | 显式 false 可关闭 |
| `temp_unschedulable_rules` | jsonb，默认 `[]` | 规则数组；code 100..599、duration 正整数、keywords 字符串数组 | 保持 `[]` | 保持旧值 | `[]` 明确清空；禁止 shape 错误或负 duration |
| `proxy_binding` | 映射 `proxy_id`/`proxy_group_id` nullable | `{mode:direct|proxy|group}` | 两列默认 NULL | 保持旧值 | direct 清两列；proxy 设正 id 清 group；group 设 trim 后非空组名清 proxy |

补充约束：前端使用 JavaScript `number` 时先拒绝超过 `Number.MAX_SAFE_INTEGER` 的 int64，后端仍以真实 int64 边界为权威；直接 API 客户端传超 int64/int32、负数、非整数或错误时间格式均返回 400。

## 5. Shape inventory（path / mode / state / actor）

| Path | Mode | State 语义 | Actor / scope |
| --- | --- | --- | --- |
| `POST /admin/v1/provider-accounts` | create | identity/credentials 为 create-only；统一高级字段缺席=DB 默认，显式 0/false/[]/null 按规格落库；混合渠道风险仍需 confirm | `tenant_operator` 使用 token scope；`platform_admin` 必须显式 tenant scope；写 `create_provider_account` 审计，payload 不含凭据 |
| `PATCH /admin/v1/provider-accounts/{id}` | partial update | 仅 `Present=true` 字段改动；显式清零/关闭/清空/清 NULL 有效；至少一个受支持字段必填 | 同上；写 `update_provider_account` 审计 |
| `GET /admin/v1/provider-accounts` | list/read | 每项回显统一高级字段；非 nullable 返回真实 0/false/[]，nullable 返回值或 null；不回凭据 | 同一 tenant scope，只读 |
| `GET /admin/v1/provider-accounts/{id}` | detail/read | 与 list/create/update 使用同一高级字段 DTO；规则数组不再成为只有详情才返回的例外 | 同一 tenant scope，只读 |
| create/update response | write readback | 写成功后从 DB 重读，回显持久化后的规范值；不能回显请求临时值冒充落库结果 | 原写 actor；审计先后顺序保持现状 |
| `ListEligibleAccountsByPoolGroup` | runtime selection | 排除 `expires_at<=NOW()`；读出 rpm/tpm/window/session/cooling 并进入 `AccountSnapshot` | 系统 actor，只读候选；本票不改算法 |
| credential refresh load | runtime refresh | `refresh_lead_seconds` NULL/0/正值按现有逻辑解释 | 系统 actor；不改凭据内容或刷新算法 |
| proxy/TLS resolver | runtime outbound | `proxy_binding` 决定直连/单代理/组；`tls_fingerprint_rotate` 决定绑定 profile/轮换池 | 系统 actor；保持既有 fail-closed/fallback 语义 |
| `CreateAccountModal` | frontend create | 高级区初始 unset；用户未触碰不发，输入 `0`/false/空数组后精确发送 | 登录管理员；tenant 由页面/API query 传递 |
| `EditAccountModal` | frontend edit | 由详情预填；只发 dirty 字段；清空/关闭形成显式 payload；无变化仍 noop | 登录管理员；保存后用完整 response 更新页面 |

兼容策略：响应新增规范化 `proxy_binding` 对象，同时暂时保留现有 `proxy_id`、`proxy_group_id` 读字段，避免已有 UI/调用方被静默破坏；前端新共用组件以 `proxy_binding` 为主，兼容从旧两列推导初值。若综合计划选择不新增响应对象，也必须把两列明确列入响应 key-set，并以同等测试证明代理读回，不能只在写请求出现 `proxy_binding`。

## 6. 后端设计与文件落点

### 6.1 新内聚子包

目标包：`backend/internal/gatewayhttp/accountconfig`。

职责：

1. `advanced.go`：字段 key/spec 表、共享 `Input`、规则与代理输入类型、presence-aware JSON 类型。
2. `normalize.go`：create/update 共用标准化和校验，产出不可变 `Mutation`。
3. `dbmap.go`：把同一个 `Mutation` 映射到 `InsertProviderAccountParams` 或 `UpdateAdminProviderAccountParams`；所有 Set-flag 只由这里产生。

该子包可仿照现有 `gatewayhttp/accountcreate` 的依赖方向：子包可以依赖 `internal/db/admin`，但不得反向 import 父 `gatewayhttp`，避免 import cycle。主 handler 只负责 auth、HTTP decode、调用、错误映射与审计。

### 6.2 create SQL：真正保持 DB 默认

`InsertProviderAccount` 不应为每个新字段在 Go 端复制一套默认常量。建议把源 SQL 改成单语句 CTE：

1. 第一段 `INSERT` 仍只写基础列，不写统一高级列，让 PostgreSQL 先应用列默认。
2. 第二段在同一语句/事务中按 `set_*` flag 对刚插入行做条件 UPDATE：`set=false` 保留该行刚取得的 DB 默认，`set=true` 写显式值（包括 0/false/[]/NULL）。
3. 最后返回 id；代理两列在同一条件 UPDATE 中构造性互斥。

这样 create 缺席语义不依赖 Go 硬编码默认，且不会把显式零值吞掉。若实现者证明当前 PostgreSQL/sqlc 版本无法稳定解析这个 CTE，备选是 source SQL 中 `COALESCE` 与 migration default 成对声明，并用真 PG 默认一致性测试锁死；不得在未留测试的情况下悄悄复制默认。

### 6.3 update SQL 与手工 sqlc 同步

1. 把全字段 update 的 named query 放进 `backend/sql/queries/admin_provider_account_mutations.sql`，以 source SQL 为审查入口。
2. 按 Owner 指令手工同步 `backend/internal/db/admin/admin_provider_account_mutations.sql.go`：SQL 常量、params、调用参数顺序与 scan 必须一一对应；不运行 `sqlc generate`。
3. 从 `backend/internal/db/admin/admin_provider_accounts.go` 移出重复的 update SQL/params/method，只保留 admin read row、SELECT 列表与共享 scanner；不能保留两份可能漂移的 update 实现。
4. nullable 字段与数组都使用显式 Set-flag + value，禁止用 `COALESCE` 表达 update presence，因为 `COALESCE` 会吞掉“显式清 NULL”。
5. 新增 source SQL ↔ generated SQL 的归一化一致性测试，至少比较 named query、列 key、placeholder/调用参数数量与顺序。

### 6.4 统一读回

1. `AdminProviderAccountRow`、`adminProviderAccountColumns`、scanner 与 DTO 同顺序补齐全部字段。
2. `providerAccountDTO` 成为 list/get/create/update 的唯一高级字段转换器；数组统一非 nil，nullable 原样回 null。
3. create/update 均以 DB 重读结果回响应，禁止用 request 拼 response。
4. 对 temp rules 若按本计划在所有响应回显，则 SELECT/DTO/OpenAPI/前端类型必须一致；若综合计划因 payload 体积决定只在详情回显，必须先取得 Owner 对“list/create/update 回显”要求的明确例外，不能自行缩水。

## 7. 前端设计

### 7.1 静态 mirror 与共享组件

1. 新增 `frontend/src/features/accounts/accountAdvancedFields.json`，逐项镜像后端 spec 的 key、kind、nullable、min/max、label/help、create/update 可用性；它是前端静态 mirror，不从运行时接口动态下载。
2. 新增 `advancedFields.ts`：强类型加载 mirror，维护 `unset/value/null/dirty` 状态，负责字符串↔API 值转换与精确 payload 构建。
3. 新增 `AccountAdvancedSettings.tsx`：create/edit 共用折叠区；按 mirror 自动渲染：
   - number：`rpm_limit`、`tpm_limit`、`window_cost_limit_cents`、`max_sessions`、`refresh_lead_seconds`
   - boolean 三态：沿用/开启/关闭，确保 create 未触碰不发、update 可显式 false
   - number-array：`custom_error_codes`
   - datetime：`expires_at`，支持明确清回 null
   - rule-array：复用现有结构化临时停调规则编辑器
   - custom：`proxy_binding`，复用代理列表/组校验 UI
4. `CreateAccountModal` 和 `EditAccountModal` 只负责基本字段、凭据/目录加载、共用组件接线与提交；现有代理、规则、错误策略逻辑从 edit 单点迁入共享实现。
5. create 高级初态全部为 unset，edit 初态来自完整账号 response；edit 只发 dirty diff。不能把默认未勾选 checkbox 当作显式 false 自动发出。
6. 更新 `createTypes.ts`、`types.ts`、create/edit payload 类型；响应保留旧代理两列兼容初值。

### 7.2 Key-set 守卫

建立一个跨层守卫，至少比较：

- Go `accountconfig.AdvancedFieldSpecs` key 集；
- frontend `accountAdvancedFields.json` key 集及 kind/nullable/min/max；
- OpenAPI `ProviderAccountCreate`、`ProviderAccountUpdate`、`ProviderAccount` 对应 key 集；
- Go create/update shared `Input` 与 response DTO 的 JSON tag 集。

建议把跨层 guard 放在新的 `backend/cmd/gateway/provider_account_advanced_contract_test.go`，使用标准库读取 frontend JSON，并复用仓库已有 OpenAPI loader；这样 `go test ./cmd/gateway/` 即是统一契约门。frontend Vitest 再从相反方向断言 mirror 中每个 key 均有 renderer 与 payload codec。不要用仅检查“数量相同”的弱断言，必须集合精确相等并打印 missing/extra。

## 8. OpenAPI 补齐

1. `ProviderAccountCreate` 与 `ProviderAccountUpdate` 声明相同的统一高级 key，按 create/update nullable 语义描述；create-only identity/credentials 仍只在 create。
2. `ProviderAccount` 补齐所有高级响应字段、现有 `static_weight/probe_model/tags/extra/proxy_id/proxy_group_id` 等实际已回但缺失的属性，并增加规范化 `proxy_binding`（若综合方案采用）。
3. 非 nullable 高级字段在 response `required` 中列出，数组明确返回 `[]`，nullable 明确 `[type, 'null']`。
4. 对 `expires_at` 写清：RFC3339；`null` 清除；候选资格由运行时当前时间判定。
5. 对所有 int64 标 `format:int64`、`minimum:0`；int32 标对应 format/range；custom error code 与 duration 给出真实边界。
6. 保证 `go test ./cmd/gateway/ -run 'TestOpenAPI_ImplementationConsistency|TestOpenAPI.*ProviderAccount'` 通过，不以关闭 consistency test 规避漂移。

## 9. 判别性测试与 mutation 对照

| 测试类 | 正向/失败断言 | 必须杀死的 mutation |
| --- | --- | --- |
| 后端字段规格单测 | 精确 key/type/nullability/range；每个 key 唯一 | 从 spec 删除/重命名字段、把 int64 改 int32、把 nullable 改 non-nullable 后测试红 |
| presence 解码表 | 缺席、0、false、[]、null 五类分别断言 Present/Null/Value | 用普通零值/指针替代 presence 类型导致显式零值或 null 被当缺席时红 |
| 逐字段 create handler | 每个字段独立 POST，捕获 Insert params，并断言 201 response 等于 DB/store 读回；另有一次全字段组合 | 漏某一 request tag、mapper 或 DTO 字段时对应子测试红；不能只断言 `!= bad` |
| create 默认 | 完全不填高级字段，真 PG 重读等于列默认；显式 0/false/[] 仍被标为显式并落库 | Go 硬编码错误默认、SQL 无条件覆盖、空数组变 nil 时红 |
| update 改一保余 | 真 PG 预置每个字段不同 sentinel，每个子测试只改一个字段，再断言目标精确变化且所有其它字段逐项不变 | 任一 `set_*` flag/placeholder 错位、UPDATE 无条件覆盖、COALESCE 吞 null 时红 |
| 边界 400 | 每个数字负数、int32/int64 超范围、非整数、坏 RFC3339、非法规则/代理 shape 均 400 且 store 未调用 | 删除 validator、decode 溢出被忽略、错误继续写 store 时红 |
| create 错误策略生效 | create 写入 pool/custom/temp 后，用 PG-backed error policy provider 读出并触发匹配/短路；另断言关闭标志时不生效 | 只回显但未持久化，或 provider 读不到字段时红 |
| create 代理生效 | single/group/direct 三态创建后重读互斥列，并用既有 proxy resolver 验证选中的 URL/组或直连 | 设一列不清另一列、绑定未进入 resolver、跨租户代理误接受时红 |
| 过期号真 PG | expired、future、NULL 三行：仅 future/NULL 可选 | 删除/反转 `expires_at > NOW()` 谓词时红 |
| 可行 RPM gate 真 PG | PG 行配置 `rpm_limit=1` → `DBAccountSource` 生成 snapshot → counter 未用时允许、记录一次后同账号拒绝，未配置账号仍允许 | SELECT 漏列、scan 顺序错、snapshot mapping 漏字段、gate 忽略 limit/counter 任一处红 |
| TLS rotate 通电 | create/update/readback true/false，并至少由 resolver fetch 观察到 rotate 状态 | 只写响应不落列、resolver 查询列断开时红 |
| frontend 完整渲染 | 用 `renderToStaticMarkup` 分别渲染 create/edit，高级 mirror 每个 key 都有唯一 `data-field-key`；断言 input kind | 删除字段、renderer switch 漏 kind、create/edit 其中一模式漏渲染时红 |
| frontend 精确 payload | create 未触碰不发；显式 0/false/[] 会发；edit 改一只发一；clear 发 null/[]/direct；全字段对象 deep-equal | truthy 判断吞 0/false、空串误当 noop、把所有 edit 字段全量发送时红 |
| 跨层 key-set | Go/frontend/OpenAPI/create/update/response 精确集合相等，missing/extra 明示 | 任一层新增/删除/拼错 key 而其它层未同步时红 |
| OpenAPI 一致性 | route/method/schema 与实现零漂移 | 为过门删 schema、放宽 `additionalProperties` 或跳过测试时红 |

测试 fixture 必须让 winner/loser、目标/非目标字段有不同 sentinel；不得用 `t.Skip` 掩盖字段为零，不得只检查“不是坏值”，不得把所有 gate 换成 `AllowAllGate`。

## 10. 拟新增/修改文件与 codebudget 影响

### 10.1 拟新增文件

| 文件 | 目标包/职责 | codebudget 影响 |
| --- | --- | --- |
| `backend/internal/gatewayhttp/accountconfig/advanced.go` | 新包 `accountconfig`：spec、input、presence | 新内聚包，目标 <350 非测试行 |
| `backend/internal/gatewayhttp/accountconfig/normalize.go` | 新包：标准化/校验 | 同包累计目标 <700 行/3 文件 |
| `backend/internal/gatewayhttp/accountconfig/dbmap.go` | 新包：create/update DB 映射 | 同包远低于 6000/20；单文件 <300 行 |
| `backend/internal/gatewayhttp/accountconfig/advanced_test.go` | 新包判别测试 | `_test.go` 不计 codebudget |
| `backend/internal/gatewayhttp/admin_pool_accounts_advanced_test.go` | handler 逐字段/400/DTO 测试 | `_test.go` 不计 codebudget，不增加生产包文件数 |
| `backend/internal/db/admin/admin_provider_account_advanced_integration_test.go` | Insert/update 真 PG round-trip | `_test.go` 不计 codebudget |
| `backend/cmd/gateway/provider_account_advanced_contract_test.go` | Go/frontend/OpenAPI key-set 与 consistency | `_test.go` 不计 codebudget |
| `frontend/src/features/accounts/accountAdvancedFields.json` | frontend 静态 mirror | 不进入 Go codebudget，无 dependency |
| `frontend/src/features/accounts/advancedFields.ts` | mirror 类型、状态、codec/payload | 前端新内聚模块，目标 <400 行 |
| `frontend/src/features/accounts/AccountAdvancedSettings.tsx` | create/edit 共用自动渲染区 | 前端新内聚组件，目标 <450 行 |
| `frontend/src/features/accounts/advancedFields.test.tsx` | 完整渲染、payload、mirror key 测试 | 前端测试文件 |

### 10.2 主要修改文件

- `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`：改为嵌入/调用 `accountconfig`，移除现有代理/规则重复校验和映射；目标是净减行，最终不得超过当前行数，更不能越过基线 5% 余量。
- `backend/sql/queries/admin_provider_account_mutations.sql` 与 `backend/internal/db/admin/admin_provider_account_mutations.sql.go`：source-first 手工同步 Insert/Update；生成 Go 文件目标仍 <600 行。
- `backend/internal/db/admin/admin_provider_accounts.go`：补 read row/columns/scanner，同时移出 update query 后应净缩或持平；`db/admin` 维持现有 13 个非测试文件，远低于 20/6000。
- `backend/internal/gatewayhttp/accountcreate/atomic.go`：只有当 Insert params 传递需要时做小改，不把高级规则塞进这个只负责原子 create 风险门的包。
- `backend/internal/db/billing/pool_accounts_eligibility_integration_test.go`：扩展 future/expired/NULL 与 RPM 完整链路，或在同包新建 `_test.go`；均不改生产预算。
- `docs/openapi/openapi.yaml`、frontend create/edit/types/tests：契约与共享组件接线。

严禁为省事在 `gatewayhttp` 根包新增非测试 `.go` 文件；严禁以 `HUAKAI_REWRITE_CODE_BUDGET_BASELINE=1` 放大基线。实现结束必须先跑 `go test ./internal/codebudget/`。

## 11. Failure modes 与缓解

| Failure mode | 影响 | 缓解/门 |
| --- | --- | --- |
| JSON 零值被当缺席 | 无法关闭/清零/清空，UI 显示成功但 DB 未变 | presence 类型 + 0/false/[]/null 表驱动测试 |
| Insert 复制默认并与 migration 漂移 | 新账号在未来 migration 后仍用旧默认 | CTE 先 INSERT 取 DB default，再按 set-flag 覆盖；真 PG 默认测试 |
| SQL placeholder/Scan 顺序错 | 字段串位，可能把限额写到别的列 | source/generated 归一化守卫 + 全字段不同 sentinel + 真 PG round-trip |
| update 无条件覆盖其它字段 | 改一个旋钮破坏代理、expiry 或限额 | 每字段“改一保余”测试，断言所有非目标 sentinel |
| create 路径绕过原子风险门 | 混合渠道检查与写入失去串行性 | 保持 `accountcreate.Insert` 事务与 advisory lock，只有 Insert params 扩展 |
| list/get/create/update response 不一致 | 编辑页预填错误、保存后字段消失 | 单 DTO + 统一 row columns + 四类 response 测试 |
| proxy 两列同时有值或跨租户 | 走错出口/破坏隔离 | 构造性互斥 + 既有 DB CHECK/tenant trigger + resolver 真 PG 测试 |
| `expires_at` 与凭据 payload expiry 混淆 | 误停账号或误判 refresh | OpenAPI/UI 文案明确“账号可调度到期”；只写 `provider_accounts.expires_at` |
| 配置已落库但运行时链断 | 运维以为限额/轮换生效，实际无效 | PG row → `DBAccountSource`/policy/resolver → gate 的端到端判别测试 |
| frontend mirror 漂移 | 字段后端可用但 UI 永远不出现，或 UI 发送未知 key | 跨层精确 key-set test + 完整渲染 test |
| 大 handler 继续膨胀 | codebudget 红、职责更混乱 | 新 `accountconfig` 子包；handler 净减；禁止加根包生产文件 |
| OpenAPI 只补字段但实现未补 | codegen 客户端与运行时不一致 | `cmd/gateway` 契约守卫同时比较 Go tags、OpenAPI、frontend mirror |
| 真 PG 不可用 | 关键落库/候选链未被验证 | `HUAKAI_TEST_DATABASE_URL` 是 landing 前置；不可用只能报告未验证，不能宣称完成 |

## 12. Decision points

### 已由仓库事实关闭

1. `tls_fingerprint_rotate`：纳入。列与消费点均已存在。
2. 不加 migration、不运行 `sqlc generate`：Owner 已明确。
3. frontend 不加测试依赖：用现有 React server render + Vitest。
4. 不改运行时 gate 算法：本票只通电控制面并验证现有链。

### 综合计划/Owner 需要确认

1. **代理响应 shape**：推荐新增规范化 `proxy_binding`，同时保留 `proxy_id/proxy_group_id` 兼容；若只保留旧两列，必须明确它们是本票的读回合同。
2. **temp rules 是否所有响应都回显**：Owner 要求 list/get/create/update 回显，本计划默认全部回显；若担心列表 payload，需 Owner 明确批准例外，不能实现者自行维持“仅详情”。
3. **create 默认实现**：优先采用 CTE 先应用 DB default 后按 presence 覆盖；仅在 sqlc/PG 兼容性有实证问题时采用 source 默认镜像备选，并保留默认一致性测试。
4. **`expires_at` 过去时间**：本计划允许，用于立即移出候选；若产品要禁止误填过去时间，应由 Owner 明确改为“必须未来”，并同步真 PG/前端/OpenAPI 测试。
5. 实施若需要改 quota/rate/session/refresh/proxy/TLS 算法、DB schema、runtime dependency 或生产环境，属于高风险扩 scope，必须停下另行确认。

## 13. Pre-execution checklist

1. Owner/综合者完成两份独立计划的 Agreements / Conflicts / Gaps 对照，并落一份无 suffix 的综合计划或明确哪份被综合修订。
2. 确认上述代理响应、temp rules 回显、create 默认实现和 past expiry 四个决策。
3. 从 repo root 重新读 `AGENTS.md`、`docs/RULES.md`、`.coordination/README.md`、本综合计划；确认 Owner start gate 仍有效。
4. `git status --short` 记录并保护所有既有改动；按文件逐个 `check.sh`/`claim.sh`，不覆盖其它 live lock。
5. 记录当前 codebudget：`gatewayhttp` 根包行数/文件数、`admin_pool_accounts_handler.go` 行数、`db/admin` 行数/文件数；设定“handler 净减、根包不新增生产文件”门。
6. 核对列、默认、类型和消费点仍与本计划一致；若自计划日后仓库已改，先更新综合计划的 shape inventory。
7. 确认 `HUAKAI_TEST_DATABASE_URL` 指向隔离测试库且 migrations 已应用；禁止对生产 DSN 跑 integration tests。
8. 确认 frontend 依赖已安装，但不运行安装/升级命令，不改 lockfile。
9. 先写 mutation-discriminating 测试骨架和 field key guard，再改实现；每一层保持可编译的小闭环。
10. 所有 Go 命令显式使用：`GOCACHE=/home/ubuntu/HUAKAI/.gocache GOTMPDIR=/home/ubuntu/.gotmp`。

## 14. Concrete execution order

1. **契约先行（测试红）**：在新 `accountconfig` 包定义预期字段规格测试、presence 测试；在 `cmd/gateway` 写跨层 key-set 预期测试；在 frontend 写 mirror/renderer/payload 预期测试。
2. **建立 backend spec/input**：实现 presence 类型、统一 `Input`、字段 spec、规则/代理校验与 `Mutation`；让负数、溢出、坏 time/array/object 在 store 前 400。
3. **source SQL first**：更新 `admin_provider_account_mutations.sql` 的 Insert/Update；随后手工逐项同步生成 Go 常量、params、调用顺序。绝不运行 `sqlc generate`。
4. **DB read model**：补 `AdminProviderAccountRow`、列列表、scanner、DTO；统一四类 response。先跑 db/admin 单测与 SQL 同步守卫。
5. **handler 瘦身接线**：create/update 都嵌入 `accountconfig.Input`，调用同一 normalize/map；移除父 handler 中重复代理/规则映射，保持 auth/tenant/risk/audit 流不变。
6. **后端 handler 判别测试**：逐字段 create 回显、update 改一保余、显式 0/false/[]/null、非法 400/store-not-called、审计不含敏感值。
7. **OpenAPI**：补 create/update/response；让 cmd/gateway key-set 与现有 OpenAPI consistency 门转绿。
8. **frontend mirror/共享组件**：先完成 `accountAdvancedFields.json`、codec/state，再实现共用折叠组件；最后接入 create/edit，迁走重复高级逻辑，不动 Sidebar。
9. **frontend 判别测试**：create/edit 全字段 server render、精确 payload、dirty/noop/clear、mirror key-set；跑 Vitest 与 tsc。
10. **真 PG 闭环**：Insert 默认/显式值、Update 改一保余、错误策略/代理/TLS 读消费、expired/future/NULL、PG → snapshot → RPM gate。
11. **全门**：build/vet/相关包/cmd-gateway/codebudget/真 PG/Vitest/typecheck 全绿；检查无 migration、无依赖/lockfile、无 Sidebar、无基线改写。
12. **per-commit review**：只 stage 本票文件，按 `AGENTS.md` 跑 uncommitted Codex review，归一化 S0–S3；修完 S0/S1 后按 round budget 复核，再提交。完整切片另走 reviewer-lane gate。

## 15. 验收命令

在 `backend/`：

```bash
GOCACHE=/home/ubuntu/HUAKAI/.gocache GOTMPDIR=/home/ubuntu/.gotmp go test ./internal/gatewayhttp/accountconfig/ ./internal/gatewayhttp/ ./internal/db/admin/ ./internal/db/billing/ ./internal/pool/router/ ./internal/credentialstore/ ./internal/tlsfpresolve/
GOCACHE=/home/ubuntu/HUAKAI/.gocache GOTMPDIR=/home/ubuntu/.gotmp go test ./cmd/gateway/
GOCACHE=/home/ubuntu/HUAKAI/.gocache GOTMPDIR=/home/ubuntu/.gotmp go test ./internal/codebudget/
GOCACHE=/home/ubuntu/HUAKAI/.gocache GOTMPDIR=/home/ubuntu/.gotmp go build ./...
GOCACHE=/home/ubuntu/HUAKAI/.gocache GOTMPDIR=/home/ubuntu/.gotmp go vet ./...
HUAKAI_TEST_DATABASE_URL="$HUAKAI_TEST_DATABASE_URL" GOCACHE=/home/ubuntu/HUAKAI/.gocache GOTMPDIR=/home/ubuntu/.gotmp go test -tags=integration_pg ./internal/db/admin/ ./internal/db/billing/ ./internal/gatewayhttp/ -run 'ProviderAccount|EligibleAccounts|RatePrecheck'
```

在 `frontend/`：

```bash
npm test -- src/features/accounts
npm run typecheck
```

最终人工/静态检查：

```bash
git diff --check
git diff --name-only
```

验收输出必须逐项记录 PASS/FAIL/SKIP 原因。真 PG 若因 DSN/基础设施未运行，不得将 unit PASS 代替真 PG PASS，也不得宣称切片完成。

## 16. 完成定义

只有同时满足以下条件才可宣布 P2-a 完成：

1. 14 个统一高级字段在 backend spec、create、update、DB、四类 response、OpenAPI 与 frontend mirror 中精确对齐。
2. 缺席/0/false/[]/null 的 create/update 语义由判别性测试证明，而非靠说明文字。
3. `tls_fingerprint_rotate`、错误策略、代理、expiry、RPM 至少各有一个“配置写入→真实消费点观察”的闭环测试。
4. frontend create/edit 共享同一高级组件；mirror 每个字段完整渲染，payload deep-equal；Sidebar 未改。
5. source SQL 与手工 sqlc Go 一致，未跑生成、未加 migration。
6. 所有验收门绿，codebudget 未放大基线，handler 没继续膨胀。
7. 无功能缩水、无参考源码污染、无新安全/凭据泄漏；任何 S0/S1 已关闭，S2/S3 按规则记录。

## 17. Owner 汇总（计划阶段）

这份独立计划把 P2-a 定义为“一个 presence-aware 后端字段契约 + 一个 frontend 静态 mirror + 一条跨层 key-set 门”，并用新 `gatewayhttp/accountconfig` 子包避免继续扩大已超预算的 `gatewayhttp` 根包。计划纳入全部指定字段，且依据 HUAKAI 当前迁移与 resolver 事实确认加入 `tls_fingerprint_rotate`；create 补齐现有 update 的错误策略、pool、临时停调和代理能力，没有删除或隐藏功能。未读取任何参考项目源码，clean-room 风险没有增加；主要安全/正确性风险是 SQL 参数串位、显式零值被吞、代理互斥/租户错误及“配置落库但运行时未消费”，均安排了 mutation-discriminating 和真 PG 闭环测试。Owner/综合计划需最终确认代理响应 shape、temp rules 是否全响应回显、create 默认的 CTE 方案及是否允许 past `expires_at`；确认后才进入实现。
