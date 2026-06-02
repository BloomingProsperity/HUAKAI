# S1b 分组路由激活 — Codex 独立草案

来源:codex gpt-5.5 / model_reasoning_effort=xhigh,只读,未看 Claude 草。任务 bs666e14l,全文 `/home/codex/codex_s1b_out.txt`。日期 2026-05-30。

## 核心判断
`GateChain.Allow` 逐候选执行(gates.go:75-80);DefaultSelector 在 filter(default_selector.go:138-145)+ tryLayer(170-174)各跑一次;PASR 段内(pasr.go:193-209)+ 全 ring fallback(319-335)逐账号跑,落到 `p.gates.Allow`(374-379)。而 `GroupPolicyGate.Allow` 每次都查 repo(gate.go:42-49)→ 直接接线把一次 Select 放大成 K 次相同 routes 查询。性能 bug 成立。

## 推荐 Hoist:selection-scoped prepared gate
- gates.go 加可选接口 `SelectionGatePreparer`(不改 `Gate` 接口 :23-25)+ `GateChain.ForSelection(ctx, req) GateChain`:先补齐默认 gate,再对实现 preparer 的 gate 做一次 select 级准备,返回本次 Select 专用局部链。**不能写回 selector 实例**(多 goroutine 共享竞态)。
- GroupPolicyGate 实现 prepare:空 UserGroup/nil repo/空允许集 → allow-all;repo 成功且有允许集 → 返回只读缓存 gate(后续 Allow 只查 map);repo 错 → fail-open + metric/log。现有注释已说明决策只依赖 SelectionRequest(gate.go:40-42),适合 hoist。
- 备选 A(selector 入口硬编码 precheck / ListAccounts 前直接拒):省一次 account 查询但绕开 gate chain + routing reason,不利复用,不推荐。
- 备选 B(PostgresRoutesRepo TTL/cache):改动少但跨请求缓存引入失效/租户隔离/配置可见性问题,不能严格保证每 Select 一次,不推荐。

## 实施步骤
1. 先写 hoist 判别测(gate_test.go,沿用 fake repo 计数):3 候选、premium、PoolGroupID:42、allowed{42},跑 Select 断言 repo 调 1 次;仍逐候选调则多次变红。
2. gates.go 加 `GateChain.ForSelection`(复用默认补齐,不改 nil gate 行为)。
3. DefaultSelector.Select 用局部 scoped gates,传入 filter/trySticky/tryLayer(替代直接读 s.gates)。
4. PASR 同一 scoped gates:Select 内生成局部链,allowAccount/healthStatus 接收局部 gates,替换段内(208)+ 全 ring(335)。
5. GroupPolicyGate 改可 prepare(Allow 保留兼容直接调用;prepare 里查一次)。repo SQL 不变(tenant/user_group/enabled/deleted_at)。
6. fail-open 观测:subscriptionenforce 加 expvar 计数 `group_policy_fail_open_total`(非冻结包);log 用 observer hook 或 wiring 层 wrapper 注 zap;cmd/gateway 已持 logger(selector_wiring.go:52)。
7. 接线真 gate:selector_wiring.go:61-72 设 `gates.GroupPolicy = subscriptionenforce.NewGroupPolicyGate(...)`(带 observer 更好);同一 gates 复用 default + actual PASR + shadow PASR。SHARED,与新机串行。
8. 灌 UserGroup:冻结 chat_completions_dispatch.go selectPoolAccount 的 `pool.SelectionRequest{}` 加 `UserGroup: ex.ident.UserGroup`(auth.Identity.UserGroup 已具备,api_key_resolver.go:142-147 已赋值)。

## 判别性测试 Fixture
1. gatewayhttp 传播测(既有 dispatch_test.go,不新建冻结文件):用 `recordingSelectionRequestSelector`,identity 设 UserGroup:"premium",断言 `selector.requests[0].UserGroup=="premium"`;删 call-site 赋值变空 → 红。
2. routes+selector 集成测(routes_repo_integration_test.go):premium `*→pgPremium`、default `*→pgDefault`;premium 池只有 acctPremium、default 池只有 acctDefault。断言 premium/pgPremium→acctPremium;default/pgPremium→ErrNoEligibleAccount;default/pgDefault→acctDefault。gate 退 AllowAll / WHERE 漏 user_group_match / allowed 判失效 → default 进 premium 池 → 红。
3. wiring 集成测(新建 selector_wiring_integration_test.go,cmd/gateway 非冻结):调 buildSelector,seed premium/default routes,default 用户打 premium 池期望 ErrNoEligibleAccount;未替换 AllowAll → 选到 premium 账号 → 红。
4. fail-open 观测测:fake repo 返 error,断言 Select 仍成功 + metric/observer 恰 +1;fail-closed→Select 红;漏接观测→红;未 hoist→按候选数多次触发→红。

## 风险与 Owner 决策点(codex 提出)
- 拒绝语义现表现为 `ErrNoEligibleAccount`→handler 映射 503 `pool_no_capacity`(dispatch.go:240-244)。若要 403/升级提示是 S1b 之外的产品语义决策。
- fail-open 会在 routes DB 抖动时让 default 进 premium 池。S1a 注释明确选 fail-open(gate.go:29-30);若视为 entitlement/security 闸应改 fail-closed,属行为决策。
- 本轮不建议加 routes 索引/改 schema(现索引 `(tenant_id, match_priority, enabled)`,0001:254-255;查询用 user_group_match)。规模上来后单独提 schema/index 计划。
- shadow 模式 default 主路径 + shadow PASR 各一次 Select → 每被采样请求可能查 routes 两次。S1b 目标是每 Select 一次,非 dispatcher 级跨 selector 共用;若要"一次 HTTP 一次查询"需额外 request cache。
