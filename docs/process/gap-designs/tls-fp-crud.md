# Gap Design: TLS Fingerprint Profile Admin HTTP CRUD

_Author: HUAKAI senior backend architect_
_Date: 2026-06-03_
_Status: READY FOR IMPLEMENTATION_

---

## Summary

The TLS fingerprint profile subsystem (F-FP-POOL Phase 1) is **partially
implemented**: migration `0037_tls_fingerprint_profiles` created the
`tls_fingerprint_profiles` table, and the full sqlc/querier layer in
`internal/db/admin` exposes all six operations (List, Get, Create, Update,
SetStatus, SoftDelete). What is missing is the **admin HTTP surface** —
a service layer that validates inputs and maps domain errors, plus the HTTP
handlers and route wiring.

This design adds:

1. `internal/tlsfpadmin` — domain types, sentinel errors, and a thin service
   that validates inputs and calls the sqlc querier directly (no intermediate
   store abstraction needed; the sqlc layer already provides the full query
   contract).
2. `internal/tlsfphttp` — chi HTTP handlers for List / Get / Create / Update /
   SetStatus / Delete, following the `routeadminhttp` structural pattern.
3. A two-line addition to `cmd/gateway/routes.go` and a one-line addition to
   `cmd/gateway/wiring.go` to mount the new routes under
   `/admin/v1/tls-fingerprint-profiles`.

No new migration is required. No frozen package is modified. No money path is
touched. Status changes travel through a dedicated `POST /{id}/status` endpoint
rather than the general update body, matching the SQL comment on
`SetTLSFingerprintProfileStatus` ("status 走专用 SetStatus 端点").

The HUAKAI-native delta versus reference gateway projects (sub2api / litellm /
portkey) is the `expected_ja3_hash` + `last_validated_at` + `drift_detected`
status fields, which are managed by the drift-detection worker (Phase 3) and
exposed read-only in admin GET/LIST responses.

---

## Package layout

Each file is kept under 500 lines; each function under 80 lines.

```
internal/tlsfpadmin/
    types.go           (~90 ln)  Domain types (Profile, CreateInput, UpdateInput,
                                 SetStatusInput) and sentinel errors
                                 (ErrNotFound, ErrInvalidInput, ErrDuplicateName,
                                 ErrInvalidStatus, ErrBackend).

    service.go         (~160 ln) Service struct wrapping admindb.Querier.
                                 Methods: List, Get, Create, Update, SetStatus,
                                 Delete. Each validates inputs then delegates to
                                 the sqlc querier. Returns typed sentinel errors
                                 so the HTTP layer has a stable error contract.

    service_test.go    (~280 ln) Unit tests with a mock Querier (interface double).
                                 One discriminating test per operation + one for
                                 each validation invariant (see §Discriminating tests).

internal/tlsfphttp/
    handler.go         (~310 ln) HTTP handlers: MountTLSFPAdminRoutes, list,
                                 get, create, update, setStatus, delete.
                                 resolveAdmin helper (auth + platform_admin gate),
                                 writeJSON / writeJSONError, writeTLSFPError,
                                 decodeJSON (MaxBytesReader 1 MiB,
                                 DisallowUnknownFields).

    handler_test.go    (~380 ln) httptest-based handler tests for each endpoint,
                                 covering auth failure, validation failure,
                                 not-found, duplicate-name, wrong-status-value,
                                 and the happy path.
```

**Wiring changes (existing files, not new packages):**

- `cmd/gateway/routes.go` — add `r.Route("/admin/v1/tls-fingerprint-profiles", ...)`
  call inside `mountAdminRoutes`.
- `cmd/gateway/wiring.go` — construct `tlsfphttp.AdminDeps` and assign to the
  `gatewayRuntime` struct (or pass inline; matches how `routeAdminService` is
  wired on line 567).

No file in `internal/gatewayhttp`, `internal/gateway`, or `internal/proto` is
created or modified.

---

## Schema / migrations

**No new migration is required.**

Migration `0037_tls_fingerprint_profiles.up.sql` already created:

