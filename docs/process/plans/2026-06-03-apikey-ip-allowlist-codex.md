# 2026-06-03 API Key IP Allowlist - Codex Plan

| Owner directive | "实现+验证...为 API key 绑定 IP/子网白名单 -- inbound 请求的 client IP 不在该 key 白名单内则拒(403)" |
| Scope | Add a nullable `api_keys.ip_allowlist` column, include it in inbound auth lookup, enforce it after bcrypt success using HUAKAI's trusted client IP resolver, and add a user-owned per-key PUT/GET control endpoint under existing `/v1/api-keys/{id}` controls. Out of scope: admin UI polish, reference-source mining, runtime dependency additions, broad auth refactors, quota/billing/auth-session changes. |
| Success criteria | `ip_allowlist` NULL/empty preserves existing behavior; `10.0.0.0/8` allows trusted client `10.1.2.3`; the same key rejects trusted client `1.2.3.4` with auth-layer forbidden and HTTP 403 where inbound handlers map it; forged XFF cannot bypass when socket peer is not trusted; user controls can set/clear/read allowlist scoped by tenant/user/key. |
| Time estimate | 60-90 minutes wall-clock in this Codex session: 15-20 min context, 15 min RED tests, 25-35 min implementation/sqlc, 15-20 min verification. |
| Blast radius | Inbound API key auth, API key SQL lookup rows, user key controls SQL/API, generated sqlc code, one additive schema migration. If wrong, valid API users could be denied or spoofed XFF could bypass allowlists. |
| Failure modes | Bad CIDR parsing could fail open or deny all; mitigation: invalid allowlist rows fail closed with `ErrForbidden` and service setters validate before storing. Direct XFF trust could be spoofed; mitigation: only call `internal/clientip.Resolver.ClientIP(req)`. Existing handlers could map mismatch to 401; mitigation: add `auth.ErrForbidden` handling to inbound handlers. Schema drift could break sqlc/build; mitigation: run sqlc generate if available and full requested gate. |
| Decision points | Schema change is high-risk under project rules but explicitly authorized by Owner in this task. Runtime dependency additions are not needed. If sqlc is unavailable or generated output drifts, report as blocker/risk instead of hiding it. No git commit; PM submits. |
| Pre-execution checklist | 1. Read `CLAUDE.md` and `AGENTS.md`. 2. Confirm no existing `ip_allowlist`/`ip_whitelist`. 3. Locate `api_keys` schema and auth sqlc query. 4. Locate bcrypt success return in `internal/auth/api_key_resolver.go`. 5. Locate trusted client IP resolver and production wiring. 6. Check `.coordination` locks before editing shared files. 7. Write RED tests before production implementation. |

## Reference Projects In Scope

Required default mirrors by project rule: CLIProxyAPI, sub2api, new-api.

This Codex run is an implementation lane and will not read non-MIT reference source. The feature shape comes from the Owner/PM directive above. The implementation and final report will make only HUAKAI-internal claims with local file:line evidence. Reference parity/source evidence remains a PM/specifier-lane follow-up if Owner wants it recorded separately.

## Target Files

- Create `backend/sql/migrations/0085_api_key_ip_allowlist.up.sql` (non-frozen schema migration).
- Create `backend/sql/migrations/0085_api_key_ip_allowlist.down.sql` (non-frozen rollback migration).
- Create `backend/internal/apikeyipallow/allowlist.go` (new non-frozen helper package for canonical CIDR/bare-IP parsing shared by auth and controls).
- Modify `backend/sql/queries/auth_inbound.sql` and generated `backend/internal/db/auth/auth_inbound.sql.go`.
- Modify `backend/sql/queries/userkey_controls.sql` and generated `backend/internal/db/userkeycontrols/userkey_controls.sql.go`, `querier.go`.
- Modify existing non-frozen auth files: `backend/internal/auth/api_key_resolver.go`, `backend/internal/auth/api_key_resolver_integration_test.go`.
- Modify existing non-frozen controls files: `backend/internal/userkeycontrols/{types.go,store.go,key_control_service.go,service_test.go}` and `backend/internal/userkeycontrolshttp/{mount.go,quota_group_handlers.go,handlers_test.go}`.
- Modify existing non-frozen inbound HTTP handler files only where needed to map `auth.ErrForbidden` to HTTP 403: `internal/modelhttp`, `internal/meusagehttp`, `internal/usageanalyticshttp`, `internal/hermeshttp`.
- Modify existing frozen `backend/internal/gatewayhttp/chat_completions_handler.go` only for the 403 mapping; no new file in frozen packages.
- Modify existing `backend/cmd/gateway/wiring.go` to pass the trusted resolver into inbound auth; no new file in `cmd/gateway`.
- Modify `docs/openapi/openapi.yaml` to keep runtime route and API contract consistency tests aligned.

## Execution Order

1. Claim all target files via `.coordination`.
2. Add RED auth integration tests for allow, deny, empty allowlist, and forged XFF anti-bypass.
3. Add RED userkeycontrols service/http tests for set/clear/validation shape.
4. Run focused tests to confirm RED.
5. Add migration and SQL query fields.
6. Run `sqlc generate` if available; manually inspect generated diffs.
7. Implement auth allowlist parsing/check with trusted `clientip.Resolver`.
8. Implement user controls service/store/http routes.
9. Update inbound handlers to map `auth.ErrForbidden` to 403.
10. Run focused tests; if failures appear, debug from the first failing symptom.
11. Run mutation check by temporarily disabling the auth allowlist guard and confirming the targeted test fails; restore immediately.
12. Run the Owner-requested gate command.
13. Release coordination lock and report evidence, risks, blockers, and Chinese summary.
