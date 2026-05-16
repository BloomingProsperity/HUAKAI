# Cross-Cutting - Channel Health Auto-Disable

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature in HUAKAI matrix | F-CH-002 |
| Evidence ledger row | E-OAI-009, E-OAI-013 via HUAKAI evidence ledger summaries only |
| Specifier session | Codex GPT-5 implementer/spec writer |
| Specifier date | 2026-05-16 |
| Reviewer session | Pending |
| Reviewer date | Pending |
| Source files read | HUAKAI docs/specs/plans only; no reference-project source read in this lane |
| Observed regions | 10 HUAKAI-owned docs and skill files |
| Inferences | 7 HUAKAI-fit inferences, marked below |
| Open questions | 4 Owner confirmation points for implementation wave |

## 1. WHY

F-CRED-001 and F-AUTH-005 make upstream credentials addable and manageable, but they do not answer the operational question: "Should this credential receive traffic right now?" Without a channel-health module, every selector and fallback path must either trust stale account state or reinvent local heuristics. That creates flapping, silent disablement, and hard-to-debug failover.

F-CH-002 centralizes that question. It converts recent attempt/probe outcomes into a state machine for `(vendor, account_credential)`. The state machine can exclude a bad channel, ramp it back gradually, and leave a F-TRUST audit trail.

HUAKAI-fit inference: this module is a neutral operations control plane. It is not an anti-detection module, not an acquisition module, and not a credential-refresh module.

## 2. WHAT

F-CH-002 owns channel-level health state:

```text
channel = (vendor, account_credential)
```

The channel state machine is:

- `active`
- `degraded`
- `cooling_down`
- `ramping`
- `disabled`
- `manual_paused`

It consumes rolling metrics:

- `error_rate`
- `latency_p99`
- `rate_limit_hit_rate`
- `upstream_5xx_rate`
- `ban_signal`

It produces:

- current routing eligibility for F-POOL-001 / PASR-lite;
- cooldown and recovery timers;
- ramp percentage;
- admin alerts;
- F-TRUST events: `channel_health_degraded`, `channel_disabled`, `channel_recovered`, plus support events for ramp and manual override.

HUAKAI-fit inference: `disabled` in this module means "do not route traffic to this channel"; it does not mean "delete the credential" or "mark the upstream account permanently unusable forever."

## 3. BOUNDARIES

### F-CH-002 vs F-CRED-001

F-CRED-001 owns first acquisition:

- guided OAuth/bootstrap/import/paste flow;
- redacted preview;
- finalizer handoff to F-AUTH-005;
- acquisition audit.

F-CH-002 begins after a credential exists:

- creates the health subject for `(vendor, account_credential_id, credential_version)`;
- defaults it to `active` unless operator policy says manual-paused;
- later degrades/disables/ramp-recovers based on runtime evidence.

F-CH-002 must not block acquisition success. A channel becoming unhealthy after creation does not reopen or fail the acquisition flow.

### F-CH-002 vs F-AUTH-005

F-AUTH-005 owns:

- encrypted credential storage;
- refresh;
- refresh CAS;
- refresh-storm control;
- token leakage discipline.

F-CH-002 owns:

- routing eligibility derived from health;
- score and state transitions;
- cooldown and ramp.

F-CH-002 may consume F-AUTH-005 refresh outcomes as health signals. It must not mutate token bytes, overwrite credentials, or bypass F-AUTH-005 refresh policy.

### F-CH-002 vs F-RATE-001

F-RATE-001 classifies individual upstream responses: rate limit, overload, token refresh needed, permanent revoke, or other normalized outcome.

F-CH-002 aggregates many classified outcomes over time. It decides whether the channel remains eligible, cools down, disables, or ramps.

HUAKAI-fit inference: keep status-code/header parsing in F-RATE-001 and keep rolling health hysteresis in F-CH-002. This avoids two modules disagreeing on a single response parser.

