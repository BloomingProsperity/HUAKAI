# 2026-06-07 Kimi OAuth Provider

| Owner directive | "TASK: Add Kimi/Moonshot UPSTREAM provider -- device-code OAuth acquisition + runtime relay adapter (branch fix/kimi-oauth)." |
| Scope | In: HUAKAI-local Kimi credential acquisition wiring, Kimi handler mode, Kimi ModePlan, default provider registry passthrough registration, and unit tests. Out: `/home/ubuntu/refs`, commits, migrations, production secrets, auth core redesign, billing/quota/schema/deploy changes, and new files in frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto`. |
| Success criteria | `kimi/kimi_oauth` resolves in the default exchanger registry; Kimi device-code config uses the Owner-provided client ID and auth.kimi.com device/token endpoints; the Kimi handler is refreshable and accepts access or refresh token payloads; `kimi_chat` resolves to an OpenAI-compatible passthrough endpoint at `https://api.kimi.com/coding/v1/chat/completions`; Kimi OAuth endpoint overrides reject non-`kimi.com` hosts and non-HTTPS URLs; required build/vet/unit commands either pass or report exact failures. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation session. |
| Blast radius | Credential acquisition defaults, credentialstore mode registry, and default provider protocol registration. Runtime gateway, quota, billing, auth core, schema, and deployment paths should remain untouched. |
| Failure modes | Clean-room leakage from reference material: mitigated by not reading `/home/ubuntu/refs` and using only Owner-provided constants plus local HUAKAI patterns. Frozen-package violation: mitigated by editing only existing non-frozen package files. Weak tests: mitigated by mutation comments and assertions on exact client ID, endpoint, mode key, runtime material, and host rejection. SSRF/auth leak drift: mitigated by static Kimi endpoint host validation plus existing SSRF-protected device-code client. |
| Decision points | Stop for Owner confirmation if implementation requires a migration, new runtime dependency, frozen-package new file, payment/auth-core/quota changes, or reading reference source. No additional confirmation needed for low/medium-risk local registry/test/doc edits. |
| Pre-execution checklist | 1. Read `docs/RULES.md` and relevant HUAKAI-local files. 2. Confirm branch/worktree and do not modify unrelated untracked files. 3. Add RED tests first. 4. Implement only enough code to satisfy tests. 5. Run `gofmt`. 6. Run targeted package tests. 7. Run `GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...` and `go vet ./...` from `backend`. 8. Produce the required 8-point Chinese Owner report without committing. |

## Concrete Execution Order

1. Extend existing tests:
   - `backend/internal/credentialacq/oauth_devicecode_test.go`: assert the Kimi mode key resolves, Kimi config constants match the Owner-provided values, and Kimi rejects non-`kimi.com` OAuth endpoints.
   - `backend/internal/credentialstore/types_test.go`: assert `kimi/kimi_oauth` handler is refreshable and accepts access or refresh token payloads.
   - `backend/internal/provider/registrydefault/default_test.go`: assert `kimi_chat` is registered and builds an OpenAI-compatible request to `https://api.kimi.com/coding/v1/chat/completions`.
2. Run targeted tests and confirm RED failures caused by missing Kimi constants/registration.
3. Edit `backend/internal/credentialstore/types.go`:
   - Add `VendorKimi = "kimi"` and `AuthModeKimiOAuth = "kimi_oauth"`.
   - Add a refreshable handler using `RuntimeSessionToken` with `anyOf: []string{"access_token", "refresh_token"}` and `allowGrace: true`.
4. Edit `backend/internal/credentialacq/types.go`:
   - Add a Kimi OAuth mode plan using `FlowKindOAuth`, `ClientSourcePublicCLI`, and OAuth/JSON import helpers.
5. Edit `backend/internal/credentialacq/oauth_devicecode.go` and `vendor_exchangers.go`:
   - Add Kimi constants and a Kimi-specific device-code exchanger wrapper that applies the Owner-provided OAuth config.
   - Reuse `startDeviceAuthorization` and the existing device-code poll loop.
   - Reject non-HTTPS or non-`kimi.com` auth/token endpoint overrides before any HTTP request.
6. Edit `backend/internal/provider/registrydefault/default.go`:
   - Add `ProtocolKimiChat = "kimi_chat"`.
   - Register `&provider.OpenAICompatPassthroughAdapter{PlatformName: "kimi", Endpoint: "https://api.kimi.com/coding/v1/chat/completions"}`.
7. Run `gofmt` on touched Go files.
8. Run required verification from `backend`:
   - `/usr/local/go/bin/go test ./internal/credentialacq ./internal/credentialstore ./internal/provider/registrydefault -count=1`
   - `GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
   - `/usr/local/go/bin/go vet ./...`
