# Gap Design: Ops Suite — Alert Rules + Proactive Monitor + Scheduled Tests

**Gap ID:** ops-suite  
**Status:** Design — ready for implementation  
**Author:** Senior HUAKAI Backend Architect  
**Date:** 2026-06-03

---

## Summary

HUAKAI's `channelhealth/` package is purely reactive: it fires `Alert` structs
when real-traffic signals cross thresholds and stores them via `Store.AppendAlert`.
Three closely related capabilities are missing:

1. **Alert rules / events / silences** — operator-defined threshold rules that
   evaluate aggregated metrics windows; fire `ops_alert_events` rows; support
   cooldown so the same rule does not re-fire immediately; send notification
   email via the existing `email` stack; scoped silences suppress firing.

2. **Proactive synthetic channel monitor** — independent credentials + endpoint +
   model probe running on a per-monitor interval; stores pass/fail/latency
   history; computes rolling 7-day availability rollup; completely separate from
   real-traffic state so it can detect "upstream is down" before real traffic
   arrives.

3. **Cron-scheduled account test plans** — a platform admin schedules a cron
   expression against a provider account; the runner fires `RunTestBackground`
   (using the existing `adminhttp.ProviderAccountCredentialTester`), stores
   results, prunes history to `max_results`, and — when `auto_recover=true` —
   clears the account's cooling-down / error state on success via the
   `channelhealth.Service`.

All three are new packages; no frozen package (`gatewayhttp`, `gateway`,
`proto`) is touched. Admin HTTP handlers live in a new package
`internal/opshttp` that is wired into `cmd/gateway/routes.go`'s
`mountAdminRoutes` function (an existing, non-frozen file).

---

## Package layout

Each section lists every planned file, its single responsibility, and a line
estimate.  All estimates are comfortably under 500 lines; files projected near
the limit are split proactively.

### `internal/opsalert` — alert rules, events, silences

| File | Responsibility | Est. lines |
|------|---------------|-----------|
| `types.go` | Domain types: `AlertRule`, `AlertEvent`, `AlertSilence`, `AlertEventFilter`; status/severity constants; `Store` interface | 130 |
| `service.go` | `Service`: `CreateRule`, `UpdateRule`, `DeleteRule`, `ListRules`; `FireEvent` (cooldown + silence check, email dispatch, store write); `CreateSilence`, `ListSilences`, `IsSilenced` | 280 |
| `store_postgres.go` | `PostgresStore`: sqlc-backed implementations of every `Store` method; no business logic | 380 |
| `store_memory.go` | In-memory `Store` for unit tests; thread-safe | 160 |
| `service_test.go` | Discriminating unit tests using in-memory store | 240 |
| `store_postgres_test.go` | Integration tests against real DB (build tag `integration`) | 180 |

**Total new files in `opsalert`: 6 — all under 500 lines.**

### `internal/syntheticmonitor` — proactive channel probe

| File | Responsibility | Est. lines |
|------|---------------|-----------|
| `types.go` | `Monitor`, `CheckResult`, `HistoryEntry`, `AvailabilityRollup`; status constants (`operational`, `degraded`, `failed`, `error`); `Store` interface | 150 |
| `checker.go` | `runCheck(ctx, monitor)` — builds HTTP request per provider adapter (openai/anthropic/gemini), sends with SSRF-safe client (private-IP dial blocked), parses response, returns `CheckResult`; credentials read from monitor struct, never logged | 390 |
| `service.go` | `Service`: CRUD for monitors (encrypt API key via `credentialstore.EncryptSecret`); `RunCheck` (single on-demand check + history write); `BatchAvailability7d` | 320 |
| `runner.go` | `Runner`: goroutine-per-interval scheduler; `Start`/`Stop`; per-monitor ticker based on `interval_seconds`; `sync.Once` safety | 180 |
| `store_postgres.go` | `PostgresStore`: all `Store` methods; history insert + pruning + availability rollup query | 420 |
| `store_memory.go` | In-memory `Store` for tests | 180 |
| `service_test.go` | Discriminating unit tests | 220 |

