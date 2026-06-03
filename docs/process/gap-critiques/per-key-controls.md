# Adversarial Review: per-key-controls.md

**Reviewed by:** Adversarial Principal Reviewer  
**Date:** 2026-06-03  
**Design file:** `docs/process/gap-designs/per-key-controls.md`  
**Migration range claimed:** 0077–0078

---

## Verdict

**needs-work**

The design is structurally coherent and respects CMB-5/CMB-7 at the surface level, but it has
multiple concrete blockers that must be resolved before implementation is safe to dispatch:
one critical blocker (PolicyWriter does not exist), one high-severity auth hole (step-up is
not actually step-up), three schema correctness issues, a fail-open ambiguity in the quota
fallback description, and an internal contradiction in the test table. No item is
individually fatal, but the combination means a worker dispatched today would either build
against a non-existent interface or ship a false sense of re-authentication security.

---

## Holes

### H-1 (CRITICAL): `PolicyWriter` interface does not exist — `SetKeyQuota` has no write path

The design states `SetKeyQuota` "must call quota store directly (not via `quota.Service.Reserve`)
— the admin write path needs a thin `PolicyWriter` interface extracted from the existing
`PGStore`." Confirmed by code search: `internal/quota/store.go` (`PGStore` interface) exposes
only `ListActivePolicies` for policy reads. There is no `UpsertQuotaPolicy`, `InsertQuotaPolicy`,
or any policy-write method anywhere in `internal/quota` or `internal/db/quota`. The `quota.sql.go`
generated file contains no policy-write query. The design references this interface as if it
already exists or is trivially extractable, but it must be designed from scratch — new SQL query,
new sqlc generation, new store method, new interface — and none of that is scoped into this gap
design. Without it, `SetKeyQuota` cannot write a `quota_policies` row. **This is a scoping gap
that blocks implementation.**

### H-2 (HIGH): Step-up is not re-authentication — it is a replay of the same session bearer

The design's "step-up" for secure reveal is:
1. Call endpoint → receive an HMAC-SHA256 challenge token signed over `(api_key_id || user_id || timestamp)`.
2. Re-present the challenge token in the body of a second call.

The session bearer in the Authorization header is the same for both calls — there is no second
factor, no TOTP, no re-entry of credentials. An attacker who has stolen the session bearer once
can pass the step-up challenge mechanically with a single extra HTTP call. The design calls this
"forward-compatible with TOTP/WebAuthn" but it is only structurally forward-compatible — the MVP
implementation provides zero additional authentication assurance beyond the session bearer itself.
Calling this "step-up" in CMB/invariant claims is misleading and creates a false audit trail
(`verified: true` in response suggests real step-up occurred). The design must either:
(a) scope this as "challenge-echo, not step-up" with honest invariant documentation, or
(b) require a TOTP/password re-entry as the first-class MVP proof.

### H-3 (MEDIUM): Fail-OPEN description is misleading / partially incorrect

The Invariants table states: "If `quota_policy_id` FK lookup fails at request time, the quota
engine defaults to deny (it already does: `ErrStoreNotConfigured` / missing policy = allow-by-
default is a `ModeObserve` outcome)."

This description conflates two distinct cases:
- **Store is nil:** `quota.Service.Reserve` immediately returns `ErrStoreNotConfigured` wrapped
  as a `DenyError` — this is fail-closed (correct).
- **Store is configured but `ListActivePolicies` returns empty (no matching policy row):**
  `evaluatePolicies` iterates zero policies, produces no `enforceWindows` and no deny, and
  the request is **allowed** without any quota check. This is NOT an "observe outcome" — it is
  unconditional allow. If `SetKeyQuota` writes a policy row but the FK link on `api_keys` is
  stale/broken (e.g. deferred FK not committed before request arrives), the key quota is
  silently bypassed. The design must acknowledge this and add a guard: if `quota_policy_id IS
  NOT NULL` on the key but `ListActivePolicies` returns zero rows, the quota layer must fail-
  closed for that key.

### H-4 (MEDIUM): `api_key_reveal_tokens` has no FK to `api_keys`

