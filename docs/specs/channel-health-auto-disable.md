# F-CH-002: Channel Health Auto-Disable

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature ID | F-CH-002 |
| Specifier | Codex GPT-5 implementer/spec writer |
| Specifier date | 2026-05-16 |
| Reviewer | Pending |
| Review date | Pending |
| Released date | Pending |
| Lane mode | Implementer/spec writer consuming HUAKAI-owned docs only; no reference-project source read |
| Supersedes | Prior F-CH-002 row wording in `docs/03_FEATURE_PARITY_MATRIX.md` |
| Superseded by | - |

## Sources

This draft consumes HUAKAI-owned specs, plans, matrix rows, and evidence-ledger summaries only. It does not read source code from `sub2api`, `new-api`, `portkey`, `helicone`, `litellm`, `all-api-hub`, or `envoy-ai-gateway`.

- `docs/RULES.md` - Owner start gate, clean-room, feature preservation, planning rules.
- `docs/03_FEATURE_PARITY_MATRIX.md` - existing F-CH-002 row and adjacent F-CRED-001, F-AUTH-005, F-RATE-001, F-POOL-001 rows.
- `docs/07_REFERENCE_EVIDENCE_LEDGER.md` - behavior-only public README evidence rows E-OAI-009 and E-OAI-013.
- `docs/11_ACCEPTANCE_TEST_MATRIX.md` - acceptance matrix format and AT-CH-001 / F-CRED-001 examples.
- `docs/specs/credential-acquisition.md` - F-CRED-001 acquisition boundary.
- `docs/specs/upstream-credential-management.md` - F-AUTH-005 credential storage and refresh boundary.
- `docs/specs/rate-limiting.md` - F-RATE-001 per-response rate-limit and cooldown boundary.
- `docs/specs/pool-routing.md` - F-POOL-001 / PASR-lite selection boundary and health gate.
- `docs/decompositions/_cross-cutting/credential-acquisition.md` - cross-module acquisition boundary.
- `docs/process/plans/2026-05-16-f-ch-002-channel-health-codex.md` - pre-execution plan for this docs wave.

## Capability

F-CH-002 defines a neutral operations mechanism for channel health scoring, automatic cooldown, and gradual recovery. A **Channel** in this spec is the pair:

```text
(vendor, account_credential)
```

Example:

```text
(anthropic, acct_42_credential_v3)
```

The channel is the runtime health subject. It is more granular than a vendor and more specific than a Provider Account when the account can hold multiple credential versions or modes. It is also independent from the acquisition flow that created the credential.

The feature outcome is:

1. Newly added credentials start eligible by default.
2. Runtime and probe signals update a rolling health score.
3. Bad signals move the channel into `degraded`, `cooling_down`, or `disabled` without deleting credentials.
4. Selection excludes `cooling_down` and `disabled` channels, so multi-account pools fail over to the next healthy channel.
5. Cooldown expiry starts a controlled traffic ramp: 1 percent -> 10 percent -> 50 percent -> 100 percent.
6. Every degradation, disable, override, and recovery writes F-TRUST audit evidence.

This is not an anti-detection or browser-fingerprint feature. TLS, device fingerprint, browser version, automation profile, and similar runtime-hardening topics remain outside this spec and belong to later R-E+1 work only after Owner approval.

## Actors

- **System, gateway request path**: emits success, failure, status-code, latency, and selection-attempt signals after each upstream attempt.
- **System, health aggregator**: folds raw attempt/probe signals into rolling windows and updates `channel_health_state`.
- **System, selector / PASR-lite**: reads channel eligibility and ramp percentage before routing.
- **Admin/Operator**: observes health states, receives alerts, applies manual pause or manual recovery override, and tunes tenant policy.
- **F-TRUST audit chain**: records lifecycle evidence without raw credential material or upstream response bodies.

## Preconditions

