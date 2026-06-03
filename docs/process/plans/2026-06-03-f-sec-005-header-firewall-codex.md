# 2026-06-03 F-SEC-005 Header Firewall Codex Plan

| Owner directive | "实现「上游响应 header allow/block list」... 只对确认会回传上游 header 给客户端的路径加 firewall... 不要 git commit(PM 提交)。实现+验证。" |
| Scope | In: verify HUAKAI header propagation paths; add `backend/internal/headerfirewall`; wire confirmed Hermes response-copy paths; extend `platformsettings` keys/validation without schema changes; add discriminating tests and mutation evidence. Out: commits, migrations, auth/billing/quota changes, production deployment, external reference-project behavior claims. |
| Success criteria | Main gateway claim is source-verified from HUAKAI code; Hermes leak paths are source-verified; Set-Cookie/Authorization are stripped while Content-Type survives in Hermes tests; operator extra deny and allow override are test-covered; required build/vet/test gate runs and output is reported. |
| Time estimate | 90-150 minutes wall clock in one Codex session. |
| Blast radius | Medium: response header behavior changes only on confirmed proxy-copy paths. Sensitive/infrastructure headers stop reaching clients; normal content headers remain. |
| Failure modes | Over-filtering breaks legitimate Hermes client metadata: mitigate with allow override for non-built-in headers and targeted tests. Under-filtering leaks credentials/session headers: mitigate with deny-by-default built-in list and discriminating Hermes tests. Settings parsing accepts malformed names: mitigate with validation tests. Frozen package violation: no new files under `internal/{gatewayhttp,gateway,proto}`. Header value logging: do not log header values. |
| Decision points | Owner confirmation needed only if implementation requires schema migration, auth/billing/quota changes, new runtime dependency, deleting files, or changing `LICENSE`; current plan avoids all. |
| Pre-execution checklist | 1. Read `AGENTS.md` and actual available `CLAUDE.md`. 2. Read Sonnet plan. 3. Use `.coordination` before editing shared files. 4. Audit actual HUAKAI write paths before wiring filters. 5. Write failing tests before production implementation. 6. Run mutation check by removing/disabling the filter and verifying Hermes test fails, then restore. 7. Run required gate commands. |

## Reference Scope Note

Per the clean-room constraint for this task, this execution does not read non-HUAKAI reference-project source and will not make reference-project behavior claims. The design basis is the Owner-supplied HUAKAI-internal Sonnet plan at `/home/ubuntu/.claude/plans/huakai-plan-f-sec-005-snuggly-muffin.md` plus direct HUAKAI code audit. Default mirror research is deferred as not needed for this minimal internal security patch; this is a recorded process tension against the broad triple-mirror rule.

## Concrete Execution Order

1. Inspect main gateway streaming/buffered response writers and `DispatchResult.Headers` use.
2. Inspect Hermes `copyProxyResponse` and `copyResponseHeaders` response-copy behavior.
3. Inspect `internal/platformsettings` key/default/validation patterns.
4. Coordinate and write RED tests for `headerfirewall`, `hermeshttp`, `hermeschat`, and platform setting validation.
5. Run targeted tests to confirm RED failures are caused by missing firewall/settings.
6. Implement focused `internal/headerfirewall` package with hard-coded built-in denylist, extra deny, and allow override for non-built-in names.
7. Wire settings-derived policy into confirmed Hermes response-copy points only.
8. Run targeted tests to confirm GREEN.
9. Run mutation check by temporarily disabling a filter call and confirming the Hermes test turns red; restore and re-run.
10. Run `cd backend && go build ./... && go vet ./... && go test ./internal/headerfirewall/... ./internal/hermeshttp/... ./internal/hermeschat/... ./internal/platformsettings/...`.
11. Report conclusions, files changed, denylist, mutation evidence, command tail, risks, and blockers in Chinese.