Migration 0078 creates `api_key_reveal_tokens` with `api_key_id bigint NOT NULL` and a FK to
`tenants(id)`, but no FK constraint to `api_keys(id)`. If a key is soft-deleted (or hard-
deleted in a hypothetical future migration), orphaned reveal tokens survive indefinitely.
Verify-by-token will return `ErrRevealTokenNotFound` (the ownership JOIN will miss), but the
orphaned rows accumulate silently. Add:
```sql
FOREIGN KEY (api_key_id) REFERENCES api_keys(id) ON DELETE CASCADE
```
or at minimum `ON DELETE SET NULL` + handle NULL in verify logic.

### H-5 (LOW): Internal test-table contradiction

`TestPutQuota_MissingSession_503` — the test description says "expects 503 (no session →
service call protected)" and then immediately adds "Correction: expects 401 (session_required)."
Confirmed by `userkeyhttp/handlers.go` `resolveSession()`: nil service → 503, missing session
→ 401. The test defends the 401 path, not 503. The test name is wrong and the description has
an in-line correction that was not applied to the test name. If the test is implemented with the
wrong expected status code, it is not discriminating — it will pass even when the session check
is absent (if the stub returns 503 for a different reason). Fix: rename the test to
`TestPutQuota_MissingSession_401` and assert exactly HTTP 401.

---

## Money / Schema / Auth / CMB Risks

### MS-1 (HIGH): No `quota_policies` write path exists in any package — double-creation risk

Because no policy-write query exists in `internal/db/quota`, the implementer will have to add
one. Two risks when adding:
1. **Upsert vs insert semantics:** If the implementer writes INSERT-only (not UPSERT), two
   calls to `SetKeyQuota` will create two rows, violating `TestSetKeyQuota_UpdatesExisting_
   DoesNotDuplicate`. The design assumes `scope_kind=api_key + scope_id=<key_id>` is a natural
   unique key but the current `quota_policies` table schema (visible in `db/quota/quota.sql.go`)
   does not surface a UNIQUE constraint on `(tenant_id, scope_kind, scope_id, metric)`. The
   implementer must add this constraint or the idempotency test will be testing behavior the DB
   does not guarantee.
2. **Transaction ownership:** `SetKeyQuota` must write a `quota_policies` row AND then update
   `api_keys.quota_policy_id` in the same transaction. If these are split across separate DB
   calls (easy mistake given the quota store's `WithTx` is a separate codepath from userkey's
   `s.tx()`), a crash between the two writes leaves a dangling policy with no FK link.

### MS-2 (MEDIUM): Deferred FK hazard under concurrent Set+Reserve

Both new FKs (`fk_api_keys_key_group`, `fk_api_keys_quota_policy`) are `DEFERRABLE INITIALLY
DEFERRED`. The design correctly notes this allows same-Tx insert+link. However, the inbound
auth resolver (`auth.APIKeyResolver.Resolve`) queries `api_keys` rows outside any transaction
(plain pool query). If `quota_policy_id` is committed but the `quota_policies` row was written
in a parallel transaction that has not yet committed (possible under `SERIALIZABLE` if the
policy-write Tx is slow), the FK deferred check defers the error to commit time — the resolver
will see `quota_policy_id != NULL` pointing to a policy that the quota engine's
`ListActivePolicies` cannot find. Per H-3 above, this produces unconditional allow. The window
is small but real under load. Mitigation: perform policy write + FK link in a single
`BEGIN`/`COMMIT` block always; document this as a service-layer invariant.

### MS-3 (LOW): `UNIQUE (tenant_id, id)` on `api_key_groups` is structurally redundant

`api_key_groups.id` is `bigserial PRIMARY KEY` — globally unique by definition.
`CONSTRAINT uq_api_key_groups_tenant_id_id UNIQUE (tenant_id, id)` adds no cross-tenant
isolation guarantee beyond what the PK already provides. The FK on `api_keys` uses
`(tenant_id, key_group_id) REFERENCES api_key_groups (tenant_id, id)` — this is the real
cross-tenant guard and it does work. But the named UNIQUE constraint is dead weight that will
confuse future developers into thinking it provides a useful uniqueness guarantee. Remove it
or comment it as "required as the referencing side of the composite FK only."

### MS-4 (LOW): `valid_from = now()` in `SetKeyQuota` — clock skew risk