**Total new files in `syntheticmonitor`: 7 — all under 500 lines.**

### `internal/schedtest` — scheduled account test plans

| File | Responsibility | Est. lines |
|------|---------------|-----------|
| `types.go` | `Plan`, `Result`; `PlanStore`, `ResultStore` interfaces; cron parser constant; `Tester` interface (one method: `RunBackground(ctx, credentialID, modelID) (*Result, error)`) | 120 |
| `service.go` | `Service`: `CreatePlan` (validate cron, compute `next_run_at`), `UpdatePlan`, `DeletePlan`, `GetPlan`, `ListByCredential`, `ListResults`, `SaveResult` (insert + prune) | 230 |
| `runner.go` | `Runner`: cron tick every minute; `ListDue` → bounded semaphore (10 workers) → `runOnePlan` → `SaveResult` + `UpdateAfterRun` + optional `AutoRecover`; `Start`/`Stop` with `sync.Once` | 210 |
| `recover.go` | `AutoRecoverAdapter`: wraps `channelhealth.Service.ManualResume`; called by runner on `status=success && auto_recover=true`; single-responsibility adapter so runner has no direct `channelhealth` import | 60 |
| `store_postgres.go` | `PostgresStore`: all `PlanStore` + `ResultStore` methods | 320 |
| `store_memory.go` | In-memory stores for tests | 160 |
| `service_test.go` | Discriminating service unit tests | 200 |
| `runner_test.go` | Discriminating runner unit tests (fake clock, fake tester) | 180 |

**Total new files in `schedtest`: 8 — all under 500 lines.**

### `internal/opshttp` — admin HTTP layer (chi, not gin)

| File | Responsibility | Est. lines |
|------|---------------|-----------|
| `alert_rules_handler.go` | `MountAlertRulesRoutes(r, Deps)`: GET/POST `/v1/admin/ops/alert-rules`, PUT/DELETE `/v1/admin/ops/alert-rules/{id}`; validates metric_type/operator/severity from allowlists | 280 |
| `alert_events_handler.go` | `MountAlertEventsRoutes(r, Deps)`: GET `/v1/admin/ops/alert-events` (cursor pagination, filter by status/severity/rule); GET/PUT `/v1/admin/ops/alert-events/{id}`; PUT `/{id}/status` | 260 |
| `alert_silences_handler.go` | `MountAlertSilencesRoutes(r, Deps)`: POST/GET `/v1/admin/ops/alert-silences`; parses `until` as RFC3339 | 160 |
| `syntheticmonitor_handler.go` | `MountSyntheticMonitorRoutes(r, Deps)`: full CRUD + `POST /{id}/run` + `GET /{id}/history`; masks API key in responses (first 4 chars + `***`) | 380 |
| `schedtest_handler.go` | `MountSchedTestRoutes(r, Deps)`: CRUD for plans (`GET /v1/admin/accounts/{account_id}/scheduled-test-plans`, `POST/PUT/DELETE /v1/admin/scheduled-test-plans/...`) + `GET /{id}/results` | 300 |
| `deps.go` | `Deps` struct aggregating all service interfaces; `AdminAuth` interface; shared `writeJSON`/`writeError` helpers; `parseID` | 100 |
| `handler_test.go` | Shared test helpers (stub auth, in-memory services) | 80 |

**Total new files in `opshttp`: 7 — all under 500 lines.**

### Migrations (new numbered files only)

| Number | File | Description |
|--------|------|-------------|
| 0077 | `0077_ops_alert_rules.up.sql` / `.down.sql` | `ops_alert_rules`, `ops_alert_events`, `ops_alert_silences` tables + indexes + seed rules |
| 0078 | `0078_synthetic_monitor.up.sql` / `.down.sql` | `synthetic_monitors`, `synthetic_monitor_history` tables + indexes |
| 0079 | `0079_schedtest_plans.up.sql` / `.down.sql` | `schedtest_plans`, `schedtest_results` tables + indexes |

---

## Schema / migrations

### Migration 0077 — ops_alert_rules

