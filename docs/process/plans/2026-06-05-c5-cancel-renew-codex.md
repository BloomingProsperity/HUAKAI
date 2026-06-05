# 2026-06-05 C5 Cancel-Renew Codex Plan

| Owner directive | "商业 C5 cancel-renew(补完订阅关续订)" |
| --- | --- |
| Scope | Add `auto_renew` storage for HUAKAI `user_subscriptions`, expose self-service cancel-renew, update OpenAPI, and commit after gates. In scope: `backend/sql/migrations`, `backend/internal/subscription`, `backend/internal/subscriptionhttp`, `docs/openapi/openapi.yaml`, and focused `backend/cmd/gateway` consistency tests if needed. Out of scope: frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto`; reference source; payment, billing ledger, quota enforcement, auth core, and production deployment. |
| Success criteria | New migration number is the next unused migration pair; `UserSubscription.AutoRenew` reads true by default and can be set false for the caller's active subscription; `POST /v1/users/me/subscriptions/cancel-renew` uses session identity and does not cancel active entitlement; `/me` returns the real flag; OpenAPI declares the route; required Go gates pass; commit is created; post-commit mutation proves `SetAutoRenew` persistence is covered. |
| Time estimate | 1-2 hours wall clock; one Codex implementation pass with TDD, verification, review, commit, mutation check. |
| Blast radius | Additive schema column and small subscription-domain/API changes. Incorrect implementation could expose cross-user mutation, break subscription reads, or diverge OpenAPI routes. |
| Failure modes | Missing tenant/user predicate could flip another user's renewal flag; omitting the new column from SELECT/scan could leave `/me` stale; migration number collision could break migrate order; touching frozen packages would violate structure rules; weak tests could pass if storage no-ops. Mitigation: tenant/user-scoped tests, scan/update assertions, grep migration numbers, no frozen package edits, mutation check after commit. |
| Decision points | None expected. High-risk areas remain out of scope: no billing ledger, no quota enforcement, no auth-core changes, no destructive migration, no runtime dependency addition. |
| Pre-execution checklist | 1. Confirm worktree state and existing highest migration. 2. Read local subscription store/service/http patterns only. 3. Write failing tests for storage/service and user HTTP. 4. Implement minimal changes. 5. Update OpenAPI. 6. Run `gofmt`, build, vet, focused tests, cmd/gateway consistency. 7. Stage intended diff, run Codex review command. 8. Commit with required trailer. 9. Mutate `SetAutoRenew` persistence, run discriminating test, then restore with `git checkout`. |

## Concrete Execution Order

1. Add failing tests in `backend/internal/subscription` for `SetAutoRenew(ctx, tenantID, userID, false)` mutating only the tenant/user current active subscription and preserving cross-user isolation.
2. Add failing tests in `backend/internal/subscriptionhttp` for `POST /cancel-renew` session-scoped mutation and `/me` returning the current subscription's `auto_renew`.
3. Add migration pair `0094_subscription_auto_renew.{up,down}.sql` after confirming no `0094` exists.
4. Add `AutoRenew bool` to `subscription.UserSubscription`, store interface/service method, memory store update, postgres column select/scan/insert/renew/update paths, and tenant/user-scoped active update.
5. Mount `POST /cancel-renew` in `subscriptionhttp`, map errors through existing subscription error handling, and keep active subscription status/expiry untouched.
6. Update `docs/openapi/openapi.yaml` by adding the cancel-renew path and changing `/me` wording from fixed false to real persisted flag.
7. Run the required gates with `/usr/local/go/bin/go` and `GOCACHE=/tmp/go-build`.
8. Stage only intended files, run `codex exec review --uncommitted --full-auto --sandbox read-only` or nearest accepted read-only equivalent if the CLI syntax differs, then commit.
9. Post-commit mutation: temporarily remove/neutralize the postgres or memory persistence assignment in `SetAutoRenew`, run the discriminating test and confirm it fails, then restore with `git checkout`.

## Clean-Room Notes

This implementer plan uses only HUAKAI repository files and the Owner brief. It does not read or cite reference source, and it does not copy external schemas, tests, identifiers, comments, or implementation structure.
