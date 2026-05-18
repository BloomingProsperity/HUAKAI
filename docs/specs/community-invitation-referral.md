# F-COMM-001: Community Invitation And Referral System

| 字段 | 值 |
|---|---|
| Status | Draft |
| Spec ID | F-COMM-001 |
| Lane | Codex specifier lane round 2 |
| Specifier | Codex GPT-5 |
| Specifier date | 2026-05-17 |
| Reviewer | Pending |
| Review date | Pending |
| Released date | Pending |
| Lane mode | Option C strict clean-room carve-out; reference source read, behavior paraphrase only |
| Base | Round 1 Codex plan; F-AUTH-007 user registration; F-BILL-002 voucher; F-AUDIT-001 receipt; F-TRUST-001 audit ledger; F-PRIV-001 redaction; F-FP-001 device fingerprint; F-OBS-005 DLQ |
| Phase | Phase 6 commercial foundation |
| Memory ref | `feedback_huakai_better_than_sub2api` |
| Scope | Tenant-scoped invitation codes, referral qualification, reward issuance, tier progress, anti-abuse gates, audit receipt linkage, and operator-facing evidence for commercial growth loops. |
| Out of scope | Runtime implementation, database migration, frontend/admin UI, Rust gateway, control plane, payment-provider settlement, multi-level commission trees, and any copy of reference schemas/source/UI. |
| UTC | 2026-05-17T00:00:00Z |
| Source truth counters | Observed regions: 24; Inferences: 5; Open questions: 4 |

## 1. 元数据与 Clean-Room Boundaries

F-COMM-001 is the HUAKAI commercial invitation/referral spec. It is a first-class Phase 6 feature, not a plugin, because referral rewards touch registration, billing credit, voucher issuance, trust ledger receipts, privacy redaction, and anti-abuse decisions.

This spec was written in a specifier lane after reading local HUAKAI docs and selected reference source regions. Reference projects are evidence only. HUAKAI schema, API contracts, acceptance tests, anti-abuse defaults, and audit linkage below are HUAKAI-owned design. No reference source code, schema, comments, UI source, function names, struct names, or line-by-line algorithm ordering is copied into this file.

## 2. 问题陈述

HUAKAI needs a commercial growth loop where an existing User can invite a new User, the new User can register with that invitation, the first real commercial activity qualifies the referral, and rewards/tier progress are issued with tamper-evident audit evidence.

The user problem is:

- 老用户需要一个可分享、可控量、可撤销的 invitation code。
- 新用户需要在注册前知道邀请是否可用，但不能看到 inviter 的敏感信息。
- 推荐奖励不能在注册瞬间发放，否则同 IP、同设备、假邮箱、脚本注册会刷出额度。
- 推荐奖励与 voucher / credit / refund / receipt / ledger 是同一商业闭环，必须可追踪、可重试、可审计。
- Tier 推广需要把有效推荐数转成可解释的 silver/gold/platinum 进度，而不是只在后台存一段无法证明的运营备注。

## 3. 设计原则

### 3.1 Invitation Code

- Code format: HUAKAI uses an 8-character random base32 string.
- Uniqueness: code creation checks uniqueness within `(tenant_id, normalized_code)` before the code is shown to the inviter.
- Raw-code handling: raw code is visible to the inviter/user only where product UX requires it. Routine logs, audit payloads, traces, DLQ payloads, and support exports use a fingerprint or redacted suffix.
- Tenant quota: default `100` active codes per tenant per calendar month. Owner/Admin may raise the quota, but the override itself is audited.
- Expiry: default `30 days`; caller may request a shorter expiry. Longer expiry needs tenant policy.
- Max usage: default `1`; multi-use campaign codes are allowed only when `max_usage` is explicitly set.
- Idempotency: repeated create requests with the same client idempotency key return the prior invitation instead of creating code spam.

### 3.2 Referral Reward

