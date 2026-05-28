# 2026-05-28 OAUTH-WEB-1 R2 Gemini Allowlist Strictness

| Owner directive | "OAUTH-WEB-1 R2 review S1 fix: make Gemini admin-callback allowlist key PROVABLY strict" |
| Scope | In: existing `backend/internal/credentialacq/gemini_oauth.go` allowlist-key normalization and targeted tests in `backend/internal/credentialacq/gemini_oauth_test.go`. Out: external reference source, commit, redirect flow helpers, auth/billing/quota/schema/deploy changes. |
| Success criteria | Malformed query percent-escape and URL userinfo are rejected even when the bare path is allowlisted; bare redirect and single non-empty `flow_id` redirect remain accepted; required `go test` and `go build` commands pass. |
| Time estimate | 20-35 minutes wall clock; one Codex implementation pass. |
| Blast radius | Gemini OAuth HTTPS admin redirect validation only. Failure could either reopen lenient allowlist bypass or over-reject valid bare/single-`flow_id` admin callbacks. |
| Failure modes | Using `parsed.Query()` would keep malformed-query bypass; missing `parsed.User` check would allow userinfo; overbroad checks could reject valid callbacks; editing existing dirty files could overwrite prior work. Mitigation: inspect diff first, add discriminating tests before implementation, keep production edit to `geminiAdminCallbackAllowlistKey`, run targeted package test plus full backend build. |
| Decision points | No Owner sign-off expected unless implementation requires high-risk scope expansion, new dependency, schema/auth/billing/quota/deploy changes, or deletion. |
| Pre-execution checklist | 1. Confirm dirty worktree and preserve existing edits. 2. Add tests for malformed query and userinfo with positive controls. 3. Run targeted tests and confirm the new tests fail against current lenient parser. 4. Replace `parsed.Query()` with `url.ParseQuery(parsed.RawQuery)` plus error check and add userinfo/opaque rejection. 5. Run required verification commands. |

Concrete execution order:

1. Add `TestGeminiRedirectURIRejectsMalformedQueryEvenWhenPathAllowlisted`.
2. Add `TestGeminiRedirectURIRejectsUserInfoEvenWhenPathAllowlisted`.
3. Run the two tests to confirm at least one expected failure under the current parser.
4. Update `geminiAdminCallbackAllowlistKey` with the seven fail-closed checks.
5. Run `GOCACHE=/tmp/go-build go test ./internal/credentialacq/ -count=1 -timeout 90s`.
6. Run `GOCACHE=/tmp/go-build go build ./...`.
