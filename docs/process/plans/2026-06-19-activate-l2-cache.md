# Plan — 激活 L2 响应缓存(F-CACHE-001 默认翻转 ON)

- 日期: 2026-06-19
- 作者: Claude PM (Owner 直接指令「激活缓存！」= 默认行为翻转授权)
- 基线: origin/feat/frontend-portal @ f1bc0b19
- 分支: feat/activate-l2-cache

## 背景 (禁止凭记忆 — 真码已核)

exact-key 非流式 L2 响应缓存早已建+合并+wired+可观测(PR#52), 仅 flag 默认关 [PARKED] 待 Owner 激活。Owner 直接指令「激活缓存！」→ 翻默认。

**激活 = config/cache_l2.go LoadL2Cache() 默认 Enabled false→true**(:35)。env override 保留: 解析块仅在 HUAKAI_CACHE_L2_ENABLED **被设置时**才覆盖(:40 `raw != ""`), 故默认 true + 运维可用 `=0/false/off/no` 关。

## billing-on-hit 正确性(真码自核 settler.go:425-545 CommitCacheHit)
命中走 Serializable Tx2, claim FOR UPDATE 要求 status='reserving' → UpdateClaimCommitted(**ActualCost=decimal.Zero** :456)→ Capture(**Zero** :465)→ InsertBillingEvent(**ActualCost/Signed=Zero** :481-482, type=claim_committed)→ InsertUsageRecord(**ProviderAccountID=nil, SettlementSource=response_cache_l2, 各 cost Zero** :504+, migration 0043 CHECK 允许)。即: 命中按 $0 成本结清 + 全 billing_event/usage_record 审计行, 无双扣、无零成本却扣费; reserve→hit→commit 正确闭合 claim(开发期 reserve 卡死等顾虑已同链修)。money-path 审计守卫在 chat_completions_billing.go:381(rejectMoneyPathAuditRef "cache_hit_commit")。

## #16 (引用既有)
缓存本身的 #16 已在 docs/process/plans/2026-05-15-f-cache-001-l2-cache-{claude,codex}.md(引 LiteLLM/Portkey/Helicone, billing-on-hit $0 spend row 4/4 验证)。本切片是激活既有功能, 非新建。**"默认 ON" 无镜像先例**: 三镜像无一有 LLM 响应缓存(PR#52 grounding 已证: new-api cache 仅静态资源/sub2/cliproxy 是 session/reasoning-replay)→ 谈不上 default-on 借鉴; 这是 Owner 显式选择(#16 "Owner's explicit choice always overrides")。

## 范围 (success criteria)
- config/cache_l2.go: 默认 Enabled true + 注释说明激活+如何关。
- config/cache_l2_test.go: TestLoadL2CacheDefaultsOff→DefaultsOn(断默认 ON, 含 scope)+ 新 TestLoadL2CacheEnvOverrideOff(env 0/false/off/no 各都能关——证 override 仍生效, 默认 ON 才使此断言 discriminating)。
- 变异验证: 默认改回 false → DefaultsOn 红(已证, grep 确认 1 行变)。

## blast radius
- 仅 config/cache_l2.go(+test)。**完整非集成基线 fail 0**(已跑): 无其它测试假设默认 off 而破。cache-hit 单测(chat_completions_handler_cache_test.go)直构 store 不经 env 默认 → 不受影响。仅 config/cache_l2_test 显式用该 env。3 个集成测试匹配是偶然(quota/billing 结算, 非缓存 HTTP 路径; 且集成测试非标准门禁)。无 schema/迁移/依赖。

## 行为变更影响(Owner 已授权, 但需周知运维)
- **升级即生效**: 未设 HUAKAI_CACHE_L2_ENABLED 的部署现默认开缓存(原默认关)。运维 `=0` 可关。
- **in-memory + 单实例**: 多 pod 各自缓存(命中率低 + 同请求跨 pod 可能一个命中一个新鲜——非正确性 bug, 是 horizontal-scale 局限; Redis 版是 roadmap F-CACHE-002, gateway-core.md:81/125)。
- **仅非流式**: 流式请求从不缓存(透传)。
- **scope=apikey 隔离**: 缓存键含 tenant+apikey principal + serve 时 defense-in-depth 复核 → 无跨 principal 泄漏。
- **新鲜度**: TTL 60s, 完全相同请求体(含 temperature)60s 内复用同响应。

## 门禁
ultracode 对抗审查(billing-path 感知)零 S0/S1 → 干净基线 fail 0(含 cmd/gateway OpenAPI) → squash → ff。**激活后 surface Owner**: 已开 + 上述运维须知 + 建议先观测 PR#52 的 huakai_cache_l2_hit/miss/size 看命中率。

## Clean-room 出处 (#11(d))
- 纯 HUAKAI 内部行为翻转, 无 reference 源读。缓存 #16 见上。Agent: Claude PM. UTC: 2026-06-19