- Qualification trigger: a referral becomes `qualified` only after the referee has a first successful billing event. Registration alone is not enough.
- Reward trigger: reward issuance starts from the qualifying billing event, then writes a reward record and either credit or voucher effect through F-BILL-002-compatible commercial primitives.
- Idempotency: the qualifying billing event id is the idempotency anchor. Retrying the billing event, reward worker, receipt writer, or DLQ replay cannot issue a second reward.
- Refund coupling: if the first billing event is later fully reversed before reward finalization, the referral remains pending or becomes disqualified by append-only correction policy. The original referral row is not silently deleted.
- Amount representation: reward money is represented as integer micros (`amount_usd_micros`) at spec level. Implementation must not use float for ledger effects.

### 3.3 Tier Reward

Tier progress is based on cumulative qualified referrals:

| Tier | Qualified referrals | Product meaning |
|---|---:|---|
| `none` | 0-2 | Normal user, no tier benefit. |
| `silver` | 3-9 | Entry-level promoter benefit. |
| `gold` | 10-49 | Higher benefit or visibility; exact benefits are commercial policy. |
| `platinum` | 50+ | Highest default referral tier; custom enterprise tiers are future work. |

Tier unlock is monotonic by default. If a later fraud review invalidates a referral, HUAKAI records an append-only correction and may freeze future benefits, but does not rewrite historical tier unlock evidence.

### 3.4 Anti-Abuse

The minimum anti-abuse gates are:

- Same IP cooling window: default `72 hours`. Multiple new accounts using the same inviter and same IP class during the window do not qualify automatically.
- Same device cross-check: once F-FP-001 R-4 closes, referral qualification checks the referee device profile against the inviter and recent referees. Same-device referrals are held for review or marked non-qualifying.
- Email domain blocklist: disposable, temporary, catch-all, or tenant-blocked domains cannot qualify for referral rewards. Registration policy remains owned by F-AUTH-007.
- Self-referral guard: a User cannot qualify a referral to itself across local account, verified email, social identity, device profile, or tenant-scoped identity aliases.
- Burst throttling: unusual invitation creation, preview, registration, and qualification bursts move events to review/DLQ rather than paying out immediately.
- Operator override: manual qualification or release is allowed only with reason class, actor id, and F-TRUST signed audit evidence.

### 3.5 Audit And Receipt

Every material step writes tamper-evident evidence:

- Invitation created, previewed, exhausted, expired, revoked, or rate-limited.
- Referral bound, rejected, pending, qualified, rewarded, disqualified, or manually overridden.
- Reward issued as credit or voucher.
- Tier unlocked.
- Abuse gate fired.
- DLQ retry, replay, or permanent failure.

Successful reward issuance writes `audit_ledger_entries`, receives an F-TRUST signature, and links a F-AUDIT-001 receipt. Payloads pass F-PRIV-001 redaction and must not contain raw code, prompt/completion text, credential material, raw email, raw IP, or raw device fingerprint.

## 4. 数据模型

This section defines HUAKAI-owned logical schema intent. It is not copied from reference projects and does not create a migration in this specifier wave.

```sql
-- HUAKAI 自研逻辑表: 邀请码主表。实现时 raw code 应使用 hash/fingerprint 查询，code 字段只表示业务标识。
CREATE TABLE invitations (
  id BIGSERIAL PRIMARY KEY,
  tenant_id BIGINT NOT NULL,
  code TEXT NOT NULL,
  inviter_user_id BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  usage_count INTEGER NOT NULL DEFAULT 0,
  max_usage INTEGER NOT NULL DEFAULT 1
);

-- HUAKAI 自研逻辑表: 推荐绑定与资格状态。状态只允许 pending / qualified / rewarded。
CREATE TABLE referrals (
  id BIGSERIAL PRIMARY KEY,
  referee_user_id BIGINT NOT NULL,
  referrer_user_id BIGINT NOT NULL,
  invitation_id BIGINT NOT NULL,
  status TEXT NOT NULL,
  qualified_at TIMESTAMPTZ,
  first_billing_event_id TEXT
);

-- HUAKAI 自研逻辑表: 推荐奖励记录。credit / voucher 二选一，receipt_id 指向 F-AUDIT-001。
CREATE TABLE referral_rewards (
  id BIGSERIAL PRIMARY KEY,
  referrer_user_id BIGINT NOT NULL,
  referee_user_id BIGINT NOT NULL,
  reward_type TEXT NOT NULL,
  amount_usd_micros BIGINT NOT NULL,
  issued_at TIMESTAMPTZ NOT NULL,
  receipt_id TEXT NOT NULL
);

-- HUAKAI 自研逻辑表: 用户 tier 进度。tier_unlocked_at 记录当前 tier 首次达成时间。
CREATE TABLE tier_progress (
  user_id BIGINT PRIMARY KEY,
  total_qualified_referrals INTEGER NOT NULL DEFAULT 0,
  current_tier TEXT NOT NULL DEFAULT 'none',
  tier_unlocked_at TIMESTAMPTZ
);
```

