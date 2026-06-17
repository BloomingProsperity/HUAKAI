<!-- Plan 文档 · CLAUDE.md #9 plan-before-execute · 2026-06-16 · PM: Claude -->
<!-- 来源: hardcoded-vs-sub2 审计(workflow wfcgbk0gg)2 个高优先 switch gap -->
<!-- 姿态: Owner ① 温和传输手段内;原则 = 争议策略加开关交给运维,默认 = 当前安全值 -->

# Plan:运维开关 v1 —— 代理回退模式 + 暴露渠道健康策略

> **目标**:把两条"sub2 给运维选、而 HUAKAI 写死"的争议策略,做成**默认安全、运维可翻**的开关。
> **原则**(Owner 2026-06-16):开发者只加开关、决定权给运维;默认 = 当前写死的安全值;优先**暴露已有结构**而非新造。
> **状态**:待 Owner 拍板(含 1 个 schema 迁移高危决策点)。实现另起 feature 分支 off `fix/h-fixes`,不进主线,强测试。

---

## 1. 范围(Scope)

**做(本 v1):**
- **切片 A**:代理不健康时的行为 `fallback_mode`(reject / direct / backup)—— **含 schema 迁移(高危,需 Owner 确认)**。
- **切片 B**:暴露 `channelhealth.Policy`(阈值/冷却/封禁时长)给运维 override —— wiring/config 改动(中危,无 schema)。

**明确不做(列后续切片或保持写死):**
- 中优先后续切片:sticky-escape 配置、slot/sticky TTL、PASR `LoadCap` 接线。
- **保持写死**(安全不变式,翻了有合规/安全/审计风险,sub2 也不暴露):跨厂商伪装矩阵、`Proxy=nil` 环境代理隔离、`ManualOverrideRequiresReason`、`AutomaticPostBanRamp=false`。
- **保持写死**(内部算法常量,两家都写死):blend 权重、FNV 轮换、EWMA alpha、段 K=3。

---

## 2. 切片 A —— 代理 `fallback_mode`

### 2.1 现状(写死点)
`backend/internal/provider/postgres_proxy_resolver.go`:`chooseProxyTier` 按 `account > tenant-default > direct` 选层,任一层 **bound-but-unhealthy → `ErrProxyUnhealthy`**(:178/:187-191),无条件 fail-closed,运维无选择权。

### 2.2 sub2 对照(市场验证)
`sub2api@e34ad2b:backend/ent/schema/proxy.go:58-63` 暴露 per-proxy `fallback_mode = none|proxy|direct` + `backup_proxy_id`(自引用链),默认 `none`(=fail)。→ **HUAKAI delta**:默认 `reject`(语义等价 sub2 `none`);HUAKAI 多一层 tenant-default tier 可作额外回退。维度:**生态**(把已有 fail-closed 行为变成运维可选,补齐市场验证过的杠杆)。

### 2.3 设计
- **Schema(迁移 `0148`,加性/可空/有默认 → 不破坏现有数据)**:
  ```
  ALTER TABLE proxies
    ADD COLUMN fallback_mode text NOT NULL DEFAULT 'reject'
      CHECK (fallback_mode IN ('reject','direct','backup')),
    ADD COLUMN backup_proxy_id bigint NULL REFERENCES proxies(id);
  ```
- **Resolver**:`chooseProxyTier` 在 bound-but-unhealthy 时读 `fallback_mode`:
  - `reject`(默认)→ 现行 `ErrProxyUnhealthy`(**零行为变化**)。
  - `direct` → 跳到 direct tier(账号从网关真实 IP 出)。
  - `backup` → 试 `backup_proxy_id`;backup 也 unhealthy / 未设 → **回落 `reject` 语义**(fail-closed 兜底)。
- **防御**:backup 链**限一跳**(不递归)、拒绝成环(backup 指向自身/循环)。
- **Admin**:proxies CRUD(`gatewayhttp` admin proxies handler + sqlc `db/admin`)加这两字段;UI 在选 `direct` 时**显式警告"破坏每账号 IP 隔离 / 抗检测"**;改动写审计(谁、何时开的)。

### 2.4 默认值 & 风险
- **默认 `reject`** = 当前 fail-closed,IP 隔离/抗检测契约**不变**。
- **Blast radius**:schema 迁移(高危类目)但加性可空有默认 → 现有部署零影响。
- **翻 `direct` 的真风险**:账号 egress 退到**网关真实 IP** → 破坏 per-account IP 绑定 + 抗检测。∴ 必须默认 `reject` + UI 警告 + 审计 + (决策点 §5.2)是否给"全局禁 direct"总开关。

### 2.5 测试(判别性,CLAUDE.md #14)
- bound unhealthy + `reject` → **必返 `ErrProxyUnhealthy`**(变异:若漏判 mode 默认走 direct,此测试必红)。
- `direct` → 返回无代理 transport。
- `backup` + backup healthy → 用 backup;backup unhealthy → 回 reject 语义。
- backup 成环 → 拒绝、不无限递归。

### 2.6 目标包(#13,确认 budget gate 绿)
`backend/sql/migrations/0148_*`、`backend/internal/provider`(resolver)、`backend/internal/db/admin`(sqlc proxies)、`backend/internal/gatewayhttp`(admin proxies handler)。

---

