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

## Done
- gap #1 **tls-fp-crud** — admin CRUD for TLS fingerprint profiles (landed 4b9d7a4).
- gap #2 **relay-log** — verified already-covered; token residual landed (3142c07).
