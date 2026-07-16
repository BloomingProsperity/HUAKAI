# 2026-07-16 三层分发与委托授权源码调研（Codex）

## 元数据

| 项目 | 值 |
| --- | --- |
| Owner 问题 | “类似发卡网那种出现下级代理的模式是什么样；再看我们实际代码里面有没有这一块，当时写这一块的时候调研过。” |
| 角色边界 | 1. 部署者；2. 多租户使用者/租户经营者；3. 最终用户 |
| 仓库范围 | `main`、全部本地分支、全部 `origin/*`；只读历史对象，不切换或修改其他工作树 |
| 文档范围 | 全历史中与角色、租户、商户、分销、代理、邀请返利和委托授权相关的文档；不把 1272 份无关领域文档逐篇重读冒充本主题核验 |
| 证据原则 | 搜索只用于定位；结论来自实际打开的迁移、鉴权、查询、worker、资金和历史分支源码 |
| Observed regions | 24 |
| Inferences | 4 |
| Open questions | 3 |
| 本轮行为 | 只更新调研记录；不修改鉴权、schema、资金、配额、部署或生产路由 |

## 一、大白话结论

HUAKAI 不是完全没做过“下级代理”。历史分支 `feat/reseller-phase1` 确实做过半套：

1. 给租户加父子关系、批发倍率、共享池/专属账号等字段。
2. 把子租户管理员识别成受限管理员。
3. 递归加载它名下整棵租户子树，并允许管理自己和所有后代租户。
4. 阻止子租户接触上游账号和凭据控制面。

但这套没有成为可用产品：

- 主线没有相关 migration。
- 分销商创建、列表、停用、改价、账号分配等 CRUD 没有实现。
- 两层扣费、批发余额、零售定价、白牌域名和分销商运营台没有实现。
- 历史 migration 自己声明只是休眠结构，查询文件也明确说 CRUD 留待后续。
- 真正进入运行时的只有“按父子树放权”的鉴权改造。

更重要的是，这套历史逻辑与 Owner 现在明确的边界冲突：

- 当前主线把任何 `users.role='admin'` 的有效会话直接变成全平台管理员。
- 历史分销分支把“谁是谁的商业上级”直接推导成“谁可以管理谁的全部业务数据”。
- Owner 现在要求部署管理员可以授权租户管理员使用某项能力，但部署管理员不能替任意租户执行；这需要独立的授权关系，不能继续靠一个 `role` 或 `parent_tenant_id` 推导。

一句话：**项目里有多租户地基、有用户返利、有历史分销半成品，但没有把“部署治理、租户经营、最终用户、商业上下级、委托管理”五件事真正拆开。**

## 二、我怎么查的

仓库当前共有 216 个本地和远端跟踪 ref。本轮使用 `git log --all`、`git show <ref>:<path>` 和 ref 级源码定位，找出全历史中 18 个本主题相关文档路径，重点亲读：

- `2026-05-30-role-panel-switch-synthesis.md`
- `2026-07-01-role-based-auth-migration-claude.md`
- `2026-07-15-coadmin-and-merchant-tenant-arc-claude.md`
- `2026-07-15-reseller-arc-final-model.md`
- `2026-07-15-reseller-phase1-codex.md`
- `2026-07-15-reseller-slice2-s1-s2-codex.md`
- `community-invitation-referral.md`
- `2026-06-26-referral-deadcode-removal.md`

并打开主线与历史分支的实际 migration、身份解析器、租户作用域查询、平台设置、邀请返利结算、订阅和启动种子源码。文档归并主索引也明确把 `reseller-distribution` 标为尚未建立源码核验 SSOT 的领域，不能把旧计划直接当现状。

## 三、主线现在到底是什么

### 1. 部署者身份与租户管理员混在一起

当前 `adminsessionauth.Resolver` 在验证用户 session 和 `users.role` 后，只要角色精确等于 `admin`，就构造：

```text
Role = platform_admin
ScopeTenantID = 0
```

证据：`backend/internal/adminsessionauth/resolver.go:67-91`。

当前 `AdminIdentity.CanIssueForTenant` 又规定：

