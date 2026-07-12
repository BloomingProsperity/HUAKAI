# 2026-06-14 ROUTE-121 主动 RPM/TPM 预算滑窗预检 (claude)

> 主线闭环 slice。无 codex(Owner 指令"全部你来做")→ Claude 自 plan + 实现 + 判别测试(#14)+ 双门(#6)。默认关 = 安全自主落地。

## 背景 / 缺口
`internal/pool/router/gates.go:241` `modelRateLimitGate` **纯被动**:只在上游 429 后写入 `RateLimitResetAt`,再据此把账号排除一小段冷却。没有**主动滑窗计数** → 每次打满 = 一个用户可感知的失败请求 + 延迟。sub2api `PreCheckUsage` / new-api channel rpm 计数器是标杆(主动在打满前绕开)。

## 范围 (in)
1. 新包 `backend/internal/rate/precheck/`(不堆进 frozen 包,守 #13 codebudget):内存滑动/固定窗计数器,key = `accountID`(可选 ×model),维 RPM 与 TPM 两个独立窗。
2. 新 gate `rpmTpmPrecheckGate`(实现 `Gate`),挂进 `DefaultGateChain` 在 `modelRateLimitGate` 之后:当某账号当前窗 RPM 或 TPM 已达配置上限 → 返回 `GateFailureRateLimitPrecheck`,把它排除出本次选号(让 selector 选别的账号,而非打 429)。
3. 计数递增:在 dispatch claim 成功后记一次请求 + 预估 token(沿用 `tokenestimate`);窗到期自动清。
4. 配置:per-account `rpm_limit`/`tpm_limit`(读已有 `provider_accounts` 配置或 cred.Extra,**不新增破坏性 schema**;无配置=不限=保持现状)。
5. 全局开关 `HUAKAI_RATE_PRECHECK_ENABLED`(**默认 off**),off 时 gate 直接 allow(零行为变化)。

## 范围 (out)
- 不接真 Redis/分布式计数(单实例内存优先,与 PASR 同构;多实例一致性进 roadmap)。
- 不动 `modelRateLimitGate` 既有 429 反应逻辑(叠加,不替换)。
- 不碰计费/账本/auth/schema 破坏性变更。

## 验收 (可检验 + 判别)
- AC1 未达限:窗内请求数 < rpm_limit → gate allow。
- AC2 达限排除:窗内请求数 == rpm_limit → 同账号 gate 拒(reason=rate_limit_precheck);**mutation:删掉计数比较 → 该测试必须变红**。
- AC3 TPM 独立:RPM 未满但 TPM 满 → 拒(反之亦然);fixture 用「RPM 低 TPM 高」一条 + 「RPM 高 TPM 低」一条,确保两窗各自生效(非冗余 fixture)。
- AC4 窗滚动:跨过窗边界 → 计数清零,重新 allow。
- AC5 默认关:`ENABLED=off` → 即使超限也 allow(零行为变化保护现状)。
- AC6 无配置=不限:账号无 rpm/tpm 配置 → 永不被本 gate 拒。

## 双门
- unit: `HUAKAI_SKIP_PERF_LATENCY_GATE=1 go vet ./... && go test ./internal/rate/... ./internal/pool/router/...`
- 接口变更面小(新增 gate 字段 + chain 装配)→ 跑 `go build ./...` 全量确认无破坏。

## blast radius / 风险
- 命中选号热路径(gate chain)。缓解:**默认关**;无配置=不限;gate 只**排除**账号不报错;计数 panic-safe(nil store → allow)。
- 误排除风险:窗口实现错可能误杀账号 → AC2/AC4 判别测试 + mutation 守。
- 内存:每账号 2 个小窗,O(活跃账号数),可控。

## 时间估
~0.5–1 天(单实例内存版 + 6 条判别测试 + 装配)。

## Owner 决策点
1. 计数粒度:`per-account` 还是 `per-account×model`?(默认 per-account,够堵 429;×model 更精细但内存×模型数)。
2. limit 配置来源:复用 `provider_accounts`/cred.Extra(本计划默认,无新迁移)vs 新 quota_policy 维度(更重)。
3. 默认关无异议(launch 前再灰度开)。

*不阻塞:按"禁止停滞"先实现默认关版本,Owner 决策点用默认值,有异议再调。*
