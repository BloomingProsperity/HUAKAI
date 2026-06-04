# 2026-06-04 image-audio-slice1 Codex plan

| Owner directive | "实现+验证... 给 HUAKAI 加图像生成端点 /v1/images/{generations,edits,variations}(OpenAI 兼容)... v1 只做 JSON 模式... 同时给计费引擎加通用 per-unit 维度... CLEAN-ROOM:严禁读 /home/ubuntu/refs 或任何外部参考。只读 HUAKAI 自有码。" |
| Scope | In: `backend/internal/pricingeval` additive per-unit pricing, new non-frozen `backend/internal/imagepricing`, new non-frozen `backend/internal/imageshttp`, and `backend/cmd/gateway/routes.go` wiring. Out: multipart/binary image upload, schema migrations, billing ledger internals, quota enforcement internals, frozen-package new files, external reference reads, git commit. |
| Success criteria | `pricingeval` keeps existing token behavior and adds unit cost tests; image pricing reads all image rates from rate-table JSON and rejects missing catalog prices; image handlers pass discriminating reserve/settle/abort/passthrough tests for per-image and token-image regimes; gateway build/vet/tests pass with the Owner gate command. |
| Time estimate | 3-5 hours wall clock / one Codex session. |
| Blast radius | Money-path reserve and settle amounts for new image endpoints; shared `pricingeval.FlatCost` if additive unit math regresses; route wiring for gateway startup. Existing chat/embeddings should remain behavior-identical when `HasPerUnit=false`. |
| Failure modes | Undercharge from missing size/quality/N factor: mutation-guarded handler tests. Zero-cost on missing image pricing or missing token-image usage: return 503/502 and abort instead. Double-close on settle backend error: return 500 without abort. Raw body loss: passthrough body test asserts unmodeled field remains. Frozen package violation: no new files under `internal/gatewayhttp`, `internal/gateway`, or `internal/proto`. |
| Decision points | PM deep review remains required before commit because this is money-path. gpt-image output token upper-bound estimation is catalog-driven in this slice; if production catalog lacks the bound key, handler returns pricing_unavailable rather than making a zero-price reservation. Multipart support is parked by Owner instruction. |
| Pre-execution checklist | 1. Read `CLAUDE.md` and `AGENTS.md`. 2. Read embeddings lifecycle template, billing contracts, dispatcher, route wiring, and pricingeval resolver. 3. Claim coordination lock before edits. 4. Write failing tests first for pricingeval, imagepricing, and imageshttp. 5. Implement minimal code. 6. Run gofmt and Owner gate. 7. Do not commit. |

## Clean-Room Note

Reference projects in scope: none for this Codex implementation lane. Owner explicitly instructed not to read `/home/ubuntu/refs` or any external reference and to implement from the PM spec plus HUAKAI self-code only. No upstream behavior claims will be made from memory.

## File Plan

- Modify `backend/internal/pricingeval/resolver.go` and `resolver_test.go`: add `Usage.BillableUnits`, `FlatRateFallback.PerUnit`, `HasPerUnit`, and additive flat-cost tests.
- Create `backend/internal/imagepricing/catalog.go` and `catalog_test.go`: parse image pricing metadata from rate-table JSON without hardcoded prices.
- Create `backend/internal/imageshttp/{handler,billing,pricing,request,response,route,attempt,handler_test}.go`: clone embeddings lifecycle shape for JSON image endpoints.
- Modify `backend/cmd/gateway/routes.go` and `wiring_test.go`: mount the three image routes and prove shared dependencies are wired.

## Execution Order

1. Add failing `pricingeval` tests for unit-only, token-plus-zero-units, and negative unit inputs.
2. Add failing `imagepricing` tests for model/provider/default lookup, size and quality multipliers, amount range, prompt max chars, scheme, and missing price errors.
3. Add failing `imageshttp` handler tests covering the Owner's discriminating matrix.
4. Implement `pricingeval` unit math.
5. Implement `imagepricing` parser and selectors.
6. Implement `imageshttp` handler lifecycle using raw-body passthrough.
7. Wire routes and deps.
8. Run `gofmt`.
9. Run targeted package tests, then the Owner gate command.