```sql
-- 0077_ops_alert_rules.up.sql
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

CREATE TABLE IF NOT EXISTS ops_alert_rules (
    id               BIGSERIAL PRIMARY KEY,
    name             VARCHAR(128) NOT NULL,
    description      TEXT,
    enabled          BOOLEAN NOT NULL DEFAULT true,
    severity         VARCHAR(8)  NOT NULL DEFAULT 'P2',  -- P0/P1/P2/P3
    metric_type      VARCHAR(64) NOT NULL,               -- allowlisted enum
    operator         VARCHAR(4)  NOT NULL,               -- > < >= <= == !=
    threshold        DOUBLE PRECISION NOT NULL,
    window_minutes   INT NOT NULL DEFAULT 5,
    sustained_minutes INT NOT NULL DEFAULT 5,
    cooldown_minutes INT NOT NULL DEFAULT 20,
    notify_email     BOOLEAN NOT NULL DEFAULT true,
    filters          JSONB,
    last_triggered_at TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ops_alert_rules_name
    ON ops_alert_rules (name);
CREATE INDEX IF NOT EXISTS idx_ops_alert_rules_enabled
    ON ops_alert_rules (enabled);

CREATE TABLE IF NOT EXISTS ops_alert_events (
    id              BIGSERIAL PRIMARY KEY,
    rule_id         BIGINT REFERENCES ops_alert_rules(id) ON DELETE SET NULL,
    severity        VARCHAR(8)  NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'firing', -- firing/resolved/manual_resolved
    title           VARCHAR(200),
    description     TEXT,
    metric_value    DOUBLE PRECISION,
    threshold_value DOUBLE PRECISION,
    dimensions      JSONB,
    fired_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ,
    email_sent      BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ops_alert_events_rule_status
    ON ops_alert_events (rule_id, status);
CREATE INDEX IF NOT EXISTS idx_ops_alert_events_fired_at
    ON ops_alert_events (fired_at DESC);

CREATE TABLE IF NOT EXISTS ops_alert_silences (
    id         BIGSERIAL PRIMARY KEY,
    rule_id    BIGINT NOT NULL,
    platform   VARCHAR(64) NOT NULL DEFAULT '',
    group_id   BIGINT,
    region     VARCHAR(64),
    until      TIMESTAMPTZ NOT NULL,
    reason     TEXT,
    created_by BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_ops_alert_silences_lookup
    ON ops_alert_silences (rule_id, until);

-- Seed default alert rules (idempotent via ON CONFLICT)
INSERT INTO ops_alert_rules (name, description, enabled, metric_type, operator,
    threshold, window_minutes, sustained_minutes, severity, notify_email, cooldown_minutes)
VALUES
  ('error_rate_high',   'Error rate > 5% for 5 min',  true, 'error_rate',    '>', 5.0,  5, 5,  'P1', true, 20),
  ('success_rate_low',  'Success rate < 95% for 5 min',true, 'success_rate', '<', 95.0, 5, 5,  'P0', true, 15),
  ('p99_latency_high',  'P99 latency > 30000ms',       true, 'p99_latency_ms','>',30000.0,5,10,'P2', true, 30),
  ('error_rate_critical','Error rate > 20% for 1 min', true, 'error_rate',   '>', 20.0, 1, 1,  'P0', true, 10)
ON CONFLICT (name) DO NOTHING;
```

### Migration 0078 — synthetic_monitor