Implementation requirements:

- `invitations.code` must be unique per tenant after normalization.
- `referrals.referee_user_id` may have only one active referral binding per tenant.
- `referrals.first_billing_event_id` must be unique where present, so a billing event qualifies only one referral.
- `referral_rewards.receipt_id` is required for all successful rewards.
- `tier_progress` is derived from qualified referrals but may be snapshotted for fast reads; corrections are append-only audit events.
- A production migration must add tenant-aware indexes, check constraints, redaction-safe metadata, idempotency keys, and foreign keys according to the final local domain model.

## 5. API Endpoint

### 5.1 `POST /v1/invitations`

Actor: authenticated User.

Body:

```json
{
  "max_usage": 1,
  "expires_in": "30d",
  "client_idempotency_key": "optional-client-key"
}
```

Behavior:

1. Resolve tenant and inviter User from F-SESSION-001.
2. Enforce tenant monthly quota and per-User burst policy.
3. Generate an 8-character base32 code and uniqueness-check it.
4. Store invitation, emit audit event, and return the raw code once.

Failure behavior:

- Quota exceeded: returns policy-safe error and writes rate-limit audit.
- Duplicate generation collision: retry generation internally; persistent collision returns retryable error.
- User not eligible: returns forbidden without revealing tenant policy internals.

### 5.2 `GET /v1/invitations/{code}/preview`

Actor: visitor or authenticated User before registration.

Behavior:

1. Normalize code, lookup tenant-scoped invitation, and verify active/expiry/capacity.
2. Return redacted inviter display, expected invitee benefit class, and expiry/capacity hints.
3. Do not reveal full inviter email, raw internal ids, reward amount if tenant policy hides it, or whether a blocked domain/device would later fail qualification.

### 5.3 `POST /v1/auth/register?invitation_code=`

Actor: visitor.

Behavior:

1. F-AUTH-007 owns registration, email verification, password/social identity validation, and local User creation.
2. F-COMM-001 validates invitation state inside the registration transaction or a transactionally equivalent local boundary.
3. Create a `referrals` record as `pending`.
4. Increment invitation usage only when registration succeeds.
5. Emit `referral_bound` and auth-owned registration audit events.

This endpoint must not issue reward at registration time.

### 5.4 `GET /v1/referrals/my`

Actor: authenticated inviter User.

Response includes:

- invitation codes owned by the User, with raw code redacted according to UI policy;
- referral list with referee display-safe identity;
- status per referral: `pending`, `qualified`, `rewarded`, rejected/review reason class if visible;
- reward records and receipt ids;
- next retry/DLQ state for delayed reward issuance.

The endpoint must not expose referee email, IP, device fingerprint, billing details, or cross-tenant identifiers.

### 5.5 `GET /v1/tiers/my`

Actor: authenticated inviter User.

Response includes:

- current tier;
- total qualified referrals;
- next tier threshold;
- tier unlock timestamp;
- frozen/reviewed referral count if anti-abuse review is pending;
- public policy version for tier benefits.

## 6. 跨 Spec 依赖

| Dependency | F-COMM-001 use |
|---|---|
| F-AUTH-007 | User registration, email/social identity validation, invite-code binding at account creation, self-referral identity checks. |
| F-BILL-002 | Voucher reward issuance, voucher receipt linkage, reward-as-credit/voucher policy, refund/correction compatibility. |
| F-AUDIT-001 | User-visible referral reward receipt, cost/credit transparency, dispute reference, receipt id in `referral_rewards`. |
| F-TRUST-001 | `audit_ledger_entries`, signature, append-only evidence, detached verification for reward/tier events. |
| F-PRIV-001 | Redaction of raw code, raw email, raw IP, full user agent, prompt/completion, and device fingerprint details. |
| F-FP-001 | Device fingerprint cross-check after R-4 closure; same-device qualification hold/reject. |
| F-OBS-005 | Async reward worker DLQ, priority lane for billing/reward/audit integrity, idempotent replay. |

