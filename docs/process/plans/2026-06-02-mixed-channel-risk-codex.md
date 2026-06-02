# 2026-06-02 mixed-channel-risk Codex plan

| Owner directive | "HUAKAI 混 channel 风险检测+显式确认(对照 sub2api,clean-room 引证不抄)。IMPLEMENTER。非冻结包。中文注释。自主->push origin HEAD:work/mixed-channel-risk。不碰 landing。" |
| Scope | In: admin provider-account create path, risk detection for adding an account into an existing tenant/channel/pool, confirm=true override, admin audit event, discriminating Go tests, clean-room evidence citations. Out: schema changes, landing branch, auth/billing/quota core, production secrets, new runtime dependencies, frozen-package new files. |
| Success criteria | Mixed source/vendor/credential-type account addition without confirm returns HTTP 400 with risk items and no account insert. Same request with confirm=true creates the account and writes a confirmation audit. Same-source addition does not report risk. `go test` targeted tests, `go test ./...`, and `go build ./...` pass or failures are reported truthfully. |
| Time estimate | 60-100 minutes wall clock, mostly code reading, TDD, and repository-wide Go verification. |
| Blast radius | Admin-only account creation flow. Runtime risk is false-positive blocking account creation or false-negative allowing risky mixed account composition. |
| Failure modes | Missing source fields in current store API: mitigate by extending existing store interface/query without schema changes. Weak tests: use fixtures that fail if detection is skipped. Clean-room leakage: record only behavior observations with repo@sha:file:line citations and avoid upstream identifiers/structure/comments. Frozen-package violation: do not add files under `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`. |
| Decision points | High-risk changes requiring Owner stop: schema migration, auth core changes, billing/quota logic, `LICENSE`, secrets, destructive commands, adding runtime dependencies. None planned. |
| Pre-execution checklist | 1. Read `docs/RULES.md` and AGENTS constraints. 2. Read HUAKAI handler/store/pool/channelhealth context. 3. Read only the requested sub2api source regions for behavior evidence. 4. Write failing tests first. 5. Implement minimal non-frozen risk helper and thin handler integration. 6. Run red/green targeted tests, full Go tests/build, self-review no more than two rounds. 7. Commit and push `HEAD:work/mixed-channel-risk`. |

## Clean-room self-guard

Owner explicitly requested a targeted clean-room source read for sub2api mixed-channel behavior while also assigning Codex as implementer. I will treat the source read as behavior evidence only: no upstream code, comments, file structure, internal names, schema, copied tests, or line-by-line algorithmic translation will enter HUAKAI. Evidence will be cited as `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:<file>:<line>`.

## Concrete execution order

1. Inspect targeted sub2api source regions and record observed behavior in working notes only.
2. Inspect HUAKAI admin provider-account query types to find available channel/account metadata.
3. Add failing tests to existing `backend/internal/gatewayhttp/admin_pool_accounts_handler_test.go`.
4. Add a focused non-frozen package for mixed-channel risk evaluation.
5. Extend the existing handler's request parsing with `confirm` and call the helper before insert.
6. Write an admin audit event for confirmed high-risk creation.
7. Run targeted tests, then full `go test ./...` and `go build ./...`.
8. Stage intended files, run required self-review/per-commit review within the two-round cap, commit, push.

## Observed clean-room evidence

- Observed: the reference admin account create/update/bulk-edit request bodies include an explicit operator confirmation boolean for mixed-channel risk. Evidence: `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:96`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:112`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:132`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:150`.
- Observed: the reference admin API has a dedicated pre-check path that returns risk details instead of mutating account state. Evidence: `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:469`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:488`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:492`.
- Observed: create/update paths compute a confirmation-derived skip flag and pass it into the service layer before account-group binding logic. Evidence: `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:528`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:551`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:612`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/handler/admin/account_handler.go:630`.
- Observed: the service layer performs risk pre-checks before writes when target groups are present and the caller has not confirmed the risk. Evidence: `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service.go:2372`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service.go:2560`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service.go:2613`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service.go:2629`.
- Observed: the reference risk checker compares the candidate account against other accounts in each target group and returns a typed warning error on incompatible channel composition. Evidence: `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service.go:3384`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service.go:3394`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service.go:3412`, `Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/admin_service.go:3420`.

Clean-room exception/risk record: this evidence section is the behavior-only specifier summary. The Owner directive above explicitly combined `IMPLEMENTER` with the targeted clean-room source read, so this same Codex session implemented from the summary after reading source. This is Owner operational approval for this work unit only, not legal approval and not normal lane separation. No reviewer-lane approval is claimed here; future non-MIT reference-source work should use a separate specifier/implementer lane unless Owner explicitly repeats this exception.

Source files read: `backend/internal/handler/admin/account_handler.go`, `backend/internal/service/admin_service.go`
Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-06-02T08:39:24Z