- `platform_admin` 可操作任意租户。
- `tenant_operator` 只能操作 `ScopeTenantID`。

证据：`backend/internal/admin/operator_auth.go:140-157`。

这意味着主线现在没有“部署者只管平台治理，但不能进入租户业务”的角色。登录管理员天然就是跨租户业务管理员。

### 2. 程序化管理员也只有两档

`admin_tokens` 只有：

- `platform_admin`：scope 必须为空，可跨全部租户。
- `tenant_operator`：必须绑定一个租户。

证据：`backend/sql/migrations/0010_admin_auth.up.sql:18-49`。

它能表达“全平台”或“一个租户”，不能表达：

- 部署者只可开通/停用租户能力，但不可查看或修改租户业务数据。
- 某租户管理员获准使用 Cookie 导入，但不能使用 Agent Identity。
- 某上级代理可以卖给下级，但没有下级后台管理权。
- 某上级代理只在限时支持关系中获得下级的指定操作权限。

### 3. 用户只能属于一个租户

`users` 行直接带一个 `tenant_id`，没有 tenant membership、organization membership、user-role assignment 或 delegated grant 关系。

证据：`backend/sql/migrations/0007_l0_inbound_auth.up.sql:17-39`。

`users.role` 也只有 `admin/user` 二值，原始 migration 说明它只是人的面板角色，不是多级授权体系。

证据：`backend/sql/migrations/0076_user_role.up.sql:1-14`。

### 4. 平台设置不是租户授权系统

`platform_settings` 第一版只支持 `scope='global'`；service 的读取和写入也固定使用 `GlobalScope`。

证据：

- `backend/sql/migrations/0077_platform_settings.up.sql:3-28`
- `backend/internal/platformsettings/service.go:73-97`
- `backend/internal/platformsettings/service.go:100-166`
- `backend/internal/platformsettings/service.go:262-276`

允许键目录中也没有“给某个租户授权某项高风险能力”的结构。Cookie、Setup Token、Agent Identity、CRS 不能靠加一个全局 bool 就满足 Owner 的授权边界。

### 5. 多租户现在是隔离地基，不是完整经营体系

`tenants` 从基础 migration 起就是各业务表的隔离锚点，但注释和启动逻辑仍把当前产品定位为“单租户后台 + 多用户运营 + 多租户预留”：

- `backend/sql/migrations/0001_pool_routing.up.sql:10-24`
- `backend/internal/tenancy/bootstrap.go:1-8`
- `backend/internal/tenancy/bootstrap.go:58-64`

启动代码只保证至少有一个工作租户，不负责租户入驻、关系、授权或代理经营。

## 四、历史分销分支实际做了什么

### 1. 建了休眠 schema

`0185_reseller_phase1_tenant_hierarchy.up.sql` 增加：

- 租户父级。
- 上游共享/专属模式。
- 平台域名/自定义域名模式。
- 公告继承模式。
- 透明度模式。
- 批发倍率。
- 专属上游账号分配表。

证据：`feat/reseller-phase1:backend/sql/migrations/0185_reseller_phase1_tenant_hierarchy.up.sql:1-75`。

该文件第一行明确说运行时在后续切片接入前不会读取这些列。主线和绝大多数分支不含该 migration；包含提交 `17350fae` 的只有：

- `feat/reseller-phase1`
- `integration/r4-test`
- `origin/feat/reseller-phase1`

### 2. 没有分销商 CRUD

历史查询文件只实现：

- 根据用户、租户、状态和 `role=admin` 解析 session 管理员。
- 递归读取活动租户子树。

文件注释明确写着分销商 CRUD 留待后续。

证据：`feat/reseller-phase1:backend/sql/queries/admin_resellers.sql:1-55`。

没有观察到 `/admin/v1/resellers`、创建分销商 service、批发倍率写 service、账号分配 service 或对应生产 handler。

### 3. 商业父子关系被直接当成管理权限

历史 resolver 的规则是：

- 根租户的 admin 用户自动成为 `platform_admin`。
- 只要租户有父级，其 admin 用户自动成为 `tenant_operator`。
- `tenant_operator` 的可操作范围是自己加全部活动后代。