```sql
-- 0078_synthetic_monitor.up.sql
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

CREATE TABLE IF NOT EXISTS synthetic_monitors (
    id               BIGSERIAL PRIMARY KEY,
    name             VARCHAR(128) NOT NULL,
    provider         VARCHAR(32)  NOT NULL, -- openai / anthropic / gemini
    endpoint         VARCHAR(500) NOT NULL,
    api_key_encrypted TEXT NOT NULL,        -- AES-GCM via credentialstore.EncryptSecret
    primary_model    VARCHAR(200) NOT NULL,
    extra_models     TEXT[] NOT NULL DEFAULT '{}',
    group_name       VARCHAR(100) NOT NULL DEFAULT '',
    enabled          BOOLEAN NOT NULL DEFAULT true,
    interval_seconds INT NOT NULL DEFAULT 300,
    last_checked_at  TIMESTAMPTZ,
    created_by       BIGINT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_synthetic_monitors_enabled
    ON synthetic_monitors (enabled);

CREATE TABLE IF NOT EXISTS synthetic_monitor_history (
    id          BIGSERIAL PRIMARY KEY,
    monitor_id  BIGINT NOT NULL REFERENCES synthetic_monitors(id) ON DELETE CASCADE,
    model       VARCHAR(200) NOT NULL,
    status      VARCHAR(16) NOT NULL,  -- operational/degraded/failed/error
    latency_ms  INT,
    message     TEXT NOT NULL DEFAULT '',
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_smh_monitor_model_checked
    ON synthetic_monitor_history (monitor_id, model, checked_at DESC);
CREATE INDEX IF NOT EXISTS idx_smh_checked_at
    ON synthetic_monitor_history (checked_at DESC);
```

### Migration 0079 — schedtest_plans

```sql
-- 0079_schedtest_plans.up.sql
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

CREATE TABLE IF NOT EXISTS schedtest_plans (
    id                BIGSERIAL PRIMARY KEY,
    -- References the provider account credential, not a user; tenant-scoped.
    -- account_credential_id from the pool / credential store, bigint FK best-effort.
    account_credential_id BIGINT NOT NULL,
    model_id          VARCHAR(100) NOT NULL DEFAULT '',
    cron_expression   VARCHAR(100) NOT NULL DEFAULT '*/30 * * * *',
    enabled           BOOLEAN NOT NULL DEFAULT true,
    max_results       INT NOT NULL DEFAULT 50,
    auto_recover      BOOLEAN NOT NULL DEFAULT false,
    last_run_at       TIMESTAMPTZ,
    next_run_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_stp_credential
    ON schedtest_plans (account_credential_id);
CREATE INDEX IF NOT EXISTS idx_stp_enabled_next_run
    ON schedtest_plans (enabled, next_run_at)
    WHERE enabled = true;

CREATE TABLE IF NOT EXISTS schedtest_results (
    id            BIGSERIAL PRIMARY KEY,
    plan_id       BIGINT NOT NULL REFERENCES schedtest_plans(id) ON DELETE CASCADE,
    status        VARCHAR(20) NOT NULL DEFAULT 'success',  -- success/failed
    response_text TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    latency_ms    BIGINT NOT NULL DEFAULT 0,
    started_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_str_plan_created
    ON schedtest_results (plan_id, created_at DESC);
```

---

## Endpoints

All new endpoints live under `/v1/admin/ops/...` and `admin/v1/...` prefixes,
requiring `platform_admin` auth (same `AdminAuth.Resolve` pattern used by
every existing `adminhttp` handler). The router writes nothing; handlers
are read-only toward credentials.

### Alert rules

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET`  | `/v1/admin/ops/alert-rules` | platform_admin | List all rules |
| `POST` | `/v1/admin/ops/alert-rules` | platform_admin | Create; validate metric_type/operator/severity against allowlists |
| `PUT`  | `/v1/admin/ops/alert-rules/{id}` | platform_admin | Full replace update |
| `DELETE` | `/v1/admin/ops/alert-rules/{id}` | platform_admin | Hard delete |

### Alert events

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET`  | `/v1/admin/ops/alert-events` | platform_admin | Cursor pagination (`before_fired_at` + `before_id`); filter `status`, `severity`, `rule_id` |
| `GET`  | `/v1/admin/ops/alert-events/{id}` | platform_admin | Single event |
| `PUT`  | `/v1/admin/ops/alert-events/{id}/status` | platform_admin | Set `resolved` or `manual_resolved`; writes `resolved_at` |

### Alert silences

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `POST` | `/v1/admin/ops/alert-silences` | platform_admin | Body: `rule_id`, `until` (RFC3339), `reason` |
| `GET`  | `/v1/admin/ops/alert-silences` | platform_admin | List active (until > now) |
| `DELETE` | `/v1/admin/ops/alert-silences/{id}` | platform_admin | Expire immediately |

