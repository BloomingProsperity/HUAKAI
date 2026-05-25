# `one-api` — Channel Auto-Disable on Permanent-Error Pattern (Claude deep decomposition)

| Field | Value |
| --- | --- |
| Status | Deep decomposition (Claude lane, peer to Codex R3 specifier output) |
| Reference | one-api (MIT, [E-LIC-004](../../07_REFERENCE_EVIDENCE_LEDGER.md)) |
| Feature in HUAKAI matrix | F-CH-002 (L2) |
| Specifier session | Claude PM-Orchestrator (Opus), 2026-04-29 |
| Source-reading delegate | Sonnet Explore agent — read 10 source files; structured factual report retained |
| Companion artifacts | docs/decompositions/one-api/channel-auto-disable-source-verified.md (Codex R3 — independent specifier read), .omc/artifacts/decomp-critic/C1-oneapi-channel-auto-disable.md (Codex critic) |
| **Truth-discipline** | **Observed regions: 10** / **Inferences: 3** / **Open questions: 8** |
| Round-1 / Round-2 superseded | docs/decompositions/_superseded-round{1,2}/one-api-channel-auto-disable-* |

> **Lane discipline**: This file is independent of any Codex specifier or critic output. It draws **only** from the Sonnet Explore agent's source-reading report (which had no access to Codex outputs). Every behavior claim is tagged `[region-N]`; inferences explicitly marked.

---

## 1. WHY (motivation)

Three pressures shape one-api's channel-auto-disable design.

**Pressure 1 — operator unattended uptime**: The product targets self-hosted deployments where the operator may be a single individual juggling multiple upstream provider accounts. Auth failures, expired credentials, quota exhaustions, or upstream policy bans must not require a human to discover them via failed customer requests. The gateway needs to detect them and route around them automatically `[region-1]`.

**Pressure 2 — provider error taxonomy fragmentation**: Each upstream provider returns errors in a different shape. OpenAI uses typed `error.type`/`error.code`. Anthropic returns text. Gemini returns yet another shape. The gateway must consolidate these into a single permanent-vs-transient classification rule with a fallback to substring matching against literal error messages from observed providers `[region-1]`. The classification is empirical, not principled — driven by what the maintainer has seen in production.

