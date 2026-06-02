# S1b 分组路由激活 — Claude 独立草案

来源:Workflow architect lane(opus,只读),未看 codex 草。任务 wvccn8pko(recon=Explore + plan=architect)。日期 2026-05-30。

## 已核实事实(file:line)
- gate 决策只依赖 `req`(TenantID/UserGroup/RequestedModel/PoolGroupID),不依赖 account(gate.go:42-58,`_ *AccountSnapshot` 被丢弃)→ K 候选间查询不变,hoist 是保正确性的 memo 化。
- gate 被两循环逐候选调:default_selector.go:144(filter)+ :173(tryLayer)→ 朴素接线 K 候选 ×2 次查库,比预想更重。
- `gates` 同时流入 default selector(:68)+ 每个 PASR 实例(:108,:126)→ 在 :62-64 接一次覆盖 5 模式。
- `pgPool` 在 selector_wiring.go:49 在 scope;`UserGroup` 字段已在 SelectionRequest(types.go:46)+ auth.Identity;dispatch struct literal(chat_completions_dispatch.go:226-239)仅遗漏它。
- **recon 修正**:call-site 是 `chat_completions_dispatch.go:226`(非 attempt.go),冻结包既有文件一行加字段。

## Hoist 设计
### 推荐 H1:SelectionRequest 上的请求级 memo(指针,gate 懒填)
`SelectionRequest` 加 `*GroupRoutesMemo{Loaded bool; Allowed map}`;Select 入口初始化一次;gate Allow 检查 memo 命中复用否则查一次。
- 优:每 Select 一次查询不论候选数 / filter+tryLayer 双遍历;gate 仍是纯 Gate(不改接口);无新包新文件;镜像既有 policy() 每 Select 一次模式;per-request 无跨请求失效。
- 缺:SelectionRequest 多一个 pointer 可变字段(轻微违值类型意图,须文档化);memo 在 pool/router 但被 subscriptionenforce gate 写(分层异味);需 Select 初始化或 gate nil-guard。
### 备选 H2:Select 内显式预过滤(弃 gate)
加 `GroupRoutesSource` 接口,Select 在 policy() 后查一次,直接按 `PoolGroupID∈allowed` 过滤,**从链里移除 GroupPolicy gate**。优:架构最干净、无分层异味;缺:丢弃已建已测 gate + 6 单测,重写语义重付 review/mutation 成本,新增 SelectorOption + 接口,偏离 gate chain 架构。
### 备选 H3:repo TTL+singleflight 缓存。不推荐 S1b(entitlement 路径引入失效语义 + 新失效关注;跨请求超 S1b 范围)。记为路线图选项。
**取舍**:H1 复用已测 gate、blast radius 最小、合"小切片闭合";H2 是长期更优形状但为性能修丢弃已测代码 → 作为 D1 surface Owner;H3 拿性能微赢换 entitlement 面失效负债,现在错。

## 有序步骤
1. types.go 加 GroupRoutesMemo 类型 + 字段;default_selector.go:75 Select 入口初始化一次。
2. gate.go Allow 查 memo(命中复用/否则查一次存)。保留所有短路(空 UserGroup/nil repo/空集/fail-open)。memo nil 时(直接单测)不缓存查询。
3. fail-open 观测:GroupPolicyGate 加可选 `OnRepoError` hook(或 logger+counter),err 分支返回前调。默认 nil = no-op。
4. selector_wiring.go:62-66 设 `gates.GroupPolicy = NewGroupPolicyGate(NewPostgresRoutesRepo(pgPool))`(SHARED,串行)。
5. 冻结 chat_completions_dispatch.go:238 加一行 `UserGroup: ex.ident.UserGroup`。
6. 新建集成测(subscriptionenforce / pool 集成包,非冻结)。
7. 验证:build;scoped test;integration_pg;再全量 `go test ./...`(OpenAPI 一致性仅全量抓)。

## 判别性集成测(经活化 Select 路径,非仅 gate 单元)
routes:premium→{pg5}、default→{pg7};两账号都在 pg5,PoolGroupID=5。
- Case1 allow:premium/pg5/claude → AccountID∈{100,101}。
- Case2 deny:default/pg5 → ErrNoEligibleAccount(default 允许集{7},pg5∉ → 全候选在 GroupPolicy 被滤)。
- memo 断言:fake repo 计数,K=2 候选过 filter+tryLayer 断言 `repo.called==1`(perf 修判别)。
mutation:M1 gate 退 AllowAll → Case2 拿到账号 红;M2 UserGroup 未穿(call-site 漏字段)→ 空档短路放行 红(加"按 dispatcher 方式构造请求"子例);M3 memo 未共享 → repo.called≥2 红。

## 风险 / 决策点
R1 SHARED selector_wiring 撞车(串行);R2 冻结包诱惑(只加一行);R3 memo 分层异味(文档化,Owner 不喜则退 H2);R4 fail-open 掩盖故障(必须 metric 非 debug log,counter `subscription_group_gate_fail_open_total`+WARN);R5 全量套件 OpenAPI 闸;R6 双遍历仍迭代全 gate(既有,scope 外)。
- D1 Hoist H1 vs H2(需 Owner 拍 #10 material decision)。
- D2 fail-open vs fail-closed(确认现 live 仍 fail-open,推荐 fail-open+loud metric)。
- D3 观测面:metric+WARN(推荐)vs log-only vs audit-ledger。
- D4 SHARED 文件串行确认。

## 参考项目对照(refs 可读已 cite)
两参考均为"档→渠道多对多、选择时过滤";HUAKAI routes(user_group_match→pool_group_id)是直接类比,gate 是选择时过滤器。
- new-api@20d3e73:`model/ability.go:17`(group 是 abilities 表主键列)、`:90` getChannelQuery `WHERE group=? AND model=? AND enabled=?`。delta(生态/架构):new-api 按 group+model 扁平 ability 表;HUAKAI 按 tenant+user_group+model_pattern→pool_group + 通配 + 决策在可组合 gate chain。
- sub2api@91da8159:`channel_repo.go:371` channel_groups M:N;`group_repo.go:40-65` Group 富实体(SubscriptionType/RateMultiplier/IsExclusive/ClaudeCodeOnly/ModelRoutingEnabled + 日周月 USD 上限)= 档=订阅档,IsExclusive/ClaudeCodeOnly 即"限档到渠道子集"。delta(架构):sub2api 档直绑渠道;HUAKAI 插 pool_group 层(user_group→pool_group→accounts),档映射到池非裸渠道,且在 gate chain 与 health/exclusion 并列评估。
两参考确认功能形状 parity(无缩水),HUAKAI 的 gate chain + pool_group 间接层是升级 delta。
