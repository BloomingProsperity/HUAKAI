# Reference deep dive workspace

Date: 2026-05-02

This directory is for the second-pass deep source review. It is separate from `docs/reference_delta/2026-05-02/`, which remains the first-pass feature delta summary.

Layout:

- `sub2api/`: commercial core first: account health, payment order lifecycle, scheduler/monitor, user self-service.
- `one-api/`: compact channel/token/quota/redeem and gzip/body handling.
- `new-api/`: billing expression, cache/body storage, payment/provider settings.
- `portkey-gateway/`: retry/fallback, guardrail hooks, log/redaction, request validation.
- `helicone/`: request explorer, ClickHouse retention, rate limits, wallet/credits.
- `litellm/`: provider breadth, budgets, cache, runtime retry/cooldown.
- `ai-gateway/`: route policy, quota policy, mutators, metrics.
- `all-api-hub/`: operator tooling, telemetry, sync/export anti-patterns.

Rules for this pass:

- Keep source evidence and conclusions in this root-level workspace.
- Do not edit backend, admin, OpenAPI, or main planning matrix files while Claude is working.
- Every claim must include source path and line number, or be labeled `open-question`.
- High-risk behavior needs a clean-room behavior spec before implementation.
