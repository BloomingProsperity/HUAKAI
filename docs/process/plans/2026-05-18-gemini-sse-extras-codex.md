# 2026-05-18 Gemini SSE extras retention
| Owner directive | "修 proto Gemini SSE 字段丢失 (audit list HIGH)" |
| Scope | In: `backend/internal/proto/gemini_sse.go`, `backend/internal/proto/openai_sse.go`, focused tests under `backend/internal/proto/`, cachemetrics calls only where finalize paths already exist. Out: frontend, Rust, vendor, reference reverse-proxy source, unrelated backend modules, schema/auth/billing/quota core. |
| Success criteria | Gemini SSE unknown fields round-trip through parse and marshal; Gemini finalize observes delivered/skipped cache metrics consistently with nearby proto code; OpenAI SSE multi-event chunks preserve per-event extras; requested build and proto race tests pass. |
| Time estimate | 45-90 minutes wall clock, one Codex executor lane. |
| Blast radius | Low to medium: changes are scoped to protocol parsing/marshalling and tests, but affect streamed response metadata delivery. |
| Failure modes | Dropping known Gemini fields while preserving extras; duplicating extra fields on marshal; mismatching cache metric labels; changing OpenAI event framing. Mitigation: follow existing helpers, add regression tests, run targeted race tests and build. |
| Decision points | Stop only if fix requires touching high-risk modules, adding runtime dependencies, or changing API/database/auth/billing/quota contracts. |
| Pre-execution checklist | Read Gemini SSE implementation; read Anthropic/OpenAI extras helpers; read cachemetrics interface and existing tests; implement scoped patch; run gofmt; run `go build ./...`; run `go test ./internal/proto/... -race -count=1 -timeout 120s`. |

