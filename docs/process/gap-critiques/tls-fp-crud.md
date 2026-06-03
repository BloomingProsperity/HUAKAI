# Critique: TLS Fingerprint Profile Admin CRUD (tls-fp-crud)

_Reviewer: adversarial principal review_
_Date: 2026-06-03_
_Design file: docs/process/gap-designs/tls-fp-crud.md_

---

## Verdict

**needs-work**

The design is structurally sound on money/schema/auth but contains two correctness
defects that would survive into the implementation and produce silent wrong
behaviour in production: the `:exec` not-found blindness on SoftDelete and
SetStatus, and the corresponding test that cannot actually discriminate the defect
it claims to defend. A third issue — the `SetStatus` 404 being undetectable at
the service layer — compounds the first. These must be resolved before implementation.
Everything else is either correct or a moderate-severity gap.

---

## Holes

### HOLE-1 (MUST-FIX): `SoftDeleteTLSFingerprintProfile` is `:exec` — `ErrNoRows` can never fire

The design states:
> `pgx.ErrNoRows` from sqlc Get/Update/SoftDelete → `ErrNotFound` → 404

This is false for `SoftDelete`. The generated function signature is:
```go
func (q *Queries) SoftDeleteTLSFingerprintProfile(ctx context.Context, arg SoftDeleteTLSFingerprintProfileParams) error
```

It is a `-- name: SoftDeleteTLSFingerprintProfile :exec` query. `:exec` calls
`db.Exec(...)` and discards the `CommandTag`; it returns `nil` when zero rows
are matched. `pgx.ErrNoRows` is only returned by `QueryRow().Scan()` (`:one`
queries). The service's ErrNotFound mapping for Delete is therefore dead code —
a DELETE on a non-existent or already-soft-deleted profile will silently return
200 `{"deleted":true}` with no error.

**Consequence:** Deleting a profile belonging to another tenant (different
`tenant_id` supplied, ID guessed) also silently returns 200 rather than 404. The
SQL `WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL` clause is correct,
but the zero-rows-affected path is undetected. This is a tenant-isolation
observability hole (not a data-corruption risk, but an information-leakage risk
via response-code oracle).

**Correct pattern** (observed in `internal/routeadmin/store_postgres.go` lines
107-118): use `:one` with `RETURNING id` or check `CommandTag.RowsAffected() ==
0` after `:execrows`. The sqlc querier must be amended or the service must use a
`GetTLSFingerprintProfile` pre-flight check inside a transaction.

### HOLE-2 (MUST-FIX): `SetTLSFingerprintProfileStatus` is also `:exec` — same blindness

`SetTLSFingerprintProfileStatus` is `-- name: SetTLSFingerprintProfileStatus :exec`.
The design specifies a `POST /{id}/status` that returns 404 when the profile does
not exist. That is impossible with the current querier: a status update on a
non-existent ID returns `nil` error and the handler fetches via `GetTLSFingerprintProfile`
(which does use `:one`) to build the response — but the design does not specify
that a post-set Get is done inside a transaction. Race: another goroutine could
soft-delete the profile between the `:exec` UPDATE and the Get, causing the
subsequent Get to return `ErrNoRows` / 404 even though the status UPDATE itself
hit nothing.

More importantly: if the profile truly did not exist, the `:exec` UPDATE silently
matches 0 rows, the subsequent Get returns `ErrNotFound`, and the handler returns
404 — which accidentally gives a correct HTTP code but for the wrong reason (it
found nothing on Get, not on Set). The audit trail is correct for happy-path; the
edge-case logic is accidental.

### HOLE-3 (MUST-FIX): Discriminating test `TestDelete_NotFound_Returns404Sentinel` cannot fail on the actual defect

The test is described as:
> Fails when: `pgx.ErrNoRows` from SoftDelete is not mapped to `ErrNotFound`

Because `SoftDeleteTLSFingerprintProfile` never returns `pgx.ErrNoRows` (see
HOLE-1), the mock in `service_test.go` must manually return an error to exercise
the mapping. A correct discriminating test would instead: (a) call the real
service with a querier that returns zero rows affected and assert the service
returns `ErrNotFound`, or (b) be written against the `:execrows` / `:one` amended
querier. As designed, the mock can be written to make the test pass while the
real defect (silent 200 on missing row) remains undetected.

### HOLE-4 (SHOULD-FIX): `PUT /{id}` does not validate that tenant_id in the request body matches the path

