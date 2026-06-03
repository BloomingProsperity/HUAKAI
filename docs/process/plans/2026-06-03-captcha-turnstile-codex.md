# 2026-06-03 captcha-turnstile Codex execution plan

| Owner directive | "实现+验证...按 /home/ubuntu/.claude/plans/huakai-plan-proud-whisper.md(F-SEC-001 CAPTCHA 残差设计)实现" |
| Scope | In: `internal/captcha`, existing auth handler wiring, gateway config/routes, discriminating tests. Out: schema, frontend widget rendering, payment/auth core rewrites, commits. |
| Success criteria | Default without `HUAKAI_CAPTCHA_TURNSTILE_SECRET` is noop; runtime settings can require Turnstile; register/login reject failed captcha and pass successful captcha; requested Go build/vet/test gate passes. |
| Time estimate | 1 focused Codex work unit, roughly 1-2 wall-clock hours depending on existing test failures. |
| Blast radius | Public `/v1/auth/register` and `/v1/auth/login`; gateway route wiring; new outbound POST client only when both gates are enabled. |
| Failure modes | Weak tests that still pass if the gate is deleted; accidental fail-closed when secret is absent; token/secret logging; new files in frozen packages; malformed siteverify parsing. |
| Mitigation | TDD RED before implementation; same-HTTP-status verifier fixtures differing only by JSON `success`; count-based no-outbound assertions; no token/secret logs; only edit existing frozen-package files. |
| Decision points | None during this work. The plan already selects fail-open when settings are enabled but env secret is missing. Future Owner decision: whether to add startup warning or fail-closed mode. |
| Pre-execution checklist | Read `CLAUDE.md`; read `AGENTS.md`; read approved F-SEC-001 plan; claim edit files in `.coordination`; write RED tests; implement minimal code; run requested gate. |

Reference projects in scope per AGENTS.md planning rule: CLIProxyAPI, sub2api,
new-api. This implementation lane does not read their non-MIT source; it follows
the approved clean-room plan and the public Cloudflare Turnstile siteverify
contract. Residual compliance risk is recorded in the final report rather than
inventing upstream behavior claims.
