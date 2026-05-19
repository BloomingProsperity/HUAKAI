# 2026-05-15 per-commit review fix
| Owner directive | "修复 per-commit review 对 exploratory/rust-core-gateway/merged/ 这批 uncommitted 改动给出的 REJECT -- 1 HIGH + 2 MEDIUM + 1 LOW" |
| Scope | In: `exploratory/rust-core-gateway/merged/` auth validation, proxy auth injection, redaction, mimicry scanner reporting/tests, and `tools/fingerprint-collector/templates/` template placement. Out: committing, production secrets, DB/schema/auth core outside this experimental Rust gateway. |
| Success criteria | The HIGH credential-mixing bypass is rejected at plan validation and injection boundaries; control-plane/advisory errors are redacted before logs and AttemptReport; mimicry secret findings never print raw matched text; top-level production templates are covered by builtin profiles; `cargo build` and `cargo test` pass in `exploratory/rust-core-gateway/merged/`. |
| Time estimate | 60-90 minutes wall clock, one Codex implementation pass plus verification. |
| Blast radius | Medium within the experimental Rust gateway: stricter credential validation may reject malformed test fixtures or previously accepted dirty tokens. Template move affects only production-ready profile discovery/tests. |
| Failure modes | Over-redaction may hide useful diagnostics: mitigate by retaining error class/code and non-secret context. Regex scanner may false-positive: mitigate with focused patterns and tests. Template coverage test may depend on path assumptions: mitigate by deriving the repo root from Cargo manifest location. |
| Decision points | No Owner sign-off expected unless a fix requires adding a new runtime dependency, changing high-risk auth core outside this experimental gateway, or committing changes. |
| Pre-execution checklist | 1. Read relevant rules and inspect dirty worktree. 2. Inspect current auth/redaction/mimicry/profile test code. 3. Apply narrow patches. 4. Run `cargo build` and `cargo test`. 5. Self-review diff for HIGH bypass and accidental secret previews. |

## Concrete execution order

1. Locate `validate_upstream_auth_material`, `apply_plan_auth`, redaction helpers, listener reporting, route-client status conversion, and mimicry secret finding structures.
2. Add strict material boundary checks and injection-boundary comparison.
3. Add or reuse a shared redaction helper for external error/advisory strings and route listener reporting.
4. Replace mimicry raw previews with match length and short digest.
5. Move `anthropic-claude-code.json` into `_pending-backfill/` and add top-level template coverage test.
6. Run build/test, then inspect `git diff` for the four review findings.
