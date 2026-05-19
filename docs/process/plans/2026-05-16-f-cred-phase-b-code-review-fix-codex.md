# 2026-05-16 F-CRED Phase B Code Review Fix
| Owner directive | "F-CRED Phase B code review fix (4 HIGH + 2 MED 必修)." |
| Scope | Fix HUAKAI-local credential acquisition, refresh locking, Gemini fallback audit/matrix, migration rollback, handler scope checks, and production-path tests. No reference-project source reads. |
| Success criteria | HIGH-1..HIGH-4 and MED-5..MED-6 have code/spec/test coverage; targeted Go test command passes or any environment blocker is reported truthfully. |
| Time estimate | 1 work session; agent time roughly 90-150 minutes depending on compile fallout. |
| Blast radius | Credential acquisition OAuth flows, credential refresh worker, Gemini refresh behavior, admin credential acquisition routes, migration 0019 audit constraints. |
| Failure modes | PKCE metadata cannot decrypt after storage; refresh transaction wrapper may not receive a real pgxpool; handler tests may over-mock DB; audit CHECK may reject new Gemini event. Mitigation: add focused unit/integration-style tests and keep changes scoped. |
| Decision points | No Owner re-confirmation is needed unless a fix would touch LICENSE, auth core, billing/quota, deployment, real secrets, or destructive migrations. |
| Pre-execution checklist | Read HUAKAI-local affected files; do not read prohibited reference source; implement PKCE encryption helper; wire refresh advisory lock in transaction; add Gemini matrix/audit; add handler scope guard and route tests; run targeted Go tests. |
| Concrete execution order | 1. Patch PKCE encryption/decryption and gateway wiring. 2. Patch refresh lock transaction path. 3. Patch Gemini fallback matrix/audit and docs. 4. Patch 0019 down/up audit constraints. 5. Replace mock-only tests and add handler route coverage. 6. Run requested tests and report status/diff. |