1. F-CRED-001 has finalized a credential, or an existing F-AUTH-005 credential exists.
2. F-AUTH-005 stores the credential and exposes only redacted credential identity to F-CH-002.
3. Gateway attempts produce normalized outcome events containing tenant id, vendor, account credential id/version, status class, latency, and safe error class.
4. F-POOL-001 / PASR-lite selection can ask whether a channel is currently eligible and, if ramping, what traffic percentage is admitted.
5. This docs wave does not add schema, code, migrations, admin UI, OpenAPI routes, or runtime dependencies.

## State Model

F-CH-002 owns channel health state, not credential lifecycle state.

| State | Routing eligibility | Meaning |
| --- | --- | --- |
| `active` | Eligible | Channel has enough recent evidence to serve normal traffic. New credentials enter here by default unless an admin creates them as manual-paused. |
| `degraded` | Eligible, lower priority | Warning state. Health score crossed a warning threshold, but not a cooldown threshold. Selector may prefer healthier channels. |
| `cooling_down` | Not eligible | Temporary automatic exclusion until `cooldown_until`. Triggered by rolling error/rate-limit/latency conditions. |
| `ramping` | Partially eligible | Cooldown elapsed; channel admits only the current ramp percentage. |
| `disabled` | Not eligible | Long cooldown or operator-confirmed exclusion after a strong ban signal. Does not delete or rotate credentials. |
| `manual_paused` | Not eligible | Admin/operator override. Automatic recovery must not clear this state without explicit operator action. |

State transitions must be monotonic with audit. A direct transition from `disabled` or `manual_paused` to `active` is allowed only through an audited manual override or a completed ramp state machine.

## Health Dimensions

The health score is computed over real-time sliding windows. The implementation must keep the threshold policy tenant-scoped and versioned. The policy values below are field names, not hardcoded production defaults.

| Dimension | Required signal | Notes |
| --- | --- | --- |
| `error_rate` | Failed attempts / total attempts over `error_rate_window_minutes` | Includes normalized 4xx and 5xx failures that are attributable to the channel. Client-side malformed requests are excluded. |
| `latency_p99` | P99 upstream latency over `latency_window_minutes` | Uses upstream-attempt latency, not end-user total request time. Minimum sample floor required. |
| `rate_limit_hit_rate` | 429 / 403 rate-limit-like outcomes over `rate_limit_window_minutes` | F-RATE-001 classifies single responses; F-CH-002 aggregates frequency. |
| `upstream_5xx_rate` | Upstream 5xx failures / attempts over `upstream_5xx_window_minutes` | Separated from local gateway 5xx and network failures. |
| `ban_signal` | Strong signal such as account suspended, token revoked, credential revoked, or account disabled | Must be normalized into a safe class. Raw upstream body text is not stored. |

Every rolling window must enforce:

1. `min_sample_count` before automatic cooldown from percentages.
2. `min_observation_minutes` before ramp success.
3. Tenant isolation in every key.
4. Policy version attached to every transition.
5. No raw credential material, raw prompt, or raw upstream response body in aggregate rows.

## Cooldown Triggers

Cooldown decisions use a health policy object:

```text
channel_health_policy {
  error_rate_threshold_pct
  error_rate_window_minutes
  error_rate_cooldown_minutes
  latency_p99_threshold_ms
  latency_window_minutes
  latency_cooldown_minutes
  rate_limit_hit_rate_threshold_pct
  rate_limit_window_minutes
  default_rate_limit_cooldown_minutes
  upstream_5xx_rate_threshold_pct
  upstream_5xx_window_minutes
  upstream_5xx_cooldown_minutes
  ban_signal_min_cooldown_hours
  ban_signal_max_cooldown_hours
  ramp_stage_min_minutes
  ramp_stage_min_samples
  ramp_error_threshold_pct
  manual_override_requires_reason
}
```

### Error-rate cooldown

Trigger:

```text
error_rate > error_rate_threshold_pct
over error_rate_window_minutes
with total_attempts >= min_sample_count
```

Action:

1. Transition `active` or `degraded` -> `cooling_down`.
2. Set `cooldown_until = now + error_rate_cooldown_minutes`.
3. Emit `channel_health_degraded` and `channel_disabled` if routing eligibility changes to excluded.
4. Exclude the channel from normal selection immediately after state commit.

