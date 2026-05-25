# 2026-05-24 OAuth Callback Registry Fix

| Owner directive | "[OWNER AUTHORIZED 2026-05-24T10:45Z workspace-write — 第三发 AI review #1 修复:OAuth HTTP 入口未接 ExchangerRegistry]" |
| Scope | In: existing OAuth credential acquisition HTTP callback handlers, their existing tests, and production wiring for the exchanger registry. Out: new dependencies, schema changes, auth/billing/quota core, and new files under frozen packages. |
| Success criteria | Canonical callback uses `CompleteOAuthCallbackWithRegistry`, calls a registered exchanger, finalizes a credential record, returns 200 on success, and returns 422 with failed audit when the registry lacks the flow vendor/mode. Required `go test ./internal/gatewayhttp/... ./internal/credentialacq/...`, `go build ./...`, and `echo DONE` complete successfully. |
| Time estimate | 45-75 minutes wall clock; one Codex executor work unit. |
| Blast radius | Admin credential acquisition OAuth callback behavior and gateway startup dependency wiring. |
| Failure modes | Callback may validate without finalizing; registry miss may fall back to defaults; tests may keep passing without proving exchanger invocation. Mitigation: add discriminating tests that remove request credentials, count exchanger calls, assert stored credential metadata, and assert 422 for unregistered `cursor/oauth`. |
| Decision points | None pending. Owner already selected the registry-backed callback path. No reference-project comparison is needed because this plan surfaces no new Owner decision and asserts no non-HUAKAI reference-project behavior. |
| Pre-execution checklist | 1. Read handler, registry, finalizer, and gateway wiring. 2. Add failing tests before production code. 3. Modify only existing files in frozen `gatewayhttp`. 4. Do not run `git add`, `git commit`, or `git push`. |

## Concrete Execution Order

1. Change `admin_credential_acquisition_handler_test.go` so canonical callback sends only `state` and `code`, proves the registered exchanger is called, and proves the credential creator receives the exchanged payload.
2. Add a mutation-style test for an OAuth flow with `vendor=cursor`, `auth_mode=oauth`, and a registry that only has the OpenAI exchanger; expected result is HTTP 422 plus failed lifecycle audit.
3. Add an `Exchangers *credentialacq.ExchangerRegistry` dependency to `AdminCredentialAcquisitionDeps`.
4. Replace both HTTP callback paths with `CompleteOAuthCallbackWithRegistry`; canonical callback finalizes the returned candidate.
5. Map missing exchanger errors to an explicit 422 HTTP response.
6. Add a registry field to gateway `deps`, initialize it in `wiring.go` from `credentialacq.DefaultExchangerRegistry()` plus `anthropicoauth.RegisterInto`, and pass it in `routes.go`.
7. Run the required verification commands.
