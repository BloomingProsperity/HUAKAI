# Plan — 自助 per-key quota 视图补 priority 字段 (inert-gap read-surfacing)

- 日期: 2026-06-18
- 作者: Claude PM (autonomous; Owner「你定但不能偏移」)
- 基线: origin/feat/frontend-portal @ c6c9c238
- 分支: feat/quota-view-priority

## 背景 + 真现状核实 (禁止凭记忆)

per-key quota policy 的 `priority` 是**真消费的解析 tiebreaker**: 多条 quota policy 重叠命中一个请求时,
priority 最小者胜(quota/policy.go:92-93)。该值已被 SELECT+scan 进 quotaPolicyRow(userkeycontrols/store.go:67,
映射于 :344/:363),但用户自助读视图 **KeyQuotaView**(types.go:39-53)与 PUT 结果 **SetKeyQuotaResult**(types.go:23-34)
都**漏投影** priority(quotaViewFromRow / quotaResultToSet 不映射它,key_control_service.go)。→ 用户看不到自己 key 的
quota policy 优先级。补上(只读 surfacing,同 /quota 多维那刀的 UNSURFACED_READ 模式)。

非 money/auth/schema(列已存在已 scan,无迁移)/avoidance;userkeycontrols 与 proxies 分支 0 碰撞(已核);只读、不改裁决。

## #16 三镜像 (clean-room specifier lane, 已完成) — NO_EQUIVALENT
「用户读 per-key quota 时是否暴露 policy 优先级/precedence tiebreaker」:
- **sub2api@e34ad2b**: 有重叠 per-user USD 窗口策略,但解析是顺序多门"首个拒绝者胜",**无存储数值 priority tiebreaker**,
  用户配额读(handler/user_handler.go:46-70)只返 usage/limit/resets,无 priority 字段。
- **new-api@1ac0f58**: per-token 是单一额度无重叠;唯一 precedence 是钱包/订阅二选一的计费偏好字段
  (service/billing_session.go:401-433),且仅在独立订阅端点暴露,非 per-token 读;非可配 per-rule priority。
- **CLIProxyAPI@2a050dc**: 无 per-user 配额,**no-equivalent**。
- **取舍 + HUAKAI delta(生态/observability)**: 三家都不在 per-key 配额读里暴露 per-policy 数值 priority tiebreaker。
  HUAKAI 在自助读里直接暴露它,让用户在配额读处即可理解重叠解析规则 → **超出**三家(生态升级)。首引 recency#12:
  三 SHA archived/disabled=false, pushed_at 2026-06-18(同 [[parity-audit-2026-06-18]])。

## 范围 (success criteria)
- types.go: KeyQuotaView + SetKeyQuotaResult 各加 `Priority int32 json:"priority"`(Mode 后)。
- key_control_service.go: quotaViewFromRow + quotaResultToSet 各加 `Priority: row.Priority`。
- openapi.yaml: UserAPIKeyQuotaResponse required + properties 加 priority。
- 测试(变异验证): TestQuotaViews_SurfacePriority 直接断两 mapper 投影 row.Priority(fixture 7≠0 防假绿;删映射→0→红,已证)。

## blast radius
- 2 struct 字段 + 2 mapping + openapi + 1 测试。store/裁决/policy 解析**不改**(priority 已 scan+消费)。无迁移、无 money、只读。
  无前端消费点(用户配额视图无专属前端,admin quota-policies 是另一面)。

## 门禁
ultracode 对抗审查零 S0/S1 → 干净基线 fail 0(含 cmd/gateway OpenAPI + userkeycontrols 集成真 DB)→ squash → ff。

## Clean-room 出处 (#11(d))
- Source files read: sub2api@e34ad2b {ent/schema/user_platform_quota.go, ent/schema/user_subscription.go, internal/handler/user_handler.go, internal/handler/quotaview/helpers.go, internal/service/billing_cache_service.go}; new-api@1ac0f58 {model/token.go, model/subscription.go, service/billing_session.go, controller/token.go, controller/subscription.go}; CLIProxyAPI@2a050dc {internal/access/config_access/provider.go, sdk/cliproxy/auth/types.go, sdk/cliproxy/auth/selector.go, internal/api/handlers/management/quota.go}
- Lane: specifier (单 agent 读三镜像). Agent: Claude PM. UTC: 2026-06-18