### Synthetic monitor

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET`  | `/v1/admin/ops/synthetic-monitors` | platform_admin | Paginated list; `api_key` masked in response |
| `POST` | `/v1/admin/ops/synthetic-monitors` | platform_admin | Creates monitor; API key encrypted before storage |
| `GET`  | `/v1/admin/ops/synthetic-monitors/{id}` | platform_admin | Single monitor; masked key |
| `PUT`  | `/v1/admin/ops/synthetic-monitors/{id}` | platform_admin | Update; empty `api_key` = keep existing |
| `DELETE` | `/v1/admin/ops/synthetic-monitors/{id}` | platform_admin | Hard delete |
| `POST` | `/v1/admin/ops/synthetic-monitors/{id}/run` | platform_admin | On-demand probe; returns `[]CheckResult` |
| `GET`  | `/v1/admin/ops/synthetic-monitors/{id}/history` | platform_admin | Recent history; `?model=` filter; `?limit=` (max 200) |

### Scheduled test plans

| Method | Path | Scope | Notes |
|--------|------|-------|-------|
| `GET`  | `/v1/admin/provider-accounts/{account_id}/schedtest-plans` | platform_admin | Plans for one credential |
| `POST` | `/v1/admin/schedtest-plans` | platform_admin | Create; validates cron via `robfig/cron`; computes `next_run_at` |
| `PUT`  | `/v1/admin/schedtest-plans/{id}` | platform_admin | Patch-style; recomputes `next_run_at` |
| `DELETE` | `/v1/admin/schedtest-plans/{id}` | platform_admin | Hard delete (cascades results) |
| `GET`  | `/v1/admin/schedtest-plans/{id}/results` | platform_admin | Recent results; `?limit=` (max 200) |

---

## Invariants honored

**CMB credential invariant.**  
`syntheticmonitor/checker.go` fetches `api_key_encrypted` from the `Store`,
decrypts it in memory, uses it in the HTTP `Authorization` header, and drops it.
The decrypted value is never logged, never stored in any struct field that
outlives the `runCheck` call, and never returned to handlers. `opshttp` handler
response DTOs expose only `api_key_masked` (first 4 bytes + `***`).

`schedtest/runner.go` calls `Tester.RunBackground` which is backed by
`adminhttp.ProviderAccountCredentialTester` — an existing package that already
honors the no-log-credential rule and fetches credentials from `credentialStore`
directly. The runner receives only `account_credential_id`; it never holds a
credential value.

**Router writes nothing / reads nothing sensitive.**  
`internal/opshttp` handlers read request bodies and write JSON responses.
They hold no credential material; all business logic is in `opsalert`,
`syntheticmonitor`, `schedtest`.

**Fail-closed on ambiguity.**  
`opsalert/service.go`: if `IsSilenced` returns an error, `FireEvent` treats
the event as NOT silenced (fires, does not suppress). If email dispatch fails,
the event is still persisted; email failure is logged, not propagated to the
caller as a fatal error, preventing alert loss.

`syntheticmonitor/checker.go`: any HTTP error, TLS error, non-2xx status, or
parse failure maps to `status=error`. A missing or empty response body maps to
`status=failed`. The check never silently succeeds on ambiguity.

`schedtest/runner.go`: any error from `RunBackground` causes the result to be
stored as `status=failed`. `auto_recover` is only triggered on explicit
`status=success`. `UpdateAfterRun` is called regardless of result status so
`next_run_at` advances and avoids a stuck plan.

**Money paths untouched.**  
No new code touches `billing`, `balancehold`, `usercostreceipts`, `audit`,
or `auditledger`. No new migrations alter those tables.

**Schema changes in new numbered migrations only.**  
Migrations 0077, 0078, 0079 — next sequential after confirmed max 0076.

**Modularity.**  
Three separate packages, not one god-package. Each file has a single
responsibility. No file exceeds 420 estimated lines; the 500-line hard limit is
met with margin.

---

## Discriminating tests

Each test is discriminating: it is written to fail specifically if the defect it
defends is introduced.

### opsalert

| Test | Defect it catches |
|------|------------------|
| `TestFireEvent_CooldownSuppresses` — fire rule, advance clock by less than cooldown, fire again, assert second event NOT inserted | Removing cooldown check → would insert duplicate event |
| `TestFireEvent_SilenceActive_Suppresses` — create silence until T+1h, fire rule, assert no event inserted | Removing `IsSilenced` check → event fires through silence |
| `TestFireEvent_SilenceExpired_Fires` — create silence until T-1s, fire rule, assert event IS inserted | Off-by-one on silence expiry check |
| `TestFireEvent_EmailDispatch_CalledOnce` — mock email sender, fire one event, assert `Send` called exactly once with non-empty `To` | Email dispatch wired incorrectly (called 0 or 2 times) |
| `TestFireEvent_EmailFailure_EventStillPersisted` — mock email sender returns error, assert event row still present in store | Fatal propagation of email error loses event |
| `TestValidateRule_InvalidMetricType_Rejected` — POST with metric_type `"backdoor"`, assert 400 | Missing allowlist check lets arbitrary metric types through |
| `TestValidateRule_ThresholdInfinity_Rejected` — POST with `threshold: Inf`, assert 400 | Missing NaN/Inf guard |
| `TestSilenceCheck_ActiveSilence_MatchesRule` — exact rule_id match returns true | Silence lookup ignores rule_id |

### syntheticmonitor

| Test | Defect it catches |
|------|------------------|
| `TestRunCheck_Non2xx_ReturnsError` — mock HTTP returning 401, assert `status=error` | Treating non-2xx as success |
| `TestRunCheck_BodyEmpty_ReturnsFailed` — mock HTTP returning 200 + empty body, assert `status=failed` | Empty-body silence |
| `TestRunCheck_PrivateIPBlocked` — attempt check against `http://192.168.1.1`, assert error before HTTP dial | SSRF-safe dial not wired |
| `TestRunCheck_APIKeyNeverLogged` — check that `checker.go` calls no logging with the decrypted key string (verified via log capture) | Accidentally logging `apiKey` variable |
| `TestAPIKeyMasked_InHandlerResponse` — create monitor with key `"sk-abcdefgh"`, GET handler, assert response `api_key_masked == "sk-a***"` | Handler serialises raw key |
| `TestAvailability7d_AllSuccess_Returns100` — insert 48 history rows `status=operational`, assert `AvailabilityPct==100.0` | Off-by-one in rollup denominator |
| `TestAvailability7d_HalfFailed_Returns50` — 24 operational + 24 error, assert `~50.0` | Counting failed as operational |
| `TestRunner_RespectIntervalSeconds` — start runner with `interval_seconds=2`, sleep 3s, assert check called at least once | Scheduler never ticks |