High-risk implementation boundaries needing Owner confirmation later: database schema migration, auth registration transaction edits, billing ledger mutation, quota enforcement, real reward amounts, and production anti-abuse defaults.

## 7. Acceptance Tests

| ID | Scenario | Expected coverage |
|---|---|---|
| AT-COMM-001-001 | Authenticated User creates an invitation with defaults. | 8-character base32 code, tenant uniqueness, `max_usage=1`, default expiry, audit event, no raw code in routine logs. |
| AT-COMM-001-002 | Tenant exceeds monthly invitation quota. | Create rejects, no invitation row, reason class audited, quota metric increments. |
| AT-COMM-001-003 | Code generation collision occurs. | Internal retry preserves uniqueness; no duplicate active code can exist within tenant. |
| AT-COMM-001-004 | Visitor previews a valid invitation. | Preview returns safe inviter display and expected benefit class; no sensitive inviter/referee data leaks. |
| AT-COMM-001-005 | Visitor previews expired/exhausted/unknown code. | Safe failure response; no distinction that helps code enumeration beyond allowed UX. |
| AT-COMM-001-006 | New User registers with a valid invitation. | F-AUTH-007 creates User; referral is `pending`; invitation usage increments once; no reward is issued yet. |
| AT-COMM-001-007 | Registration transaction fails after invitation validation. | No referral binding, no usage increment, no partial audit success. |
| AT-COMM-001-008 | Same User or equivalent identity attempts self-referral. | Binding rejects or remains non-qualifying; self-referral reason is audited without raw identity data. |
| AT-COMM-001-009 | Referee tries to bind a second inviter. | Existing binding wins; duplicate attempt is idempotent or rejected without changing referrer. |
| AT-COMM-001-010 | Referee completes first successful billing event. | Referral moves to `qualified`, reward worker starts, first billing event id anchors idempotency. |
| AT-COMM-001-011 | Fake account registers but never bills. | Referral remains `pending`; no reward, no tier progress. |
| AT-COMM-001-012 | Same IP class creates multiple referee accounts inside 72 hours. | Later referrals are held/rejected per policy; no automatic reward; alert metric increments. |
| AT-COMM-001-013 | Same device fingerprint appears for inviter and referee after F-FP-001 is enabled. | Qualification is held or rejected; audit references redacted fingerprint profile only. |
| AT-COMM-001-014 | Disposable or blocked email domain is used. | Registration policy may allow/deny, but referral cannot qualify for reward. |
| AT-COMM-001-015 | Reward worker retries the same qualifying billing event. | Exactly one reward record and one receipt are produced. |
| AT-COMM-001-016 | Reward is issued as voucher. | F-BILL-002 voucher effect, F-AUDIT receipt, and F-TRUST ledger entry all agree. |
| AT-COMM-001-017 | User reaches 3/10/50 qualified referrals. | Tier unlocks to silver/gold/platinum once, with monotonic progress and audit evidence. |
| AT-COMM-001-018 | Audit writer or reward worker fails after qualification. | Event enters F-OBS-005 DLQ, replay is idempotent, user-facing status shows delayed reward instead of false success. |

## 8. 测试策略

Unit tests:

- Code normalization, random base32 length, uniqueness retry, tenant quota, expiry, max usage.
- Referral state transitions: `pending -> qualified -> rewarded`.
- Idempotency by invitation create key, registration retry, first billing event id, reward worker replay, and DLQ replay.
- Tier threshold transitions at 2/3, 9/10, and 49/50.
- Redaction helpers for raw code, email, IP, user agent, and device fingerprint.

Integration tests:

- F-AUTH-007 registration with invitation binding.
- F-BILL-002 voucher reward issuance and receipt linkage.
- F-AUDIT-001 receipt visibility for referral reward.
- F-TRUST-001 signed ledger entry for reward and tier unlock.
- Anti-abuse fake-account matrix: same IP, same device, blocked domain, duplicate identity, no-billing account, refunded first billing event.
- F-OBS-005 DLQ replay for reward worker and audit writer failure.

