# 2026-07-15 模型主体运维 CRUD（Codex 独立计划）

> 本计划由 Codex 在未读取同名 `-claude.md` 的前提下独立起草。Codex 本轮是实现者车道，不读取非 MIT 参考项目源码；三镜行为证据与引用只从已隔离的 specifier 规划稿继承，随后再做计划差异核对。

| 项目 | 内容 |
| --- | --- |
| Owner directive | “给「模型主体(model registry)」补运维 CRUD 写口(代号 C③)。” |
| Scope | 新增模型主体 admin HTTP 包、registry 写服务、sqlc 查询与手工生成码、gateway 接线、模型注册前端列表/编辑面、OpenAPI、单元与 `integration_pg` 判别测试。 |
| Out of scope | 不改数据库 schema、不新增迁移、不改公开 `/v1/models` 语义、不改鉴权核心、不改计费/配额、不引入运行时依赖、不执行 `git commit`。 |
| Success criteria | 五个 REST 操作可用；租户与全局 scope 权限闭合；所有写操作与快照递增同事务；重复 canonical id 返回冲突；数字模型 id 可由 UI 选择；Go 编译及非 PG 单测、前端 tsc/vitest、gateway OpenAPI 一致性均通过；PG 测试可编译。 |
| Time estimate | 预计 2.5–4 小时墙钟时间，约 4–6 工程小时；真 PostgreSQL 执行留给 Owner 本机。 |
| Blast radius | `models` 运维写路径、模型解析缓存版本、admin 路由、模型注册运维页及 OpenAPI；不触及请求转发、计费与配额热路径。 |
| Package budget | 新 HTTP 逻辑进入 `backend/internal/modeladminhttp`；registry 数据写入集中在单一 `models_admin.go`；生产文件均控制在 600 行内，新包控制在 20 个非测试文件/6000 行内。 |

## 参考项目范围与车道边界

`REFERENCE PROJECTS IN SCOPE`：CLIProxyAPI、sub2api、new-api。

- 本会话为 HUAKAI 实现者车道，不打开 `/home/ubuntu/refs`。
- 三镜的功能形态、无等价项与源码引用，由独立 specifier 车道产出的 Claude 规划稿提供。
- 实现只依据 Owner 契约、该规划稿的行为摘要和 HUAKAI 现有代码范式，不复制上游函数名、字段名、注释、目录、SQL、UI 或测试。

## 独立设计

### 1. HTTP 权限与资源选择

- 新建 `backend/internal/modeladminhttp`，仅依赖窄接口：`adminAuth` 与模型主体管理服务。
- 请求身份一律来自 `Deps.Auth.Resolve`；租户目标来自已认证身份或显式 `tenant_id`，不把请求体中的身份声明当授权依据。
- `tenant_operator`：读取自身 tenant scope 与可见 global；只能写自身 tenant scope；全局创建、更新、删除、停用、启用均在 handler 直接返回 403。
- `platform_admin`：可读取/写入 global；写 tenant scope 时必须显式给出正整数 `tenant_id`，并调用 `CanIssueForTenant`。
- handler 是第一道 IDOR 防线；service 在 SQL 的 `scope + tenant_id + id + deleted_at` 谓词中再次核验，是第二道防线。对外租户资源统一按不存在/无权访问处理，不泄露资源枚举信息。
- 严格 JSON：`MaxBytesReader`、`DisallowUnknownFields`、只允许单个 JSON 值；路径 id 必须为正整数；所有字符串先 `TrimSpace`，协议族与状态采用白名单。

### 2. REST 契约

- `GET /v1/admin/models`：按调用者与查询 scope 返回未软删模型，响应带数字 `id`、`tenant_id`、`scope` 和全部运维字段。
- `POST /v1/admin/models`：创建 global 或 tenant 模型；tenant 创建按权限解析目标租户；默认状态为 `active`。
- `GET /v1/admin/models/{id}`：按读权限与 scope 读取单条。
- `PATCH /v1/admin/models/{id}`：只改既有可编辑字段；字段用指针表达“未提供”，支持状态在 `active`/`disabled` 间切换，但不能借 PATCH 复活 `deleted`。
- `DELETE /v1/admin/models/{id}`：软删为 `status=deleted` 并写 `deleted_at`，成功返回 204。
- `canonical_id` 与 `scope`/`tenant_id` 在创建后保持身份不变；改身份走删建，避免唯一域与现有关联产生歧义。

### 3. registry 与 SQL

- 新增 `backend/internal/registry/models_admin.go`：定义 admin 投影、访问上下文、Create/Update 输入与 List/Get/Create/Update/SoftDelete。
- 新增 `backend/sql/queries/models_admin.sql` 并加入 registry 的 sqlc 配置块；按 Owner 要求手工新增对应 `internal/db/registry/models_admin.sql.go` 和 `Querier` 方法，禁止全量 `sqlc generate`。
- 查询只投影模型元数据，不连接任何凭证、API key、计费或配额表；全部参数化。
- 每个写操作开启事务并锁定/更新目标；成功写入后，在同一事务中按受影响租户递增 `model_registry_snapshots.version`。
- global 模型没有自身 tenant id：全局写入必须对所有已存在的租户快照行递增，使继承全局目录的缓存全部失效；若权威计划对 global 快照有既有契约，核对后以权威契约为准并补判别测试。
- 唯一约束错误码 `23505` 归一为 `ErrConflict`；`pgx.ErrNoRows` 归一为 not found/forbidden-safe 错误；事务失败不得提交快照或模型半成品。