```sql
CREATE TABLE tls_fingerprint_profiles (
    id                     bigserial   PRIMARY KEY,
    tenant_id              bigint      NOT NULL REFERENCES tenants(id),
    name                   text        NOT NULL,
    description            text,
    grease_enabled         boolean     NOT NULL DEFAULT false,
    cipher_suites          integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    supported_curves       integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    ec_point_formats       integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    signature_algorithms   integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    alpn_protocols         text[]      NOT NULL DEFAULT ARRAY[]::text[],
    tls_supported_versions integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    key_share_groups       integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    psk_modes              integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    extensions_order       integer[]   NOT NULL DEFAULT ARRAY[]::integer[],
    expected_ja3_hash      text        NOT NULL DEFAULT '',
    last_validated_at      timestamptz,
    status                 text        NOT NULL DEFAULT 'active'
                                       CHECK (status IN ('active','disabled','drift_detected')),
    created_at             timestamptz NOT NULL DEFAULT NOW(),
    updated_at             timestamptz NOT NULL DEFAULT NOW(),
    deleted_at             timestamptz
);
```

Unique partial index on `(tenant_id, name) WHERE deleted_at IS NULL` enforces
tenant-scoped name uniqueness and is the source of the `ErrDuplicateName`
sentinel.

The current maximum migration number is **0076** (`0076_user_role`). If a
schema change is needed in a future iteration, the next number is **0077**.

---

## Endpoints

All endpoints are mounted under the prefix `/admin/v1/tls-fingerprint-profiles`.
All require a valid `Authorization: Bearer hk_admin_...` header that resolves to
a `platform_admin` token. `tenant_operator` tokens are rejected with 403
(matching the `routeadminhttp` precedent — TLS profiles are a platform-level
resource configuration, not a per-tenant self-service).

| Method   | Path            | Auth scope      | Description |
|----------|-----------------|-----------------|-------------|
| `GET`    | `/`             | platform_admin  | List all non-deleted profiles for a tenant. `?tenant_id=<id>` required. Returns `{"object":"tls_fingerprint_profiles_list","items":[...]}`. |
| `GET`    | `/{id}`         | platform_admin  | Get a single profile by ID. `?tenant_id=<id>` required. Returns `{"profile":{...}}`. |
| `POST`   | `/`             | platform_admin  | Create a new profile. Body: JSON with all TLS fields (see §Input validation). Returns 201 + `{"profile":{...}}`. |
| `PUT`    | `/{id}`         | platform_admin  | Full-field update (name, description, TLS arrays, expected_ja3_hash). Does NOT change `status`. Returns 200 + `{"profile":{...}}`. |
| `POST`   | `/{id}/status`  | platform_admin  | Set profile status. Body: `{"tenant_id":<id>,"status":"active"\|"disabled"}`. Drift worker uses the sqlc layer directly (not this endpoint). Returns 200 + `{"profile":{...}}` fetched after set. |
| `DELETE` | `/{id}`         | platform_admin  | Soft-delete (sets `deleted_at`). `?tenant_id=<id>` required. Returns 200 + `{"deleted":true,"id":<id>}`. |

Query parameter `tenant_id` on GET and DELETE is parsed as positive `int64`;
absent or non-positive → 400 `tenant_id_required`.

Path parameter `{id}` is parsed as positive `int64`; invalid → 400
`invalid_profile_id`.

---

## Invariants honored

**CMB (cross-module boundary) invariants:**

- CMB-1: `internal/tlsfphttp` and `internal/tlsfpadmin` do NOT import
  `internal/router`, `internal/auth`, `internal/gateway`, or
  `internal/gatewayhttp`. The customer hot path is unaffected.
- CMB-5: no credential, key hash, or upstream payload is logged or included
  in any error response body. TLS profile fields (cipher suites, ALPN) are
  not credentials.
- CMB-7: the new packages write only to `tls_fingerprint_profiles`. No billing,
  pool, or registry table mutation.

**Fail-closed on ambiguity:**

- Nil-guard on `Auth` and `Service` fields → 503 `gateway_not_configured` (not
  panic, not 500).
- `pgx.ErrNoRows` from sqlc Get/Update/SoftDelete → `ErrNotFound` → 404; never
  silently returns an empty row.
- An unrecognized `status` value in the SetStatus body → 400
  `invalid_status` before any DB write.

**Tenant isolation:**