**Pressure 3 — false-disable cost > false-pass cost (operator default)**: Disabling a channel mid-traffic punishes the operator (lost capacity) more than letting a few bad requests through. Hence both auto-disable gates are **disabled by default** in shipped configuration `[region-9]`. The product respects operator caution: opt-in to auto-disable is a deliberate choice, not the default. (See §6 R-1 — HUAKAI's multi-tenant context inverts this default cost.)

---

## 2. WHAT (algorithm in HUAKAI vocabulary)

The auto-disable feature is composed of **two orthogonal gates** plus a **scheduled-test path**, all writing to the same channel-status mutation surface.

### Sub-behaviors S-1..S-21 (observed-only)

**S-1: Permanent-error classifier (Gate A)** `[region-1]`. A pure function that takes an upstream error object + HTTP status code and returns a boolean indicating "should disable this channel". The function consolidates 5 independent detection paths:

- **S-1a HTTP 401**: any upstream returning Unauthorized triggers Gate A immediately.
- **S-1b error type match**: error.type ∈ {`insufficient_quota`, `authentication_error`, `permission_error`, `forbidden`}.
- **S-1c error code match**: error.code ∈ {`invalid_api_key`, `account_deactivated`}.
- **S-1d English message substring (case-insensitive)**: 11+ literal patterns including "your access was terminated", "violation of our policies", "credit balance is too low", "permission denied", "organization has been disabled", "organization has been restricted", "api key not valid", "api key expired".
- **S-1e Chinese message substring**: includes "已欠费" (account in arrears).

The detection paths are evaluated in order; a match by any path returns true.

**S-2: Gate A guard flag** `[region-9]`. The classifier is an empty-return short-circuit when the operator config flag `AutomaticDisableChannelEnabled` is false. **Default: false**. The classifier function exists but its boolean output is forced to false until the operator opts in.

**S-3: Success-rate metric window (Gate B)** `[region-2]`. Per-channel rolling boolean array. Each request's success/failure outcome is appended; when the array length exceeds the configured window size (default 10), the oldest entry is dropped (FIFO). The window is in-process memory only.

**S-4: Window threshold + traffic floor evaluation** `[region-2]`. Gate B fires when:
- The window is **full** (length ≥ window size — the traffic floor); AND
- success-count / window-size < threshold (default 0.8).

When Gate B fires, the per-channel array is reset to empty (no continuation of a "still-failing" signal across the disable boundary).

**S-5: Gate B guard flag** `[region-9]`. The metric collection consumer goroutines only start if `EnableMetric` is true. **Default: false**. When the flag is off, no metric data is recorded; Gate B is dormant.

**S-6: Gate composition — additive** `[inferred from region-1, region-2]`. Both gates write to the same disable surface. Either can fire independently. There is no priority or precedence — first to fire wins; both can fire on the same channel across different requests in close temporal proximity.

**S-7: Scheduled health-test runner** `[region-3]`. A periodic background goroutine probes channels with a synthetic chat-completions request. Trigger sources: (a) HTTP-endpoint manual invocation; (b) automatic timer with operator-configurable `RequestInterval`. The runner iterates channels sequentially within a single test run.

**S-8: Singleton lock on test runs** `[region-3]`. A package-level `sync.Mutex` plus a boolean flag prevents concurrent invocations of the all-channel test. Re-entry returns "测试已在运行中" error, rejecting overlapping admin clicks or scheduler ticks.

**S-9: Test probe shape** `[region-3]`. The probe uses OpenAI ChatCompletion format with model = "gpt-3.5-turbo" or the channel's first listed model. Payload: a single user message containing the operator-configured `TestPrompt`. The probe runs through the same adaptor used for live traffic — no shortcut path.

**S-10: Test response-time threshold disable** `[region-3]`. If an enabled channel's response time exceeds `ChannelDisableThreshold * 1000` ms (default 5 seconds), Gate A is invoked with a synthetic permanent error to disable the channel. This is a separate trigger from S-1 — slow ≠ permanent error in live traffic, but slow IS auto-disable trigger in scheduled probes.

**S-11: Test permanent-error disable** `[region-3]`. The classifier S-1 is also invoked on the test response. If the probe returned a permanent-error pattern, the channel is disabled.

**S-12: Auto-enable path** `[region-3, region-1]`. After a successful probe with no errors at all, a separate function `ShouldEnableChannel()` evaluates whether to re-enable the channel. This path is **also guard-flagged**: `AutomaticEnableChannelEnabled` (separate from auto-disable flag). When all three conditions hold (auto-enable on, no go-error, no openai-error), the channel is re-enabled.

**S-13: Channel status persistence (two-phase)** `[region-5, region-6]`. Status mutation is a two-table update:
- Phase 1 — `UpdateAbilityStatus(channelId, enabled)`: updates the Ability table (per (Group, Model, ChannelId) tuple) setting `enabled = false` for ALL of the channel's ability rows.
- Phase 2 — `UPDATE Channel SET status = ?`: sets the channel's status column to one of {0=Unknown, 1=Enabled, 2=ManuallyDisabled, 3=AutoDisabled}.

The two phases are NOT wrapped in a database transaction — see R-3 + Q-5.

**S-14: Memory cache (no event-driven invalidation)** `[region-7]`. The gateway maintains an in-process cache of channel candidates per (Group, Model). The cache is rebuilt on a TTL: `SyncFrequency` default 10 minutes. Channel-status changes do NOT trigger immediate invalidation; the cache is eventually consistent on the order of minutes.

**S-15: Redis cache (TTL only)** `[region-7]`. Optional Redis caches (token, user-group, group-models) all use TTL-based eviction. No explicit invalidation on channel disable.

**S-16: Selection-time exclusion** `[region-8]`. When picking a channel for a request, `CacheGetRandomSatisfiedChannel()` returns from the in-memory cache of enabled channels. Disabled channels are excluded **only** when the cache rebuilds (S-14 TTL) — not at status-change time.

**S-17: Retry pool exclusion (single-request)** `[region-8]`. Within a single request's retry loop, the retry pool selects a different channel than the last failed one (`channel.Id != lastFailedChannelId`). The just-disabled channel is NOT explicitly excluded from this loop — the cache lag in S-14 means it could be re-selected within the same request.

**S-18: Async disable launch** `[region-8]`. After a relay error, the disable decision is launched in a goroutine: `go processChannelRelayError(...)`. The retry loop does NOT wait for the goroutine to complete. Same-request retries may pick the same channel that is concurrently being disabled.

**S-19: Notification dispatch (best-effort)** `[region-4]`. On status flip (disable or enable), a `notifyRootUser()` function dispatches:
- Primary: Message Pusher (HTTP webhook) if `MessagePusherAddress` configured.
- Fallback: SMTP email to root user if pusher missing or fails.
- Logging: `logger.SysLog` for the status change reason.

Failures of either dispatch are logged and swallowed; notification has no retry, no delivery confirmation.

**S-20: Notification timing** `[region-4]`. `notifyRootUser()` is called synchronously from `DisableChannel()`/`EnableChannel()`. Those callers themselves often run in a goroutine (S-18 async-launch path), making notification fire-and-forget at the request level but blocking at the goroutine level.

**S-21: Re-enable resets nothing automatically** `[inferred from region-1, region-2]`. Re-enabling a channel does not reset its metric window state (S-3) — though the window was reset to empty when Gate B previously fired (S-4). The interaction means a re-enabled channel starts metric tracking fresh, but until the window fills again (≥10 events), Gate B cannot fire.

### 2-bis Lifecycle traces (3 observed, 2 marked open)

**L-1 Happy disable (default config)**: With both guard flags off (S-2, S-5), live traffic NEVER triggers auto-disable. Only manual operator action (admin UI calling `UpdateChannelStatusById(id, ChannelStatusManuallyDisabled)`) flips status. **This is the shipping-default path.** `[region-1, region-9]`.

**L-2 Happy disable (operator opts in)**: Operator sets `AutomaticDisableChannelEnabled=true` and `EnableMetric=true`. Channel returns 401 mid-traffic → S-1a fires → goroutine launches → S-13 two-phase persists status=3 → S-19 notifies operator. Selection at next cache rebuild (≤10 min later) excludes the channel. Within the failing request: S-17/S-18 retry picks another channel from cache (which still includes the to-be-disabled channel for ~10 min); if the retry happens to land on the same channel, another 401 is observed.

**L-3 Scheduled-probe disable**: Background timer ticks → S-7 enters → S-8 acquires lock → for each channel, S-9 sends probe. Channel X probe takes 6s (>5s threshold) → S-10 calls S-1 with synthetic error → status=3 → S-19 notifies. Lock releases. Next tick re-probes.

**L-4 Partial-failure stuck (cache lag DOS)** `[inferred from S-14, S-17, S-18]`: Channel hits 401 once, async disable starts, goroutine completes status mutation. But the in-memory cache returns the channel for the next ~10 minutes. Every request landing on this channel during the lag window fails immediately. Operator does not know unless they read logs or the cache is rebuilt and notification arrives. **In-cache stale-channel window is the dominant operator-pain failure mode.**

**L-5 Hostile (replay-attack on an already-disabled channel)** — moved to §9 Q-5.

---

## 3. INPUTS (data structures touched)

**Per-Request inputs**: error object (`model.Error` shape), HTTP status code, channel id (lastFailed for retry exclusion), Group, Model, retry counter.

**Per-Channel state read/mutated**: status enum (0/1/2/3), success/failure boolean array (in-memory map keyed by channel id), Ability rows (per (Group, Model, ChannelId)), name (for logging).

**Per-Process state**: in-memory channel cache (map of (Group, Model) → channel candidates), Redis cache when configured, success/failure consumer goroutines (paired channels), test-runner singleton mutex + flag, root-user email cache.

**Persistent state**: `Channel` table (status column), `Ability` table (enabled column), no audit/log table for disable events observed (S-19 logs go to general log sink, not a typed audit table).

**Configuration inputs (operator-supplied via env or admin)**: `AutomaticDisableChannelEnabled` (default false), `AutomaticEnableChannelEnabled` (default false), `EnableMetric` (default false), `MetricQueueSize` (default 10), `MetricSuccessRateThreshold` (default 0.8), `ChannelDisableThreshold` (response time, default 5s), `RequestInterval`, `SyncFrequency` (default 10m), `TestPrompt`, `MessagePusherAddress`, `RootUserEmail`.

---

## 4. FAILURE MODES (observed-only)

| FM-id | Trigger | Observable outcome | Operator signal | Recovery | Blast radius |
|---|---|---|---|---|---|
| FM-1 | Default config (both gates off) | Auto-disable never fires; broken channel keeps consuming retries until manual op | none | manual operator action | single-channel — but lost requests across all groups using it |
| FM-2 | Cache lag after auto-disable | Up to 10 minutes of requests still landing on disabled channel | none (eventual cache rebuild masks the issue) | wait for cache rebuild | all requests in (Group, Model) for cache lag duration |
| FM-3 | Notification dispatch fails | Status flip happened, operator never notified | log only (fire-and-forget) | none | operator awareness only |
| FM-4 | Two-phase status update partial fail | Ability table updated but Channel table fails (or vice versa) — inconsistent state `[region-5,6]` | error log | none observed | one channel inconsistency until next manual update |
| FM-5 | Permanent-error pattern not in classifier | New provider error string slips through; channel never disabled | log line if logged | manual classifier extension | single-channel ongoing failure |
| FM-6 | Metric collection consumer goroutine dies | Metric Gate B silently stops working | none | process restart | single-process |
| FM-7 | Scheduled-test singleton lock held forever (panic mid-run?) | No further scheduled probes possible until restart | log only (panic if unrecovered) | process restart | all channels — no probes |
| FM-8 | Race: same channel disabled concurrently from Gate A + Gate B + scheduled-test | Multiple status updates queued; last-write-wins; multiple notifications dispatched | duplicate notifications | none | duplicate ops noise |
| FM-9 | Manual re-enable while metric window still has failure history | Channel re-enabled but Gate B can't re-fire until window fills (≥10 events); intermediate failures un-counted | none (silent ramp-up) | none | single-channel ramp risk |

---

## 5. INTERFACES TO HUAKAI

**Personal Edition**:
- HUAKAI's existing `pool.HealthGate` is the structural analog of one-api's two-gate disable system. The 9-gate chain in F-POOL-001 covers gate A behavior (typed errors → temp_unsched). HUAKAI's `provider_accounts.health_state IN ('operational','degraded')` filter is the runtime selection-time exclusion (analogous to S-16 but cache-free since PostgreSQL handles consistency).
- Default-on policy: HUAKAI Personal Edition's auto-disable is on by default — DR-001 multi-tenant pressure inverts one-api's default-off choice (see R-1).

**SaaS Edition**:
- Cache-lag failure mode (FM-2) is unacceptable in multi-tenant: an operator's misconfigured tenant cannot punish other tenants via cache-stale routing. HUAKAI's PostgreSQL-direct reads (no in-memory channel cache) eliminate this mode (DR-006).
- Notification dispatch must be tenant-aware: a tenant's account auto-disabled notifies that tenant's operator, not the platform root. one-api's "root user only" pattern doesn't generalize.

**Cross-feature interfaces**:
- F-AUTH-005 OAuth refresh failure → HUAKAI's `temp_unsched` mechanism overlaps with one-api's "auto-disable on auth fail". HUAKAI separates per-failure-class duration (timeout=5m, OAuth 401=10m, invalid_grant=permanent) — finer-grained than one-api's binary disable.
- F-OBS-001 audit event row: HUAKAI MUST log each status flip as a typed audit row (one-api's general logger is insufficient for money-grade audit).
- F-POOL-001 9-gate chain Health gate: implementation of S-1 classifier behavior with HUAKAI's typed error classes.

---

## 6. RISKS HUAKAI MUST GUARD AGAINST

**R-1 [DR-001 multi-tenant default cost inversion]**: one-api defaults both gates OFF because false-disable cost > false-pass cost in single-tenant deploy. In HUAKAI multi-tenant, false-pass cost is HIGHER (one bad channel charging multiple tenants in cents while operator sleeps). HUAKAI's default MUST be auto-disable ON. Default-off would silently regress operators upgrading from one-api thinking the gate is the same.

**R-2 [DR-006 PostgreSQL — cache lag elimination]**: one-api's 10-minute cache-rebuild lag is a feature artifact of in-memory caching. HUAKAI uses PostgreSQL-backed selection (DR-006), so status flips are immediately visible to the next selection. **Do not introduce a memory cache for `provider_accounts.health_state`** — the round-trip cost is acceptable; the consistency benefit is essential.

**R-3 [Two-phase status update without transaction]**: one-api updates Ability and Channel tables in two separate SQL statements, no transaction `[region-5,6]`. HUAKAI MUST wrap analogous two-row updates in a serializable transaction with rollback on failure (DR-006 + F-OBS-001 §Tx pattern).

**R-4 [DR-001 — notification scope]**: one-api's "notify root user" is a single-operator pattern. HUAKAI multi-tenant MUST send notifications to the **owning tenant's operator** of the affected provider account, not a platform-wide superadmin. The notification target is part of tenant context, not a global config field.

**R-5 [Empirical classifier brittleness]**: S-1d/S-1e literal English/Chinese substring patterns are brittle — a provider changing the wording from "your access was terminated" to "access has been terminated" silently breaks the classifier. HUAKAI's reference-tracking policy MUST include "monitor upstream error message wording" as a recurring task with integration tests against real upstreams.

**R-6 [DR-002 SaaS Edition — async disable + retry race (FM-2 + FM-8)]**: A single tenant's runaway retry loop on a failing channel can fire S-18 hundreds of times concurrently before the goroutines complete. The duplicate-notification issue (FM-8) becomes a notification-storm DOS against the tenant's webhook endpoint. HUAKAI MUST debounce status-flip notifications per (account_id, status_class) with a window (e.g., 60 seconds).

**R-7 [Scheduled-probe singleton vs multi-replica deploy]**: one-api's `sync.Mutex` is process-local. In a horizontally-scaled SaaS Edition, every replica runs its own mutex — N replicas means N concurrent test runs probing the same channels. HUAKAI MUST elect a leader (advisory lock or coordinator) for scheduled probes, not rely on intra-process mutex.

**R-8 [Re-enable + metric reset semantics (FM-9)]**: A re-enabled channel cannot re-trigger Gate B until the window fills (≥10 events) — a stealth "honeymoon period" where a degraded channel passes silently. HUAKAI MUST either: (a) reset window to a degraded-state baseline on re-enable, OR (b) lower the traffic floor for the first window after re-enable.

**R-9 [Audit-grade durability]**: one-api logs status flips via general `logger.SysLog` — same channel as application logs. HUAKAI MUST persist status flips as typed audit rows in a durable table (similar to `oauth_refresh_audit_events` for F-AUTH-005). General logs may be rotated, sampled, or lost; audit must not be.

---

## 7. SAFE ADAPTATION (concrete divergences)

1. **Default both gates ON, with operator opt-out** instead of default off.
2. **Eliminate in-memory channel cache for status filtering**; use PostgreSQL `WHERE health_state IN (...)` per request.
3. **Wrap two-row status updates in serializable Tx** with rollback semantics.
4. **Tenant-scoped notification routing**: each tenant has their own notification endpoint; platform root is fallback only.
5. **Persistent typed audit row for every status flip** (table `pool_routing_audit_events` already in HUAKAI schema; reuse).
6. **Notification debounce per (account_id, status_class) within 60s window**.
7. **Distributed leader election for scheduled probes** (PostgreSQL advisory lock).
8. **Re-enable window grace policy**: lower traffic floor to 3 events for first window after re-enable, OR seed window with two failures so Gate B can re-fire faster.
9. **Per-failure-class duration** (already in F-AUTH-005): timeout 5m, OAuth 401 10m, invalid_grant permanent — finer than one-api's binary on/off.
10. **Reference-tracking integration tests** that probe real provider error messages weekly to catch wording drift (DR-022/24 cadence).

---

## 8. EVIDENCE LEDGER ROWS (proposed additions)

- **E-OAI-DEEP-006**: existing — promote with deep contents from this decomposition.
- **E-OAI-DEEP-NEW-1**: two-gate composition (Gate A + Gate B independent, additive) `[region-1, region-2]`.
- **E-OAI-DEEP-NEW-2**: cache-lag failure mode FM-2 — 10-minute eventual consistency window `[region-7]`.
- **E-OAI-DEEP-NEW-3**: empirical classifier brittleness — 11+ literal substring patterns `[region-1]`.
- **E-OAI-DEEP-NEW-4**: notification fire-and-forget pattern `[region-4]`.

---

## 9. OPEN QUESTIONS (for synthesis)

1. **Q-1 Cache invalidation event-driven path**: is there ANY code path that explicitly invalidates the in-memory channel cache on status flip, or is it purely TTL? Sonnet did not find one — confirm via deeper read.
2. **Q-2 UpdateAbilityStatus failure semantics**: what happens if the Ability update fails but the Channel update succeeds (or vice versa)? Sonnet noted no transaction wrapping — confirm whether there's compensating logic elsewhere.
3. **Q-3 TestPrompt configurability**: is TestPrompt per-channel, per-group, or global? Affects HUAKAI's whether tenant operators can customize their probe.
4. **Q-4 Metric window persistence on restart**: per-channel array is in-memory; on restart, all channels start with empty windows. Are there metrics that this is intentional (graceful warm-up) or a bug?
5. **Q-5 Re-disable after manual re-enable**: if operator manually re-enables a channel and traffic still fails, does Gate B's "ramp window" (need ≥10 events) effectively delay re-disable indefinitely under low traffic?
6. **Q-6 Concurrent test-runner + live-traffic disable interaction**: if scheduled probe fires Gate A concurrent with live traffic firing Gate A on the same channel, what's the observable status sequence? Last-writer wins per S-13, but the audit log entries may interleave.
7. **Q-7 Permanent-error classifier extensibility**: is there a hot-reload or admin-tunable surface for new patterns, or does adding a new pattern require code change + redeploy?
8. **Q-8 Cross-replica metric coordination**: in a multi-replica deploy (which one-api supports per docs), do replicas share the metric window, or each replica has its own per-channel window? Affects whether disable threshold is "10 events per replica" or "10 events globally".

---

## 10. SOURCE COVERAGE PROOF (Sonnet Explore agent reading, ~30min, 10 files)

| Region | URL | Contribution |
|---|---|---|
| region-1 | github.com/songquanpeng/one-api/main/monitor/manage.go (lines 11-44) | Permanent-error classifier; `ShouldDisableChannel`/`ShouldEnableChannel` logic; 5 detection paths |
| region-2 | .../monitor/metric.go (full file) | Success-rate window structure; threshold + traffic-floor evaluation; reset-on-fire |
| region-3 | .../controller/channel-test.go (lines 219-305) | Scheduled test runner; singleton mutex; probe shape; response-time threshold disable |
| region-4 | .../monitor/channel.go (lines 12-77) | DisableChannel/EnableChannel functions; notifyRootUser dispatch path |
| region-5 | .../model/channel.go (lines 190-199) | UpdateChannelStatusById two-phase mutation |
| region-6 | .../model/ability.go (lines 94-96) | UpdateAbilityStatus separate UPDATE |
| region-7 | .../model/cache.go (lines 173-255) | InitChannelCache, SyncChannelCache periodic rebuild; TTL semantics |
| region-8 | .../controller/relay.go (lines 45-132) | Retry loop; lastFailedChannelId exclusion; async disable launch via `go processChannelRelayError` |
| region-9 | .../common/config/config.go (lines 95-150) | Default-off flags: AutomaticDisableChannelEnabled, EnableMetric, AutomaticEnableChannelEnabled; defaults for thresholds, intervals |
| region-10 | .../common/message/message-pusher.go (full) | Webhook notification primary path; failure → email fallback |

---

## 11. ROUND-2 CRITIC FINDINGS (C1 one-api)

> Codex critic-lane file at `.omc/artifacts/decomp-critic/C1-oneapi-channel-auto-disable.md` enumerated 10 findings (C-001..C-010). This Claude-deep file is written WITHOUT reading the critic per cross-validation discipline. Synthesis stage merges Codex specifier-deep + C1 critic + this Claude-deep into a final deliverable. Critic findings will be reconciled at synthesis.

---

## Owner Chinese summary

本 deep 拆解依据 Sonnet Explore agent 真读 10 个 one-api 源文件（30min），由我（Claude Opus）合成 21 个 sub-behavior + 4 个 lifecycle + 9 个 failure 模式 + 9 个 HUAKAI-fit 风险 + 10 项 safe adaptation。**最关键发现**：one-api 的两个 disable gate（permanent-error 分类 + 滚动成功率）**默认全部关闭**——单租户部署的合理选择，但 HUAKAI 多租户必须默认开（R-1），否则其它操作员升级后会以为机制一致而暴露 false-pass 风险。次关键：内存 cache 10 分钟 TTL 不事件驱动失效（FM-2），HUAKAI 用 PostgreSQL 直读消除（R-2）；两表状态更新无事务（R-3）；Notification 只通知 root 不通知 tenant operator（R-4）；定时探针单进程 mutex 多副本失效（R-7）。本文件未读 codex specifier 或 critic 输出，是独立第二视角。
