# Plan — 路由可视化向导（数据层 + 后端能力补强）

- 日期: 2026-06-18
- 作者: Claude PM-Orchestrator
- 状态: Owner 已拍板 4 决策（带三镜像对照重问后），分 3 slice 执行
- 触发: Owner 选定下一波 = 路由可视化向导（L、最大缺口）
- 调研依据: workflow `w53fo12bp`（三镜像 new-api / sub2api / CLIProxyAPI clean-room 路由配置 shape inventory + HUAKAI 缺口综合）

## 1. 背景 — HUAKAI 的「路由」是三层，不是单一对象

核查 HUAKAI 真实现状（读真后端契约，非凭记忆）后，路由由三层拼成：

| 层 | 职责 | 源（HUAKAI 内部，file:line） |
|---|---|---|
| **routes 表** | 用户分组×模型 → 允许哪些 pool_group 的**白名单准入 gate**（只判准入、不排序选号） | `backend/internal/subscriptionenforce/gate.go:96-151`；`backend/internal/routeadmin/*`（写侧 CRUD） |
| **model_bindings** | 模型×pool_group 的**选号属性载体**（priority/weight/selection_mode/provider_model_id_override/rpm-tpm/fallback_class/effective_from-until），**已有完整 CRUD 含 PATCH** | `backend/internal/modelbindingadminhttp/routes.go` |
| **pool / PASR-lite router** | 池内**实际选账号**（HRW locality+headroom+load、strict_priority/priority_weighted、sticky、fallback timeout） | `backend/internal/pool/router/types.go` |

三镜像把 weight / 模型映射 / 优先级 / 状态全堆在「channel/route 单对象」上；HUAKAI 故意把它们分散到 bindings + router 层、routes 表保持窄（纯准入）。因此很多「看似缺失」的镜像能力（weight、priority-weighted、模型覆盖、fallback 分类、限流、限时生效 effective_from/until）在 HUAKAI binding 层已存在甚至超出（**effective_from/until 三镜像都没有**）。

## 2. 真缺口（收窄为 5 项）

1. **routes 无 update**：编辑=删+建非原子，唯独 routes 缺而 bindings 已有 PATCH。← **slice A 修**
2. **match_priority 存了但运行期不消费**：gate 是纯白名单，多条命中不按优先级裁决（语义陷阱）。← **slice B 修**
3. 模型映射仅单层（provider_model_id_override），无链式 / 别名分叉 / 思考后缀保留。← roadmap
4. 无跨 pool_group 链式兜底（仅 sub2api 有显式分组链式降级 + 环检测）。← roadmap
5. 无客户端类型（官方 CLI / 协议）路由维度。← roadmap

## 3. Owner 决策（带三镜像对照 = §15；2026-06-18 拍板）

> 决策呈现时每选项都带了三镜像做法（AskUserQuestion 两轮），满足 §15 参考对照 + §16 三镜像调研。

| 决策点 | 三镜像做法 | Owner 拍板 |
|---|---|---|
| **本期数据层范围** | 三镜像都把选号属性堆单对象（new-api 可用性表 / sub2api 分组+关联边优先级 / CLIProxyAPI 凭证字段）；只接 routes 无法展示「怎么选号」 | **routes + bindings 两层** |
| **编辑语义** | 三镜像全部就地 update、无一删+建：new-api 编辑入口重建其可用性行 `~/refs/new-api/controller/channel.go:893`；sub2api 分组/渠道/账号均有改 `~/refs/sub2api/backend/internal/handler/admin/group_handler.go:273-431`；CLIProxyAPI 按索引/值 PATCH 单条凭证 `~/refs/CLIProxyAPI/internal/api/handlers/management/config_lists.go:148-216` | **后端补 PUT/PATCH `/v1/admin/routes/{id}`**（镜像对齐 + bindings 已有 PATCH 内部一致） |
| **match_priority 语义** | 三镜像 priority 都是真选路键：new-api 两级（先档后档内加权随机）`~/refs/new-api/model/ability.go:61-104`；sub2api 优先级数值升序作排序主键 `~/refs/sub2api/backend/ent/schema/account_group.go:29-40`；CLIProxyAPI 先取最高优先档再档内策略 `~/refs/CLIProxyAPI/sdk/cliproxy/auth/selector.go:116-129` | **后端 gate 实现 priority 真裁决** |
| **向导 UX 本期做吗** | 三镜像其实都没有集中可视化路由规则对象（new-api/sub2api 内嵌在渠道/分组编辑表单、CLIProxyAPI 无中心对象）；HUAKAI routes 表反而是一等可 CRUD 路由实体 | **否，本期只数据层**（UX 后置 P2） |

