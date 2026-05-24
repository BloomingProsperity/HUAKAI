# 2026-05-24 Kiro Vendor Skeleton
| Owner directive | "HUAKAI 账号转 API, Kiro vendor (provider code: kiro)." |
| --- | --- |
| Scope | In: `backend/internal/provider/kiro/bootstrap.go`, `backend/internal/provider/kiro/refresher.go`, `backend/internal/provider/kiro/credential_store_adapter.go`, focused Kiro package tests, and `backend/internal/credentialacq/vendor_exchangers.go` registration check. Out: DB schema, auth core, billing/quota, production secrets, scheduler wiring, deployment, and non-MIT reference source. |
| Success criteria | Kiro AWS SSO bootstrap is operator-config fail-closed; refresh uses only operator-configured token endpoint/client identity; refresh request follows AWS SSO OIDC `CreateToken` refresh-token path; 401 maps to `auth_expired`, 429 to `rate_limit_exceeded`, and 403 risk bodies to `risk_control_triggered`; session-to-credentialstore adapter exists; `kiro/aws-sso` exchanger is registered without dropping existing alias; requested build and race test commands run or failures are reported honestly. |
| Time estimate | 60-90 minutes wall clock, one Codex session. |
| Blast radius | Low/medium. New files stay in non-frozen `backend/internal/provider/kiro`; one low-risk registry edit stays in `credentialacq`. No new runtime dependency, schema migration, billing/quota/auth-core edit, or real network call. |
| Failure modes | The implementation could accidentally trust credential-supplied endpoints, silently hard-code speculative Kiro endpoints, flatten HTTP outcomes, or write a success payload after a classified failure. Mitigation: write discriminating tests first for fail-closed config, SSRF prevention, status classification, and transaction failure recording. |
| Decision points | Stop for Owner confirmation if implementation needs DB schema, auth-core changes, billing/quota changes, production credentials, new runtime dependencies, or hard-coded Kiro/AWS client IDs/secrets/endpoints beyond documented operator-config helpers. |
| Pre-execution checklist | 1. Use only HUAKAI code, allowed MIT CLIProxyAPI behavior reading, Kiro public docs, and AWS public docs. 2. Keep reference evidence behavior-only. 3. Confirm `backend/internal/provider/kiro` is not frozen. 4. Write failing tests before implementation. 5. Do not `git add`, commit, or push. 6. Run `cd backend && GOCACHE=/tmp/go-build go build ./internal/provider/kiro/...` and `GOCACHE=/tmp/go-build go test ./internal/provider/kiro/... -count=1 -race`. |

## Evidence Anchors

- AWS IAM Identity Center OIDC enables CLI/native clients to fetch access tokens after authentication and notes refresh support in current CLI versions: https://docs.aws.amazon.com/singlesignon/latest/OIDCAPIReference/Welcome.html lines 7 and 16-18.
- AWS `CreateToken` supports `refresh_token` grant and accepts a refresh token for short-lived token refresh: https://docs.aws.amazon.com/ja_jp/singlesignon/latest/OIDCAPIReference/API_CreateToken.html lines 72-95.
- AWS `CreateToken` response returns access token, expiry seconds, optional refresh token, and bearer token type: https://docs.aws.amazon.com/ja_jp/singlesignon/latest/OIDCAPIReference/API_CreateToken.html lines 108-154.
- AWS `RegisterClient` response includes authorization and token endpoints, so HUAKAI should treat those as operator/configured values rather than inventing Kiro-specific defaults: https://docs.aws.amazon.com/boto3/latest/reference/services/sso-oidc/client/register_client.html lines 635-668.
- Kiro CLI docs state IAM Identity Center login and device flow are supported, including `--identity-provider` and `--region`: https://kiro.dev/docs/cli/reference/cli-commands/ lines 357-404.
- Kiro firewall docs list `oidc.<sso-region>.amazonaws.com` for IAM Identity Center token exchange and `prod.us-east-1.auth.desktop.kiro.dev` for Kiro token exchange/refresh/logout, but this plan keeps defaults empty and requires operator config: https://kiro.dev/docs/cli/privacy-and-security/firewalls/ lines 84-120.
- Allowed CLIProxyAPI local source at `/home/codex/refs/CLIProxyAPI` did not expose a direct Kiro-specific handler in the searched regions; this is an observed absence, so no Kiro handler mechanism will be inferred from it.

## File Structure Check

- Create `backend/internal/provider/kiro/bootstrap.go`: Kiro constants and AWS SSO operator-config helpers.
- Create `backend/internal/provider/kiro/refresher.go`: AWS SSO OIDC refresh adapter, HTTP outcome classification, token merge, retry metadata.
- Create `backend/internal/provider/kiro/credential_store_adapter.go`: bridge `credentialstore.Store` to Kiro refresher transaction interfaces.
- Create `backend/internal/provider/kiro/bootstrap_test.go`: fail-closed config and operator-config flow tests.
- Create `backend/internal/provider/kiro/refresher_test.go`: SSRF prevention, success merge, HTTP classification, and locked failure recording tests.
- Modify `backend/internal/credentialacq/vendor_exchangers.go`: add `kiro/aws-sso` registration if missing; keep `kiro/sso` as compatibility alias.

`backend/internal/provider/kiro` currently has one non-test source file. Adding three non-test files keeps it below the package budget.

## Execution Order

1. Add Kiro bootstrap tests proving default config contains no endpoint/client defaults and validation fails with `ErrKiroSSOConfigRequired`.
2. Add Kiro SSO config merge test proving operator-supplied endpoints/client identity are preserved.
3. Run Kiro package tests and confirm the new tests fail because bootstrap symbols do not exist.
4. Implement `bootstrap.go`.
5. Add Kiro refresher tests for success merge, credential-supplied endpoint/client rejection, HTTP outcome classification, and refresh-lock failure recording.
6. Run Kiro package tests and confirm refresher tests fail because implementation symbols do not exist.
7. Implement `refresher.go` and `credential_store_adapter.go`.
8. Add or verify `kiro/aws-sso` exchanger registration while preserving `kiro/sso`.
9. Run requested build/test commands.

## Clean-Room Tail

No LGPL/AGPL/GPL source was read for this Kiro implementation plan. CLIProxyAPI was searched as an allowed MIT reference, but no direct Kiro handler was found; implementation will rely on AWS/Kiro public docs plus existing HUAKAI Cursor/Windsurf patterns.

Source files read: `AGENTS.md`; `docs/RULES.md`; `docs/process/plans/2026-05-24-token-refresh-worker-closure-synthesis.md`; `docs/process/plans/2026-05-24-decisions-locked.md`; `docs/process/plans/2026-05-24-windsurf-vendor-skeleton-codex.md`; `backend/internal/provider/kiro/kiro_session.go`; `backend/internal/provider/kiro/kiro_session_test.go`; `backend/internal/provider/cursor/bootstrap.go`; `backend/internal/provider/cursor/refresher.go`; `backend/internal/provider/cursor/credential_store_adapter.go`; `backend/internal/provider/windsurf/bootstrap.go`; `backend/internal/provider/windsurf/refresher.go`; `backend/internal/provider/windsurf/credential_store_adapter.go`; `backend/internal/credentialacq/vendor_exchangers.go`; `backend/internal/credentialacq/oauth_sso.go`; `backend/internal/credentialacq/oauth_devicecode.go`; `/home/codex/refs/CLIProxyAPI/.huakai-head-sha`; searched `/home/codex/refs/CLIProxyAPI` for Kiro/AWS SSO evidence.
Lane: specifier
Agent: GPT-5 Codex
UTC timestamp: 2026-05-24T00:00:00Z
