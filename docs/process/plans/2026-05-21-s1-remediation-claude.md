# §1 用户与权限 收口计划(Claude 独立草案)

> 全面自查后按状态树逐 section 收口。本文件 Claude 独立起草,配对 `2026-05-21-s1-remediation-codex.md`,两份交叉后执行。
> 缺口来源:`docs/process/research/2026-05-21-audit-A.md` §1 逐叶核实。参照口径 sub2api@16793d3af0 / CLIProxyAPI@21fad9db / new-api@20d3e737。

## 1. §1 现状(audit-A)

| 叶子 | 现状 | 缺口 |
|---|---|---|
| 用户注册/登录 | ✅ | MED:缺 2FA、验证码/防滥用、注册默认权益初始化 |
| Session 会话 | ✅ | LOW:已强于三参照,不动 |
| API Key 管理 | ✅ | MED:缺用户自助 CRUD、令牌级 quota/IP/model 策略 |
| 用户组/权限组 | 🟡 HIGH | 未找到组实体/成员/CRUD;routes 表只有 user_group_match 字符串 |
| 管理员权限 | ✅ | LOW:不动 |
| 多租户隔离 | 🟡 MED | 底层 schema 强,但 pool 管理 handler 硬编码默认租户 |

## 2. 收口子任务(按依赖排序)

### 1A 用户组/权限组(HIGH,优先 —— 是 §4 绑定的前置)
- 新增 migration:`user_groups`(tenant_id、name、描述、enabled、优先级、rate 倍率、模型 allow-list/策略 JSON、排他标志)+ `user_group_members`(group_id、user_id,复合唯一)。
- 用户与组:`users` 表加 `default_group_id` 或用 members 表多对多。建议**多对多 members 表**(更灵活,sub2api/new-api 也是组关系)。
- admin CRUD endpoints:创建/列表/详情/更新/删除组 + 加/移成员。租户 scoped。
- 接线:把用户的组解析喂进 `routes` 表的 `user_group_match` 路由判定(audit-A 已确认 routes 有这个字段但无组实体)。
- **决策点 D1**:统一一张 `user_groups`(策略全塞 JSON)vs 分 `user_groups` + `permission_groups` 两张表。倾向单表 + 策略 JSON(HUAKAI 已有 capability flags JSON 先例,简洁;sub2api 的 group 也是单实体多策略)。

### 1B 多租户隔离修(MED,小)
- `admin_pools_handler.go` 及同类 handler 去掉 default-tenant 硬编码,从 admin token 的 scope 取真实 tenant。
- audit-A 证据:`admin_pools_handler.go:84/112/186` 用默认租户;`admin_pool_accounts_handler.go` 已有 tenant scope 校验可参照。

### 1C 注册增强(MED)
- 注册时默认权益初始化(默认组 / 默认配额)—— 依赖 1A 的组。
- 验证码 / 防滥用(注册失败计数已有,补 IP 维度节流)。
- **2FA(TOTP)** —— **碰 authentication core,HIGH 风险,需 Owner 确认**才动。决策点 D2。

### 1D API Key 自助 + 令牌级策略(MED)
- 用户自助 endpoints:用户创建/列表/撤销**自己的** API key(现在只有管理员签发)。
- 令牌级策略:key 上加 quota / IP allow-list / model allow-list(sub2api 令牌、new-api token 都有)。
- 注意:API key 解析是 auth 链路,改要保证现有 resolver 回归绿。

## 3. 决策点(交叉后给 Owner)
- **D1 用户组 schema**:单表+策略 JSON / 双表。
- **D2 2FA**:现在做还是 §1 暂不做(auth-core HIGH 风险);若做,TOTP vs email OTP。
- **D3 §1 范围**:HIGH+MED 全做,还是先做 1A+1B(HIGH+前置),1C/1D 之后补。

## 4. 执行方式
- 1A 先行(§4 前置)。1B 小、可并。1C/1D 在 1A 后。
- 每子任务:codex 实现 + TDD + 全测 + codex review + commit。一子任务一 commit(一 commit 一模块)。
- 1A 的 migration 是 additive(新表),非破坏性。

## 5. blast radius / 风险
- 1A:新表 + 新 CRUD,additive,中风险。
- 1B:改 admin handler 取 tenant,中风险(要测跨租户不串)。
- 1C 2FA:auth-core,HIGH,Owner 门。
- 1D:碰 API key 解析,要回归保证。

## 6. 估时
1A ~2-3 天 / 1B ~0.5 天 / 1C(不含 2FA)~1-2 天 / 1D ~2 天。§1 合计 ~6-8 天 codex。