### 4. gateway、OpenAPI 与前端

- gateway 使用已有 `d.adminAuth` 与 `d.modelRegistry` 注入新包；新增 typed-nil 接线测试，确保缺池时 fail closed。
- OpenAPI 同步五个方法、查询参数、请求/响应 schema、错误响应和 scope 权限说明；补路径与 schema 的一致性测试。
- `types.ts`、`api.ts` 增加 admin model DTO 与请求函数。
- `ModelRegistryPage.tsx` 顶部增加模型列表卡、创建/编辑/启停/软删交互；已选行的数字 id 回填后续能力与绑定卡，消除正常工作流中的手输 id。
- 若页面将超过 600 行，把新卡拆到同 feature 目录的独立组件，不继续膨胀现有文件。

### 5. 判别性测试

- HTTP 单测：角色/scope 矩阵、`CanIssueForTenant`、全局写拒绝、严格 JSON、字段传播、错误归一、外租户不泄露。
- registry `integration_pg`：
  - Create → Get 全字段 → Update 子集且其他字段不变 → SoftDelete 后 List/Get 不可见。
  - A 租户模型由 B 的访问上下文读/改/删均失败。
  - tenant operator 对 global 的创建与既有 global 写操作失败。
  - Create/Update/Disable/Enable/Delete 每次成功写后快照版本精确 `+1`；失败写不递增。
  - 同 scope 重复 `canonical_id` 返回 `ErrConflict`，不同租户可使用同名作为正控制。
  - 事务回滚与 global 快照传播按最终契约补覆盖。
- 前端 vitest：DTO 到列表行的数字 id、scope/status 映射，选中模型回填，表单校验与状态按钮行为。
- gateway：服务注入非 nil 且非 typed-nil；OpenAPI 路径/方法/schema 与路由一致。

## 判别变异清单

| 破坏点 | 应转红测试 |
| --- | --- |
| 删除 handler 的 `CanIssueForTenant` | tenant B 请求 tenant A 时，HTTP IDOR 用例由 403 变成功并转红 |
| 删除 service 的 `scope + tenant_id` 谓词 | `integration_pg` 跨租户 Get/Update/Delete 用例读写到 A 的行并转红 |
| 放开 tenant operator 的 global 写守卫 | HTTP 全局 Create/Patch/Delete 拒绝用例由 403 变成功并转红 |
| 删除任一写操作后的 snapshot bump | 对应 Create/Update/Disable/Enable/Delete 精确 `version+1` 断言转红 |
| 把软删改成物理删除或 List 不过滤 deleted | CRUD 回读用例的状态/不可见性断言转红 |
| 忽略唯一错误归一 | 重名创建期待 `ErrConflict` 的用例转红 |
| gateway 注入 typed-nil | typed-nil 接线测试转红 |
| PATCH 用零值覆盖未提供字段 | “只改子集、其余字段保持”用例转红 |
| global 写只 bump 单个/零个租户 | 多租户全局变更的快照传播用例转红 |

## 失败模式与缓解

- global 模型无 tenant id，快照语义容易漏失效：先核对既有缓存/同步 writer 的全局 bump 约定，再写多租户判别测试。
- 手改 sqlc 生成码容易参数顺序或 scan 顺序漂移：查询源码与生成常量做逐项对照，并运行 db 包编译/查询契约测试。
- PATCH 指针/nullable 字段可能混淆“未给”和“清空”：请求 DTO 用指针，service 先锁行再合并，SQL 参数只写最终值。
- 软删可能被厂商同步重新激活：核对同步 writer 的自动管理判定；手工模型和同步模型的边界写入风险报告，不在本切片静默改变同步策略。
- UI 文件超预算：新列表卡拆文件，公共纯逻辑放 `modelregistry.ts` 并用 vitest 覆盖。
- 工作树已有 Owner/其他 agent 改动：使用 `.coordination` 锁，逐文件检查，只改本切片目标，不覆盖无关未提交内容。

## 决策点

1. global 变更的快照传播必须有明确租户集合；若现有权威计划选择不同的无 schema 方案，差异核对后记录理由。
2. List 的默认可见集合与 `scope` 查询语义以权威 Claude 规划稿为准；安全下限是 operator 不能枚举其他租户，platform 的 tenant 视图必须显式 tenant id。
3. 删除有现存 alias/binding 依赖时，本切片按软删模型本身处理；不级联删除关联数据，解析路径因 model 非 active 而 fail closed。

## 预执行检查与顺序

1. 完成本 Codex 独立计划并落盘。
2. 读取 Claude 规划稿，列 agreements/conflicts/gaps，形成执行口径；不读取三镜源码。
3. 重新检查工作树与 `.coordination`，一次性认领目标文件。
4. 先写 SQL/生成码和 registry 服务，再写 `integration_pg` 判别测试。
5. 写 HTTP 包单元测试与实现，随后接入 gateway 并补 typed-nil/OpenAPI 测试。
6. 更新 OpenAPI 与前端类型/API/页面/纯逻辑测试。
7. `gofmt`，运行 Go 目标包、全量非 PG 单测/编译、gateway、前端 tsc/vitest、代码预算门。
8. 检查所有新增/修改注释均为中文，检查 clean-room 与秘密字段边界，输出未提交的 Owner 报告。
