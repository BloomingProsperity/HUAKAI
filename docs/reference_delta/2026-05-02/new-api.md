# New API reference delta

## Repo snapshot

- Repo: `.omc/reference-src/new-api`
- Branch: `main`
- Commit: `dac55f0fdeb1`
- Tag: `v1.0.0-rc.2`
- File count: `1876`
- State: clean.

## Source areas read

- Billing expression engine: `.omc/reference-src/new-api/pkg/billingexpr/*`
- Claude usage DTO: `.omc/reference-src/new-api/dto/claude.go`
- Channel model/settings and Vertex support: `.omc/reference-src/new-api/model/channel.go`, `.omc/reference-src/new-api/dto/channel_settings.go`, `.omc/reference-src/new-api/relay/channel/vertex/*`
- API admin routes: `.omc/reference-src/new-api/router/api-router.go`
- Body/disk cache: `.omc/reference-src/new-api/common/body_storage.go`, `.omc/reference-src/new-api/pkg/cachex/hybrid_cache.go`
- Web settings and pricing UI: `.omc/reference-src/new-api/web/default/src/features/system-settings/*`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | Billing expression engine has token variables for request length and cache-token dimensions such as `CR`, `CC`, and `CC1h`. | `.omc/reference-src/new-api/pkg/billingexpr/types.go:15`, `.omc/reference-src/new-api/pkg/billingexpr/run.go:14`, `.omc/reference-src/new-api/pkg/billingexpr/billingexpr_test.go:430` |
| source-confirmed | Billing expressions are compiled and cached with a bounded cache. | `.omc/reference-src/new-api/pkg/billingexpr/compile.go:14`, `:29`, `:75` |
| source-confirmed | Tests cover tiered length pricing and image/audio/cache-token variables. | `.omc/reference-src/new-api/pkg/billingexpr/billingexpr_test.go:957`, `:1010` |
| source-confirmed | Claude usage DTO tracks cache creation/read tokens and 5m/1h creation-token variants. | `.omc/reference-src/new-api/dto/claude.go:558`, `:569` |
| source-confirmed | Channel settings include Vertex key type and Vertex credentials; channel model supports group, used quota, parameter override, and cache-related fields. | `.omc/reference-src/new-api/dto/channel_settings.go:12`, `.omc/reference-src/new-api/model/channel.go:38`, `:899` |
| source-confirmed | API exposes pricing, channel affinity cache, disk cache, secure channel key, token key, and batch key operations. | `.omc/reference-src/new-api/router/api-router.go:33`, `:177`, `:198`, `:218`, `:259`, `:264` |
| source-confirmed | Body storage can spill request bodies to disk, use thresholds, and clean old cache files. | `.omc/reference-src/new-api/common/body_storage.go:100`, `:243`, `:311` |
| source-confirmed | Hybrid cache supports namespace, Redis/local fallback, TTL set, purge, and prefix delete. | `.omc/reference-src/new-api/pkg/cachex/hybrid_cache.go:20`, `:80`, `:111`, `:159` |
| source-confirmed | Admin UI exposes disk cache performance settings, clear action, stats, and disk-cache status. | `.omc/reference-src/new-api/web/default/src/features/system-settings/maintenance/performance-section.tsx:48`, `:186`, `:653`, `:747` |
| source-confirmed | Admin UI exposes payment integrations including Epay, Stripe, and Creem sections. | `.omc/reference-src/new-api/web/default/src/features/system-settings/integrations/payment-settings-section.tsx:544`, `:664`, `:918`, `:1124` |

## Inferred features

- inferred: New API is a strong reference for pricing expression UX and cache-token billing, not just static model-price tables. Basis: `.omc/reference-src/new-api/pkg/billingexpr/billingexpr_test.go:430` and `.omc/reference-src/new-api/web/default/src/features/system-settings/models/model-ratio-dialog.tsx:1225`.
- inferred: Disk/hybrid cache is used as operational infrastructure, so HUAKAI should make cache visibility and cleanup admin-facing if it adopts similar behavior.

## Open questions

- open-question: Need deeper reading of relay billing settlement to prove how billing-expression results are persisted and audited.
- open-question: Need clean-room spec for Vertex credentials and URL building; provider-specific credential shapes can leak implementation detail.

## HUAKAI delta

- `F-BILL-001` and `F-BILL-003` mention pricing and cache prompt billing, but they do not yet specify expression safety, compile-cache invalidation, versioning, or test vectors.
- Current HUAKAI plan mentions real pricing as L0/L1 need, but New API shows production pricing is not a flat table once cache, image, audio, and tiered rules are included.
- Cache and channel-affinity operations are missing from HUAKAI's admin incident workflow.

## Suggested Feature IDs

| Feature ID | Name | Level | Delta |
| --- | --- | --- | --- |
| `F-PRICE-EXPR-001` | Versioned pricing expression engine | L2/L3 | Safe expression DSL, compile cache, variables for input/output/cache/image/audio, fixture-based tests, rollback. |
| `F-CACHE-OPS-001` | Channel affinity and disk cache operations | L2 | Admin view, clear actions, TTL, namespace, stats, and cleanup job. |
| `F-VERTEX-001` | Vertex provider credential profile | L3 | JSON/API-key credential modes, model override, URL builder behavior, redacted admin display. |
| `F-PAY-METHOD-001` | Payment provider configuration matrix | L2/L3 | Stripe/Epay-like provider instances, method enablement, webhook status, and test mode. |