### schedtest

| Test | Defect it catches |
|------|------------------|
| `TestCreatePlan_InvalidCron_Rejected` — cron `"not a cron"`, assert error returned, no row inserted | Missing cron validation |
| `TestCreatePlan_ComputesNextRunAt` — valid cron `"0 * * * *"`, assert `NextRunAt` is non-nil and in future | `next_run_at` left nil |
| `TestRunner_ListDue_ExecutesPlan` — insert plan with `next_run_at = now-1s`, tick runner, assert result row inserted | Runner never queries due plans |
| `TestRunner_AutoRecover_CalledOnSuccess` — insert plan with `auto_recover=true`, mock tester returns `success`, assert `ManualResume` called on `channelhealth.Service` | `auto_recover` never wires to `ManualResume` |
| `TestRunner_AutoRecover_NotCalledOnFailure` — insert plan with `auto_recover=true`, tester returns `failed`, assert `ManualResume` NOT called | Auto-recover fires on failure |
| `TestSaveResult_PrunesOldRows` — create plan with `max_results=3`, insert 5 results via `SaveResult`, query DB, assert only 3 rows remain | Pruning never runs |
| `TestRunner_UpdateAfterRun_AlwaysCalled` — tester returns error, assert `UpdateAfterRun` still called (plan not stuck) | `UpdateAfterRun` inside error branch |
| `TestScheduledTestHandler_Delete_RemovesCascade` — delete plan, assert no orphan result rows | Missing `ON DELETE CASCADE` |