- Every sqlc query enforces `WHERE tenant_id = $1 AND deleted_at IS NULL`.
  Cross-tenant access is rejected at the SQL layer (DR-001 / TS-006).
- `platform_admin` callers supply `tenant_id` explicitly; no implicit
  scope-tenant leakage.

**No money path, no Tx1/Tx2:**

TLS profile CRUD does not touch billing or balance tables. Standard single-
statement sqlc calls are used with no explicit transaction wrapper.

**Modularity:**

- Two new packages, each with a single responsibility.
- No file exceeds 500 lines; no function exceeds 80 lines.
- The HTTP layer depends on the service layer via a narrow interface (`Service`)
  — not on `*admindb.Queries` directly — so handler tests use a mock.

**Status separation:**

- `PUT /{id}` calls `UpdateTLSFingerprintProfile` (updates content fields,
  resets `updated_at`). It does NOT accept a `status` field in the body
  (`DisallowUnknownFields` would reject it even if smuggled).
- `POST /{id}/status` calls `SetTLSFingerprintProfileStatus` (does not touch
  content fields or `updated_at`). This matches the SQL comment: "status 走专用
  SetStatus 端点; updated_at 不动".

---

## Discriminating tests

Each test **fails** if the specific invariant it defends is violated and passes
otherwise. Tests are in `internal/tlsfpadmin/service_test.go` (service layer,
mock querier) and `internal/tlsfphttp/handler_test.go` (HTTP layer,
`httptest.NewRecorder`).

### Service layer (`internal/tlsfpadmin/service_test.go`)

| Test name | What it defends | Fails when |
|-----------|-----------------|------------|
| `TestCreate_EmptyName_RejectsBeforeDB` | Input validation: empty name | Service calls querier Create with empty name instead of returning `ErrInvalidInput` |
| `TestCreate_ZeroTenantID_RejectsBeforeDB` | Input validation: zero tenant_id | Service forwards zero tenant_id to DB |
| `TestCreate_DuplicateName_MapsToErrDuplicateName` | Error mapping: unique index violation | pgx unique constraint error is not mapped to `ErrDuplicateName` sentinel |
| `TestUpdate_StatusFieldIgnored` | Status immutability | UpdateInput accepted by `Update()` contains a status field that reaches DB |
| `TestSetStatus_InvalidValue_Rejected` | Status enum guard | `SetStatus` with `"drift_detected"` from the admin endpoint reaches DB (drift_detected is drift-worker-only) |
| `TestDelete_NotFound_Returns404Sentinel` | Not-found mapping | `pgx.ErrNoRows` from SoftDelete is not mapped to `ErrNotFound` |
| `TestGet_TenantIsolation_WrongTenantReturnsNotFound` | Tenant isolation | Querier is called with the wrong tenant_id and returns a row |
| `TestList_EmptyResult_ReturnsEmptySlice` | Nil-safety on empty result | Returns nil slice instead of empty slice (breaks JSON `[]` vs `null`) |

### HTTP layer (`internal/tlsfphttp/handler_test.go`)

| Test name | What it defends | Fails when |
|-----------|-----------------|------------|
| `TestList_MissingTenantID_Returns400` | Mandatory tenant_id query param | Missing tenant_id returns 200 or 500 instead of 400 |
| `TestGet_NilDeps_Returns503` | Nil-guard / fail-closed | Nil Auth/Service panics or returns 200 |
| `TestGet_UnauthorizedToken_Returns401` | Auth gate | Invalid bearer returns 200 |
| `TestGet_TenantOperatorRole_Returns403` | platform_admin-only | tenant_operator token accepted |
| `TestCreate_UnknownField_Returns400` | DisallowUnknownFields | Unknown JSON key is silently ignored |
| `TestCreate_HappyPath_Returns201` | Create flow end-to-end | Status code is not 201 or body missing `profile.id` |
| `TestUpdate_StatusInBody_Returns400` | Status field locked out of PUT | Body with `"status":"disabled"` is accepted by PUT handler |
| `TestSetStatus_DriftDetectedRejected_Returns400` | Admin cannot set drift_detected | `drift_detected` is accepted through the status endpoint |
| `TestDelete_NotFound_Returns404` | Not-found → 404 mapping | `ErrNotFound` from service returns 503 |
| `TestDelete_SoftDelete_Returns200WithDeletedTrue` | Soft-delete response shape | Response body missing `deleted:true` field |

