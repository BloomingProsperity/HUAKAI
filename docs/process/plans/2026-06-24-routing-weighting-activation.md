# 路由加权激活闭环(routing weighting activation)

日期:2026-06-24
分支:`feat/routing-weighting-activation`(off `feat/frontend-portal` @a45354b3)
作者:Claude(claude-routing-weight)

## 1. 范围(scope)

把"admin 已可设/可校验/可落库的 binding `selection_mode='priority_weighted'` + 账号 `static_weight`"
从**死开关**接成**真闭环**:admin 显式给某 binding 设 `priority_weighted` 后,该 binding 命中的请求
在同优先级账号之间按账号 `static_weight` 加权选号(高 weight 账号被选概率显著更高);binding 默认
`strict_priority` 时选号行为与接线前**逐一字节一致**(仍走均匀 Shuffle)。

**opt-in 激活,非全局默认翻转**——这是本切片可自主合并的硬前提。

## 2. 两处断点(已亲核真码,file:line)

### 断点1:生产从不注入 RoutingPolicySource → policy() 恒 nil
- `backend/internal/pool/router/default_selector.go:170-175` `policy()`:`if s.policies == nil { return nil, nil }`。
- `backend/internal/pool/router/default_selector.go:261` 加权分支门控:`if policy != nil && policy.SelectionMode == SelectionModePriorityWeighted`。
- 生产构造 `backend/cmd/gateway/selector_wiring.go:116-122` `NewDefaultSelector(...)` **没传** `WithRoutingPolicySource`,
  故 `s.policies==nil`,`policy()` 恒返回 `nil`,加权分支永不可达,恒走 `default_selector.go:271-273` 的 `Shuffle`(均匀随机)。
- 旁证:`backend/scripts/deadcode-baseline.txt:452/474` 把 `WithRoutingPolicySource` 标记为 `unreachable func`(死代码)。

### 断点2:routerPoolMetadataFromRegistry 丢弃 selection_mode/weight
- `backend/internal/gatewayhttp/chat_completions_dispatch.go:376-379`:把 `registry.BindingMetadata` 拷成
  `router.PoolCandidateMeta` 时只拷 `PoolGroupID`+`ProviderModelID`,**丢弃** `SelectionMode`/`Weight`。
- `registry.BindingMetadata`(`backend/internal/registry/registry.go:71-78`)本身**已带** `SelectionMode string`
  与 `Weight int32`,且 `postgres_registry.go:186-187` 已从库正确解析进来——元数据到了 dispatch 但没往下传。

## 3. weight 语义辨析(两个 weight 概念,务必别混)

- **account 级 `static_weight`**:`provider_accounts.static_weight` → `AccountSnapshot.Weight`
  (`backend/internal/pool/dispatcher/account_source.go:68` `Weight: r.StaticWeight`)。
  这是 `weightedReservoirIndex`(`default_selector.go:279-290` `accountWeight()`)**真正读取**的权重——
  即"同优先级账号之间谁更可能被选"。**本切片要点亮的就是这条**:account 级 weight 已全程接好,只差 mode 开关。
- **binding 级 `weight`**:`model_pool_bindings.weight` → `BindingMetadata.Weight`。这是**跨 binding/pool_group**
  的排序权重(哪个 pool group 优先),与"同 pool 内账号加权"是**不同维度**。`weightedReservoirIndex` **不读它**。
  本切片**不**用 binding 级 weight 改 intra-pool 选号(避免语义混淆);binding 级 weight 的 inter-binding 排序
  是另一独立切片,留 TODO。

### selection_mode 按谁取?
按**当前请求命中的 binding** 取,不是 pool_group 级全局。
dispatch 端 `ex.attempt.PoolGroupID` 唯一定位本次 attempt 的 binding,
`ex.activeBindingMetadata()`(`chat_completions_dispatch.go:384-400`)已能按 `PoolGroupID` 取到对应
`BindingMetadata`(含 `SelectionMode`)。故 selection_mode 是 per-binding、per-attempt 的。

## 4. RoutingPolicy / WithRoutingPolicySource 签名(已核)

- `type RoutingPolicySource interface { GetRoutingPolicy(ctx, req SelectionRequest) (*RoutingPolicy, error) }`
  (`types.go:187-189`)——入参是完整 `SelectionRequest`。
- `func WithRoutingPolicySource(v RoutingPolicySource) SelectorOption`(`default_selector.go:42`,
  `pool` 包别名 `api.go:155`)。
- `RoutingPolicy` 其它字段(`ModelAccountIDs`/`TopKDefault`/`BroadTopK`/`OperatorScoring`/`Fallback*`)若全留零值,
  与"policy==nil"行为**完全等价**(已核 `topK`/`hasModelRoute`/`fallbackPlan` 三处对 nil 与零值策略走同分支)。
  故只填 `SelectionMode` 的 policy 是**行为保持**的,唯一变化就是预期的加权激活。

## 5. 实现路线(闭环穿线)

为让 `RoutingPolicySource` 拿到 per-request 的 selection_mode,**给 `SelectionRequest` 加一个
`SelectionMode string` 字段**(默认空="strict_priority" 等价),并:

1. **穿线(断点2)**:`router.PoolCandidateMeta` 加 `SelectionMode string`(`backend/internal/router/route_plan.go`);
   `routerPoolMetadataFromRegistry`(dispatch:376)把 `binding.SelectionMode` 带进去。
