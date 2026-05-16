# 2026-05-16 POL-1 upstream policy monitor

| Owner directive | "你是 HUAKAI 项目 codex executor lane, 任务 = POL-1 上游政策追踪工具." |
| Scope | In: create a neutral upstream policy monitor under `tools/upstream-policy-monitor/`, mocked tests/fixtures, cron example, runbook, and alert output path. Out: backend, Rust `core_gateway`, auth, billing, quota, real cron registration, and any non-MIT reference source reading. |
| Success criteria | The tool defaults to dry-run fixtures, requires `--live` for network fetches, scans configured official vendor/blog/status/GitHub API targets for policy-risk keywords, writes positive hits to `docs/alerts/YYYY-MM-DD-upstream-policy-alert.md`, and passes stdlib-only syntax/unit checks. |
| Time estimate | 1-2 focused hours in this executor lane; Owner roadmap estimates 1-2 days including later review/operation. |
| Blast radius | Low. New docs/tool/test files only; no production service path changes. The main risk is noisy or missed alerts, not runtime product breakage. |
| Failure modes | False positives from broad keywords: include source, keyword, matched snippet, and runbook suppression guidance. False negatives from vendor page layout drift: keep target definitions simple and expose dry-run fixture tests. GitHub API rate limits: document optional `GITHUB_TOKEN` and keep live mode explicit. |
| Decision points | Owner decides whether to install the sample cron/systemd timer, whether a live alert triggers Phase ADV-1 or L1-L5 changes, and which false-positive terms should be suppressed after operational review. |
| Pre-execution checklist | Read project rules; read POL-1 roadmap context; avoid forbidden reference project source; inspect existing tool/runbook style; create files only in `tools/upstream-policy-monitor/`, `docs/runbooks/`, `docs/alerts/`, and this plan; run syntax and mock tests; report `git status`. |

## Concrete execution order

1. Create the monitor package with `run.py`, `run.sh`, fixtures, and a stdlib unittest file.
2. Implement target registry, dry-run fixture loader, optional live fetch via `urllib`, GitHub API command fallback via `gh api` only in live mode, keyword scanning, markdown alert generation, and no-op behavior when there are no hits.
3. Add `cron.example` with crontab and systemd examples that do not register anything.
4. Add Owner runbook covering local execution, alert triage, output files, and false-positive tuning.
5. Run `python3 -m py_compile`, `bash -n`, and mock unittest with stdin closed.
6. Report changed files, tests, and residual risks.

## Clean-room lane note

This is an implementer-lane neutral monitoring tool. It must not read or reuse source from `sub2api`, `new-api`, `portkey`, `helicone`, `litellm`, `all-api-hub`, or `envoy-ai-gateway`. Vendor policy pages, official status JSON, and official GitHub API metadata are monitoring inputs only, not reference project implementation source.
