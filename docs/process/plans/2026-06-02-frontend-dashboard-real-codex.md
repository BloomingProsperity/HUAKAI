# 2026-06-02 frontend-dashboard-real

| Owner directive | "HUAKAI 前端 /dashboard 接真后端(去 mock 假数据,审计 F-07)。IMPLEMENTER 前端。中文注释。自主→push origin HEAD:work/frontend-dashboard-real。不碰 landing。" |
| Scope | In: `frontend/app/dashboard/page.tsx`, dashboard chart component/data helpers, frontend API typing/client wrappers, discriminating frontend source test. Out: landing page, backend core, auth, billing ledger, quota enforcement, database schema, new runtime dependencies. |
| Success criteria | `/dashboard` no longer imports `dashboard-mock` fake constants; it calls real admin API client wrappers for `/admin/v1/usage`, `/admin/v1/provider-accounts`, `/admin/v1/provider-accounts/{id}/health`, `/admin/v1/account-modes`, `/admin/v1/providers`, and `/admin/v1/channels`; UI has loading and error states; no fake fallback data is rendered; `npm run type-check` and `npm run build` pass from `frontend`; self-review is run before push. |
| Time estimate | 1-2 wall-clock hours, one Codex session. |
| Blast radius | Frontend dashboard only, plus low-risk API type/wrapper additions and source-level tests. If this fails, `/dashboard` may show empty/error state or fail type-check/build; landing remains untouched. |
| Failure modes | API response shape mismatch: verify against backend/OpenAPI and keep unknown values transparent. Empty backend data: render empty-state text instead of fabricated rows. Parallel health calls fail for one account: treat dashboard load as error so operators see trust-chain break, not partial fake data. Chart data too sparse: show "真实数据不足" instead of generated trend. |
| Decision points | No Owner sign-off needed for low-risk frontend/test/doc changes. Stop before adding runtime dependencies, touching backend high-risk files, or altering auth/billing/quota/database/deploy logic. |
| Pre-execution checklist | 1. Read requested files. 2. Confirm real endpoint response shapes from frontend types/backend handlers/OpenAPI. 3. Add discriminating source test and verify it fails before implementation. 4. Replace mock imports with real API wrappers and derived view models. 5. Add loading/error/empty states without layout shrinkage. 6. Run focused test, `npm ci || npm install`, `npm run type-check`, `npm run build`. 7. Stage intended diff, run `codex exec review --uncommitted --full-auto --sandbox read-only`, normalize findings, then push `origin HEAD:work/frontend-dashboard-real`. |

## Concrete execution order

1. Add `frontend/lib/dashboard-real-api.test.ts` using Node's built-in test runner. The test reads dashboard source files and asserts:
   - `frontend/app/dashboard/page.tsx` does not import `@/lib/dashboard-mock` or fake constants.
   - `frontend/components/dashboard/TrendChart.tsx` does not import `@/lib/dashboard-mock`.
   - dashboard/API source contains real client wrapper names for provider accounts, account health, usage, provider catalog, channel catalog, and account modes.
2. Run the focused test to verify RED.
3. Add frontend API types/wrappers for admin catalogs and provider-account health in existing API modules or a small focused module under `frontend/lib/api/`; do not add dependencies.
4. Refactor `frontend/app/dashboard/page.tsx` into a client component with `useEffect`/`Promise.all` real reads. Derive usage totals, cost, latency, health stats, provider/channel/model metadata, and chart points only from backend responses.
5. Change `frontend/components/dashboard/TrendChart.tsx` to accept real chart data as props and render an honest empty-data state when the backend data cannot support a trend.
6. Run focused test again for GREEN.
7. Run requested install/check/build commands.
8. Stage only intended files, run Codex self-review per project rule, fix S0/S1 findings if any, and push.

## Assumptions

- The current Owner directive is the valid start signal for this work.
- Existing admin token behavior in `frontend/lib/api/client.ts` remains unchanged; dashboard calls use the same Bearer-token injection.
- `/admin/v1/usage` is the source of observed dashboard usage; there is no separate aggregate endpoint in scope, so frontend aggregates the returned records without inventing missing values.
- Provider display names come from `/admin/v1/providers`; if a provider id/code cannot be resolved, the UI marks it as unresolved instead of guessing.

## Execution note

- Current Codex CLI rejected the canonical `--sandbox/--full-auto` review flags, so review used the closest available read-only form: `codex exec review --uncommitted -c sandbox_mode='"read-only"'`.