Operator recovery tests:

- Manually release a held referral with audited reason.
- Revoke invitation before use and after partial use.
- Recompute tier progress from qualified referrals and compare to snapshot.
- Export support view and verify it contains only redacted metadata.

## 9. 监控与告警

Metrics:

- `invitation_create_rate_by_tenant`
- `invitation_quota_reject_total`
- `invitation_preview_fail_ratio`
- `referral_pending_age_seconds`
- `referral_qualification_rate`
- `referral_reward_failure_rate`
- `referral_reward_dlq_depth`
- `referral_reward_idempotency_replay_total`
- `tier_unlock_latency_seconds`
- `anti_abuse_same_ip_hold_total`
- `anti_abuse_same_device_hold_total`
- `anti_abuse_email_domain_block_total`

Alerts:

- Invitation creation rate exceeds tenant baseline by 5x within 10 minutes.
- Reward failure rate exceeds 1% for 15 minutes.
- Reward DLQ has any billing-linked message older than 15 minutes.
- Tier unlock latency p95 exceeds 10 minutes after qualification.
- Same IP/device holds spike above tenant baseline.
- Receipt/ledger mismatch for any successful reward.

Dashboards:

- Invitation funnel: created -> previewed -> registered -> first billing -> rewarded.
- Abuse funnel: held/rejected reason classes by tenant and campaign.
- Reward integrity: qualified referrals, rewards issued, receipts produced, DLQ replays, idempotency skips.
- Tier progress: unlock counts and latency by tier.

## 10. vs Sub2API 差异化

| feature | sub2api how | HUAKAI delta | dimension |
|---|---|---|---|
| 邀请码生成 | Observed source supports generated redeem-style codes and an invitation-like zero-value category for registration gating (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/redeem_service.go:119-188`). | HUAKAI separates commercial invitation from general redeem code, uses 8-character base32, monthly tenant quota, one-time raw display, and redacted audit by default. | 架构 + 生态 |
| 注册绑定 | Observed registration accepts both a registration gate code and a share-attribution code, then passes them into registration flow (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/auth_handler.go:43-52`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/auth_handler.go:153-176`). | HUAKAI binds invitation through F-AUTH-007 but keeps reward pending until first successful billing event, so registration alone has no payout. | 架构 + 算法 |
| Reward trigger | Observed reward accrual is tied to positive paid balance order and positive balance-style redeem success (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/payment_fulfillment.go:368-441`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/redeem_service.go:309-388`). | HUAKAI uses first successful billing event as the qualification anchor, links reward idempotency to that event, and couples refund/correction policy to F-AUDIT/F-BILL. | 算法 + 生态 |
| Anti-abuse | Observed safeguards include failed-code rate limiting, code-level lock, enable switches, format checks, self-referral rejection, already-bound guard, freeze windows, and per-invitee caps (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/redeem_service.go:217-280`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/affiliate_service.go:269-386`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/setting_handler.go:603-639`). | HUAKAI adds first-billing qualification, 72h IP cooling, F-FP-001 device cross-check, blocked email domains, DLQ hold state, and manual release with signed audit. | 算法 + 生态 |
| Audit trail | Observed reward application records applied/skipped/failed audit states around the paid-order reward transaction (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/payment_fulfillment.go:376-441`). | HUAKAI makes every reward and tier unlock user-verifiable through F-TRUST signatures and F-AUDIT receipts; raw invitation and abuse signals remain redacted by F-PRIV. | 架构 + 生态 |
| Tier promotion | Observed referral detail exposes inviter summary, invitee list, available/frozen/history quota, and effective rebate policy (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/affiliate_service.go:241-267`). | HUAKAI adds explicit silver/gold/platinum thresholds based on qualified referrals, with monotonic tier snapshot and signed unlock evidence. | 架构 + 生态 |

## 11. Out Of Scope

- Multi-level referral trees and downstream commission chains.
- Affiliate marketplace, paid influencer campaigns, or third-party ad attribution.
- Tier benefit pricing policy beyond the default silver/gold/platinum thresholds.
- Admin UI, frontend implementation, or user-facing copy.
- Database migration, backend Go implementation, Rust gateway implementation, or control-plane rollout.
- Payment provider settlement, chargeback, tax, invoice, or KYC flows.
- Retroactive migration of legacy invite/redeem data.
- Capturing raw prompt/completion, raw email, raw IP, raw user agent, or raw fingerprint for referral investigation.

