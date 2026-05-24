# 2026-05-24 Refresh OAuth Endpoint Hardening

| Owner directive | "Owner 直接发现 2 项 production-impact bug" |
| Scope | In: cursor/windsurf refresh OAuth endpoint source, client ID/scope config source, refresh audit outcome wrapping, focused regression tests. Out: schema, auth core redesign, billing/quota, deployment, git staging/commit/push. |
| Success criteria | Cursor and Windsurf refreshers only use operator config for OAuth endpoint/client/scope; missing operator token URL fails closed; credential endpoint SSRF regression test fails on vulnerable code and passes after fix; cursor 401 produces `auth_expired` scheduler audit outcome; requested build and targeted race tests pass or any blocker is reported. |
| Time estimate | 45-75 minutes wall clock, one Codex session. |
| Blast radius | Provider refresh paths for cursor/windsurf; scheduler audit classification for vendor refresh errors. No database or external network dependency expected in tests. |
| Failure modes | Weak SSRF test that still passes if credential endpoint is read; incorrect wrapping that changes saved failure class; accidental edits to unrelated dirty files; race/build failures from existing unrelated worktree changes. Mitigation: TDD red/green, narrow `apply_patch`, inspect diffs, run requested commands. |
| Decision points | No additional Owner sign-off needed for this S0 because Owner supplied exact fix strategy. Stop only before high-risk schema/auth/billing/quota/LICENSE/secrets/destructive changes, which are out of scope. |
| Pre-execution checklist | 1. Read cursor/windsurf refresher code and existing tests. 2. Add failing SSRF and audit-outcome regression tests. 3. Run focused tests to confirm red. 4. Patch only existing refresher/test files. 5. Run focused tests, race tests, full backend build. 6. Run mutation self-check for cursor credential endpoint fallback. 7. Report diffs, risks, and verification. |
| Concrete execution order | Add tests first; update cursor/windsurf adapter config source and error wrapping; run `gofmt`; run requested test/build commands; perform temporary mutation and revert it without staging; do not `git add`, commit, or push. |
