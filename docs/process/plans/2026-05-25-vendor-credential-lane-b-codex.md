# 2026-05-25 vendor credential Lane B Codex plan

| Owner directive | "你是 codex Lane B, 实施 5 vendor credential implementation (clean-room paraphrase from local reference snapshots)." |
| Scope | In: `backend/internal/credentialacq` exchanger wiring/tests, small provider bootstrap/refresher support under `backend/internal/provider/{openai_codex,antigravity,gemini,windsurf,kiro}`, license/source evidence notes. Out: frozen packages, schema, auth core, billing/quota, cursor protobuf implementation, commits. |
| Success criteria | Five requested vendors are either implemented with discriminating tests or explicitly deferred/blocked with reason. `cd backend && go build ./...`, `go vet ./...`, and `go test ./internal/credentialacq/... ./internal/provider/... -count=1 -race` are attempted and reported with exact status. |
| Time estimate | 90 minutes target; Codex time mostly spent on source evidence, tests, narrow exchanger implementation, and verification. |
| Blast radius | Medium: credential acquisition payloads and OAuth/token request construction can affect operator onboarding for these vendor modes. No production secrets, schema, billing, quota, or frozen-package file additions. |
| Failure modes | Source clone can fail under sandbox; mitigation: do not invent behavior, record fallback/deferred status. Public client constants can become stale; mitigation: keep operator-config required/fail-closed defaults. Tests can become non-discriminating; mitigation: each vendor test checks a positive required field and a negative/wrong-shape path. |
| Decision points | High-risk changes are out of scope. If a vendor reference license cannot be verified, do not read/implement from that source; defer with `docs/process/reviews/DEFERRED-vendor-<name>-license-blocked.md`. |
| Pre-execution checklist | 1. Confirm start signal and branch. 2. Read HUAKAI rules and relevant credential acquisition/provider files. 3. Verify reference license/source evidence. 4. Write failing tests before production changes. 5. Implement only narrow exchanger/provider support. 6. Run requested verification. |

## Clean-Room Evidence Plan

- CLIProxyAPI evidence uses the GitHub-visible short SHA `router-for-me/CLIProxyAPI@50d19e2`, whose commit list shows HEAD on 2026-05-23 (`docs(readme): add APIKEY.FUN sponsorship details to README files`), within the 90-day first-cite window for 2026-05-25. MIT license evidence: `router-for-me/CLIProxyAPI@50d19e2:LICENSE:1`, `router-for-me/CLIProxyAPI@50d19e2:LICENSE:6-14`.
- OpenAI Codex device behavior observed: asks one OpenAI device endpoint for a user/device identifier, displays the user code and verification URL, polls the token endpoint, then exchanges the returned authorization code with a PKCE verifier against the OAuth token endpoint. Evidence anchors: `router-for-me/CLIProxyAPI@50d19e2:sdk/auth/codex_device.go:24-33`, `router-for-me/CLIProxyAPI@50d19e2:sdk/auth/codex_device.go:71-109`, `router-for-me/CLIProxyAPI@50d19e2:sdk/auth/codex_device.go:171-227`; token exchange endpoint/parameters: `router-for-me/CLIProxyAPI@50d19e2:internal/auth/codex/openai_auth.go:22-28`, `router-for-me/CLIProxyAPI@50d19e2:internal/auth/codex/openai_auth.go:95-180`.
- Antigravity behavior observed: Google OAuth callback flow, Google token endpoint, userinfo fetch, and project discovery after token acquisition. Evidence anchors: `router-for-me/CLIProxyAPI@50d19e2:sdk/auth/antigravity.go:46-70`, `router-for-me/CLIProxyAPI@50d19e2:sdk/auth/antigravity.go:146-188`; constants/scopes/endpoints: `router-for-me/CLIProxyAPI@50d19e2:internal/auth/antigravity/constants.go:4-25`.
- Gemini behavior observed: Google OAuth callback flow with Google endpoint and token storage containing OAuth token material plus project/email metadata. Evidence anchors: `router-for-me/CLIProxyAPI@50d19e2:sdk/auth/gemini.go:30-72`; constants/scopes/config: `router-for-me/CLIProxyAPI@50d19e2:internal/auth/gemini/gemini_auth.go:29-41`, `router-for-me/CLIProxyAPI@50d19e2:internal/auth/gemini/gemini_auth.go:91-125`, `router-for-me/CLIProxyAPI@50d19e2:internal/auth/gemini/gemini_auth.go:171-180`.
- WindsurfAPI source behavior remains not used for this plan. GitHub commit view shows `dwgx/WindsurfAPI@c028576a56b9fa19f84810643610cae4af824238` on 2026-05-13 (`release: 2.0.96`), within the 90-day first-cite window, but no `dwgx/WindsurfAPI` source file behavior is claimed here because the source clone/API verification path is unavailable in this sandbox.

## Execution Order

1. Add/adjust credential acquisition tests for vendor-specific positive and negative behavior.
2. Implement narrow exchanger helpers in `backend/internal/credentialacq` or a non-cyclic subpackage only if the existing package boundary permits it. Do not add files to frozen packages.
3. Add provider bootstrap hardening where existing provider packages already own configuration defaults.
4. Add deferred review tickets for vendors whose references cannot be license/source verified.
5. Run `gofmt` and requested Go verification commands.

## Assumptions

- The explicit Owner directive is the start signal for this Lane B implementation.
- Current workspace branch is the intended working branch; no new worktree is created because the Owner specified the branch and sandbox prevents edits outside writable roots.
- `backend/internal/credentialacq/exchangers/` as a Go subpackage would create an import-cycle risk if it imports parent types and parent registry imports it. If this remains true after final inspection, implementation will stay in the existing `credentialacq` package and the deviation will be reported.

## Manual Verification Note

- Sandbox API verification limit: `curl -fsS https://api.github.com/repos/router-for-me/CLIProxyAPI` and `curl -fsS https://api.github.com/repos/dwgx/WindsurfAPI` both failed with `Operation not permitted` on 2026-05-25, so `archived`, `disabled`, and `pushed_at` could not be verified through the GitHub REST API inside this lane.
- Browser-visible GitHub commit pages were used only to pin HEAD candidates and recency: `router-for-me/CLIProxyAPI@50d19e2` dated 2026-05-23 and `dwgx/WindsurfAPI@c028576a56b9fa19f84810643610cae4af824238` dated 2026-05-13.
- Owner must re-run `https://api.github.com/repos/router-for-me/CLIProxyAPI` and `https://api.github.com/repos/dwgx/WindsurfAPI` before merge to verify `archived:false`, `disabled:false`, `pushed_at` freshness, and the full 40-character CLIProxyAPI HEAD SHA for the `50d19e2` short-SHA citations. If either repo is archived/disabled/stale, keep the feature outcome but reopen the evidence citation gate before release.