证据：

- `feat/reseller-phase1:backend/internal/adminsessionauth/store_postgres.go:28-50`
- `feat/reseller-phase1:backend/internal/admin/operator_auth.go:314-331`
- `feat/reseller-phase1:backend/sql/queries/admin_resellers.sql:20-55`

这相当于把“我从你这里进货”自动解释成“你能管理我的全部后台”。成熟平台通常不会把这两个关系焊在一起。

### 4. 部署者仍可为任意租户处理

历史 `CanActOnTenant` 对平台全域身份直接放行所有正租户。

证据：`feat/reseller-phase1:backend/internal/admin/operator_auth.go:314-331`。

这与 Owner 最新要求“部署管理员不可为任意租户处理”直接冲突。

### 5. 历史 branch 还有一个 dormant schema 风险

专属账号分配表把账号所有者和账号做了复合外键，但 `consumer_tenant_id` 没有直接外键到 `tenants`。

证据：`feat/reseller-phase1:backend/sql/migrations/0185_reseller_phase1_tenant_hierarchy.up.sql:59-75`。

当前它没有生产读写者，所以不构成主线线上故障；若未来复用该 migration，必须先补齐消费者租户存在性、租户关系和删除策略约束。

## 五、邀请返利与订阅不是代理体系

### 1. 邀请返利是同租户用户增长

正式规格明确把 multi-level commission trees 排除在范围外。

证据：`docs/specs/community-invitation-referral.md:14-19`。

生产结算链在最终用户首次成功计费后，给同租户推荐人发一次幂等奖励：

- settle 后触发资格/奖励：`backend/internal/audit/receipt_worker.go:390-444`
- Serializable 事务发奖励：`backend/internal/payment/referral_reward.go:67-120`
- 创建充值单、credit、审计并标记 rewarded：`backend/internal/payment/referral_reward.go:126-214`

它是推广奖励，不是代理批发、加价、下级租户或委托管理。

### 2. 订阅是同租户最终用户权益

订阅计划和实例都以 tenant/user 为边界，授予用户组和配额窗口。

证据：`backend/sql/migrations/0073_subscription.up.sql:15-96`。

它能作为下级用户的商品，但不能表达代理商批发余额、代理利润、下级租户关系或管理权限。

## 六、成熟分发模式给出的关键答案

### 1. 商业关系和管理关系必须分开

Microsoft CSP 官方文档明确区分：

- reseller relationship 允许合作方代表客户购买。
- 要管理客户服务或订阅，还必须另有独立 admin relationship。
- 只做采购/交易并不自动获得客户后台管理权。

来源：

- https://learn.microsoft.com/en-us/partner-center/customers/request-a-relationship-with-a-customer
- https://learn.microsoft.com/en-us/partner-center/enroll/csp-supported-partner-relationships

这正好解释 HUAKAI 历史方案的问题：`parent_tenant_id` 可以表达商业上下级，但不能自动成为授权树。

### 2. 委托管理应是显式、细粒度、可撤销关系

Microsoft GDAP 要求客户显式批准具体角色和确定期限，并把关系生命周期与访问分配分开。官方也说明这种能力是可选的，不是每个客户关系自动拥有。

来源：

- https://learn.microsoft.com/en-us/graph/api/resources/delegatedadminrelationships-api-overview?view=graph-rest-1.0
- https://learn.microsoft.com/en-us/partner-center/customers/gdap-faq

HUAKAI 不一定照搬“客户审批”流程，但至少应保留这些性质：

- 显式授权。
- 指定能力。
- 指定目标租户。
- 默认无权。
- 可撤销。
- 有完整审计。
- 可选有效期。

### 3. 部署治理账号不应承载租户业务资源

AWS Organizations 建议管理账号只用于必须由管理账号完成的任务，把业务资源放在成员账号；委托管理员也是按具体服务授予，而不是天然获得所有业务操作权。

来源：https://docs.aws.amazon.com/organizations/latest/userguide/orgs_integrate_delegated_admin.html

