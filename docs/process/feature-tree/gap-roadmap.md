# Feature-tree gap closure — PM roadmap (verify-then-residual)

**PM:** Claude (Opus). **Date:** 2026-06-03. **Method:** every flagged gap is
coverage-verified against real code BEFORE implementation; we build only the
*true residual*, not the design's from-scratch proposal.

## Why this exists

The sonnet-generated gap designs (`docs/process/gap-designs/`) proposed building
features from scratch without checking what already ships. Verification proved
this over-states the work badly:

- **relay-log** (first verified): design wanted a new `relay_request_logs` table
  + a fail-open money-path settler hook + 2 packages. Reality: `GET /v1/me/usage`
  (`internal/meusagehttp`) already *is* the relay log (~90%). True residual was
  exposing the token counts the query already SELECTs. **Landed 3142c07** — no
  table, no migration, no money-path touch.

The coverage sweep (`coverage-verification.md`) + an independent table/route/
package inventory confirm most gaps are 5–30% pre-built. We exploit the reuse.

## Cross-cutting rules

- **Migration-number collision:** 7 designs all claim `0077` (current max = 0076).
  Assign sequential numbers at implementation time (0077, 0078, …). NEVER assume 0077.
- **totp-2fa is Owner-gated** — the design's "Owner Decision Context" excludes TOTP
  (Owner chose email OTP). DO NOT dispatch without separate Owner authorization.
- **billing_settings KV pattern** (migration 0046 + `admin_billing_settings_handler.go`,
  allow-list + audit) is the template for platform-settings, pricing-catalog admin,
  and app-rate-limit policy stores — reuse, don't reinvent.
- **Pre-launch index policy:** defer `CREATE INDEX CONCURRENTLY` (a no-transaction
  migration landmine) — plain indexes (or no index in the first cut) are fine until
  data volume warrants it.
- **Money/auth/hot-path gaps** (tiered-billing, app-rate-limit, content-moderation,
  multi-oauth) get extra-careful review; money/schema → park for Owner before landing.

## Wave order (value × reuse ÷ risk, completable-first)

### Wave 1 — high-reuse, launch-value, completable
1. **usage-dashboard** (25%, read-only, trust/retention) ← IN PROGRESS. Data layer
   (`usage_records`, `ListUsageRecords`) exists; residual = aggregation queries +
   handlers. No migration in first cut (defer indexes).
2. **platform-settings** (20%, billing_settings template, foundational toggles, low-risk).
3. **multi-oauth** (25%, user acquisition: WeChat/DingTalk/LinuxDo; full PKCE/SSRF/
   session infra reusable via `OAuthProvider` iface; extend `normalizeSocialProvider`).

### Wave 2 — monetization + enterprise
4. **pricing-catalog** (30%, `RateTableSource` + `modelsync.HTTPFetcher` reusable).
5. **per-key-controls** (20%, quota engine reusable; groups/reveal/batch-revoke new).
6. **app-rate-limit** (20%, `gateway.TokenBucket` + `ipBucketRegistry` reusable; hot-path).

### Wave 3 — build-new, larger
7. **tiered-billing** (25%, money-path; DSL + funding-source resolver; Owner-park).
8. **notifications** (5%, only email transport reusable; channels + balance-low worker).
9. **content-moderation** (10%, hot-path screener; only error-class + billing reusable).
10. **ops-suite** (10%, internal ops; alert-rules + synthetic-monitor + scheduled-test).

### Blocked
- **totp-2fa** — Owner approval required.
- **AUTH-169 user role-assignment endpoint** — Owner design decision required (parked
  2026-06-14). Triple-mirror research (rule #16): the mature account-hub default
  tiebreaker deliberately exposes **no** role-change endpoint (role is write-protected
  in its admin user-update path); the one-api-lineage mirror does expose promote/demote
  but on a numeric Common/Admin/Root hierarchy with a "manage only lower roles" guard +
  explicit last-root protection — a model HUAKAI does not share; the relay mirror has no
  user/role concept at all. HUAKAI's `platform_admin`/`tenant_operator` are **operator**
  identities, not user-table roles, so "assign a role to a user" is a privilege-grant
  design choice (which roles are user-assignable, last-admin protection, self-demote,
  horizontal-privilege guard) that is auth-core high-risk and should be Owner-decided,
  not solo-landed. Surface options to Owner before building.

## Done
- gap #1 **tls-fp-crud** — admin CRUD for TLS fingerprint profiles (landed 4b9d7a4).
- gap #2 **relay-log** — verified already-covered; token residual landed (3142c07).
- **OPS-002 latency-SLO alerting** — TTFT p95/p99 threaded into the per-tenant alert
  metric snapshot (`usage.latency_p95_ms` / `usage.latency_p99_ms`), so latency
  regressions are alertable like success/error rate already were. Additive query
  columns only, no migration. Strong + mutation-verified unit & integration_pg tests.
- **SEC-084 private-IP passthrough kill-switch** — master env toggle
  `HUAKAI_PASSTHROUGH_PRIVATE_IPS_ENABLED` (default on = unchanged); an explicit
  `false` force-denies every private-IP host regardless of the per-host allowlist
  (emergency SSRF lockdown that can only tighten, never widen). Self-contained in
  `internal/ssrfpolicy`, no schema. Mutation-verified: dropping the guard lets a
  disabled toggle admit an allowlisted private host -> test RED.
