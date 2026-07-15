# 2026-07-15 模型主体运维 CRUD 综合执行计划

| 项目 | 内容 |
| --- | --- |
| Owner directive | “给「模型主体(model registry)」补运维 CRUD 写口(代号 C③)。” |
| 输入计划 | `2026-07-15-model-registry-admin-crud-claude.md` 与 `2026-07-15-model-registry-admin-crud-codex.md` |
| Scope | 模型主体 admin REST、registry CRUD、sqlc 查询/手工生成码、gateway 接线、前端模型列表与编辑、OpenAPI、判别测试。 |
| Out of scope | 无 schema/迁移、无计费/配额/auth-core 改动、无新依赖、无 commit。 |
| Success criteria | CRUD 与状态流转可回读；双层 scope 防线；每个受影响租户快照在同事务精确递增；数字 id 回填 UI；指定 Go/前端/OpenAPI 门全绿，PG 测试可编译。 |
| Blast radius | `models` 写面、admin 路由、目录快照失效、模型注册运维页；不改变公开模型发现与转发热路径。 |

## 三镜与 clean-room 口径

`REFERENCE PROJECTS IN SCOPE`：CLIProxyAPI、sub2api、new-api。

Codex 是实现者车道，不读取三镜源码；三镜行为证据和引用继承自独立 specifier 车道规划输入。实现只复用 HUAKAI 内部 C②/registry 范式，不复制任何上游源码、命名、注释、结构、SQL、UI 或测试。

## 双计划一致点

1. 新建职责单一的 `backend/internal/modeladminhttp`，注册 List/Create/Get/Patch/Delete。
2. `backend/internal/registry/models_admin.go` 提供 List/Get/Create/Update/SoftDelete，写操作与 snapshot bump 同事务。
3. handler 从 `Auth.Resolve` 取身份并调用 `CanIssueForTenant`；service 再以 `scope + tenant_id + id` 参数化谓词校验。
4. tenant operator 只写自身 tenant scope，对 global 只读；platform admin 可写 global 与显式目标 tenant。
5. canonical 唯一冲突归一为 `ErrConflict`；严格 JSON、正整数路径 id、typed-nil 接线均有判别测试。
6. 前端使用 admin list 的数字 id 回填既有能力/绑定操作，OpenAPI 与 gateway 路由同步。

## 综合后补充口径

- API 使用 `scope=tenant|global` 与可选 `tenant_id` 查询参数确定目标域：tenant operator 的 tenant id 从身份默认并强制相等；platform admin 管 tenant 时必须显式 `tenant_id`；global 不接受 tenant id。
- service 接收由 handler 从认证身份构造的访问上下文，并自行验证角色/scope，保证绕过 handler 的直接调用仍不能跨租户或由 tenant operator 写 global。
- global 模型没有 `tenant_id`，其写操作复用既有 `bumpAffectedSnapshots`：递增所有开启全局继承或已绑定该 model 的受影响租户；tenant 模型只递增所属 tenant。该逻辑纳入多租户 `integration_pg` 判别测试。
- PATCH 用指针区分“未提供”与零值；锁行后合并，仅允许改具体字段清单与 `active/disabled`，不允许 PATCH 复活 deleted。
- `canonical_id` 按具体 Update 字段清单保持不可变；背景“改名”与具体字段清单的张力列入最终风险，不静默扩权。
- 现有 `ModelRegistryPage.tsx` 已 515 行，新模型卡拆到同 feature 目录独立组件，生产文件不超过 600 行。
- 不级联删除 alias/binding；模型软删后 resolver 因主体不可用 fail closed，保留关联记录供审计/后续修复。

## 执行顺序

1. 增加 SQL 查询、手工生成码与 registry 服务/PG 判别测试。
2. 增加 HTTP 包与 handler 判别测试。
3. 接入 gateway，补 typed-nil 与 OpenAPI 同步测试。
4. 拆分并加入前端模型卡、类型/API/纯逻辑测试。
5. gofmt、目标测试、全量非 PG Go 门、gateway OpenAPI、前端 tsc/vitest、代码预算、中文注释与 clean-room 自检。

## 必证变异

| 破坏点 | 转红测试 |
| --- | --- |
| 去 handler `CanIssueForTenant` | HTTP 跨租户拒绝 |
| 去 service scope/tenant 谓词或授权 | PG 跨租户读/改/删拒绝 |
| 放开 global 写守卫 | HTTP/PG tenant operator 提权拒绝 |
| 去任一 snapshot bump | 对应写操作版本 `+1` |
| PATCH 零值覆盖未提交字段 | CRUD 子集更新保留断言 |
| List 不过滤 deleted | 软删后 List 不返 |
| 唯一错误不归一 | 重名 Create 返回 `ErrConflict` |
| gateway 注入 typed-nil | 接线判别测试 |
