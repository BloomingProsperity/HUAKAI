# 2026-05-27 Early Heartbeat Reference Research
| Owner directive | "Owner 让你和另一个 sonnet agent 同时调研 LLM gateway 项目中 SSE 流式转发的 \"early heartbeat\" 实现" |
| Scope | In: read only allowed MIT/Apache reference directories named by Owner, read HUAKAI current stream-dispatch files, inspect public GitHub issue metadata for the same topic when locally available or via allowed web search if needed, write findings to `/tmp/codex-early-heartbeat-findings.md`. Out: LGPL/AGPL reference directories, source copying, implementation changes, repo docs beyond this required plan artifact. |
| Success criteria | `/tmp/codex-early-heartbeat-findings.md` has one independent section per allowed project, with file:line evidence for early header/heartbeat behavior where found, strategy A/B/C/D, error dispatch shape, heartbeat interval, issue tracker evidence or "not found", and a cited HUAKAI recommendation. |
| Time estimate | 45-75 minutes wall clock; one Codex session. |
| Blast radius | Low. Reads external refs and HUAKAI code; writes one `/tmp` report and this plan artifact. |
| Failure modes | Missing a project-specific stream path: mitigate with broad `rg` patterns for SSE, flush, keepalive, heartbeat, stream, and response headers. Accidentally reading forbidden refs: mitigate by explicit allowlist paths and no recursive commands over `~/refs`. Unsupported claims: mitigate by file:line citations and "not found in searched regions" wording. |
| Decision points | Owner confirmation required only if the task needs reading forbidden LGPL/AGPL refs or changing HUAKAI implementation. Neither is planned. |
| Pre-execution checklist | 1. Confirm allowed refs exist. 2. Confirm current git/head for allowed refs when available. 3. Inspect HUAKAI stream dispatch lines cited by Owner. 4. Search each allowed project for streaming response handling and issue references. 5. Write behavior-only findings with clean-room-safe paraphrase. |

Concrete execution order:

1. List only the seven allowed reference roots and record whether they exist.
2. Read HUAKAI stream path around the Owner-cited lines to verify local gap shape.
3. For each allowed project, search for SSE headers, flushing, heartbeat/keepalive timers, upstream request dispatch, and error event handling.
4. For GitHub issues, search local issue archives first if present; otherwise use web search restricted to the project repository/issues.
5. Write `/tmp/codex-early-heartbeat-findings.md` with observed/inferred/open-question markers and citations.
6. Re-scan the report for forbidden refs, raw code snippets, uncited behavior claims, and unsupported recommendation claims.
