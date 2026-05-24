# 2026-05-24 Antigravity Provider Skeleton

| Owner directive | "CONTEXT: HUAKAI 账号转 API, Antigravity vendor ... mirror cursor/kiro/openai_codex/gemini/windsurf 模式 ... 不动 scheduler.go ... 不动 frozen 包" |
| Scope | In: `backend/internal/provider/antigravity` bootstrap/refresher/store adapter/tests and `backend/internal/credentialacq/vendor_exchangers.go` registration. Out: scheduler wiring, frozen packages, DB schema, auth core, billing/quota, real secrets, git add/commit/push. |
| Success criteria | Defaults fail closed with no guessed endpoint/client/scope; OAuth authorize and refresh use only operator config; refresh rejects credential-supplied endpoints; HTTP 401/429/403 map to distinct audit outcomes; `antigravity/oauth` exchanger is registered; targeted build/test commands pass or failures are reported exactly. |
| Time estimate | 1-2 hours wall clock; one Codex implementation lane. |
| Blast radius | Low-medium: new provider package files and one acquisition registry line. No scheduler, schema, auth core, billing, quota, or frozen package changes. |
| Failure modes | Accidentally trusting credential JSON for token endpoint/client/scope; guessed public Google OAuth constants; weak tests that pass if credential endpoint is used; breaking existing `gemini/antigravity` compatibility; touching unrelated dirty `provider/gemini` files. Mitigation: TDD tests first, operator-config-only adapter fields, record matcher accepts both local alias and existing credentialstore mode, targeted tests. |
| Decision points | If official Antigravity OAuth endpoint/client/scope is not found quickly, keep fail-closed operator config. If integration requires scheduler wiring or new credentialstore mode/schema, stop and report as Owner-confirmation/next task rather than editing high-risk scope. |
| Pre-execution checklist | Read AGENTS/RULES; inspect existing Cursor/Kiro/OpenAI Codex/Gemini/Windsurf patterns; cap Antigravity public-doc research; confirm target package is not frozen; write failing tests; implement minimal code; run requested build/test; attempt Codex review without staging if CLI supports it, otherwise report limitation. |

Execution order:

1. Add Antigravity tests for operator-config fail-closed OAuth bootstrap and SSRF regression.
2. Add Antigravity tests for refresh HTTP 401/429/403 audit outcomes and store failure writes.
3. Add Antigravity test proving `credentialacq.DefaultExchangerRegistry` includes `antigravity/oauth`.
4. Implement `bootstrap.go`, `refresher.go`, and `credential_store_adapter.go` by mirroring local safe patterns with Antigravity names and no guessed OAuth defaults.
5. Register `antigravity/oauth` using the existing PKCE fake exchanger shape.
6. Run the requested build/test commands and targeted mutation self-check by temporarily breaking the operator token URL path, then restore.
7. Run at most two review rounds per CLAUDE.md #8 if the local Codex CLI supports uncommitted read-only review without staging.
