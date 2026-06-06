# 2026-06-06 subscription-change-plan-codex

| Owner directive | "Add subscription CHANGE-PLAN (admin + user) to HUAKAI (branch fix/sub-change-plan). Verified partial: the override engine exists ... Reach CLOSURE. Entitlement path - idempotent, audited, guarded. No shortcuts." |
| Scope | In: HUAKAI-native subscription change-plan service/store path, admin and user HTTP routes, OpenAPI paths, focused unit tests, integration_pg tests. Out: reading `/home/ubuntu/refs`, schema migrations, frozen package new files, commits, integration_pg/socket execution by Codex. |
| Success criteria | Active subscription can swap to a new plan in one transaction, old quota policies close, new caps install, audit records one renewed event per request_id, downgrade guard rejects when not allowed, inactive subscriptions are rejected, admin and user routes pass actor/request context, OpenAPI includes both public routes, build/vet/unit checks pass locally. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation lane. |
| Blast radius | Subscription entitlement writes, quota policy ownership links, user/admin subscription HTTP contract, OpenAPI consistency tests. No billing ledger, auth core, production secrets, deployment scripts, or database schema changes. |
| Failure modes | Double application on retry: use audit request_id replay check before mutation. Two active quota policies: close active links/policies before installing new caps in the same tx. Unauthorized downgrade: reuse caps dominance guard through Activate semantics or equivalent store logic. Wrong target: require active and unexpired target subscription. OpenAPI drift: add both new paths. |
| Decision points | None requiring Owner sign-off if no migration and no new dependency are needed. Stop if implementing safely requires schema changes, frozen package new files, auth core changes, billing/quota-enforcement semantic changes, or real secret/deployment edits. |
| Pre-execution checklist | 1. Read `docs/RULES.md` and requested subscription files. 2. Confirm no `/home/ubuntu/refs` access. 3. Confirm target packages are not frozen and no new frozen-package files are added. 4. Write failing tests first. 5. Implement minimal store/service/http/OpenAPI changes. 6. Run targeted unit tests, build, vet, and available requested checks. |

## Concrete execution order

1. Add service/store input/result types for `ChangePlan`, plus `Store.ChangePlan`.
2. Add fast service/memory-store tests for validation, idempotency, downgrade guard, inactive rejection, and actor/request propagation.
3. Add `integration_pg` tests in the existing admin ops integration file for cap/policy swap, downgrade guard, request-id replay, and non-active rejection.
4. Run the new tests to confirm RED from missing APIs.
5. Implement PG `ChangePlan` using the existing serializable transaction pattern: lock target subscription, replay by `AuditSubscriptionRenewed` + request_id, verify active/non-expired, load target plan, enforce caps dominance unless downgrade is allowed, compute the same renewal expiry as activation, call `renewSubscriptionTx`, close old caps, install new caps, write `subscription_renewed` audit, and commit.
6. Implement memory-store mirror semantics for unit tests.
7. Add `Service.ChangePlan` validation and expose it through `subscriptionhttp.Service`.
8. Add admin route `POST /assignments/{id}/change-plan` and user route `POST /change-plan`; user route resolves current active subscription and calls `AllowDowngrade=false`.
9. Map `ErrDowngradeNotAllowed` to HTTP conflict.
10. Update `docs/openapi/openapi.yaml` for `/v1/admin/subscriptions/assignments/{id}/change-plan` and `/v1/users/me/subscriptions/change-plan`.
11. Run targeted tests and the requested build/vet commands. Do not commit.

## Assumptions

- "Higher tier" is represented by cap dominance, matching the existing `EnforceUpgradeOnly` activation semantics.
- Change-plan intentionally reuses `AuditSubscriptionRenewed` as the audit event because the enum already contains `subscription_renewed` and the Owner requested no migration.
- Same request_id idempotency is scoped to the target subscription and `subscription_renewed` event, matching existing admin-op patterns.
