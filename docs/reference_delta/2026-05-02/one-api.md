# one-api reference delta

## Repo snapshot

- Repo: `.omc/reference-src/one-api`
- Branch: `main`
- Commit: `8df4a2670b98`
- Tag: `8df4a26`
- File count: `548`
- State: clean.

## Source areas read

- Relay routes: `.omc/reference-src/one-api/router/relay.go`
- Channel monitoring: `.omc/reference-src/one-api/monitor/*`
- Channel and ability models: `.omc/reference-src/one-api/model/channel.go`, `ability.go`, `cache.go`
- Token and user quota models/controllers: `.omc/reference-src/one-api/model/token.go`, `model/user.go`, `controller/token.go`, `controller/user.go`
- Redemption models/controllers: `.omc/reference-src/one-api/model/redemption.go`, `controller/redemption.go`
- Process config: `.omc/reference-src/one-api/main.go`

## Source-confirmed features

| Status | Feature | Evidence |
| --- | --- | --- |
| source-confirmed | Relay supports OpenAI-compatible models, proxy, completions, chat completions, edits, image generation, embeddings, audio speech/transcription/translation, plus explicit "not implemented" OpenAI endpoints. | `.omc/reference-src/one-api/router/relay.go:14`, `:20`, `:23`, `:35` |
| source-confirmed | Gateway applies gzip decode middleware before relay handling. | `.omc/reference-src/one-api/router/relay.go:12` |
| source-confirmed | Channel monitor can consume success/failure events and disable a channel after too many failures. | `.omc/reference-src/one-api/main.go:79`, `.omc/reference-src/one-api/monitor/metric.go:40`, `.omc/reference-src/one-api/monitor/channel.go:31` |
| source-confirmed | Error-based disable rules include 401, insufficient quota, authentication error, permission error, and forbidden. | `.omc/reference-src/one-api/monitor/manage.go:11` |
| source-confirmed | Channel model tracks type, key, status, priority, weight, models, group, base URL, and used quota. | `.omc/reference-src/one-api/model/channel.go:34`, `:190`, `:201` |
| source-confirmed | Ability cache can choose random satisfied channel by group/model. | `.omc/reference-src/one-api/model/ability.go:22`, `.omc/reference-src/one-api/model/cache.go:155`, `:227` |
| source-confirmed | Token model supports expiry, remaining quota, used quota, unlimited quota, model allow-list, subnet restriction, and pre/post consume accounting. | `.omc/reference-src/one-api/model/token.go:23`, `:74`, `:173`, `:217` |
| source-confirmed | Token admin supports list/search/count, subnet validation, create, and update. | `.omc/reference-src/one-api/controller/token.go:24`, `:97`, `:110`, `:144`, `:216` |
| source-confirmed | User model supports quota, used quota, request count, affiliate fields, and default token generation. | `.omc/reference-src/one-api/model/user.go:47`, `:148`, `:374`, `:411` |
| source-confirmed | Redemption code lifecycle includes create, batch count cap, status, quota, and update. | `.omc/reference-src/one-api/model/redemption.go:20`, `.omc/reference-src/one-api/controller/redemption.go:79`, `:157` |

## Inferred features

- inferred: one-api is a good reference for minimum viable OpenAI-compatible channel management: simple status, priority, weight, model/group ability cache, and quota accounting. Basis: `.omc/reference-src/one-api/model/ability.go:22` and `.omc/reference-src/one-api/model/token.go:217`.
- inferred: Gzip decode is a feature, but HUAKAI should add a decompression-bomb guard instead of merely copying "decode then relay." Basis: `.omc/reference-src/one-api/router/relay.go:12`.

## Open questions

- open-question: The exact request body size/decompression guard is not proven from the route surface. Need middleware implementation reading before adopting behavior.
- open-question: Whether failure metrics are durable enough for restart-safe channel health is unclear from the monitor files alone.

## HUAKAI delta

- HUAKAI has API key and quota concepts in the L1 matrix, but model allow-list, subnet restriction, expiry cleanup, and remaining-quota UX need explicit acceptance tests.
- `F-GW-004` retry/fallback is too high-level. one-api has concrete channel disable/re-enable behavior that should become separate health acceptance criteria.
- Request compression is missing as a first-class commercial edge case. Without a limit after decompression, gzip support can become a production risk.

## Suggested Feature IDs

| Feature ID | Name | Level | Delta |
| --- | --- | --- | --- |
| `F-REQ-001` | Request body compression decode guard | L1/L2 | Support expected gzip bodies, enforce post-decompression byte limits, reject suspicious ratios, log sanitized reason. |
| `F-KEY-SCOPE-001` | API key scope and lifecycle controls | L1/L2 | Model allow-list, subnet restriction, expiry, remaining quota, unlimited flag, and cleanup. |
| `F-CH-HEALTH-001` | Channel failure auto-disable/re-enable | L1/L2 | Define fail counters, disable reasons, success-rate thresholds, and admin recovery. |
| `F-REDEEM-001` | Redeem code and top-up lifecycle | L2 | Batch creation cap, redeem audit, status, quota conversion, and user history. |