### Latency cooldown

Trigger:

```text
latency_p99 > latency_p99_threshold_ms
over latency_window_minutes
with total_attempts >= min_sample_count
```

Action:

1. Transition `active` -> `degraded` for the first threshold crossing.
2. Escalate to `cooling_down` only when latency remains above threshold for a second policy window or combines with error/rate-limit threshold crossing.
3. Emit `channel_health_degraded`.

Latency alone should not permanently disable a channel.

### Rate-limit cooldown

Trigger:

```text
rate_limit_hit_rate > rate_limit_hit_rate_threshold_pct
over rate_limit_window_minutes
```

Action:

1. If F-RATE-001 provides a concrete reset time, set `cooldown_until` to that reset time plus bounded jitter.
2. If no reset time exists, use `default_rate_limit_cooldown_minutes`.
3. Transition to `cooling_down`.
4. Emit `channel_health_degraded` and `channel_disabled`.

This path must not mutate F-AUTH-005 credential material. If the root cause is credential refresh, F-AUTH-005 remains the owner of refresh and replacement.

### Upstream 5xx cooldown

Trigger:

```text
upstream_5xx_rate > upstream_5xx_rate_threshold_pct
over upstream_5xx_window_minutes
with total_attempts >= min_sample_count
```

Action:

1. Transition to `degraded` first.
2. Transition to `cooling_down` only after a second threshold crossing or when selection has at least one alternate healthy channel.
3. If the pool has no alternate healthy channel, policy may choose `degraded_last_resort` behavior by keeping the channel eligible with lower priority and alerting the operator. This is a safety valve, not a silent success.

### Ban-signal cooldown

Trigger:

```text
ban_signal detected
```

Examples of safe normalized classes:

- `account_suspended`
- `token_revoked`
- `credential_revoked`
- `account_disabled`
- `subscription_or_workspace_disabled`

Action:

1. Transition to `disabled`.
2. Set cooldown to a policy duration in the 24-72 hour range.
3. Emit `channel_disabled` and an admin alert.
4. Require operator acknowledgement before recovery ramp unless tenant policy explicitly enables automatic post-ban ramp.
5. Preserve the credential row for operator investigation; do not delete or overwrite secrets.

The ban-signal classifier must store a reason class and confidence tier, not raw upstream text.

## Recovery Ramp

When `cooldown_until` expires, the system does not immediately restore full traffic. It enters `ramping`.

Default stage sequence:

```text
1% -> 10% -> 50% -> 100%
```

Each stage requires:

1. At least `ramp_stage_min_minutes` elapsed.
2. At least `ramp_stage_min_samples` channel attempts or health probes.
3. Error/rate-limit/5xx dimensions remain under the ramp rollback threshold.
4. No new ban signal.

If a stage passes:

```text
1% -> 10%
10% -> 50%
50% -> 100%
100% -> active
```

If any stage fails:

1. Transition back to `cooling_down`.
2. Increase cooldown by the policy backoff factor or reuse the policy cooldown duration.
3. Emit `channel_health_degraded` and `channel_disabled`.
4. Keep the ramp failure reason visible in admin history.

If a ban-signal channel reaches the end of its cooldown but lacks required operator acknowledgement, it remains `disabled` with `recovery_blocked_reason = operator_ack_required`.

## Interaction With F-CRED-001

F-CRED-001 owns first acquisition. F-CH-002 must not block acquisition flow.

Required behavior:

1. When F-CRED-001 finalizes a credential successfully, F-CH-002 creates or resets the channel health subject for `(vendor, account_credential_id, credential_version)`.
2. The new channel defaults to `active` unless admin created it as `manual_paused`.
3. If the channel later degrades or disables itself, the acquisition flow remains completed; operators should not have to reacquire credentials just because F-CH-002 is cooling down.
4. If F-CRED-001 creates a new credential version for the same account, F-CH-002 treats it as a new channel subject and keeps old-version history for audit and diagnosis.