映射到 HUAKAI：部署者负责系统、租户生命周期、能力授权、全局安全和汇总健康，不应该天然成为每个租户的业务管理员。

### 4. 多方资金要分账，不要把一个余额当三种钱

Stripe Connect 把平台和 connected account 建模为各自独立余额，并明确多方资金在平台、商户和其他参与方之间分配。

来源：

- https://docs.stripe.com/connect
- https://docs.stripe.com/connect/account-balances

若 HUAKAI 后续建设批发/零售模式，代理预付、最终用户余额、平台收入和代理利润必须是可区分的责任与账，不应只在一个用户余额上乘两次倍率。

## 七、推荐的 HUAKAI 模型

不要再用一个 `role` 字符串或一棵租户树承担全部语义。至少拆成四层关系：

### 1. 主体身份

- `deployment_operator`：部署治理主体。
- `tenant_admin`：租户经营主体。
- `end_user`：最终用户。

“代理商”更适合作为租户的商业类型或关系，不是第四种万能管理员角色。

### 2. 资源所有权

- 每条租户业务资源只属于一个 tenant。
- 部署级资源只属于 platform scope。
- 部署者不能通过传 `tenant_id` 进入租户业务 handler。

### 3. 商业关系

单独表达谁向谁进货、谁负责批发价、谁承担余额和坏账。该关系可以支持多级，但不自动授予后台管理权。

### 4. 授权关系

分成两类：

- **租户能力授权**：部署者给某个租户开通 Cookie、Setup Token、Agent Identity、CRS 等能力，默认关闭。
- **委托管理授权**：某主体是否可以替某个目标租户执行哪些操作。默认不存在，不能由商业父子关系推导。

Owner 已明确的边界应落成：

```text
部署者：可以 grant/revoke 某租户能力
部署者：不能调用该租户的账号导入、凭据、用户、余额等业务操作
租户管理员：只有获权后，才能在自己 tenant 内执行对应能力
最终用户：只能使用自己的用户资源和 API 权益
```

若未来允许上级代理帮助下级运营，必须另建显式委托关系，并至少限制：

- 目标租户。
- 可执行能力。
- 有效期。
- 是否允许继续委派。
- 审计和撤销。

绝不能恢复成“只要是父租户，就能管理整棵子树”。

### 两种可落地运营方案

**方案 A：严格独立经营**

- 代理可以创建或邀请下级租户，形成商业关系。
- 每个租户管理员只管理自己的租户。
- 上级只能看订单、批发额度、结算和脱敏汇总，不能进入下级用户、Key、余额或凭据。
- 优点：权限最直观、跨租户风险最低、最符合“部署者/租户/用户”三层边界。
- 代价：下级遇到运营问题时，上级不能直接代处理，只能给指导或由部署者提供平台级诊断。

**方案 B：独立经营 + 可选委托支持**

- 默认行为与方案 A 相同。
- 下级租户可以额外授予上级指定能力、指定期限的委托支持权限。
- 委托只能是明确白名单，默认不含凭据明文、余额调整、审计导出和继续委派。
- 优点：保留成熟代理体系的服务能力，同时不把商业父子关系自动升级成后台控制权。
- 代价：需要授权生命周期、撤销、到期、审计和更完整的跨租户测试。

推荐先按方案 A 建地基，把方案 B 作为显式可选能力。这样不会因为未来可能需要代运营，就在首版把整棵子树默认暴露给上级。

## 八、与历史方案相比的成果

| 维度 | 历史 `feat/reseller-phase1` | 推荐模型 |
| --- | --- | --- |
| 部署者 | 根租户 admin 自动成为全平台管理员 | 独立部署治理主体，不进入租户业务 |
| 租户管理员 | 子租户 admin 自动管理全部后代 | 默认只管自身 tenant |
| 商业上下级 | 直接使用租户父子树 | 独立商业关系 |
| 委托管理 | 从父子树自动推导 | 显式 grant，默认无权 |
| 高风险能力开通 | 没有租户能力授权模型 | 部署者 grant/revoke，租户管理员自行执行 |
| 最终用户 | 与代理体系没有清晰分层 | 始终是租户内用户，不参与后台授权 |
| 钱账 | 文档计划两层扣费，未实现 | 先定义独立责任与账，再决定批发/佣金 |
| 运维 | 依赖角色和树推断 | 可直接看到谁授权、谁执行、作用域和到期 |