---

## Parity-or-better vs reference

The HUAKAI schema comment (`0037_tls_fingerprint_profiles.up.sql:14-22`)
explicitly identifies the HUAKAI-native deltas relative to reference gateway
projects (sub2api, litellm, portkey gateway). This design honors and exposes
all three:

| Behavior | Reference | HUAKAI parity / delta |
|----------|-----------|-----------------------|
| CRUD of TLS ClientHello template profiles | `sub2api`: no TLS profile admin surface found (no analog table or endpoint in decompositions). `portkey`/`litellm`: no TLS fingerprint admin. | HUAKAI adds this surface. Parity = "better" — reference projects have no equivalent. |
| Tenant scoping of profiles | Not present in reference projects. | `tenant_id` mandatory on every query; cross-tenant access rejected at SQL layer. Behavioral delta: `0037` schema comment line 14, `DR-001/TS-006`. |
| `expected_ja3_hash` + `last_validated_at` drift detection metadata | Not present in reference projects (`0037` schema comment lines 16-19: "这层在外部 gateway 项目里没有先例"). | Fields are exposed read-only in GET/LIST responses. `drift_detected` status is set only by the drift-detection worker (Phase 3), not by this admin endpoint (SetStatus from admin rejects `drift_detected` → 400). |
| `status` as separate mutation from content | Reference projects with TLS-like config generally use single PUT. | Separate `POST /{id}/status` endpoint; `UpdateTLSFingerprintProfile` never touches `status`; `SetTLSFingerprintProfileStatus` never touches content fields or `updated_at`. Parity-or-better: cleaner audit trail. |
| Soft-delete with FK non-cascade fallback | Standard in HUAKAI (DR soft-delete pattern). | `SoftDeleteTLSFingerprintProfile` sets `deleted_at`; any `provider_accounts.tls_fingerprint_profile_id` FK still intact but resolver skips soft-deleted rows (falls back to builtin). |

Reference path anchor: `C:\HUAKAI\repo\docs\process\2026-05-24-ref-anchor.md`
and decompositions at `docs/decompositions/sub2api/`, `docs/decompositions/litellm/`.

---

## Effort

**S** (small).

Justification: the DB schema, indexes, and the entire sqlc/querier layer are
already complete and tested. The gap is purely the HTTP surface (~700 lines
total across two new packages: ~250 ln service + types, ~310 ln handler, ~380
ln tests split across two files). The structural pattern is established by
`routeadminhttp` (9 003 lines of handler + test) and `adminhttp`
provider\_catalog\_handler. Wiring is two lines in existing files.

No migration, no schema change, no new dependency, no money path, no frozen
package modification.

---

## Risks

| Risk | Likelihood | Mitigation |
|------|-----------|------------|
| `drift_detected` admin leak: admin SetStatus endpoint accepting `drift_detected` would let an admin forge drift state and cause the gateway to skip valid profiles | Medium (easy mistake) | Service-layer `SetStatus` rejects `"drift_detected"` with `ErrInvalidStatus`; discriminating test `TestSetStatus_DriftDetectedRejected_Returns400` defends this. |
| Unique index violation on create leaks tenant name info via error code | Low | Error response uses generic `profile_name_conflict` code without echoing the conflicting name. |
| `DisallowUnknownFields` breaking existing callers on future field additions | Low | Documented in handler comments; future schema additions to request body require handler update. |
| Status/content conflation: PUT body accidentally includes `"status"` (e.g. copy-paste from GET response) | Medium | `DisallowUnknownFields` + `ErrInvalidInput` for status in Update body; discriminating test `TestUpdate_StatusInBody_Returns400`. |
| pgx `NoRows` not mapped to `ErrNotFound` → 503 instead of 404 | Low | Explicit mapping in service layer; discriminating test `TestDelete_NotFound_Returns404Sentinel`. |
| Nil `Auth`/`Service` deps panic in handler | Low | Nil-guard at handler entry returns 503; discriminating test `TestGet_NilDeps_Returns503`. |
| Large request body abuse (array fields) | Low | `http.MaxBytesReader(1 MiB)` applied before `json.Decoder`; mirrors `routeadminhttp.maxBodyBytes`. |