The design's Update endpoint accepts `tenant_id` in the JSON body and also
requires `tenant_id` in the query parameter for GET/DELETE, but the `PUT /{id}`
description says only "full-field update" with body fields and no `?tenant_id`.
The `UpdateTLSFingerprintProfile` sqlc params include `TenantID int64`. The design
does not specify where that `TenantID` comes from in the PUT handler — body, query
param, or derived from auth identity. If it comes from the body unchecked, a
caller could supply any `tenant_id` and the SQL `WHERE tenant_id = $1` acts as
the only guard. This is correct from an isolation standpoint, but the design omits
any mention of how PUT validates or binds tenant_id, which is a specification gap
that will lead to implementation inconsistency.

### HOLE-5 (LOW): No idempotency key / conflict-safe Create

The design does not specify idempotency on Create. A network retry after a
server-side commit would produce a duplicate-name error (caught as
`ErrDuplicateName`/409), which is technically correct. However, there is no
`Idempotency-Key` header mechanism. For an admin CRUD surface this is acceptable,
but it should be explicitly acknowledged and documented. Currently the design is
silent on this.

### HOLE-6 (LOW): `status` in `SetStatus` response — GET after SET is not atomic

After `SetTLSFingerprintProfileStatus` (`:exec`), the handler is described as
returning `{"profile":{...}}` fetched after set. This is a separate `GET` call,
meaning the returned profile body could reflect a state modified by a concurrent
writer (drift worker sets `drift_detected` between the admin's SET and the
re-GET). This is a TOCTOU cosmetic issue but worth calling out: the response body
may not reflect the state the admin set.

---

## Money/Schema/Auth/CMB risks

### SCHEMA-1 (CORRECT): No new migration required — confirmed

Migration `0037_tls_fingerprint_profiles` exists at
`backend/sql/migrations/0037_tls_fingerprint_profiles.up.sql`. The current
maximum migration number is `0076_user_role` (confirmed by listing
`backend/sql/migrations/`). The design correctly states no new migration is
needed and that the next available number is `0077`. No collision risk.

### SCHEMA-2 (CORRECT): Schema matches sqlc output

The schema in the design matches the generated `admin_tls_fingerprint_profiles.sql.go`
exactly (field names, types, array types, `*string` for description, `pgtype.Timestamptz`
for timestamps). No discrepancy found.

### AUTH-1 (CORRECT): `resolveAdmin` pattern verified against reality

The `resolveAdmin` pattern in the design (nil-guard → Auth.Resolve → role check
for `RolePlatformAdmin` → 403 for tenant_operator) is the exact pattern in
`internal/routeadminhttp/handler.go` lines 181–199, which is the declared
structural reference. The error mapping (`ErrAdminBackend` → 503, otherwise → 401)
is also correct.

### AUTH-2 (CORRECT): CMB-1 import boundary respected

The new packages (`internal/tlsfpadmin`, `internal/tlsfphttp`) are not listed as
importing `internal/router`, `internal/auth`, `internal/gateway`, or
`internal/gatewayhttp`. The `AdminResolver` is in `internal/admin` and is reached
via the `AdminAuth` interface, which is the CMB-1-compliant indirection.

### AUTH-3 (CORRECT): CMB-5 — no credential logging specified

TLS profile fields (cipher suites, ALPN, JA3 hash) are not credentials. The
design correctly excludes them from CMB-5 scope. No bearer token, key hash, or
upstream payload appears in any specified response body or error message.

### AUTH-4 (CORRECT): Tenant isolation at SQL layer

All six sqlc operations enforce `WHERE tenant_id = $1 AND deleted_at IS NULL`.
The `GET` and `UPDATE` use `:one` (returns `ErrNoRows` on miss — correct). The
design's tenant_id enforcement is correct for those paths.

### MONEY-1 (CORRECT): No money path touched

Confirmed: no billing, balance, hold, settlement, or subscription table is
referenced by any of the six sqlc operations. No Tx1/Tx2, shopspring/decimal,
or double-charge risk. The design's assertion is accurate.

---

## Parity gaps

### PARITY-1 (CORRECT): Reference projects have no TLS profile admin surface

The design correctly states sub2api, litellm, and portkey have no equivalent
table or endpoint. The HUAKAI-native `expected_ja3_hash` + `last_validated_at` +
`drift_detected` delta is documented in the `0037` migration comment (lines
16–19). Parity claim "better" is well-founded.

### PARITY-2 (GAP — LOW): `drift_detected` admin-block is correct but underdocumented