## 九、确认问题与风险

### 已确认问题

1. 主线 session admin 当前是全平台跨租户身份，不满足三角色边界。
2. 主线没有租户能力授权、委托管理、成员关系或商业关系模型。
3. 历史分销 branch 是半成品，不可直接并入主线。
4. 历史 branch 把商业父子关系自动变成管理子树权限，方向不适合当前要求。
5. 邀请返利和订阅不能替代代理体系。
6. 历史专属账号分配表缺消费者租户直接外键，复用前必须修正。

### 推断

1. 继续沿用 `platform_admin/tenant_operator` 两档会让新增 Cookie、Setup Token 等授权再次混入跨租户管理员语义。
2. 若批发余额和最终用户余额不分责任账，后续退款、坏账、重放和争议会难以解释。
3. 若允许上级代理自动管理下级，账号凭据和用户余额会成为最高风险的跨租户越权面。
4. 最稳妥的迁移顺序应先拆身份与授权，再恢复历史分销 schema，而不是先把 0185 合并。

### Open questions

1. 下级代理创建一个子租户后，默认只能卖额度，还是需要管理子租户的用户、Key 和余额？
2. 若允许帮助下级运营，授权由部署者授予、下级租户确认，还是双方都要确认？
3. 商业收益首发采用“批发加价”还是“平台按成交发佣金”？两者资金和税务责任完全不同。

## 十、下一步建议

1. 暂不合并 `feat/reseller-phase1`，把它视为历史原型和测试素材。
2. 先写一份新的三角色授权决策稿，锁定部署者不可代操作这一不变量。
3. 第一实现切片只建设身份/授权判断和只读权限矩阵测试，不碰资金。
4. 第二切片建设租户能力 grant/revoke，让 Cookie、Setup Token 等高风险功能有正确授权底座。
5. 第三切片再决定商业父子关系和是否需要显式委托管理。
6. 批发余额、两层结算、佣金、退款和坏账责任必须作为独立 money 决策，不夹在普通租户 schema 中实现。

## Source Coverage Proof

实际读取的主要源码区域：

- 当前 admin session：`backend/internal/adminsessionauth/resolver.go`
- 当前 admin 身份：`backend/internal/admin/operator_auth.go`
- 当前 admin token schema：`backend/sql/migrations/0010_admin_auth.up.sql`
- 当前 users/tenant 关系：`backend/sql/migrations/0007_l0_inbound_auth.up.sql`
- 当前 users.role：`backend/sql/migrations/0076_user_role.up.sql`
- 当前 platform settings：`backend/sql/migrations/0077_platform_settings.up.sql`、`backend/internal/platformsettings/service.go`、`types.go`
- 当前 tenant 地基：`backend/sql/migrations/0001_pool_routing.up.sql`、`backend/internal/tenancy/bootstrap.go`
- 当前邀请返利：`backend/internal/audit/receipt_worker.go`、`backend/internal/payment/referral_reward.go`
- 当前订阅：`backend/sql/migrations/0073_subscription.up.sql`
- 历史分销 schema：`feat/reseller-phase1:backend/sql/migrations/0185_reseller_phase1_tenant_hierarchy.up.sql`
- 历史分销查询：`feat/reseller-phase1:backend/sql/queries/admin_resellers.sql`
- 历史身份解析：`feat/reseller-phase1:backend/internal/adminsessionauth/store_postgres.go`
- 历史授权裁决：`feat/reseller-phase1:backend/internal/admin/operator_auth.go`

真实性摘要：确认事实来自上述源码和全历史对象读取；成熟模式结论来自 AWS、Microsoft、Stripe 官方文档。四项推断已单独标注，没有把历史计划当成现网实现。当前有 3 个产品问题必须由 Owner 决定后，才能进入 auth/schema/money 实现。
