# Critique: Gap Design ops-suite

**Reviewer:** Adversarial Principal Review  
**Date:** 2026-06-03  
**Design file:** `docs/process/gap-designs/ops-suite.md`  
**Status:** needs-work

---

## Verdict

**needs-work**

The design is structurally sound on separation of concerns, migration numbering,
and high-level modularity. It contains no money-path or CMB violations of a
catastrophic kind. However it has **seven must-fix defects** before a worker
should touch a single line: one phantom API that does not exist in the codebase,
one missing `go.mod` dependency that the design mislabels as a "Low" risk, one
fail-open race on cooldown that the design acknowledges but the proposed
mitigation is incomplete, one under-specified wiring path for both new goroutine
runners that contradicts how `lifecycle.go` works, one silence-table FK
omission that creates a dangling-reference class of bug, and two discriminating-
test gaps that would not actually catch the defects they claim to catch.

---

## Holes

### H-1 `credentialstore.EncryptSecret` does not exist

The design repeatedly references `credentialstore.EncryptSecret` (e.g.,
`syntheticmonitor/service.go`: "encrypt API key via
`credentialstore.EncryptSecret`"). That function does not exist in the codebase.

The real API is `credentialstore.Cipher.Encrypt(ctx, plaintext []byte, aad AAD)
(Envelope, error)` — a method on a `*Cipher` struct that requires a
`KeyProvider` and a caller-supplied `AAD` struct (which encodes
`TenantID/ProviderAccountID/Vendor/AuthMode/Version/KeyID`). It also returns an
`Envelope` struct (not a `string`), and the HMAC-bound AAD is **required** for
correct decryption later.

Consequence: `syntheticmonitor/service.go` and `store_postgres.go` as designed
cannot be implemented. A worker will either: (a) invent their own raw-AES
encryption bypassing the project's envelope format, or (b) store the plaintext
key base64-encoded and call it "encrypted". Both outcomes are security failures.

**Fix:** The design must specify the full encryption contract: what AAD fields
will be used for synthetic-monitor keys (the monitor has no `TenantID` scoping
in the current schema — see H-4), what the `Envelope` serialisation format is in
`api_key_encrypted TEXT`, and that `*credentialstore.Cipher` (not a free
function) must be injected into `syntheticmonitor.Service` via its constructor.
The `store_postgres.go` line estimate of 420 lines is also likely to grow once
the actual serialisation/deserialisation of the envelope is included.

---

### H-2 `robfig/cron` is not in `go.mod` — but the design calls this "Low" severity

`go.mod` has exactly eight direct dependencies. `github.com/robfig/cron/v3` is
not among them and is not in `go.sum`. Adding a new third-party cron library is
not a zero-cost "add as direct dependency" step: it must pass the project's
dependency-license review and must be declared with a pinned version. The design
labels this "Medium likelihood / Low severity". Missing a required dependency is
actually a **build-blocking** defect. Until the dependency is approved and added,
`schedtest` cannot compile.

**Fix:** Either (a) vendor `robfig/cron/v3` through the normal dependency gate
before implementation starts, or (b) re-design the cron parsing to use the
standard library `time.Parse` with a lightweight custom 5-field parser, avoiding
a new dependency entirely. Option (b) keeps the project's minimal-deps discipline
and removes the risk entirely. The design must resolve this before dispatch.

---

### H-3 Cooldown race — proposed mitigation is in the Risk table but NOT in the service spec

Section "Invariants honored" states `FireEvent` compares `time.Now()` vs
`rule.LastTriggeredAt + cooldownDuration`. Section "Risks" calls out the
concurrent-fire race and proposes the correct fix (`UPDATE ... WHERE
last_triggered_at < NOW() - interval '...' RETURNING id`). But the service spec
(`opsalert/service.go` description) still says the check is done in Go code,
which is inherently racy under concurrent alert evaluation.

The test `TestFireEvent_CooldownSuppresses` uses an **in-memory** store with a
single goroutine, so it will pass even if the race exists. It does not catch the
actual defect (concurrent DB writes bypassing the cooldown).

**Fix:** The design must upgrade the risk-table mitigation into the normative
spec for `store_postgres.go` (`PostgresStore.FireEventIfCooldownElapsed` is the
correct layer). The service layer does the logic; the store layer executes an
atomic `UPDATE ... RETURNING` so only one concurrent winner proceeds. The
discriminating test must use two goroutines hitting a real DB or a mock that
rejects the second update.

---

### H-4 `synthetic_monitors` has no `tenant_id` column

Every other table in the project scopes sensitive data to `tenant_id`.
`synthetic_monitors` stores an `api_key_encrypted` blob with no tenant scoping.
This creates two problems:

1. **CMB isolation**: the `Cipher.AAD` struct requires `TenantID` for the HMAC
   binding on the key envelope. Without a tenant column the design cannot call
   `Cipher.Encrypt` correctly (connecting back to H-1: the phantom
   `EncryptSecret` hid this because a free function could ignore AAD).

2. **Access isolation**: `opshttp/syntheticmonitor_handler.go` is `platform_admin`
   only, so for now all admins see all monitors. If tenant-operator access is
   ever added (the design defers this as out-of-scope), a missing `tenant_id`
   column means there is no safe upgrade path without a new migration.

**Fix:** Add `tenant_id BIGINT NOT NULL DEFAULT 0` (or a real FK) to
`synthetic_monitors` in migration 0078. Use it as `AAD.TenantID = 0` for
platform-level monitors with a clear code comment explaining why. This costs two
lines in the migration and prevents a future schema trap.

---

### H-5 `ops_alert_silences` has no FK constraint on `rule_id`

`ops_alert_silences.rule_id` is declared `BIGINT NOT NULL` with no `REFERENCES
ops_alert_rules(id)` foreign key. Compare to `ops_alert_events.rule_id` which
correctly has `REFERENCES ops_alert_rules(id) ON DELETE SET NULL`. As a result:

- Deleting a rule leaves orphan silence rows that will silently suppress future
  alerts fired against a rule that happens to reuse the same BIGSERIAL `id`.
  BIGSERIAL IDs are monotonically increasing so reuse within a live system is
  unlikely but not impossible across a drop/re-seed cycle.
- `IsSilenced` will return true for a silence whose `rule_id` points to a
  deleted and re-created rule with the same ID, suppressing valid alerts.

**Fix:** Add `REFERENCES ops_alert_rules(id) ON DELETE CASCADE` to
`ops_alert_silences.rule_id`. This aligns with the reference schema
(`037_ops_alert_silences.sql`) and removes the orphan class of bug.

---

### H-6 Runner goroutines wiring into `lifecycle.go` is unspecified and the real shutdown pattern is different from what the design implies

The design says both runners implement `Start`/`Stop` with `sync.Once` and
"wire into `lifecycle.go` `OnShutdown` hook." There is no `OnShutdown` hook in
`lifecycle.go`. The real pattern (visible in `shutdownGateway`) is:

1. `shutdownGateway` manually calls `Stop(ctx)` on each registered worker.
2. Workers are registered as named fields on `gatewayRuntime` (e.g.,
   `subscriptionExpiryWorker`, `credentialScheduler`).
3. The design says it wires into `cmd/gateway/routes.go`'s `mountAdminRoutes`,
   but workers are started in `wiring.go` / `main.go` and registered on
   `gatewayRuntime`, not in the route-mounting function.

This means the design's wiring story for the two new `Runner` types
(`syntheticmonitor.Runner` and `schedtest.Runner`) is incomplete. They cannot be
started from `mountAdminRoutes` and gracefully drained unless `gatewayRuntime`
is extended and `shutdownGateway` calls `Stop` on them. Without this, the
runners will leak goroutines on server shutdown.

**Fix:** The design must explicitly show that:
1. Both runners are added as fields on `gatewayRuntime` (or a sub-struct).
2. `shutdownGateway` in `lifecycle.go` calls `runner.Stop(ctx)` with an
   independent `context.WithTimeout`.
3. `wiring.go` (not `routes.go`) starts both runners.

---

### H-7 `schedtest.Tester` interface spec mismatches the real `ProviderAccountCredentialTester` signature

The design spec for `schedtest/types.go` defines:

```go
Tester interface { RunBackground(ctx, credentialID, modelID) (*Result, error) }
```

But the real `adminhttp.ProviderAccountCredentialTester` interface (the existing
code the design claims to reuse) is:

```go
TestProviderAccountCredential(ctx context.Context, tenantID, accountID int64, now time.Time) (credentialworker.ProviderAccountCredentialTestResult, error)
```

The real method: (a) takes `tenantID` and `accountID`, not just `credentialID`;
(b) takes a `time.Time now`; (c) returns `ProviderAccountCredentialTestResult`,
not `*schedtest.Result`; (d) is called `TestProviderAccountCredential`, not
`RunBackground`.

The design acknowledges `runner.go` calls `Tester.RunBackground` backed by
`adminhttp.ProviderAccountCredentialTester`, but never shows the adapter that
bridges the two interfaces. Without the adapter, the `Tester` interface and the
existing code are incompatible. The `schedtest` package either needs its own
internal adapter converting the result, or the `Tester` interface must match the
real signature.

**Fix:** Add an explicit `schedtest/tester_adapter.go` to the package layout
that wraps `adminhttp.ProviderAccountCredentialTester` and satisfies the
`schedtest.Tester` interface, converting `ProviderAccountCredentialTestResult`
to `*schedtest.Result`. Account for the fact that `tenantID` is required; the
`Plan` struct must carry `tenant_id` or the adapter must derive it from the
account lookup.

---

## Money/Schema/Auth/CMB risks

**M-1 Money paths: correctly untouched.** Migrations 0077–0079 touch no
`billing`, `balancehold`, `usercostreceipts`, `audit`, or `auditledger` tables.
No new code touches Tx1/Tx2 reserve+settle paths. No `shopspring/decimal`
arithmetic is introduced. No double-charge or refund risk identified.

**M-2 Migration numbering: correct.** Confirmed max is `0076_user_role`.
Migrations 0077, 0078, 0079 are the correct next sequential numbers. Down
migrations are planned but not shown in detail — implementer must ensure each
`.down.sql` only drops objects created by the corresponding `.up.sql` (no
cross-migration drops).

**M-3 CMB credential invariant: partially correct, broken by H-1.** The intent
(encrypt at rest, decrypt in-memory only, never log, mask in API responses) is
correct. The implementation is broken because `credentialstore.EncryptSecret`
does not exist (H-1), which means the actual encryption call will be invented by
the worker.

**M-4 Auth isolation: acceptable for platform-admin scope.** All new endpoints
require `platform_admin` auth via the existing `AdminAuth.Resolve` pattern.
`opshttp/deps.go` holds no credential material. The router reads nothing
sensitive. However, `ops_alert_events.dimensions JSONB` and
`ops_alert_rules.filters JSONB` must be validated to ensure they cannot store
credential material embedded in a payload. The design does not address this.
Recommend a server-side size cap (e.g., 8 KB) and a field-level allowlist in the
service layer before persisting these JSONB columns.

**M-5 No tenant isolation in new ops tables.** `ops_alert_rules`,
`ops_alert_events`, `ops_alert_silences` are platform-level tables with no
`tenant_id` column — this is intentional for platform alerting and is acceptable
given the `platform_admin` auth gate. Document this explicitly in `types.go` to
prevent future confusion.

---

## Parity gaps

**P-1 Silence scope regression.** The reference schema
(`037_ops_alert_silences.sql`) silences are scoped by `rule_id + platform +
group_id + region`. The design includes `platform`, `group_id`, `region` columns
in the schema but `service.IsSilenced` is only specified to query
`rule_id, until > now`. The `platform`/`group_id`/`region` filter columns are
wired in schema but not in the `IsSilenced` query spec. This means a silence
created with `platform=openai` will also suppress alerts for other platforms —
a regression from reference. The design parity table claims parity on silences
but the service spec does not implement dimension-scoped filtering.

**P-2 Reference `MonitorBodyOverrideMode` deferred without feature flag.** The
design defers `MonitorBodyOverrideMode` (merge/replace/off) to a follow-on gap
with no feature flag and no roadmap item. The reference documents this as
"advanced" but it is used in production flows. Deferral is acceptable only if
explicitly added to the gap backlog with a follow-on design ID.

**P-3 Alert event `GET /{id}` handler not in parity table.** The endpoint
`GET /v1/admin/ops/alert-events/{id}` appears in the endpoints table but is
absent from the parity table. Verify the reference has a single-event fetch and
confirm the response shape matches (especially `dimensions JSONB` field name).

---

## Maintainability (god-file check)

No file in the design exceeds 420 estimated lines. The 500-line hard limit is
met. Three packages with clear single-responsibility files. No god-file
violation.

However, two structural observations:

**G-1** `opsalert/service.go` at ~280 lines bundles `CreateRule`, `UpdateRule`,
`DeleteRule`, `ListRules`, `FireEvent`, `CreateSilence`, `ListSilences`,
`IsSilenced` — eight public methods including both admin CRUD and the hot-path
alert-firing logic. At ~280 lines this is below the hard limit, but the alert
evaluation path (`FireEvent`) mixes business rule enforcement with notification
dispatch and persistence. If email dispatch grows (templates, rate limiting),
this file will break 500 quickly. Recommend splitting `fire_event.go` from the
CRUD surface.

**G-2** `syntheticmonitor/store_postgres.go` at ~420 lines is the closest to the
limit. The `BatchAvailability7d` SQL window query will add non-trivial sqlc
boilerplate. Monitor the actual implementation line count; split into
`store_postgres_history.go` if the combined CRUD + history + rollup exceeds 450.

---

## Must-fix before implementation (numbered list)

1. **Replace phantom `credentialstore.EncryptSecret` with the real
   `credentialstore.Cipher.Encrypt` contract.** Specify the `AAD` fields for
   synthetic-monitor keys, the `Envelope` serialisation format for
   `api_key_encrypted TEXT`, and add `*credentialstore.Cipher` to
   `syntheticmonitor.Service` constructor. Update `store_postgres.go` line
   estimate accordingly.

2. **Resolve the `robfig/cron` dependency before dispatch.** Either add it to
   `go.mod` through the license/dependency gate, or replace with a lightweight
   in-package 5-field cron parser to avoid a new transitive dependency. Mark this
   as a build-blocking prerequisite, not a "Low" risk.

3. **Upgrade the cooldown race mitigation from the Risk table into the normative
   spec.** `opsalert/store_postgres.go` must implement an atomic
   `UPDATE ops_alert_rules SET last_triggered_at = now() WHERE id = $1 AND
   (last_triggered_at IS NULL OR last_triggered_at < now() - $2::interval)
   RETURNING id` pattern. The discriminating test for cooldown must exercise two
   concurrent goroutines, not just a clock advance in a single goroutine.

4. **Add `tenant_id` column to `synthetic_monitors` in migration 0078.** Use it
   as the `AAD.TenantID` binding for the cipher (platform-level monitors use
   `tenant_id = 0` with an explicit code comment).

5. **Add `REFERENCES ops_alert_rules(id) ON DELETE CASCADE` to
   `ops_alert_silences.rule_id`.** Remove the orphan-silence bug class.

6. **Specify the full goroutine runner wiring in `lifecycle.go`.** Both
   `syntheticmonitor.Runner` and `schedtest.Runner` must be added as named fields
   on `gatewayRuntime` (in `lifecycle.go`), started in `wiring.go`, and stopped
   in `shutdownGateway` with independent `context.WithTimeout` calls. Remove the
   incorrect claim that runners are "wired into `mountAdminRoutes`".

7. **Add `schedtest/tester_adapter.go` to the package layout.** Define the
   adapter that bridges `adminhttp.ProviderAccountCredentialTester` (real
   signature: `TestProviderAccountCredential(ctx, tenantID, accountID int64,
   now time.Time)`) to `schedtest.Tester`. Add `tenant_id` to `schedtest_plans`
   or derive it from the account record, since the real tester requires it.
   Update `schedtest_plans` schema and `runner.go` spec accordingly.
