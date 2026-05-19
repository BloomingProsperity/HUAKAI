# 2026-05-19 Go Test Infra Windows Python Codex
| Owner directive | "修 tests 全仓不绿 + Windows python3 alias 卡 (audit list HIGH)." |
| Scope | In: backend Go test failures caused by local environment assumptions, especially Python launcher naming and external command availability. Out: frontend, Rust, vendor/boring, backend/internal/pool, docs/openapi/openapi.yaml plus related implementation, reference reverse-proxy source, database-backed tests gated by HUAKAI_DATABASE_URL. |
| Success criteria | `cd backend && GOCACHE=/tmp/go-cache go test ./... -count=1 -timeout 300s` passes, or any remaining failures are classified as true product bugs / gated infrastructure issues and surfaced to Owner without unsafe edits. |
| Time estimate | 30-60 minutes wall clock, one Codex implementer lane. |
| Blast radius | Test-only or narrowly scoped test helper changes should affect CI portability. Risk is accidentally hiding a real product regression by over-skipping. |
| Failure modes | Overbroad skip hides coverage; mitigate by only skipping when an external dependency is missing or Windows alias behavior is known. Touching forbidden paths; mitigate by checking path before edits. Network/DB-dependent tests fail; classify and do not mask unless already gated. |
| Decision points | If failures point to true backend behavior bugs, auth/billing/quota/schema/core routing, or forbidden paths, stop and report instead of patching in this lane. |
| Pre-execution checklist | 1. Confirm clean worktree. 2. Run requested full backend test command with local GOCACHE. 3. Group failures by package and cause. 4. Patch only environment compatibility tests/helpers. 5. Re-run affected packages then full backend tests. 6. Stage, run per-commit review if available, commit. |

Concrete execution order:
1. Run `cd backend && GOCACHE=/tmp/go-cache go test ./... -count=1 -timeout 300s`.
2. Inspect failing package logs beyond the tail when needed.
3. Locate test code with `rg`, avoiding prohibited paths.
4. Implement minimal compatibility fixes for Python command detection or external-command skips.
5. Re-run full backend tests.
6. Commit with the review/test result in the message.