## 3. 切片 B —— 暴露 `channelhealth.Policy`

### 3.1 现状(写死点)
`backend/cmd/gateway/wiring.go:858`:`channelhealth.NewService(store, channelhealth.DefaultPolicy(), ...)` 传**字面默认**;`Policy` 结构体(24 字段,`types.go:113-166`)机制已在,但**无任何 config/DB override 路径**。

### 3.2 sub2 对照
sub2 把冷却/限流阈值下放 per-account DB 字段 + `config.go`(`account.go:151-183`、`config.go:1260-1261`)。→ **HUAKAI delta**:用**集中的 `Policy` 结构 + `platform_settings` KV** 做全局 override(生态:集中策略对象比 sub2 散落字段更可审计)。

### 3.3 设计(Phase 1,无 schema 改动)
- 加 `loadChannelHealthPolicy()`:从 **`platform_settings`**(已有 KV,scope=platform,key 如 `channel_health_policy`)或 env 读全局 override,**与 `DefaultPolicy()` 合并**(未设字段用默认)。wiring 改用它,不再传字面 `DefaultPolicy()`。
- **首批文档化旋钮**(运维最想调):`ErrorRateThresholdPct`、`LatencyP99ThresholdMS`、`BanSignalMin/MaxCooldown`、`DefaultRateLimitCooldown`。(全字段都可被覆盖,但先讲这几个)
- **`Policy.Validate()`**:范围校验(pct∈[0,100]、cooldown>0、max≥min),**越界拒绝 → fallback 默认 + 告警**,绝不 panic。
- **安全字段 Phase 1 锁默认、不开放**:`ManualOverrideRequiresReason`、`AutomaticPostBanRamp`(翻了有审计/恢复风险,§5.3 决策点)。

### 3.4 默认值 & 风险
- **默认 = 现 `DefaultPolicy()` 全值**(零行为变化,除非运维显式 override)。
- **Blast radius**:中危,改 wiring 注入 + 加 loader,**不动 schema**(复用已有 platform_settings KV)。
- **误配风险**:阈值太松→坏账号不冷却;太严→好账号误封。→ `Validate()` 范围校验兜底。

### 3.5 测试(判别性)
- platform_settings 设 `ErrorRateThresholdPct=80` → Service 用 80(变异:loader 漏读仍用 50 → 必红)。
- 越界 `pct=150` → 拒绝 + 用默认 50 + 不 panic。
- 未设 → **完全等于 `DefaultPolicy()`**。

### 3.6 目标包
`backend/cmd/gateway`(wiring + loader)、`backend/internal/channelhealth`(加 `Policy.Validate()`),复用 `platform_settings` store。**Phase 2(后续切片)**:per-pool / per-provider override(DB 表)——本切片不做。

---

## 4. 执行顺序、工期、成功标准

| 序 | 切片 | 危险度 | 工期 | 成功标准 |
|---|---|---|---|---|
| 1 | **B**(暴露 Policy) | 中(无 schema) | ~0.5d | platform_settings override 生效 + 越界拒绝 + 未设=默认;判别性测试绿;wiring 不再字面 DefaultPolicy() |
| 2 | **A**(fallback_mode) | 高(schema) | ~1d | 默认 reject 行为不变;direct/backup 按设计;成环拒绝;迁移 up/down 可逆;admin 可配 + UI 警告 |

- 实现在 **feature 分支 off `fix/h-fixes`**,不进主线;每切片:实现 + 判别性测试 + `integration_pg` 绿 + budget gate 绿 → 提交推送。
- 先 B 后 A(B 无 schema、可先独立见效;A 待 Owner 确认迁移)。

---

## 5. 决策点(Owner 拍)

1. **Schema 迁移确认(切片 A,`0148`)**:proxies 加 `fallback_mode`+`backup_proxy_id`(加性/可空/默认 reject)。**批不批?**
2. **direct 回退的合规底线**:允许运维把账号 egress 退到**网关真实 IP** 吗?默认 reject 保护隔离;在你 ① 姿态下 direct = 可用性 vs 隐身的权衡,**交给运维**——但要不要再加一个"**全局禁 direct**"的平台级总开关(防租户运维误开)?
3. **安全字段是否暴露**:`ManualOverrideRequiresReason` / `AutomaticPostBanRamp` —— Phase 1 建议**锁默认不开放**(翻了有审计/恢复风险)。确认锁?
4. **顺序**:先 B 后 A(推荐),还是只先做 B、A 等迁移单独批?

---

## 6. Clean-room & 引用
- sub2 行为为 paraphrase + `repo@sha:path:line`;HUAKAI 为 repo 相对 `file:line`。
- 关键引用:`postgres_proxy_resolver.go:178,187-191`(fail-closed)、`channelhealth/types.go:113-166`(Policy)、`cmd/gateway/wiring.go:858`(写死注入)、`sub2api@e34ad2b:backend/ent/schema/proxy.go:58-63`(fallback_mode)、`sub2api@e34ad2b:backend/ent/schema/account.go:151-183`+`config.go:1260-1261`(冷却下放)。
- 相关记忆:[[owner-prefers-operator-switches]]、[[rust-mimicry-posture-decision]]。审计源:workflow `hardcoded-vs-sub2-audit`(18 项开关表,2 高 + 3~4 中)。