The design says `SetKeyQuota` sets `valid_from = now()` unconditionally to prevent a future
`valid_from` from leaving the key unprotected. Correct. But if the application calls
`now()` in Go and passes it to SQL as a parameter, it must be the DB server's clock (use
`NOW()` in SQL, not the Go `time.Now()`), otherwise clock skew between app server and DB
server can set `valid_from` slightly in the future and leave a brief window (~milliseconds to
seconds) where `ListActivePolicies` (filtered by `valid_from <= NOW()`) returns no rows,
causing unconditional allow per H-3.

### CMB-1: `userkeycontrols` must never be imported by `gateway`/`gatewayhttp`/`proto`

The design states this explicitly. However, the design also says the inbound resolver gains
`key_group_id` via a sqlc column add and `GetAPIKeyGroupByID` query in `internal/db/auth`.
This is fine. But the `auth.Identity` struct addition (`KeyGroupID *int64`) is in
`internal/auth` — which IS imported by `gateway`. The design must explicitly state that
`internal/auth` is the only package that gains this field addition, and that `gateway`'s use
of `Identity.KeyGroupID` remains read-only (no write path back to `userkeycontrols`). This is
implied but not stated, creating future confusion.

### CMB-5: `nonce_hash` stored as `text` not `bytea`

`api_key_reveal_tokens.nonce_hash text NOT NULL` stores a bcrypt hash as text. Bcrypt output
is ASCII-safe, so text storage is technically correct. However, storing as `text` means
string comparison operators work, creating a subtle risk: a future developer could accidentally
write `WHERE nonce_hash = $1` with a plaintext nonce, which would never match (bcrypt hash vs
plaintext) but would silently "work" — returning no rows and producing `ErrRevealTokenNotFound`
rather than a compile error. Use `bytea` and enforce that the verify path always calls
`bcrypt.CompareHashAndPassword`, never string equality.

---

## Parity Gaps

### PG-1: Group-scoped policy enforcement is claimed but not wired

The design claims "inbound resolver stamps `Identity.KeyGroupID` so downstream routing/quota
can enforce group policies." The `auth.Identity` struct today has `UserGroup string` (stamped
from `users.user_group`). Adding `KeyGroupID *int64` is correct. However, the design provides
no specification for HOW the quota engine or router actually uses `KeyGroupID` for policy
dispatch. The parity claim "group-scoped policies apply transparently" (E-OAI-010 F-GROUP-001)
requires that when `Identity.KeyGroupID != nil`, the quota Reserve call includes a
`ScopeAPIKeyGroup` scope. No such scope kind exists in `internal/quota/types.go` (`ScopeKind`
constants). The design either needs to add this scope and wire it through `ReserveRequest.Scopes`,
or explicitly scope-down the parity claim to "key carries group tag; policy dispatch in future
gap."

### PG-2: Reveal endpoint documents "proves key identity" — reference behavior is "plaintext reveal gated by re-auth"

The design correctly acknowledges bcrypt makes plaintext recovery impossible. The "better"
disposition is architecturally honest. However, the reference behavior (E-OAI-005 F-KEY-001 /
E-NAI-008 F-OPS-001) is specifically about an operator recovering the key value for forensic
use. HUAKAI's nonce-proof design proves "this session holder owns this key" — it does NOT help
an operator who has lost the session and needs the key value. The parity claim should be
downgraded from "Better" to "Partial — different capability surface; original forensic use case
is architecturally impossible in HUAKAI (bcrypt)." This must be acknowledged in the feature
tree so no future PM re-opens it as a gap.

---

## Maintainability (god-file check)

All 10 new files are budgeted under 500 lines individually. No file breaches the hard rule.
However, two concerns:

**M-1:** `reveal_handler.go` is budgeted at ~150 lines but must implement:
- Parse empty body → issue challenge (HMAC construction, DB write of challenge token)
- Parse body with `step_up_proof` → verify challenge expiry + HMAC → issue nonce (DB write)
- Separate verify endpoint path

That is two distinct flows plus error handling in one file. At 150 lines each function would
average ~30–40 lines, which is borderline. The verify endpoint (`POST /reveal-token/verify`)
is listed as a separate URL but the design does not allocate a separate handler file for it.
Recommend: split into `reveal_issue_handler.go` (~120 lines) and `reveal_verify_handler.go`
(~80 lines).

