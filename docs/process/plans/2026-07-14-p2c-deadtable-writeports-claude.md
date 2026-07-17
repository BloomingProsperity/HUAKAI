# P2-c 死表写口+模型主体+租户默认出口+配额 · Claude 独立计划(双计划我方稿)

日期:2026-07-14。前提:P0/P1/P2-a/P2-b + 绑定级三死字段(weight/并发/fallback)全部激活完毕。P2-c 是"半死通电"批的剩余项。

## 0. 核心裁定:P2-c 大多撞 gate,不是"补写口"批

已踩点五子项四层落地度(先证伪再信),关键发现——**除配额外全部无现成 admin 面**:

| 子项 | L1 DB | L2 写口 | L3 前端 | L4 消费 | 性质 → gate |
|---|---|---|---|---|---|
| tenants.default_proxy_id | ✓ | ✗ 无 UpdateTenant | ✗ 无 tenant 设置页 | ✓ resolver:77/126/136 读 | **建新 tenant 管理面** → 页面级 Owner-gated |
| model_routing_overrides | ✓ | ✗ | ✗ | ✓ pool_accounts.sql.go:275 GetModelRoutingForGroup 读 | **建新面**(模型→账号 pin 管理)→ 页面级 Owner-gated |
| api_key_groups | ✓ | ✗ | ✗ | ✓ userkey_controls JOIN 读 | **建新面**(Key 分组管理)→ 页面级 Owner-gated |
| models 表 admin 建改 | ✓ | ✗ 仅自动同步 | ✗ | 自动同步链 | **建新面**(模型主体 CRUD)→ 页面级 Owner-gated |
| quota_policies.burst_value | ✓ | ✓ crud 收 | 待核 | **半死**:pg_store_map load 进内存但 enforce 判定(service_assess/service.go)从不读 | **碰配额强制面**(接 enforce=改判定语义)→ Owner-gated |
| quota calendar_month 白名单 | ✓ | ✓ | — | 枚举白名单 4 处 | **碰配额枚举**→ Owner-gated |

**结论:P2-c 无一是"现有面补最后一层"的自主项**(不同于 P0-P2b/fallback)。全部要么建新管理面(撞[[frontend-pages-not-owner-approved]]页面级设计 Owner-gated),要么碰配额 enforcement 语义(Owner-gated)。这是本计划最重要的裁定,必须 surface Owner 而非硬派。

## 1. 各子项语义与三镜对照

### model_routing_overrides(模型→账号强制 pin)
- 消费:`GetModelRoutingForGroup(tenant,pool_group,model)` 返回 provider_account_ids 列表——某模型在某池组内强制只走指定账号子集。与刚做的 model_pool_bindings(weight/并发/fallback,池级)是**不同层**:binding 管"模型→哪个池",routing_override 管"池内该模型只用哪些账号"。
- 三镜:new-api 有渠道级 ModelMapping(controller/channel.go,模型名重写)但非账号 pin;sub2api 账号调度按平台/模型过滤候选(gateway_scheduling)。HUAKAI 的账号级 pin 更细,属架构升级。需核 override 与 binding 选号叠加顺序(override 先收窄候选,再走 binding 的 weight/并发?)。

### api_key_groups(Key 分组)
- 用途待精确定:配额继承 / 路由约束 / 计费分组?userkey_controls JOIN 读了 group 信息但消费语义需核。三镜:new-api token 分组、sub2api 用户组。

### models 表(模型主体建改)
- 当前靠自动同步(从上游拉模型列表?)。手工建改需与自动同步不冲突(手工标记 protected 不被同步覆盖?)。三镜:new-api 渠道模型列表编辑。

### burst_value(配额突发额度)
- 接 enforce 两种语义:①limit+burst 硬上限(窗口内允许冲到 limit+burst);②令牌桶(burst 为桶容量)。三镜:new-api 有无 burst 待核。**碰配额判定语义 = Owner-gated**,默认 0=无突发(零行为翻转)可作接入前提。

## 2. 切片建议(全部 Owner-gated,按投产比排序)

Owner 拍板顺序建议(每片独立双计划+实现):
1. **model_routing_overrides 管理**(运营常用:某模型出问题时紧急 pin 到好账号)——建面,但可复用现有 routing feature 前端骨架(RoutingPage 已在),不是全新页,gate 较轻。
2. **burst_value + calendar_month enforce**(纯后端,不建面,只碰配额判定)——工作量小,默认零翻转,gate=配额强制面。
3. **api_key_groups 管理**——建面,依赖用途厘清。
4. **models 主体 CRUD**——建面,依赖自动同步冲突处理。
5. **tenant default_proxy_id**——建面,但 HUAKAI 单租户导入为主([[sub2-migration-survey]]),实用性最低,排最后。

## 3. 请 Owner 拍板

1. P2-c 这批要不要现在开建?(全部撞页面级/配额强制面 gate)
2. 若开建,先做哪片?(建议 model_routing_overrides 或 burst,见 §2)
3. burst enforce 语义选硬上限还是令牌桶?
4. 是否复用现有 routing/quota 前端骨架以减轻页面 gate?

**在 Owner 拍板前,P2-c 不动生产代码**;本计划与 codex 独立稿交叉后呈报。
