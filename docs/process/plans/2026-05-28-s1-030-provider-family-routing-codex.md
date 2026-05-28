# 2026-05-28 S1-030 provider-family routing fix

| Owner directive | "Pool account selection can route a request for a model to an account whose provider/vendor/protocol-family does not serve it... AccountSnapshot carries provider/vendor/protocol family; BOTH the SQL selection and the in-memory selector validate the match." |
| Scope | In: add exact protocol-family propagation through `SelectionRequest`, SQL candidate filtering, generated billing query shape, DB account source mapping, router gate fallback, and discriminating tests. Out: schema migrations, auth core, billing ledger, quota enforcement, dependency changes, commits/pushes. |
| Success criteria | Router test fails before the gate and passes after; SQL text/generation shape proves provider join + exact-family predicate; `go test ./internal/pool/... ./internal/db/billing/... ./internal/gatewayhttp/... -count=1` and `go build ./...` are run from `backend/`. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus verification. |
| Blast radius | Medium: candidate selection affects production gateway routing and PASR/default selector eligibility. Existing frozen package `backend/internal/gatewayhttp` will be edited only in `chat_completions_dispatch.go`; no new file in frozen packages. |
| Failure modes | Protocol vocabulary mismatch could over-filter to 503; mitigation: use `ex.resolved.ProtocolFamily` directly and compare to `providers.upstream_protocol`. Generated SQL drift could break build; mitigation: run sqlc if available, otherwise hand-edit generated file and compile. Weak tests could pass despite removing guard; mitigation: wrong-family account has higher priority so missing gate selects the wrong account. |
| Decision points | None requiring Owner sign-off in this work unit. The Owner already directed exact `ProtocolFamily` over lossy `Vendor`; no reference-project behavior decision is introduced. |
| Pre-execution checklist | 1. Read `AGENTS.md` and `CLAUDE.md`. 2. Confirm actual SQL/migration paths. 3. Confirm `providers.upstream_protocol` and `providers.deleted_at` exist. 4. Confirm target packages are not frozen or are existing-file edits only. 5. Write RED router test before production code. 6. Prefer `sqlc generate` from `backend/`; fall back to generated-file hand edit only if unavailable. |

## Concrete Execution Order

1. Add a router test with two same-tenant healthy accounts: higher-priority `openai_chat`, lower-priority `anthropic_messages`; request `ProtocolFamily: "anthropic_messages"` and assert selected account is the anthropic one.
2. Run the focused router test and record the RED failure selecting the wrong-family account or missing field compile failure.
3. Add `ProtocolFamily` to `SelectionRequest` and `AccountSnapshot`.
4. Add a `ProtocolFamilyGate` to `DefaultGateChain` and `GateChain.ordered`.
5. Update `ListEligibleAccountsByPoolGroup` to join `providers`, select `p.upstream_protocol`, and filter with `requested_protocol_family = '' OR p.upstream_protocol = requested_protocol_family`.
6. Regenerate sqlc from `backend/`; if unavailable, hand-edit `internal/db/billing/pool_accounts.sql.go` and `querier.go` consistently.
7. Update `DBAccountSource` to pass `req.ProtocolFamily` and populate `AccountSnapshot.ProtocolFamily`.
8. Update `chat_completions_dispatch.go` to set `SelectionRequest.ProtocolFamily` from `ex.resolved.ProtocolFamily`.
9. Add or strengthen a generated SQL text test proving provider join and family predicate are present.
10. Run focused tests, then the Owner-specified full test/build commands.