## Interaction With F-AUTH-005

F-AUTH-005 owns credential storage, encryption, refresh, CAS, and refresh-storm control. F-CH-002 consumes only redacted credential identity and normalized outcomes.

Required behavior:

1. F-AUTH-005 refresh failures may emit normalized health signals to F-CH-002.
2. F-CH-002 may mark the channel `cooling_down` or `disabled`, but must not mutate credential bytes.
3. A F-AUTH-005 refresh success can be used as a recovery signal, but it does not automatically bypass the F-CH-002 ramp.
4. F-CH-002 audit must reference credential id/version, not raw token fingerprints.

## Interaction With F-RATE-001

F-RATE-001 classifies individual upstream responses and computes per-response cooldown hints. F-CH-002 aggregates the history into channel-level health state.

Boundary:

- F-RATE-001: "This response says rate limit / overload / token refresh needed."
- F-CH-002: "This channel is healthy, degraded, cooling down, disabled, or ramping based on recent history."

F-CH-002 may use F-RATE reset timestamps, but must not duplicate rate-limit header parsing logic.

## Interaction With F-POOL-001 / PASR-lite

F-POOL-001 and PASR-lite own selection. F-CH-002 provides eligibility and score inputs.

Required behavior:

1. `cooling_down`, `disabled`, and `manual_paused` channels are excluded from normal selection.
2. `degraded` channels remain eligible but carry lower health priority.
3. `ramping` channels are sampled according to the current ramp percentage.
4. When one channel is disabled, selection must automatically attempt the next healthy channel in the same eligible pool.
5. If every channel is unhealthy, the selector returns an operator-visible `pool_exhausted` or `all_channels_degraded` reason; it must not silently route to a disabled channel unless an audited last-resort policy allows it.

## Storage Contract

This section is a future implementation contract only. This docs wave does not add migrations.

### `channel_health_state`

One row per current channel subject.

| Field | Purpose |
| --- | --- |
| `tenant_id` | Tenant isolation. |
| `channel_id` | Stable HUAKAI channel identity. |
| `vendor` | Vendor dimension. |
| `provider_account_id` | Provider Account identity, if available. |
| `account_credential_id` | F-AUTH-005 credential id. |
| `credential_version` | Credential version used to separate old/new health. |
| `state` | `active`, `degraded`, `cooling_down`, `ramping`, `disabled`, or `manual_paused`. |
| `score` | Current normalized health score. |
| `reason_class` | Safe normalized reason class. |
| `confidence_tier` | `observed`, `inferred`, or `operator_override`. |
| `cooldown_until` | Nullable timestamp for automatic cooldown. |
| `ramp_stage` | Nullable current stage: `1`, `10`, `50`, `100`. |
| `ramp_started_at` | Current ramp start time. |
| `last_transition_at` | Last state transition. |
| `policy_version` | Health policy version used for the transition. |
| `manual_override_actor_id` | Set only for manual transitions. |
| `manual_override_reason` | Required when policy requires it. |
| `created_at`, `updated_at` | Lifecycle timestamps. |

### Time-series aggregation

The implementation needs time-windowed aggregate records. The exact storage may be a table, rollup materialization, or derived query over Usage Records, but the semantics must support:

- tenant + channel + window key;
- success/failure counts;
- 4xx and 5xx status-class counts;
- 429 and 403 rate-limit-like counts after F-RATE normalization;
- upstream 5xx counts distinct from local gateway failures;
- latency distribution sufficient for P99;
- ban-signal count and latest reason class;
- sample floor and window completeness metadata.

Retention and compaction must preserve audit-grade state transitions even after high-cardinality raw attempt events expire.

## Admin Controls

Minimum future admin operations:

| Operation | Required behavior |
| --- | --- |
| View channel health | Show current state, score, window counts, cooldown/ramp timer, last transition, policy version, and safe reason class. |
| Manual pause | Set `manual_paused`; selection excludes the channel; audit required. |
| Manual resume into ramp | Move from `manual_paused`, `disabled`, or `cooling_down` into `ramping` at 1 percent; audit required. |
| Force active | Break-glass only; requires reason and elevated role. Must emit high-severity audit. |
| Policy tune | Update tenant health thresholds as a versioned policy; new decisions use new version, old audit remains tied to prior version. |
| Acknowledge ban signal | Allows post-ban recovery ramp after cooldown. Does not erase the ban event. |

## Audit / Alert Evidence

F-CH-002 emits these F-TRUST event types:

| Event | When | Payload allowlist |
| --- | --- | --- |
| `channel_health_degraded` | Score crosses warning or rollback threshold. | tenant id, channel id, vendor, account credential id/version, previous state, new state, reason class, score, policy version, window summary, request id if request-triggered. |
| `channel_disabled` | Channel becomes ineligible due to cooldown, ban signal, or manual pause. | tenant id, channel id, vendor, account credential id/version, previous state, new state, reason class, cooldown_until, operator actor if manual, policy version. |
| `channel_recovered` | Ramp completes to `active` or operator resumes successfully. | tenant id, channel id, vendor, account credential id/version, previous state, new state, ramp history, policy version. |
| `channel_ramp_started` | Cooldown expires and ramp begins. | tenant id, channel id, vendor, account credential id/version, ramp stage, cooldown source, policy version. |
| `channel_manual_override` | Admin pauses, resumes, or force-activates. | tenant id, channel id, actor id, operation, reason class, policy version. |

Payloads must not include raw upstream response bodies, raw prompts, API keys, access tokens, refresh tokens, cookies, private keys, or client secrets.

Alert requirements:

1. Ban-signal `channel_disabled` always alerts admin.
2. Error/rate-limit cooldown alerts when the channel was carrying production traffic or when the pool has no healthy alternate.
3. Repeated ramp rollback alerts after policy-configured recurrence threshold.
4. Manual force-active emits security-grade audit and alert.

## Acceptance Test Direction

Acceptance coverage is `AT-CH-002-001..013` in `docs/11_ACCEPTANCE_TEST_MATRIX.md` (含 AT-CH-002-013 latency P99 spike triggers cooldown + recovers when latency normalizes).

The test family must cover:

- default active state after F-CRED-001 finalization;
- error-rate cooldown;
- rate-limit cooldown to reset window;
- ban-signal 24-72 hour cooldown and alert;
- gradual 1/10/50/100 percent recovery;
- ramp rollback;
- multi-vendor and multi-credential isolation;
- automatic failover to next healthy channel;
- manual override;
- F-TRUST audit chain;
- sample-floor / no-flapping guard;
- boundary with F-AUTH-005 and F-RATE-001.

## Open Questions

1. Owner must confirm production threshold defaults for error rate, rate-limit hit rate, latency P99, upstream 5xx, and sample floors.
2. Owner must confirm whether ban-signal recovery always requires operator acknowledgement or can be automatic per tenant policy.
3. Owner must confirm future admin API/UI shape for manual pause/resume/force-active.
4. Owner must confirm whether `channel_health_state` should be a dedicated table in Phase B or initially derived from existing account/usage state with a migration deferred.

## Implementer Notes

- 2026-05-16 - Codex GPT-5 - Draft only. No backend code, schema migration, OpenAPI route, runtime dependency, billing/quota/auth-core change, or LICENSE change was made.
- 2026-05-16 - Codex GPT-5 - R-E+1 anti-detection mechanics are explicitly out of scope.

Source files read: docs/RULES.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/07_REFERENCE_EVIDENCE_LEDGER.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/specs/credential-acquisition.md; docs/specs/upstream-credential-management.md; docs/specs/rate-limiting.md; docs/specs/pool-routing.md; docs/decompositions/_cross-cutting/credential-acquisition.md; docs/decompositions/_cross-cutting/pool-selection-synthesis-v2.md; docs/process/plans/2026-05-16-f-ch-002-channel-health-codex.md; .agents/skills/acceptance-test-writer/SKILL.md; .agents/skills/feature-parity-auditor/SKILL.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T07:00:34Z
