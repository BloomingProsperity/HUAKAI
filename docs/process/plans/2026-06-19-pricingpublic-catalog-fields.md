# Plan — public pricing page catalog-metadata parity (owned_by / mode / max_output_tokens / capabilities)

Date: 2026-06-19 · Author: Claude PM · Slice: disjoint-mining backlog #1 · Feature area: F-pricing-public (public pricing surface)

## Scope
Surface four already-in-hand catalog descriptive fields on the unauth public pricing endpoint
(`/v1/pricing/page`, `backend/internal/pricingpublichttp/handler.go`): `owned_by`, `mode`,
`max_output_tokens`, `capabilities`. The values already flow on `registry.ListedModel`
(`backend/internal/registry/models_list.go:12` — `OwnedBy`/`Mode`/`MaxOutputTokens`/`Capabilities`)
and are read in the existing projection loop; they are simply not projected onto the response item.

## Not-already-built (verified real code, 2026-06-19)
- `pricingItem` struct (`handler.go:28-34`) has only Model/CanonicalID/prices/ContextLength — the four
  fields are absent from the response (grep confirmed).
- Source values present on `ListedModel` (no registry/schema change needed).
- Exact precedent: `backend/internal/controlhttp/model_list_handler.go:106-126` already maps these four
  onto the authed `/v1/models` response with nil-guard/non-empty/omitempty — so they are sanctioned
  public catalog metadata, not a new disclosure. This slice mirrors that projection on the public page.

## #16 triple-mirror (real source cites)
- new-api `dto/pricing.go:10` — its public `/api/pricing` DTO (`docs/openapi/api.json:327`) exposes `owned_by`.
- sub2api `backend/internal/handler/gateway_handler.go:1074` (production) sets `owned_by` on its gateway
  model listing; model-pricing resource README lists "Model capabilities".
- CLIProxyAPI `sdk/api/handlers/openai/openai_handlers.go:70-84` exposes `owned_by` on its models handler.
- **HUAKAI delta (生态/ecosystem + parity)**: exposes the richer four-field catalog set (owned_by + mode +
  max_output_tokens + capabilities) sourced from the canonical registry catalog on the unauth pricing page,
  reusing the identical projection already sanctioned on the authed model-list — single source of truth,
  no per-surface drift. Not an architecture/algorithm change.

## Changes
1. `handler.go` — add 4 fields to `pricingItem` (json `owned_by,omitempty` / `mode,omitempty` /
   `max_output_tokens,omitempty` / `capabilities,omitempty`) + project in the loop mirroring controlhttp's
   guards (non-empty string / non-nil *int / non-empty map).
2. `handler_test.go` — add 4 fields to `decodedPricingItem`; seed `publicPricingFixture()` with discriminating
   values (Mode="chat", MaxOutputTokens=16384, Capabilities{vision,tools}; OwnedBy already "openai"); add a
   projection test asserting all four surface, plus that the existing forbidden-leak set stays absent.
3. `docs/openapi/openapi.yaml` — add the 4 properties to the `PublicPricingItem` schema.

## Success criteria
- `go build ./...` + `go vet ./...` clean; codebudget gate green (pricingpublichttp tiny).
- New projection test passes; mutation (delete an `owned_by`/`mode`/`max_output_tokens`/`capabilities`
  assignment) makes it RED (fixture values ≠ zero).
- OpenAPI↔route consistency gate (cmd/gateway) green.
- No secret/identity/cost leak: owned_by/mode/max_output_tokens/capabilities are catalog metadata; the
  existing forbidden-field leak test (actual_cost/user_id/api_key_id/provider_account_id/internal_ratio/
  model_multiplier) remains green.

## Blast radius
Single non-collision package (`pricingpublichttp`, one commit in history, no active parallel branch) + OpenAPI
doc. Zero db/schema/money/auth/quota. Additive omitempty fields = backward-compatible for existing clients.

## What could go wrong / mitigations
- Capabilities map shared by reference (same as controlhttp) — read-only serialization, no mutation. OK.
- owned_by on a PUBLIC endpoint: it is model-catalog provenance (same class as the already-public model name +
  context_length), not the actual upstream account/channel; the authed list already exposes it. Safe.

## Owner decision points
None — additive public catalog metadata, no gated risk class. Standard autonomous slice.
