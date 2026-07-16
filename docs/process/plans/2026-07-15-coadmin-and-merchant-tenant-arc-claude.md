# 平台协管员 + 入驻商家(白牌多租户)大 arc · Claude 独立稿

日期:2026-07-15。基于三路调研(HUAKAI 现状 / 三镜 / 成熟 RBAC)+ Owner 澄清。

## 0. 需求定性(Owner 澄清后,两个独立机制)

| | 协管员(平台店员) | 入驻商家(独立店主) |
|---|---|---|
| 本质 | 帮 Owner 打理**平台本身**的内部管理员 | 入驻平台的**独立个体**,Owner 是房东 |
| 作用域 | Owner **自己的租户内** | **各自独立的新租户** |
| 权限 | Owner 的顶级权限的**自定义子集**(100 给 90) | 在自己租户内是完整 admin |
| 数量 | 可设很多个 | 可入驻很多个 |
| 数据 | 就是 Owner 的数据 | 独立、隔离 |
| 公告 | **跟随平台公告**(他就是平台方) | 自己的独立公告 |
| 域名 | 平台域名 | **自己的独立域名(白牌)** |

结论:两机制**共用一套 RBAC 底座**,只是作用域不同——协管员的权限作用在平台租户,商家(及其自己的协管员)作用在商家租户。

## 1. 入驻商家(白牌多租户)—— HUAKAI 的现成资产

**核心判断:HUAKAI 从地基就是多租户(tenant_id 贯穿全表 + 复合 FK),商家要的隔离大半免费。**
- 数据隔离:每表 tenant_id,天然隔离(现状调研坐实)。
- 公告隔离:`announcements` 已是 tenant 级一份(0102,唯一过滤维度 tenant_id)——商家独立公告=现成。
- 用户组/账号池/配额:pool_groups、user_group、quota_policies 全 tenant 级——现成。
- 三镜均为单租户,**此项 HUAKAI 领先,无可借鉴,靠自有架构**。

**要新建的:**
1. **租户自定义域名(白牌)**:新表/列 tenant_domains(tenant_id ↔ domain);网关入站按 Host header 认租户(现在按认证上下文定 tenant,需加 Host→tenant 解析层)。TLS 证书(每域名)运维侧。
2. **平台侧「商家管理」面**(超管专属):建商家(开新租户+初始 admin)/停用/配额上限/看概览。碰租户创建=schema+auth,Owner-gated。
3. **商家计费/分账**(碰钱,可后置):商家向平台买额度 or 按用量结算;此前 [[multi-level-agent-reseller-direction]] 的逐级额度分配在此落。

## 2. 协管员(平台内操作级 RBAC)—— 照 new-api 借鉴

**三镜里唯一有真分层+操作级 RBAC 的是 new-api**(4 级角色 + casbin resource:action,root 独占授权、admin 不可委派、admin 管不了同级/root)。借鉴其模型,不抄实现。

**模型:**
1. **权限点目录(capability catalog)**:平台全部功能模块登记成权限点。粒度两档融合(见决策点 D1):模块级为主(账号管理/渠道/用户/配额/…),敏感模块再拆操作级(如渠道:读/改/建删/看密钥;账号:增/删)。目录做成单一真相源(前端授权勾选界面从目录自动渲染)。
2. **授权**:超管给每个协管员勾选任意子集;可存为**角色模板**(如 客服/财务只读/账号运营)复用+可微调。
3. **安全铁律**(借 new-api,硬性):① 授权权**超管独占**(协管员不能给自己或他人加权);② 协管员**不能再委派**下级;③ 协管员**不能管理超管和同级协管员**;④ 后端每个受控 API **强制校验权限**(前端隐藏菜单只是 UX,不是安全边界)。
4. **公告**:协管员在平台租户内,公告本就 tenant 级——**天然共享平台公告,零额外工作**(正是 Owner 要的「公告随我」)。

**HUAKAI 现状缺口:**
- `users.role` CHECK 卡死 admin/user 两值(0076)→ 需放开/新增角色维度(auth-core Owner-gated)。
- 零权限表/权限位 → 新建 capability 模型(admin×权限点 或 角色模板表)。
- session-admin 是 all-or-nothing 平台全权(adminsessionauth/resolver.go)→ 改为按权限集裁。
- 前端壳二值(user/operator)、nav 无权限字段、router 无逐路由守卫 → 升级为权限驱动壳。
- 可复用原语:panelauth deny-by-default 解析器、admin_tokens 的 scope 雏形、nav 数据结构。

## 3. 统一底座与分期

**底座**:一套 capability 模型,作用域参数化(platform tenant / merchant tenant 同构复用)。

