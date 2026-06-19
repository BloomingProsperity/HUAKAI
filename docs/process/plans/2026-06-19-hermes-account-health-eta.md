# Plan — account_health_diagnose: surface health_state_until + 5h session window start/end

Date: 2026-06-19 · Author: Claude PM · Slice: disjoint-mining backlog #4 · Feature area: F-hermes (Ask-Hermes ops diagnostics)

## Scope
Add three timestamp keys to the read-only `account_health_diagnose` Ask-Hermes ops tool's summary
(`backend/internal/hermesops/tools_health.go`): `health_state_until` (when the current health state
expires — the degradation/cooldown recovery ETA) and `session_window_5h_start` / `session_window_5h_end`
(the account's 5-hour usage-window boundaries). The tool already surfaces `session_window_5h_status`
but not the recovery ETA or the window boundary timestamps, so an operator cannot see *when* a degraded
account recovers or *when* its 5h window resets.

## Not-already-built (verified real code, 2026-06-19)
- The `account_health_diagnose` summary map (tools_health.go:57-68) projects health_state /
  session_window_5h_status / probe + refresh timestamps, but NOT health_state_until,
  session_window_5h_start, or session_window_5h_end (grep confirmed absent).

## Value-in-hand (verified — zero db/schema change)
- The source row `admindb.GetAdminProviderAccountHealthRow` already carries `HealthStateUntil`,
  `SessionWindow5hStart`, `SessionWindow5hEnd` (all `pgtype.Timestamptz`), already SELECTed+scanned by
  GetAdminProviderAccountHealth. The `tsAny(pgtype.Timestamptz) any` helper (tools_health.go:115) already
  nil-guards invalid timestamps.

## Blast radius (verified contained)
- The summary map is built inline inside `AccountHealthDiagnoseSpec` only — no other tool shares it. No
  OpenAPI (hermes tools are an internal registry). Existing `TestAccountHealthDiagnoseShape` asserts
  specific keys (health_state + error class), not an exact key set → adding keys does not break it.

## #16 triple-mirror (real source cites)
- sub2api `backend/internal/handler/api_key_handler.go:42,59` + `admin/apikey_handler.go:28` — models a
  rolling 5-hour rate-limit usage window with reset semantics: precedent for the 5h session-window concept.
- CLIProxyAPI `sdk/cliproxy/auth/types.go:84-87` (struct) consumed in the live refresh scheduler
  `sdk/cliproxy/auth/auto_refresh_loop.go:351-362` — tracks an account's last-refreshed and
  next-refresh-due timestamps (a recovery/retry ETA); its Quotio companion exposes per-account 5-hour
  quota bars (README:193): precedent for both recovery ETA and the 5h window.
- new-api `model/channel.go:67` — tracks per-key disable timestamps for channel auto-recovery: precedent
  for recovery timing (no 5h window).
- **HUAKAI delta (生态/ecosystem)**: surfaces the health-state expiry (recovery ETA) AND the 5h window
  start/end together in one read-only, RBAC-gated, tenant-scoped diagnostic correlation — operator sees
  "degraded until T, 5h window S→E" without secrets or raw bodies.

## Changes
1. `tools_health.go` — add `health_state_until` (after health_state), `session_window_5h_start` and
   `session_window_5h_end` (beside session_window_5h_status) to the summary map, each via `tsAny(...)`.
2. `tools_test.go` — discriminating test: seed the row with distinct valid HealthStateUntil / window
   start / window end, assert all three surface (type-assert to time.Time + Equal); mutation reds it.

## Success criteria
- build + vet clean; codebudget green; hermesops tests green (-count=1).
- New projection test passes; mutation (drop a key) goes RED, verified -count=1.
- Diagnostic-only invariant intact: no secrets/raw bodies; these are state-machine timestamps.

## Blast radius summary
Single non-collision package (`hermesops`; not in proxies avoidance list; no other active hermes branch),
one inline projection used only by account_health_diagnose, plus its test. Zero db/schema/money/auth.

## Owner decision points
None — additive read-only diagnostic timestamps on an RBAC-gated ops tool, no gated risk class.