**M-2:** `reveal.go` at ~220 lines implements both challenge issue and nonce issue logic.
These are two independent sub-features with different DB tables (challenges are transient
in-memory or ephemeral table; nonces are `api_key_reveal_tokens`). If challenges are stored
in a DB table (not specified in the design), a third table is needed but not budgeted in any
migration. If challenges are in-memory signed tokens (no DB row), the design must state this
explicitly — currently ambiguous.

**M-3 (existing file concern):** `internal/userkey/userkey.go` is currently 566 lines
(confirmed by read). The design adds +60 lines delta, which would bring it to ~626 lines —
**above the 500-line hard rule**. This violates the Owner modularity rule. The `BatchRevoke`
method and associated types must go into a new file `internal/userkey/batch_revoke.go`, not
be added to the existing `userkey.go`. The design's own table says "+60 to `userkey.go`" —
this must be corrected before dispatch.

---

## Must-fix before implementation

1. **[BLOCKING] Design and scope the `PolicyWriter` interface.** Add to the gap: new SQL file
   (`sql/quota/upsert_policy.sql`), new sqlc query `UpsertQuotaPolicy`, new `PolicyWriter`
   interface in `internal/quota`, and new `PostgresStore.UpsertPolicy` method. Add a UNIQUE
   constraint on `quota_policies (tenant_id, scope_kind, scope_id, metric)` (or confirm one
   exists). Without this, `SetKeyQuota` has no write path.

2. **[HIGH] Rename step-up to "challenge-echo" or require a real second factor.** Either:
   (a) Change invariant documentation to honestly describe the MVP as "challenge-echo — proves
   request intentionality but not re-authentication; step-up upgrade tracked as follow-on item,"
   and remove any language implying CMB/re-auth guarantees; or
   (b) Require TOTP/password as the `step_up_proof` value in the MVP, not an HMAC challenge
   re-presentation.

3. **[HIGH] Fix fail-OPEN description and add guard.** Update the Invariants table to correctly
   state: "if `quota_policy_id IS NOT NULL` on `api_keys` but `ListActivePolicies` returns no
   rows (e.g. FK broken or policy disabled), the userkeycontrols quota path must fail-closed
   for that key (return 429 or 503, not allow)." Add logic in `quota.go` to detect this case.

4. **[MEDIUM] Add FK from `api_key_reveal_tokens.api_key_id` to `api_keys(id)`.** Use
   `ON DELETE CASCADE` or document the orphan cleanup strategy.

5. **[MEDIUM] Move `BatchRevoke` to a new file.** `internal/userkey/userkey.go` is already
   566 lines. Adding +60 lines violates the 500-line hard rule. New file:
   `internal/userkey/batch_revoke.go`.

6. **[MEDIUM] Fix test name `TestPutQuota_MissingSession_503`.** Rename to
   `TestPutQuota_MissingSession_401` and assert HTTP 401, not 503. As written the test
   description contradicts itself and risks being implemented with the wrong assertion.

7. **[LOW] Remove or explain the redundant `UNIQUE (tenant_id, id)` on `api_key_groups`.**
   Add an inline SQL comment explaining the constraint exists solely as the referencing target
   for the composite FK on `api_keys`, not as a cross-tenant isolation mechanism.

8. **[LOW] Clarify challenge storage.** State explicitly whether challenge tokens are:
   (a) HMAC-signed and verified statelessly (no DB row needed), or
   (b) stored in a DB table. If (b), a third migration is required. If (a), the 5-minute TTL
   check in `TestRevealToken_ExpiredChallenge_Rejected` must be enforced via embedded timestamp
   in the HMAC payload, not a DB `expires_at` — document this clearly so the worker does not
   add an unplanned DB table.

9. **[LOW] Downgrade PG-2 parity claim.** Change the `api_key_reveal_tokens` parity row from
   "Better" to "Partial — forensic plaintext recovery is architecturally impossible in HUAKAI
   (bcrypt); this feature proves key identity to the session holder only." Add to feature tree.

10. **[LOW] Specify group-policy dispatch scope.** Either add `ScopeAPIKeyGroup ScopeKind` to
    `internal/quota/types.go` and wire it through `ReserveRequest.Scopes` in the inbound hot
    path, or explicitly defer this to a follow-on gap and remove the "group policies apply
    transparently" parity claim from this design.