**分期(全 Owner-gated,逐期拍板):**
- **P1 权限内核**:权限点目录 + capability schema + 后端权限校验中间件 + 超管授权面。先让「给协管员勾权限、后端真强制」跑通(后端安全边界优先于前端好看)。
- **P2 权限驱动前端**:运营台壳从二值升级为按权限集裁菜单/页/按钮;每受控路由挂守卫。
- **P3 商家租户**:自定义域名 + 网关 Host 路由 + 平台商家管理面(建/停/配额/概览);复用 P1 RBAC 让商家在自己租户内当 admin。
- **P4 商家计费/分账**(碰钱,可再后置或与 P3 合)。

安全监测+日志模块(Owner 另提)独立排期,不进本 arc。

## 4. 请 Owner 拍板的决策点

- **D1 权限粒度默认档**:Owner 举例「100 模块给 90」像**模块级**(每模块一个开关),但「账号 增/删 分开」又是**操作级**。建议:**模块级为主 UX(勾模块)+ 少数敏感模块内置操作级拆分**(钱/密钥/删除类)。要确认这个默认,还是要全模块都拆到操作级(更细但勾选更累)。
- **D2 角色模板**:要不要预设几个角色模板(客服/财务/运营/账号管理)开箱即用,还是纯自定义逐项勾选(建议:两者都给,模板可微调)。
- **D3 商家计费**:P3 先只做「开商家+隔离+域名」不接钱(手动配额),还是一步到位接分账。
- **D4 分期起点**:先做协管员(P1-P2,你内部马上能用)还是先做商家(P3,对外招商)?建议先协管员(纯内部、风险小、见效快)。

## 5. 权限点清单(操作级,resource:action)—— 协管员授权的原子单位

动词统一 read/create/update/delete/manage(manage=该资源全权)。按模块分组,前端授权界面按此渲染(勾模块=该模块全操作,展开可细调单操作)。

- **账号池**:account:read/create/update/delete + account:credential(看/轮换凭证,敏感单列)
- **渠道/分组**:channel:read/manage;group:read/create/update/delete
- **API Key**:apikey:read/create/update/revoke
- **用户**:user:read/create/update/delete + user:impersonate(代登录,高危)
- **配额限流**:quota:read/update
- **计费余额**:billing:read + billing:adjust(手动充扣,高危)+ billing:pricing(改价,高危)
- **公告/设置**:announcement:read/manage;settings:read/manage
- **可观测**:log:read;audit:read(审计,通常仅超管)
- **授权自身**:role:read/assign/manage(委派,受防提权约束)

**敏感项默认只留超管、不进任何下级模板**:account:credential、billing:adjust、billing:pricing、user:impersonate、audit:read、role:manage(与既有「动钱/凭证/审计=Owner-gated」裁定一致)。

## 6. 预设角色模板(模板+可微调,借 GitLab Custom Roles 范式)

| 模板 | 用途 | 关键权限 |
|---|---|---|
| 超级管理员 | Owner 本人 | 全部含敏感项+role:manage |
| 账号运营 | 维护账号池/分组/渠道 | account/channel/group 全套+apikey 读建改+log:read |
| 客服 | 处理用户,不碰钱不碰账号池 | user 读改+apikey 读改吊+quota+billing:read+公告读+log |
| 财务只读 | 对账 | billing:read+quota:read+log+user:read |
| 只读观察 | 旁观/审计 | *:read(排除凭证/审计) |
| 二级代理 | 分销(需 scope) | account/apikey/user 建改+billing:read,**强制限自己名下** |

Owner 场景「下属只加账号/删账号/建分组/管 Key」= 账号运营模板按需砍 account:delete,不必重定义。

## 7. 三档路线(成熟范式定型:K8s RBAC / AWS IAM ABAC / GitLab / Casbin)

- **A 模块级**(只控能否进模块页):不够,进了账号页就建删同权,满足不了「能加不能删」。不作终态。
- **B 操作级 RBAC**(resource:action,四表 roles/permissions/role_permissions/user_roles+RequirePermission 中间件):**协管员首发目标**,自研即可(暂不引 Casbin,契合「代码不堆砌」),覆盖 Owner 当前诉求。
- **C 操作级+数据范围 ABAC**(叠 owner_id/tenant scope,服务层注入过滤防 IDOR):二级代理/商家「只管自己名下」的终态;配 IDOR 集成测试(有前科:auditexporthttp)。

强制点铁律:后端每 API 校验 resource:action(中间件)+ 数据层注入 scope(防越权读);前端隐藏菜单只是 UX 不算安全边界(有前科:裸 fetch 绕过/漏注 admin Bearer)。委派防提权三规则(借 K8s escalation prevention):不能授予自己没有的权限、只能授自己数据范围子集、超管/平级受保护。

## 8. 下一步

本稿 = Claude 独立稿。按双计划规则,Owner 定 D1-D4 方向后,codex 出独立稿交叉,再合成综合方案,分期实现。**建议起点:协管员 B 档(P1 权限内核→P2 前端权限壳)**——纯内部、风险小、你马上能用;商家白牌(P3)、代理树 scope+委派(碰钱)随后。全程 auth-core/schema/钱 = Owner 逐期点头。