The design correctly blocks admin `SetStatus` from setting `drift_detected` (to
prevent admin-forged drift state). However, the design does not specify what
happens when an admin calls `SetStatus` with `active` on a `drift_detected`
profile — i.e. the "admin manually clears drift" workflow. The SQL
`SetTLSFingerprintProfileStatus` allows any valid status value including
`active`, so clearing drift by admin is technically possible (the SQL will
accept it). The design should explicitly state this is intentional (admin
override of drift) or block it. Currently it is silently permitted, which may
conflict with Phase 3 drift detection semantics.

### PARITY-3 (GAP — LOW): List endpoint returns all statuses; active-only list via `ListActiveTLSFingerprintProfilesByTenant` not exposed

The querier has two list functions: `ListTLSFingerprintProfilesByTenant` (all
non-deleted, used by admin) and `ListActiveTLSFingerprintProfilesByTenant`
(active only, used by drift worker). The admin `GET /` uses the all-statuses
version, which is correct for admin management. No gap here. However the design
does not acknowledge that the drift worker uses a different query path — this
should be noted for Phase 3 implementers.

---

## Maintainability (god-file check)

| File | Budgeted lines | Assessment |
|------|---------------|------------|
| `internal/tlsfpadmin/types.go` | ~90 ln | OK |
| `internal/tlsfpadmin/service.go` | ~160 ln | OK |
| `internal/tlsfpadmin/service_test.go` | ~280 ln | OK |
| `internal/tlsfphttp/handler.go` | ~310 ln | OK — under 500 |
| `internal/tlsfphttp/handler_test.go` | ~380 ln | OK — under 500 |

No god-file violation in the stated budgets. All five files are under 500 lines.
All helper functions (resolveAdmin, writeJSON, decodeJSON, parsePathID,
parsePositiveQuery) are utilities under ~20 lines each, consistent with
`routeadminhttp` precedent.

**One concern:** the design says `internal/tlsfphttp/handler.go` contains both
the HTTP handlers AND `resolveAdmin` + JSON helpers. The `routeadminhttp`
reference package does the same (single handler.go file) so this is consistent.
No violation.

**Package separation:** Two packages for two responsibilities (domain/service vs
HTTP) is correct. No cross-contamination with frozen packages specified.

---

## Must-fix before implementation (numbered list)

1. **HOLE-1 / HOLE-3: Fix SoftDelete not-found detection.** Either (a) convert
   the `SoftDeleteTLSFingerprintProfile` sqlc query from `:exec` to
   `:execrows` (returns `pgconn.CommandTag`) so the service can check
   `CommandTag.RowsAffected() == 0` → `ErrNotFound`, or (b) use a
   `RETURNING id` / `:one` variant. Update the service implementation and
   the discriminating test `TestDelete_NotFound_Returns404Sentinel` to assert
   against the zero-rows-affected path, not a manually-injected `ErrNoRows`.
   The generated `querier.go` interface must be updated accordingly (requires
   sqlc regeneration of `admin_tls_fingerprint_profiles.sql.go`).

2. **HOLE-2: Fix SetStatus not-found detection.** Similarly convert
   `SetTLSFingerprintProfileStatus` to `:execrows` and check
   `RowsAffected() == 0` → `ErrNotFound` before issuing the post-set Get.
   Alternatively, wrap Set + Get in a single transaction so the post-set read
   is atomic. Update the service and add discriminating test
   `TestSetStatus_NotFound_Returns404Sentinel`.

3. **HOLE-4: Specify tenant_id binding for PUT /{id}.** The design must
   explicitly state whether `tenant_id` for the Update call is taken from
   (a) a `?tenant_id` query parameter (consistent with GET/DELETE), (b) the
   request body, or (c) the auth identity's `ScopeTenantID`. Recommend
   query parameter for consistency with the other mutating-by-ID endpoints.
   Add a discriminating test `TestUpdate_MissingTenantID_Returns400`.

4. **HOLE-1 consequence: Regenerate sqlc.** The fix to items 1 and 2 requires
   changing the `.sql` source queries and regenerating the sqlc layer. Since
   `admin_tls_fingerprint_profiles.sql.go` is generated code, the design must
   specify the corrected query annotations (`:execrows` or `:one` with
   `RETURNING id`) in the sql source file
   `backend/sql/queries/admin_tls_fingerprint_profiles.sql` before
   implementation begins.

5. **PARITY-2: Clarify admin override of drift_detected status.** Add a one-line
   policy statement to the design: does `POST /{id}/status` with `active`
   intentionally allow clearing `drift_detected` state (admin override), and if
   so, should it also reset `last_validated_at`? The current SQL sets
   `last_validated_at = NOW()` when transitioning to `active`, which is the right
   behaviour for a manual clear, but this should be explicitly documented rather
   than implicit.
