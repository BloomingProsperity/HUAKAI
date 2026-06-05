# 2026-06-05 C6 Referral Reward Codex Plan

| Owner directive | "任务: 商业 C6 邀请返佣发放(MONEY, 极保守)" |
| Scope | In: implement `/home/ubuntu/decomp-referral-reward.md` HUAKAI C6 design in the existing invitation qualification hook, add minimal schema guard, config, discriminating tests, verification, commit. Out: reading `/home/ubuntu/refs`, freezing/lazy-thaw, per-user reward rates, transfer-to-withdrawable-balance split, frozen packages. |
| Success criteria | Qualifying a pending referral with a positive configured reward atomically creates one `referral_rewards` row, credits the referrer through the payment balance path, advances status to `rewarded`, updates `tier_progress`, and records reward audit evidence. Replays and billing-event repeats stay single-credit. Zero reward still qualifies without issuing money. Tenant scope is enforced. Required Go gates pass. |
| Time estimate | 2-4 wall-clock hours; one Codex implementation lane. |
| Blast radius | Money tables (`payment_orders`, `payment_credits`, `billing_events`, `user_balances`), referral tables, and migration constraints. A bug could double-credit, skip qualified referrals, or cross tenant boundaries. |
| Failure modes | Duplicate reward on retry: guard with status transition plus unique `referral_rewards(tenant_id, referral_id)` plus payment order idempotency key. Partial qualify without money/audit: perform PG work in one serializable transaction. Config parsing error: default to zero-off and reject invalid negative values by skipping reward safely. Existing `receipt_id NOT NULL`: migrate to nullable because C6 reward issuance is not a user request cost receipt. |
| Decision points | No Owner mid-flight decision expected because the Owner explicitly authorized this money/schema task. Stop only if there is no single qualify hook or no safe local payment balance path. |
| Pre-execution checklist | 1. Read `/home/ubuntu/decomp-referral-reward.md` fully. 2. Read `docs/RULES.md` clean-room and money rules. 3. Inspect only HUAKAI local invitation/payment/billing code. 4. Confirm frozen packages are not touched. 5. Add tests before production implementation. 6. Run required gates before commit. 7. Stage and run per-commit Codex review before commit. 8. Commit, then run mutation self-check and restore mutation. |

## Concrete Execution Order

1. Add package-level unit tests around `Service.QualifyPendingReferral` using an in-memory store that records reward issue effects. These tests must fail before implementation because the current service only calls the old `qualifyPendingReferral` interface.
2. Add PG-focused unit tests for migration text and helper behavior where possible; keep integration tests build-tagged only if a live database is required.
3. Add migration `0094_referral_reward_issuance`:
   - unique index on `(tenant_id, referral_id)` for `referral_rewards`;
   - nullable `receipt_id`;
   - allow payment credit `reason_class='referral_reward'`.
4. Extend invitation qualification with a new optional store interface for reward-capable stores, parse `HUAKAI_REFERRAL_REWARD_USD_MICROS`, and default to zero-off.
5. Implement `PostgresStore.qualifyPendingReferral` as a serializable transaction:
   - lock the pending referral by tenant/referee/status and update it to `qualified`;
   - if reward amount is zero or referrer is missing, leave status `qualified` and insert skip audit where supported;
   - if positive, insert one reward row, create one completed manual payment order keyed by referral id, insert payment credit + billing event + legacy balance update, update referral to `rewarded`, upsert tier progress, and insert reward-issued audit.
6. Keep helper functions short and localized to `internal/community/invitation`; do not modify frozen packages.
7. Run TDD red/green commands, then required gates:
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/community/invitation -count=1`
   - `cd backend && gofmt -w ...`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./internal/community/... ./internal/billing/... ./internal/payment/...`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/community/invitation ./internal/payment ./internal/billing -count=1`
8. Stage intended files, run `codex exec review --uncommitted --full-auto --sandbox read-only`, normalize any findings, fix S0/S1, then commit with the required co-author trailer.
9. After commit, mutate the idempotency guard locally, confirm the targeted duplicate-credit test fails, then `git checkout --` the mutation and rerun the targeted test.

## Clean-Room Self-Check

This Codex implementation lane will not read `/home/ubuntu/refs` or reference-project source. The only reference-derived design input is `/home/ubuntu/decomp-referral-reward.md`; implementation details come from HUAKAI local code and schema.