---

## Parity-or-better vs reference

### Alert rules / events / silences

| Behavior | Reference path:line | HUAKAI design |
|----------|--------------------|--------------------|
| Allowlisted `metric_type` (13 values) | `reference-src/sub2api/backend/internal/handler/admin/ops_alerts_handler.go:19-41` | `opsalert/service.go` validates same set; `opshttp/alert_rules_handler.go` re-validates at handler boundary |
| Allowlisted operators `> < >= <= == !=` | `ops_alerts_handler.go:43` | Same 6-value set in `opsalert/types.go` |
| Cooldown check on `last_triggered_at` | `033_ops_monitoring_vnext.sql` `cooldown_minutes` column used by reference service | `opsalert/service.go FireEvent` compares `time.Now()` vs `rule.LastTriggeredAt + cooldownDuration` before inserting |
| Scoped silences (`rule_id + platform + group_id + until`) | `037_ops_alert_silences.sql:5-17` | Migration 0077 `ops_alert_silences` table; `service.IsSilenced` queries `rule_id, until > now` |
| `notify_email` per rule + email dispatch | `033_ops_monitoring_vnext.sql:599` `notify_email` column | `opsalert/service.go` calls `email.EmailSender.Send` after inserting event when `rule.NotifyEmail=true` |
| Status lifecycle `firing → resolved / manual_resolved` | `ops_alerts_handler.go:441` | `opshttp/alert_events_handler.go PUT /{id}/status` with same two valid statuses |
| Cursor pagination (`before_fired_at` + `before_id`) | `ops_alerts_handler.go:556-581` | `opshttp/alert_events_handler.go` same two-param cursor; both required or both absent |
| Seed default alert rules | `033_ops_monitoring_vnext.sql:621-707` | Migration 0077 seeds 4 canonical rules covering P0/P1/P2 |

**HUAKAI is parity-or-better:** the reference uses Gin `ShouldBindBodyWith` +
second `ShouldBindBodyWith` call to parse the same body twice (lines 270-296),
a Gin-specific workaround that would be architecturally unsound in chi. HUAKAI
validates via a single-pass `validateAlertRuleInput(r *http.Request)` that
decodes once into a `map[string]json.RawMessage`, providing cleaner separation.

### Synthetic channel monitor

| Behavior | Reference path:line | HUAKAI design |
|----------|--------------------|--------------------|
| Independent credentials (not pool creds) | `channel_monitor_types.go:24-28` APIKey stored encrypted | `synthetic_monitors.api_key_encrypted` column; decrypted only inside `checker.go` |
| Provider adapters: openai / anthropic / gemini | `channel_monitor_checker.go:56-67` `callProvider` dispatch | `syntheticmonitor/checker.go` same three-provider dispatch |
| SSRF-safe HTTP client (private-IP blocked) | `channel_monitor_ssrf.go` `safeDialContext` | `syntheticmonitor/checker.go` uses same pattern: custom `DialContext` that resolves then validates IP range |
| Availability rollup (7d) | `channel_monitor_aggregator.go` | `syntheticmonitor/store_postgres.go BatchAvailability7d` SQL window query |
| API key masked in responses | `channel_monitor_handler.go:118-123` `maskAPIKey` | `opshttp/syntheticmonitor_handler.go` same first-4 + `***` mask |
| Batch N+1 avoidance in list | `channel_monitor_handler.go:244-255` `batchSummaryFor` | `opshttp/syntheticmonitor_handler.go` single `BatchAvailability7d` call for page of IDs |
| `interval_seconds` min 15 max 3600 | `channel_monitor_handler.go:44` binding tag | `opshttp/syntheticmonitor_handler.go` validates range with explicit error |

