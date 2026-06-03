# 2026-06-03 Gap Route Wiring - Codex Plan

| Owner directive | "Properly WIRE the 5 gap route helpers that exist in cmd/gateway/routes_*.go but are currently NOT called in mountRoutes ... Activate all 5 WITHOUT panic and WITH OpenAPI docs." |
| Scope | In: inspect existing `cmd/gateway/routes.go` route patterns, make the five helper mounts collision-safe, call `mountPlatformSettingsRoutes`, `mountUserKeyControlsRoutes`, `mountPricingCatalogRoutes`, `mountNotificationRoutes`, and `mountModerationAdminRoutes` from `mountRoutes`, and document every newly registered route in `docs/openapi/openapi.yaml`. Out: changing service behavior, changing database schema, changing auth/quota/billing hot-path logic, adding dependencies, committing. |
| Success criteria | `cd backend && go build ./...` passes; `cd backend && go test ./cmd/gateway/... 2>&1 | tail -18` shows `TestInternalRunnerBootstrap...` does not panic and `TestOpenAPI_ImplementationConsistency` is green with `spec_only=0` and `impl_only=0`. |
| Time estimate | 1-2 wall-clock hours in one Codex session. |
| Blast radius | Runtime route table for existing HTTP handlers and OpenAPI documentation. No new packages, migrations, dependencies, or production data changes. |
| Failure modes | Duplicate chi `Route` patterns can panic at bootstrap; mitigate by grepping existing `r.Route` registrations and mounting subroutes inside an existing parent when needed. OpenAPI paths can drift from implementation; mitigate by reading each helper plus its HTTP package and running the existing consistency test. Handler request/response shapes can be misdocumented; mitigate by using existing handler tests/types as source of truth. |
| Decision points | No Owner sign-off expected unless the collision fix would require changing a high-risk auth, billing, quota, schema, or secret path. No such change is expected. |
| Pre-execution checklist | 1. Run the existing `cmd/gateway` gate as RED evidence. 2. Grep existing `r.Route("` patterns in `cmd/gateway/routes.go` and each gap helper. 3. Read each gap helper and its HTTP package route registration. 4. Patch only route wiring/collision-safe mounting and OpenAPI docs. 5. Run build and the requested gateway test command. 6. Report collisions, wired paths, OpenAPI additions, test tail, and blockers. |

## Concrete Execution Order

1. Run `cd backend && go test ./cmd/gateway/...` to capture the current panic or consistency failure before production edits.
2. Inspect `backend/cmd/gateway/routes.go` for existing `r.Route("...")` patterns.
3. Inspect `backend/cmd/gateway/routes_platformsettings.go`, `routes_userkeycontrols.go`, `routes_pricingcatalog.go`, `routes_notifications.go`, and `routes_moderationadmin.go`.
4. Inspect the corresponding `internal/*http` packages for exact route paths, methods, request bodies, and response bodies.
5. Change any colliding helper to register leaf paths without redeclaring an already mounted parent route.
6. Add calls for all five helpers in `mountRoutes` near related user/admin route groups.
7. Add OpenAPI 3 path entries for every newly registered method/path.
8. Run `cd backend && go build ./...`.
9. Run `cd backend && go test ./cmd/gateway/... 2>&1 | tail -18` and inspect the full command exit status plus tail output.
