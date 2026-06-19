# Plan — request_diagnose: surface requested_model / upstream_model (model rewrite/fallback)

Date: 2026-06-19 · Author: Claude PM · Slice: disjoint-mining backlog #3 · Feature area: F-hermes (Ask-Hermes ops diagnostics)

## Scope
Add two model-identity keys to the `request_diagnose` Ask-Hermes ops tool's usage diagnostic shape
(`backend/internal/hermesops/tools_observability.go` `usageDiagnosticShape`): `requested_model` and
`upstream_model`. This lets a tenant operator see model rewrite / fallback (requested ≠ upstream) when
correlating a `request_id` — currently the shape shows classes/counts/stream-state but not which model
was asked for vs delivered.

## Not-already-built (verified real code, 2026-06-19)
- `usageDiagnosticShape` (tools_observability.go:108-127) projects id/claim_id/tokens/classes/stream-state/
  settlement but NOT requested_model/upstream_model (grep confirmed absent).

## Value-in-hand (verified — zero db/schema change)
- The projection input is `dbbilling.ListUsageRecordsRow`, which already carries `RequestedModel string`
  and `UpstreamModel *string`, both already SELECTed+scanned by `ListUsageRecords`. The `deref(*string) any`
  helper (tools_credential.go:130) already handles the nullable upstream model.

## Blast radius (verified contained)
- `usageDiagnosticShape` is called ONLY by `RequestDiagnoseSpec` (tools_observability.go:81) — no other tool
  shares it, so the change touches request_diagnose only. No OpenAPI (hermes tools are an internal registry,
  not REST schema). `spec.go` does not enumerate output fields. Existing `TestRequestDiagnoseCorrelatesAndDropsCost`
  asserts counts + actual_cost absence, not an exact key set → adding keys does not break it.

## #16 triple-mirror (real source cites)
- sub2api `backend/internal/handler/ops_error_logger.go:31,403,719` — its ops error logger keeps an
  upstream-model context key distinct from the requested-model field when assembling error context:
  strongest precedent for the requested-vs-upstream diagnostic pairing.
- new-api `model/log.go:42` — its user log row carries a single model-name column; it surfaces the model
  but does not pair requested↔upstream at this granularity.
- CLIProxyAPI — pure relay; no per-request model-rewrite diagnostic (no equivalent).
- **HUAKAI delta (生态/ecosystem)**: pairs requested↔upstream in a read-only, RBAC-gated, diagnostic-shape-only
  ops tool (no cost, no prompts) scoped per tenant by request_id — operator-visible model rewrite/fallback
  without exposing money or raw bodies.

## Changes
1. `tools_observability.go` — add `"requested_model": u.RequestedModel` and `"upstream_model": deref(u.UpstreamModel)`
   to `usageDiagnosticShape`.
2. `tools_test.go` — add a discriminating test: a usage row with RequestedModel ≠ UpstreamModel (a real rewrite),
   assert both surface in usage_records[0]; mutation (drop either key) reds it.

## Success criteria
- build + vet clean; codebudget green; hermesops tests green (-count=1).
- New projection test passes; mutation (drop a projection key) goes RED, verified with -count=1.
- Diagnostic-only invariant intact: the existing cost-leak guard (actual_cost dropped) still holds; model
  names are diagnostic identity, not money/prompt/raw-body.

## Blast radius summary
Single non-collision package (`hermesops`; not in proxies avoidance list; no active parallel branch), one
projection fn used only by request_diagnose, plus its test. Zero db/schema/money/auth. Additive map keys.

## Owner decision points
None — additive read-only diagnostic identity on an RBAC-gated ops tool, no gated risk class.