**HUAKAI is better:** the reference `MonitorBodyOverrideMode` (merge/replace/off)
adds complexity that is out of scope for this gap (the reference itself documents
it as advanced). HUAKAI defers body-override to a follow-on gap, keeping
`checker.go` under 400 lines and discriminating.

### Scheduled test plans

| Behavior | Reference path:line | HUAKAI design |
|----------|--------------------|--------------------|
| Cron validation via `robfig/cron` | `scheduled_test_service.go:11-12` | `schedtest/service.go computeNextRun` same parser (`Minute\|Hour\|Dom\|Month\|Dow`) |
| `next_run_at` computed on create + update | `scheduled_test_service.go:32-36`, `56-60` | Same in `schedtest/service.go CreatePlan` and `UpdatePlan` |
| Runner: 1-minute tick, 10-second delay, bounded semaphore (10) | `scheduled_test_runner_service.go:59,89,107` | `schedtest/runner.go` exact same pattern |
| `UpdateAfterRun` always called even on error | `scheduled_test_runner_service.go:144` outside error branch | Same in `schedtest/runner.go runOnePlan` |
| Auto-recover on success clears channelhealth state | `scheduled_test_runner_service.go:133-136`, `tryRecoverAccount:150` | `schedtest/recover.go AutoRecoverAdapter` wraps `channelhealth.Service.ManualResume` |
| `max_results` prune old entries | `scheduled_test_service.go:85-86` | `schedtest/service.go SaveResult` calls `ResultStore.PruneOldResults` |

**HUAKAI is better on modularity:** the reference `tryRecoverAccount` directly
imports `RateLimitService` from the same package. HUAKAI isolates recovery
in `recover.go` with a `RecoveryPort` interface, making `runner.go` testable
without any `channelhealth` or rate-limit dependency.

---

## Effort

**L** (Large)

Three independent packages totalling ~28 new Go files, 3 new migrations, and
integration into `cmd/gateway/routes.go`. Each package is self-contained and
can be implemented and reviewed in parallel by separate workers. The largest
single file (`opsalert/store_postgres.go`, ~380 lines) requires careful sqlc
query design against the new schema.

Breakdown:
- Migration authoring + sqlc query definitions: ~1 day
- `opsalert` package (service + stores + tests): ~1.5 days
- `syntheticmonitor` package (checker + runner + stores + tests): ~2 days
- `schedtest` package (service + runner + recover + stores + tests): ~1.5 days
- `opshttp` handlers + wiring into `routes.go`: ~1 day
- Integration tests + review gate: ~0.5 day

**Total: ~7.5 engineer-days** (parallelisable to ~4 calendar days with 2 workers).

---

## Risks

| Risk | Likelihood | Severity | Mitigation |
|------|-----------|----------|------------|
| `robfig/cron` not yet in `go.mod` | Medium | Low | Add as direct dependency; version-lock; existing reference uses it |
| Synthetic checker SSRF: DNS rebinding bypasses IP check on dial | Medium | High | Resolve hostname to IP before dial AND validate IP in custom `DialContext`; same pattern as `channel_monitor_ssrf.go` |
| Alert email flooding: cooldown logic has a race between two concurrent firings | Low-Medium | Medium | Wrap the `check + update last_triggered_at` in a single SQL `UPDATE ... WHERE last_triggered_at < NOW() - interval 'N minutes' RETURNING id`; only the winner fires email |
| `schedtest` runner and `syntheticmonitor` runner both hold goroutines at server shutdown | Low | Medium | Both implement `Stop()` with `sync.Once` + `context.WithTimeout(5s)` drain; wire into `lifecycle.go` `OnShutdown` hook |
| Migration 0077 seed rules conflict on re-run | Low | Low | `ON CONFLICT (name) DO NOTHING` is idempotent |
| `schedtest` `account_credential_id` FK: credential deleted while plan active | Low | Low | No hard FK (avoids write amplification); runner skips plan if `Tester.RunBackground` returns `credential not found`; result stored as `failed` |
| `syntheticmonitor` history table grows unboundedly | Medium | Medium | `Runner` calls `store.DeleteHistoryBefore(now - 30*24h)` once per hour; configurable retention constant |