## 12. Roadmap

| Sub-phase | Estimate | Scope |
|---|---:|---|
| COMM-1-A | 1 day | Final implementation plan, Owner confirms schema/auth/billing/audit blast radius, select default reward amounts. |
| COMM-1-B | 2-3 days | Invitation create/preview contracts, registration binding through F-AUTH-007, idempotency and quota checks. |
| COMM-1-C | 3-4 days | Qualification/reward worker, first billing event hook, F-BILL-002 credit/voucher effect, F-AUDIT receipt, F-TRUST ledger entry. |
| COMM-1-D | 2-3 days | Anti-abuse gates, F-FP-001 integration after R-4, email-domain policy, tier progress and unlock audit. |
| COMM-1-E | 2 days | Acceptance tests, DLQ replay tests, support/export redaction tests, dashboards, release gate review. |

Mandatory Owner decisions before implementation:

1. Reward type default: credit, voucher, or tenant-configurable.
2. Default reward amount and tier benefit policy.
3. Whether registration can proceed when invitation preview passed but reward anti-abuse later blocks qualification.
4. Whether Personal Edition disables F-COMM-001 by default under DR-002.

## 13. Reference 引用

### 13.1 Recency And Lane

Local reference repos were already present under `/home/codex/refs`. Network access is restricted in this environment, so GitHub API archived/disabled/pushed_at checks were not performed. Local HEAD timestamps were inspected by git metadata in this session and were within May 2026. Claims below are limited to source regions actually read.

### 13.2 Sub2API Observed Behaviors