### F-CH-002 vs PASR-lite / F-POOL-001

PASR-lite and F-POOL-001 select a channel/account for a request. F-CH-002 is an input to that decision, not the selector itself.

F-CH-002 provides:

- eligible or not eligible;
- current state;
- score;
- ramp percentage;
- reason class.

PASR/F-POOL decides:

- which eligible account to use;
- how to rank degraded candidates;
- how to fail over when one channel is ineligible;
- how to report `pool_exhausted` or `all_channels_degraded`.

HUAKAI-fit inference: F-CH-002 should not embed a routing algorithm. It should expose simple, testable health facts to the selector.

### F-CH-002 vs R-E+1 anti-detection hardening

F-CH-002 records vendor-neutral operational health. It must not specify:

- TLS mimicry;
- device fingerprint;
- Chrome/browser version;
- automation runtime profile;
- transport sidecar details.

Those topics are future R-E+1 work and require separate Owner/legal/security approval.

## 4. INPUTS

F-CH-002 consumes:

- tenant id;
- vendor;
- provider account id when available;
- account credential id and credential version;
- gateway attempt outcome;
- normalized F-RATE-001 class;
- upstream status class;
- upstream-attempt latency;
- retry/failover attempt number;
- safe ban-signal reason class;
- F-AUTH-005 refresh outcome class;
- health policy version;
- admin override command.

F-CH-002 mutates future implementation storage:

- `channel_health_state`;
- sliding-window aggregates or derived rollups;
- alert records or outbox;
- F-TRUST audit events.

It must not mutate:

- backend code in this docs wave;
- database schema in this docs wave;
- credential secret bytes;
- billing ledger;
- quota enforcement;
- user/session auth core;
- `LICENSE`.

## 5. STORAGE

### `channel_health_state`

The future state table should hold one current row per channel subject:

```text
(tenant_id, vendor, account_credential_id, credential_version)
```

Required state fields:

- channel id;
- provider account id if applicable;
- state;
- normalized score;
- reason class;
- confidence tier;
- cooldown_until;
- ramp stage;
- ramp_started_at;
- last_transition_at;
- policy_version;
- manual override actor/reason;
- created_at / updated_at.

### Sliding windows

The implementation also needs time-series aggregation over N-minute windows:

- attempts total;
- success count;
- 4xx count;
- 5xx count;
- upstream 5xx count;
- rate-limit-like count after F-RATE normalization;
- latency distribution sufficient for P99;
- ban-signal count and latest reason;
- window completeness and sample floor.

The aggregate can be a dedicated table, materialized rollup, or query derived from Usage Records in an early implementation. The contract is the behavior, not the exact physical design, until Owner confirms schema.

HUAKAI-fit inference: a dedicated state table plus derived/rollup windows is the cleanest Phase B path because selection needs a fast current-state read, while operators need historical windows for diagnosis.

## 6. FAILURES HANDLED

### Rolling error spike

- Detection: error rate exceeds tenant policy over the configured window and sample floor.
- Response: move to `cooling_down`; exclude from normal selection.
- Recovery: after cooldown, ramp 1 percent -> 10 percent -> 50 percent -> 100 percent.

### Rate-limit spike

- Detection: rate-limit hit rate exceeds policy.
- Response: use F-RATE reset time when available; otherwise default policy cooldown.
- Recovery: ramp after cooldown.

### Upstream 5xx spike

- Detection: upstream 5xx rate exceeds policy.
- Response: degrade first, then cooldown if repeated or alternate healthy channels exist.
- Recovery: ramp or last-resort operator policy.

### Latency degradation

- Detection: latency P99 exceeds threshold and sample floor.
- Response: degrade first; cooldown only after sustained or combined threshold breach.
- Recovery: clear degraded state after clean windows or ramp after cooldown.

### Ban signal

- Detection: safe normalized ban reason class appears.
- Response: `disabled`, 24-72 hour cooldown policy, admin alert.
- Recovery: operator acknowledgement by default, then ramp.

