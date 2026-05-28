# 2026-05-28 S1-017 Buffered SSE Fallback Codex Plan

| Owner directive | "You are implementing a correctness fix ... S1-017 ... Do NOT git add, commit, or push." |
| Scope | In: add `backend/internal/protosse` package and tests; edit existing `backend/internal/gateway/upstream_dispatcher_hcsf.go` and `backend/internal/gatewayhttp/chat_completions_handler.go` only for fallback wiring. Out: new files under frozen `gateway`, `gatewayhttp`, or `proto`; edits under `backend/internal/proto`; billing/auth/quota/schema/deployment changes; commits. |
| Success criteria | SSE-looking buffered 2xx bodies reconstruct into `proto.HCSF.BufferedResponse` with assistant content and usage; normal JSON bodies return `(nil, nil, false)` from the sniffer; gateway buffered paths use the fallback only after normal adapter parsing errors; requested `go test` and `go build` commands pass or failures are reported honestly. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass with test-first verification. |
| Blast radius | Medium: non-streaming upstream response parsing and billing settlement depend on this path. The fallback is gated behind adapter parse error plus explicit SSE sniff to avoid changing normal JSON behavior. |
| Failure modes | False-positive SSE detection could misparse JSON text; mitigate by requiring a trimmed line prefix of `data:` or `event:` and adding JSON control test. Incorrect event/state pairing could break non-OpenAI adapters; mitigate by mirroring existing stream state selection from `StreamForwarder.newUpstreamState` using public adapter types. Missing usage/content in reconstruction could still abort billing; mitigate with fixture asserting text and usage. |
| Decision points | No high-risk decision expected. If the existing public adapter interface cannot reveal a safe stream state type, stop and report instead of guessing. |
| Pre-execution checklist | 1. Read `AGENTS.md`, `CLAUDE.md`, and `docs/RULES.md` Owner gate. 2. Confirm frozen package rule and target package is not frozen. 3. Inspect `proto.UpstreamAdapter`, streaming adapter state types, and buffered error paths. 4. Write failing `protosse` tests first. 5. Implement fallback without reading or copying non-HUAKAI reference source. 6. Run requested backend verification commands. |

## Concrete Execution Order

1. Add `backend/internal/protosse/reconstruct_test.go` with an OpenAI-style SSE body fixture and a normal JSON control.
2. Run the `protosse` test and confirm RED because the package/function does not exist yet.
3. Add `backend/internal/protosse/reconstruct.go`:
   - sniff SSE by scanning trimmed lines for `data:` or `event:`;
   - split SSE blocks on blank lines and join `data:` lines per event;
   - create fresh upstream state by adapter concrete type (`openai`, `gemini`, default `anthropic` as in streaming path);
   - feed data payloads to `ProviderEventToCanonicalEvents`, then finalize;
   - fold canonical events into `CanonicalResponse` content blocks and usage.
4. Wire fallback in `upstream_dispatcher_hcsf.go` only when `ProviderResponseToCanonical` returns an error.
5. Wire the same fallback in `chat_completions_handler.go` only when `ProviderResponseToCanonical` returns an error.
6. Run `gofmt`, the requested `go test`, and the requested `go build`.

