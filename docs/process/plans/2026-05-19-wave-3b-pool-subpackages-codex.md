# 2026-05-19 Wave 3-B pool subpackages

| Owner directive | "Wave 3-B 拆 pool/ 包按职责分子包" |
| Scope | In: `backend/internal/pool` Go package refactor into router/scoring/binding/dispatcher with root API compatibility; necessary Go importer updates and tests. Out: reference reverse-proxy source, frontend, Rust, `vendor/boring`, `proto`, `backend/internal/db`, schema/sqlc changes, runtime dependency additions. |
| Success criteria | Root `pool` retains public selector interfaces/types through `api.go`/`types.go`; implementation files move into responsible subpackages; no import cycle; `cd backend && GOCACHE=/tmp/go-cache go build ./...` passes; `cd backend && GOCACHE=/tmp/go-cache go test ./... -race -count=1 -timeout 300s` is run and reported. |
| Time estimate | 60-120 minutes wall clock; mostly mechanical Go package moves plus build/test repair. |
| Blast radius | Gateway selector wiring, pool tests, provider/channel-health users of pool interfaces, and expvar metric initialization. |
| Failure modes | Import cycles from root re-export and subpackage type use; missed importer path changes; moved tests still assuming old package-private helpers; expvar duplicate registration if tests import multiple wrappers incorrectly; DB adapter package split accidentally touching sqlc generated code. Mitigation: introduce a thin shared `pool/types` contract package, root aliases only, move tests with their source package, and iterate with `go test ./internal/pool/...` before full backend checks. |
| Decision points | Adding the thin `backend/internal/pool/types` helper subpackage is required to avoid Go import cycles while preserving root `pool` API. No high-risk files are in scope. |
| Pre-execution checklist | Read `docs/RULES.md`; inspect all current `backend/internal/pool/*.go`; confirm no reference source reads; create destination directories; move files by responsibility; update package/imports via Go tooling; run build and race tests. |

## Concrete Execution Order

1. Read current pool files and importer list.
2. Create `router`, `scoring`, `binding`, `dispatcher`, and the minimal shared `types` package.
3. Move HRW/PASR/segment/feedback/aging and default selection into `router`.
4. Move score blend/locality/demote primitives into `scoring`; keep root aliases for public score-facing types where needed.
5. Move sticky and claim-gate adapters into `binding`.
6. Move slot/DB account/repo and selector dispatcher into `dispatcher`.
7. Add root `api.go`, `types.go`, and `doc.go` aliases/wrappers so external import path remains stable.
8. Move or adjust tests so each subpackage owns its unit tests and root keeps cross-package integration tests.
9. Update gateway and other importers only where constructor ownership requires direct subpackage use.
10. Run `go build ./...`, then `go test ./... -race -count=1 -timeout 300s`, and record any blockers honestly.