### Flapping during ramp

- Detection: ramp stage exceeds rollback threshold.
- Response: return to cooldown, record ramp failure.
- Recovery: retry ramp after new cooldown.

### Manual override

- Detection: admin pauses/resumes/force-activates.
- Response: apply state transition with reason and actor audit.
- Recovery: follow explicit operator command; automatic recovery cannot clear `manual_paused`.

## 7. FAILURES NOT HANDLED

F-CH-002 does not repair a bad credential. It can mark the channel unhealthy and alert. F-AUTH-005/F-CRED-001 own replacement or reacquisition.

F-CH-002 does not prevent client-side abuse. F-SEC-001/F-SEC-004 own user/IP/model rate limits.

F-CH-002 does not decide pricing, refunds, or quota settlement. F-OBS-001/F-BILL-001 own usage and billing semantics.

F-CH-002 does not implement real browser/runtime anti-detection. R-E+1 remains separate.

F-CH-002 does not claim a production schema in this docs wave. It records the storage contract for future Owner-confirmed implementation.

## 8. KEEP / IMPROVE / AVOID

- **KEEP**: channel health as a first-class operator concern; no silent feature drop.
- **KEEP**: auto-disable/cooldown outcome, but make it audited and recoverable.
- **KEEP**: failover outcome: one bad channel should not take down a pool with healthy alternates.
- **IMPROVE**: use multi-signal windows instead of single last-error decisions.
- **IMPROVE**: ramp recovery instead of immediate full re-enable.
- **IMPROVE**: separate manual pause, auto cooldown, and ban-signal disable.
- **IMPROVE**: preserve credential acquisition success even when runtime health later degrades.
- **AVOID**: raw upstream text in health rows or audit.
- **AVOID**: permanent disable from ambiguous 5xx/latency alone.
- **AVOID**: automatic recovery of manually paused channels.
- **AVOID**: embedding PASR selection logic in the health module.
- **AVOID**: anti-detection implementation details in this neutral operations feature.

## 9. ACCEPTANCE TEST MAP

`AT-CH-002-001..013` should cover:

1. default active state after F-CRED-001 finalization;
2. error-rate cooldown;
3. upstream 5xx degraded -> cooldown behavior;
4. rate-limit cooldown to reset;
5. ban-signal 24-72 hour disabled state and alert;
6. failover to next healthy channel;
7. successful ramp stages;
8. ramp rollback;
9. multi-vendor / multi-credential isolation;
10. manual override;
11. F-TRUST audit chain;
12. sample floor and no-flapping guard.

## 10. ATTRIBUTION

This file is an implementer/spec-writer decomposition derived from HUAKAI-owned docs and Owner instructions. It does not introduce new reference-project source claims and does not read prohibited reference source code.

Clean-room checklist:

- CL-001/CL-002/CL-003: no reference function names, file structures, or schema names copied from upstream.
- CL-004/CL-005: no upstream UI/source or line-by-line algorithm translation.
- CL-006: evidence IDs used here are existing ledger summaries only.
- CL-007: lane mode recorded.
- CL-008: F-CH-002 exists in the feature matrix.
- CL-009: Owner implementation questions are explicit.
- CL-010: no source URLs in behavior sections.

## Review Sign-Off

Pending independent reviewer.

Source files read: docs/RULES.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/07_REFERENCE_EVIDENCE_LEDGER.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/specs/credential-acquisition.md; docs/specs/upstream-credential-management.md; docs/specs/rate-limiting.md; docs/specs/pool-routing.md; docs/decompositions/_cross-cutting/credential-acquisition.md; docs/decompositions/_cross-cutting/pool-selection-synthesis-v2.md; docs/plans/2026-05-16-f-ch-002-channel-health-codex.md; .agents/skills/acceptance-test-writer/SKILL.md; .agents/skills/feature-parity-auditor/SKILL.md
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T07:00:34Z