2. **请求构造**:`selectPoolAccount`(dispatch:597)给 `pool.SelectionRequest` 填
   `SelectionMode: ex.activeBindingMetadata().SelectionMode`(命中 binding 的 mode;取不到 binding 则留空=默认)。
3. **注入(断点1)**:在 `backend/cmd/gateway/routing_policy_source.go`(**新文件,放 cmd/gateway 而非 pool/router**,
   因 pool/router 已达 codebudget 20 文件上限)实现一个无状态 `bindingRoutingPolicySource`:
   `GetRoutingPolicy` 据 `req.SelectionMode == "priority_weighted"` 返回 `&RoutingPolicy{SelectionMode: priority_weighted}`,
   否则返回 `&RoutingPolicy{}`(strict_priority 等价,**非 nil**——但因 SelectionMode 非 priority_weighted 仍走 Shuffle)。
   `selector_wiring.go:116` `NewDefaultSelector(...)` 追加 `pool.WithRoutingPolicySource(newBindingRoutingPolicySource())`。

> 注:返回非 nil 但零值的 policy(strict 路径)不改任何既有行为(见 §4),只让加权分支**可达**。

## 6. 默认保持策略(default-preserved,关键)

- binding 未设 / 设 `strict_priority` → `req.SelectionMode` 为空或 "strict_priority" → policy.SelectionMode≠priority_weighted
  → `rankFresh` 走 `else` 的 `Shuffle`(`default_selector.go:271-273`)——与接线前**同一行 Shuffle**,
  **同输入同种子选号结果逐一相同**(self-proving 测试 B 钉死)。
- 只有 binding 显式 `priority_weighted` 才走 `weightedReservoirIndex`。存量默认 binding 流量分布**一字不变**。

## 7. §16 三镜对照(只读,不抄标识符)

- **sub2api(默认 tiebreaker)** `gateway_service.go@e34ad2b1:2982-3012`:priority 排序后,同
  `(Priority, LoadRate, LastUsedAt)` 组内做**均匀** `mathrand.Shuffle` 分散并发热点,**无**账号级权重。
  = HUAKAI 当前 `strict_priority` 行为。
- **new-api(one-api 血统)** `model/ability.go@1ac0f580:124-138`:先取最高 priority band,band 内做**加权随机**,
  概率 ∝ `weight + floor`(floor 让 weight=0 仍有基础概率)。= HUAKAI `priority_weighted` 经
  `weightedReservoirIndex` 镜像的模式(HUAKAI 自有蓄水池实现,**不抄** `+10` 常量/标识符)。
- **CLIProxyAPI** `@21fad9db`:纯 relay account→API,无同优先级加权选号模块(round-robin/sticky)。**无等价**。
- **delta**:HUAKAI 把两家**融合 + 升级**为 per-binding opt-in 开关——sub2api 永远均匀(无 weight 旋钮)、
  new-api 永远加权(无 per-binding strict 回退);HUAKAI 默认=sub2api 均匀(保行为),opt-in=new-api 式加权,
  **逐 binding 可切**。维度=**算法升级**(同优先级账号选号策略可插)。

## 8. §6 协作冲突协调

涉包均为高发冲突包(pool/router、gatewayhttp、cmd/gateway、internal/router)。
- 进场 `bash .coordination/check.sh`:无 live 编辑。已 `claim.sh` 锁定目标文件。
- **codebudget**:`pool/router` 已达 20 文件上限——**新 RoutingPolicySource 文件放 cmd/gateway**,不碰 pool/router 文件数。
  `chat_completions_dispatch.go` 基线 855 行 + 5% 余量(~42 行),本次只加几行,稳在预算内。
- 改完跑 pool/router 全包测试确认既有选号不变量未破。

## 9. blast radius

- `SelectionRequest` 加字段:加性,零值=旧行为;所有其它 dispatch 站点(embeddings/images/rerank 等)
  不填该字段=空=strict_priority=旧行为,**不回归**。
- `PoolCandidateMeta` 加字段:加性;`internal/router` 的 ranking 当前不读它,无影响。
- 生产注入非 nil policy:仅令加权分支可达;strict 路径行为零变化(§4/§6 已论证 + 测试 B 钉死)。
- 风险点:若误把 strict 也走加权 → 全局翻转(测试 B 变异专抓);若穿线丢字段 → 加权永不激活(测试 A 变异专抓)。

## 10. 测试(变异证伪)

- **A 闭环点亮**:binding=priority_weighted + 账号不同 weight → 大样本统计高 weight 被选概率显著更高。
  变异:穿线改回丢 selection_mode → 退回均匀 → 红。
- **B 默认不翻转(self-proving)**:binding=strict_priority,接线前后同种子选号序列逐一相同。
  变异:strict 也走加权 → 红。
- **C 注入真实**:生产 selector 构造后,对 priority_weighted 的 req,policy() 返回非 nil 且 SelectionMode=priority_weighted。
  变异:不注入 → A 红。

## 11. 成功判据

build/vet/codebudget 绿;pool/router 全包 -count=1 绿;A/B/C 全绿且变异均红;
干净基线 fail 0;默认 binding 选号分布证实与接线前一致。

## 12. 工时估算

~0.5 人日(纯接线 + 测试,无 schema 改动,无 money/auth 触碰)。