- Registration source regions show that the registration request can carry both a gate-style invite and a share-attribution input, and the registration handler passes both into the account creation path (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/auth_handler.go:43-52`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/auth_handler.go:153-176`).
- Pre-registration validation checks feature enablement, code lookup, expected category, and unused state before returning a safe validity response (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/auth_handler.go:503-561`).
- Code generation supports a dedicated invitation-like category without value and caps large batch creation (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/redeem_service.go:119-188`).
- Code redemption uses failed-attempt throttling and a per-code concurrency guard before the transactional effect (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/redeem_service.go:217-280`).
- Positive balance-style redemption can trigger referral reward accrual after the redeem transaction succeeds (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/redeem_service.go:309-388`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/redeem_service.go:439-456`).
- Attribution binding normalizes the submitted code, ignores empty input, rejects malformed/self references, and avoids rebinding an already-attributed User (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/affiliate_service.go:269-312`).
- Reward calculation is gated by feature enablement, positive base amount, inviter presence, configurable duration, effective rate, cap, freeze window, and idempotent application outcome (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/affiliate_service.go:318-386`).
- Paid balance orders attempt referral reward accrual inside a transaction with applied/skipped/failed audit states (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/payment_fulfillment.go:368-441`).
- User-facing referral detail includes referral quota states and invitee summary; this is evidence for a user-visible referral center, not for HUAKAI's exact schema (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/affiliate_service.go:241-267`).
- Admin setting code bounds global reward rate, freeze duration, active duration, and per-invitee cap (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/setting_handler.go:603-639`).

### 13.3 New API Observed Behaviors

- Source regions expose configurable new-user, inviter, and invitee quota amounts (`new-api@d146e45e2f9515ff78078a3954c40023cc79baee:common/constants.go:123-125`).
- Account creation assigns a new-user quota, creates a share code, and when an inviter exists, grants invitee benefit and records inviter-side credit/progress (`new-api@d146e45e2f9515ff78078a3954c40023cc79baee:model/user.go:381-435`).
- Email/password signup resolves the submitted share code to an inviter before creating the account (`new-api@d146e45e2f9515ff78078a3954c40023cc79baee:controller/user.go:166-188`).
- OAuth signup reads stored referral state, creates the local user and identity binding transactionally, then runs post-creation reward handling (`new-api@d146e45e2f9515ff78078a3954c40023cc79baee:controller/oauth.go:265-328`).
- Invitation-derived credit can be transferred into usable quota through a transactional locked update (`new-api@d146e45e2f9515ff78078a3954c40023cc79baee:model/user.go:344-379`).
- Billing settlement compares pre-consumed and actual quota and applies positive or negative deltas; this supports HUAKAI's need to couple reward qualification to final billing evidence, not raw registration (`new-api@d146e45e2f9515ff78078a3954c40023cc79baee:service/billing.go:32-75`, `new-api@d146e45e2f9515ff78078a3954c40023cc79baee:service/funding_source.go:13-64`, `new-api@d146e45e2f9515ff78078a3954c40023cc79baee:service/quota.go:408-449`).

### 13.4 All API Hub Observed Behaviors

- Source regions show cumulative-value grouping into bronze/silver/gold sponsor classes and exclude non-positive amounts from those groups; this is evidence for tier presentation by cumulative contribution, not direct referral qualification (`all-api-hub@893e832d0f9211763f549a17abb7364ea9b39ce0:docs_assistant/afdian_api.py:103-155`).
- Sponsor display maps medal text to tier styling; this is UI/docs-assistant evidence only and not a backend referral mechanism (`all-api-hub@893e832d0f9211763f549a17abb7364ea9b39ce0:docs_assistant/contributors.py:263-286`).
- Docs source describes user group switching for price preview; this supports the idea that tier/group state affects commercial display, not that referrals unlock the group (`all-api-hub@893e832d0f9211763f549a17abb7364ea9b39ce0:docs/docs/model-list.md:37-41`).
- Redemption service source delegates a submitted code to the matched account and returns credited value plus account display data; this is adjacent evidence for code-to-credit user flows, not invitation attribution (`all-api-hub@893e832d0f9211763f549a17abb7364ea9b39ce0:src/services/redemption/redeemService.ts:16-80`).

### 13.5 HUAKAI-Fit Inferences

1. HUAKAI should not pay on registration because Sub2API and New API both show commercial reward/credit mechanisms can be tied to value-bearing events, while fake registrations are easy to generate. The HUAKAI delta is first successful billing event qualification.
2. HUAKAI should separate invitation from voucher because F-BILL-002 already owns voucher lifecycle and code redemption; invitations are attribution and qualification contracts.
3. HUAKAI should use F-AUDIT/F-TRUST for reward receipts because reward issuance changes user-visible value and must be independently verifiable.
4. HUAKAI should keep tier unlock monotonic because retroactive mutation would make user-facing promotion evidence hard to verify.
5. HUAKAI should mark All API Hub as tier/progress inspiration only, not as direct referral parity evidence.

### 13.6 Open Questions

1. Owner must set default reward amounts and whether reward type defaults to credit or voucher.
2. Owner must decide if same-IP/device holds require manual review or automatic non-qualification.
3. F-FP-001 R-4 completion date controls when device-based qualification gates can become blocking.
4. GitHub API first-cite recency checks could not run under current network restrictions; future reviewer should verify archived/disabled/pushed_at before release gating.

### 13.7 Source Coverage Proof

| Region read | Contribution |
|---|---|
| `docs/plans/2026-05-17-f-comm-001-invitation-referral-spec-codex.md` | Round 1 plan, scope, success criteria, clean-room constraints. |
| `CLAUDE.md` §11-§12 | Clean-room and source-must-read rules. |
| `docs/specs/_TEMPLATE.md` | Spec metadata and acceptance-test structure. |
| `docs/specs/voucher-system.md` | F-BILL-002 boundary and voucher linkage. |
| `docs/specs/user-consumption-transparency.md` | F-AUDIT-001 receipt and refund/audit linkage. |
| `docs/specs/user-authentication.md` | F-AUTH-007 registration and invite binding boundary. |
| `docs/specs/trust-chain-user-verifiable-ledger.md` | F-TRUST-001 audit ledger and signature boundary. |
| `docs/specs/privacy-no-user-data-logs.md` | F-PRIV-001 redaction boundary. |
| `docs/specs/device-fingerprint-binding.md` | F-FP-001 device profile dependency. |
| `docs/03_FEATURE_PARITY_MATRIX.md` | Existing F-COMM-001 row and dependent feature ids. |
| `/home/codex/refs/sub2api/backend/internal/handler/auth_handler.go:43-176,503-561` | Registration inputs and pre-registration code validation behavior. |
| `/home/codex/refs/sub2api/backend/internal/service/redeem_service.go:119-188,217-280,309-456` | Code generation, throttling, lock, transactional redemption, positive-value reward hook. |
| `/home/codex/refs/sub2api/backend/internal/service/affiliate_service.go:241-386` | Referral detail, attribution binding, reward gates, cap/freeze behavior. |
| `/home/codex/refs/sub2api/backend/internal/service/payment_fulfillment.go:368-441` | Paid-order reward transaction and audit states. |
| `/home/codex/refs/sub2api/backend/internal/handler/admin/setting_handler.go:603-639` | Configured reward policy bounds. |
| `/home/codex/refs/new-api/common/constants.go:123-125` | Invite/new-user quota settings. |
| `/home/codex/refs/new-api/model/user.go:344-435` | Account creation reward/progress and credit transfer behavior. |
| `/home/codex/refs/new-api/controller/user.go:166-188` | Signup attribution behavior. |
| `/home/codex/refs/new-api/controller/oauth.go:265-328` | OAuth signup attribution and transaction boundary. |
| `/home/codex/refs/new-api/service/billing.go:32-75`, `/home/codex/refs/new-api/service/funding_source.go:13-64`, `/home/codex/refs/new-api/service/quota.go:408-449` | Billing adjustment/refund behavior used as credit-flow anchor. |
| `/home/codex/refs/all-api-hub/docs_assistant/afdian_api.py:103-155` | Cumulative-value tier grouping evidence. |
| `/home/codex/refs/all-api-hub/docs_assistant/contributors.py:263-286` | Tier display evidence. |
| `/home/codex/refs/all-api-hub/docs/docs/model-list.md:37-41` | Group/tier price preview evidence. |
| `/home/codex/refs/all-api-hub/src/services/redemption/redeemService.ts:16-80` | Code-to-credit adjacent behavior evidence. |

中文摘要: F-COMM-001 邀请/推荐 spec 已落档。真实观察来自 Sub2API 注册/邀请码/返利、New API 注册赠额/额度流转、All API Hub tier/兑换相邻行为；合理推断 5 项；open question 4 项。功能无缩水：邀请码、推荐奖励、tier、反作弊、audit/receipt 均保留。clean-room 风险按 paraphrase + file:line 控制；安全风险主要在后续 schema/auth/billing/audit 实施，需 Owner 再确认。

Source files read:
- docs/plans/2026-05-17-f-comm-001-invitation-referral-spec-codex.md
- CLAUDE.md
- docs/specs/_TEMPLATE.md
- docs/specs/voucher-system.md
- docs/specs/user-consumption-transparency.md
- docs/specs/user-authentication.md
- docs/specs/trust-chain-user-verifiable-ledger.md
- docs/specs/privacy-no-user-data-logs.md
- docs/specs/device-fingerprint-binding.md
- docs/03_FEATURE_PARITY_MATRIX.md
- /home/codex/refs/sub2api/backend/internal/handler/auth_handler.go
- /home/codex/refs/sub2api/backend/internal/service/redeem_service.go
- /home/codex/refs/sub2api/backend/internal/service/affiliate_service.go
- /home/codex/refs/sub2api/backend/internal/service/payment_fulfillment.go
- /home/codex/refs/sub2api/backend/internal/handler/admin/setting_handler.go
- /home/codex/refs/new-api/common/constants.go
- /home/codex/refs/new-api/model/user.go
- /home/codex/refs/new-api/controller/user.go
- /home/codex/refs/new-api/controller/oauth.go
- /home/codex/refs/new-api/service/billing.go
- /home/codex/refs/new-api/service/funding_source.go
- /home/codex/refs/new-api/service/quota.go
- /home/codex/refs/all-api-hub/docs_assistant/afdian_api.py
- /home/codex/refs/all-api-hub/docs_assistant/contributors.py
- /home/codex/refs/all-api-hub/docs/docs/model-list.md
- /home/codex/refs/all-api-hub/src/services/redemption/redeemService.ts
Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-05-17T00:00:00Z