## 4. Slice 分解（各自 worktree → 强变异测试 → ultracode 对抗审查零 S0/S1 → PR → squash 合并）

### Slice A — routes PUT/PATCH update 端点（后端，本计划首发）
- **范围**: `routeadmin` 包加 `UpdateInput` + `Store.Update` + memory/postgres 实现 + `Service.Update`（复用 `ValidateModelPattern`）；`controlhttp` 加 `RouteAdminService.Update` + `updateRouteRequest` + handler + 挂 `PUT /{id}`；`AuditSink` 加 `RouteUpdated`。
- **关键设计**:
  - `PUT /v1/admin/routes/{id}?tenant_id=N`，body = {name, user_group_match, model_pattern_match, pool_group_id, match_priority}。**tenant_id 取自 query 非 body**（DisallowUnknownFields 拒 body 内 tenant_id → 防跨租户搬移/走私），id 取自 path，AdminID 取自已认证身份。
  - 全替换 PUT 语义：match_priority 省略 → COALESCE 回落 DB 默认 100（与 Create 一致；前端 client 永远显式带它）。
  - 保留 ID/TenantID/CreatedAt/Enabled，改 name/user_group/model_pattern/pool_group/match_priority + bump updated_at。
  - 错误：route 不存在/已软删/跨租户 → ErrRouteNotFound；目标 pool_group 非同租户/已删 → ErrPoolGroupNotFound（postgres 用 WHERE EXISTS 原子 + ErrNoRows 时反查消歧）；改名撞同租户活路由名 → ErrDuplicateName（排除自身）；mid-string 通配 → ErrInvalidModelPattern。
- **爆炸半径**: 仅 routeadmin + controlhttp 两包；**无 migration、无 schema 变更、无 cmd/gateway 改**（生产 audit=nil，仅测试 capturingAudit 实现接口，加方法零生产破坏）。
- **成功标准**: 能改一条 route 的任一字段（含 pool_group/优先级/pattern）；非法 pattern/缺字段/跨租户/撞名/改不存在各返正确错误；DisallowUnknownFields 拒 tenant_id 走私；go build/test/vet/codebudget 全绿；零 S0/S1。

### Slice B — match_priority 真裁决（后端 routing/auth core）— 设计已定（workflow weel7kelw）
- **范围**: `subscriptionenforce` repo 层 `GroupRoutes` 收窄 `Allowed` 到命中本 model 的路由中**最高优先档**的 pool_group；gate/decideGroupRoutes/Configured 语义全部不变。
- **已决设计**（设计 workflow #10 平行草案 + #16 三镜像方向复核 + 对抗风险 → 综合 ready_to_implement，blocking_owner_decisions=[]）:
  - **放 repo 层**: `GroupRoutes` SELECT 补 `r.match_priority`，命中本 model 的路由交纯函数 `highestPriorityAllowed` 取最小档并集填 Allowed。纯函数与 `ModelPatternMatches` 同包、免 DB 单测。不放 gate 层（否则要把 priority 漏进 GroupRoutes 结构体 + 改 decideGroupRoutes，破坏「repo 返回已裁 Allowed」单一来源）。
  - **方向 = 小值高优先（MIN）**: 源码定夺 `docs/schema/pool-routing.sql:247 "lower = match first"` + store ASC + DEFAULT 100 + validate≥0 四处内部一致。三镜像 new-api（`Calcium-Ion/new-api@1ac0f58:model/ability.go:67,92` DESC/MAX）、CLIProxyAPI（`@2a050dc:sdk/cliproxy/auth/scheduler.go:349,366` >best）用大值=高，但 sub2api（#16 默认 tiebreaker）**无 route/channel priority 等价物**，且翻方向必须连带翻默认值 100→0 的破坏性迁移 → **取 MIN 不翻**；方向对齐镜像若将来要做，单列独立 Owner-gated 迁移切片。
  - **并列同档取并集；命中集非空⇒结果非空（防 S0 硬不变量）；无命中→空集（白名单据此拒）。**
  - **不加 feature flag 直接收窄**: MIN+默认100 使全默认配置等价旧全量集（等价性单测守门），仅「同组同模型多路由且优先级显式不同」受影响（正是 Q3 要收的）；收窄 bug 只会收成空→**fail-closed 硬拒（安全）**非 fail-open 误放，回滚=revert 即恢复死字段态、零 schema/数据迁移。
