# 2026-05-19 Wave 3-A proto vendor subpackages

| Owner directive | "Wave 3-A 拆 proto/ 包为 vendor 子包" |
| Scope | In: `backend/internal/proto` vendor-specific SSE/EventStream files, their tests, and Go importers in `backend/internal/gatewayhttp`, `backend/internal/provider`, and `backend/cmd/huakai-verify`. Out: reference reverse-proxy source, frontend, Rust, `vendor/boring`, audit, billing, pool, and `cmd/gateway/main.go`. |
| Success criteria | Vendor-specific proto stream/event helpers live under `proto/anthropic`, `proto/openai`, `proto/gemini`, and `proto/bedrock`; root `proto` retains shared contracts and generic passthrough; all importers compile; `go build ./...` and `go test ./... -race -count=1 -timeout 300s` pass from `backend/`. |
| Time estimate | 45-90 minutes wall clock; one Codex executor pass plus build/test time. |
| Blast radius | Go package import graph across gateway handlers, provider adapters, CLI tools, and proto tests. Failed refactor can break compilation or move shared symbols into vendor packages incorrectly. |
| Failure modes | Missed importer after renaming; mitigation: `rg "proto\\.(Anthropic|OpenAI|Gemini|Bedrock)"` and full build. Package cycle from vendor subpackage importing root `proto`; mitigation: only vendor packages may import root shared types, root must not import vendor packages. Test fixture paths may break after moving tests; mitigation: preserve relative fixture access or adjust paths. |
| Decision points | Stop for Owner only if implementation requires touching high-risk files: auth core, billing ledger, quota enforcement, database schema, deployment scripts, real secrets, `LICENSE`, or forbidden scopes. |
| Pre-execution checklist | 1. Inspect all `backend/internal/proto` Go files. 2. Classify vendor-specific vs shared files. 3. Move vendor SSE/EventStream files and matching tests. 4. Strip vendor prefixes from exported symbols inside vendor packages. 5. Update importers with explicit vendor subpackages. 6. Run residual grep checks. 7. Run build and race tests. |
| Concrete execution order | Create subdirectories, move files with `git mv`, patch package names and symbol names, update importers using `rg`-driven edits, run `gofmt`, then run build/test. |

Assumption: this Codex lane is already Owner-authorized to execute after writing the plan artifact; no reference-project source is needed or allowed for this refactor.
