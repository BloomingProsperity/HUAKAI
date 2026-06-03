# 2026-06-03 platform-settings slice1
| Owner directive | "Implement the FIRST SLICE of gap \"platform-settings\"." |
| Scope | In: backend migration 0077, sqlc query/output config for `platform_settings`, new `internal/platformsettings` store/service, new `internal/platformsettingshttp` handlers, focused unit tests, and `cmd/gateway/routes_platformsettings.go`. Out: hot-path registration/OAuth/voucher/stream/cooldown integrations, `cmd/gateway/routes.go` wiring, frozen-package new files, frontend work. |
| Success criteria | `cd backend && sqlc generate && go build ./... && go vet ./internal/platformsettings/... && go test ./internal/platformsettings/...` pass; tests are discriminating with recorded mutation evidence; commit contains only intended source/generated files. |
| Time estimate | 3-5 wall-clock hours for implementation and verification; single Codex worker. |
| Blast radius | New admin settings surface and schema artifacts. Existing routes remain unchanged because the new route helper is intentionally not wired into `routes.go`. |
| Failure modes | sqlc output block omitted, causing missing generated package; audit action/target not added to CHECK constraints, causing production writes to fail; tests passing despite missing validation; generated line-ending noise in unrelated db packages; accidental frozen-package file creation. Mitigation: verify generated package, extend audit allow-list in 0077, mutation-test each guard, restore unrelated generated files, and inspect staged diff. |
| Decision points | 0077 must extend `admin_audit_events` action/target CHECKs to make the requested audit write usable. This is schema work, but it is necessary for the already-authorized audited upsert slice. No `routes.go` wiring will be done. |
| Pre-execution checklist | 1. Confirm current branch is `work/platform-settings-slice1`. 2. Read the spec/design and billing settings pattern. 3. Confirm actual sqlc config and audit schema constraints. 4. Write tests before implementation. 5. Implement only first slice. 6. Run required checks and mutation verification. 7. Stage only intended files and commit. |

## Concrete execution order

1. Add service tests for fail-closed defaults, allow-list rejection, bool/int validation, audit payload shape, and nil-store sentinel behavior.
2. Add handler tests for platform-admin-only access, unknown key/missing value rejection, default single-key response, optional reason, nil deps, and 64 KiB body limit.
3. Add migration 0077 for `platform_settings` plus audit CHECK allow-list values required by the upsert audit event.
4. Add `sql/queries/platform_settings.sql` and a sqlc output block for `internal/db/platformsettings`.
5. Implement `internal/platformsettings` types, store, service, memory test store, and admin audit sink.
6. Implement `internal/platformsettingshttp` route handlers with local JSON/auth helpers.
7. Add `cmd/gateway/routes_platformsettings.go` with `mountPlatformSettingsRoutes(r chi.Router, d *deps)` and no integration call.
8. Run sqlc, restore unrelated generated noise, and run the required build/vet/test commands.
9. Mutation-verify each discriminating test, restore code, rerun required checks, stage intended files, run the mandated review if available, and commit.