- **爆炸半径**: 仅 `subscriptionenforce` 包（routes_repo_postgres + 新 routes_arbitration + gate.go 注释 + 集成测试）；**无 migration、无 schema 变更**（match_priority 列已存在）；gate_test `configured(ids...)` helper 不变（直接构造已裁集）。
- **Deferred（后续 ecosystem 切片，非本 slice）**:
  - **可观测 metric**: repo 发生收窄（命中集 size > Allowed size）时计数 + 标签 tenant/group，让运维区分「priority 收窄拒」与「configured-but-model-miss 拒」。当前 `PostgresRoutesRepo` 无 logger/observer 注入，加之属架构添加 → 单列。收窄本身 fail-closed 安全、不依赖它。
  - **Deploy-gate 审计（Owner）**: 上线前跑审计 query 统计「同 (tenant,user_group,model 重叠) 有 2+ 启用路由且 match_priority 不同」的配置数，量化冲击面；需生产 DB（Owner deploy gate）。
  - **方向对齐镜像**（若 Owner 将来要 DESC）= 独立破坏性迁移切片。
- **风险**: routing/auth core 运行期行为变更（Owner 已带三镜像当面确认 Q3）；安全网=纯函数判别单测 + 集成测试 + 对抗审查 + fail-closed 兜底。

### Slice C — 前端数据层接线（routes + bindings CRUD client）
- **范围**: `routes` admin client（create/list/get/delete + **PUT-based update**，依赖 slice A）+ `model-pool-bindings` client（依赖现有 bindings CRUD）+ 前端镜像后端 `model_pattern` 校验（exact/prefix*，拒中段多通配）+ 错误码映射 + 精确 key-set + 强判别测试。
- **不做**: 向导 UX（Owner 拍板后置 P2）。
- **依赖**: slice A 的 PUT 端点。

## 5. 执行顺序与门禁
1. Slice A（本 PR）→ squash 合并 feat/frontend-portal。
2. Slice B 设计 workflow → 计划呈 Owner（routing/auth core，再确认实现语义）→ 实现 → 深审 → 合并。
3. Slice C 前端接线 → 合并。
- 每 slice: worktree base `origin/feat/frontend-portal`；ultracode 多 agent 对抗审查（refute-by-default）是 #8 替代门禁（codex 本环境 401）；零 S0/S1 才提交；审查后重跑完整干净基线（fail 0）。
- proxies 分支仍活跃 → 继续避让 provider/channel + 不动 Sidebar.tsx（本计划三 slice 均不触及）。

## 6. 会出什么错（预案）
- match_priority 全替换 PUT 省略即重置（slice A）→ 文档明示 + 前端永远显式带；slice B 落地后该字段变真选路键，重置即改选路 → slice C client 强制带 match_priority。
- slice B 改 gate 可能误拒付费用户 → 保留现有 fail-open（repo 出错放行 + observer 告警）语义，只收窄「已配置且命中」分支。
- 跨租户路由/pool 泄漏 → slice A 写侧 fail-closed（WHERE EXISTS 同租户）+ 测试覆盖跨租户 update 拒。
