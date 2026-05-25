# HUAKAI cache hit-rate audit

Lane: codex / Time: 2026-05-08

Scope: reviewer-only audit. No repository code was changed. Primary files read:

1. `backend/internal/gateway/system_rewrite.go`
2. `backend/internal/gateway/mimicry_compose.go`
3. `backend/internal/gateway/forwarder.go`
4. `backend/internal/proto/anthropic_sse.go`
5. `backend/internal/provider/bedrock/anthropic_request_translator.go`

Additional context read: `cache_control_apply.go`, `cache_control.go`, `tool_name_rewrite.go`, `metadata_user_id.go`, `forwarder_types.go`, `hcsf.go`, `gemini_sse.go`, `cachemetrics.go`, and `provider/bedrock/passthrough.go`.

External current-behavior references checked:

- Anthropic/Claude prompt caching docs: cache prefix order is `tools`, `system`, then `messages`; exact matching requires identical prompt segments and stable `cache_control` locations; usage exposes `cache_read_input_tokens` and `cache_creation_input_tokens`.
  Source: https://platform.claude.com/docs/en/build-with-claude/prompt-caching
- Gemini context caching docs: implicit caching is default for Gemini 2.5+; stable common content should be at the beginning; hit tokens are visible in `usage_metadata`.
  Source: https://ai.google.dev/gemini-api/docs/caching

## Executive verdict

Cache-breaking patterns were found in optional rewrite/mimicry paths. Current `forwarder.go` does not call `ApplyMimicryPlan` or `RewriteSystem`, and repo-wide search found production references only in the implementation/tests, so these are not currently wired into the response forwarder path. If enabled in the request path, `system_rewrite` and `mimicry_compose` can break or degrade vendor prompt-cache hits.

Highest-risk item: `mimicry_compose.go` step 2 deletes `system[].cache_control`, which is a direct prompt-cache marker loss.

## Severity legend

- BLOCKING_HITS: deletes/moves required cache markers or replaces the stable prefix so prior cache entries cannot be read.
- DEGRADES_HITS: deterministic mutation that causes first-run misses, reduces cross-client reuse, or changes cache namespace but may stabilize after warmup.
- SAFE: no request prompt/cache marker mutation in the audited path.

## Findings

### F1. `mimicry_compose.go` strips system cache markers

Severity: BLOCKING_HITS

Evidence:

- Step 2 is explicitly documented as system `cache_control` strip at `backend/internal/gateway/mimicry_compose.go:10-12` and `:72-74`.
- `stripSystemCacheControl` iterates `system` array blocks and deletes `cache_control` at `backend/internal/gateway/mimicry_compose.go:270-280`.
- It then reserializes `system` and the body at `backend/internal/gateway/mimicry_compose.go:290-299`.

Impact:

- Explicit Anthropic block-level prompt caching depends on the `cache_control` marker staying on the cacheable block. This path silently removes the marker from `system[]`, so the vendor cannot write/read the intended system prompt cache.
- It is also an explicit ephemeral marker drop: `{"type":"ephemeral"}` and `{"type":"ephemeral","ttl":"1h"}` are both removed without preserving TTL.

One-line patch description:

- Replace step 2 with "preserve existing system cache_control; only skip conflicting new breakpoint insertion and audit reason `already_has_cache_control`".

### F2. `system_rewrite.go` can replace all system content and drop existing markers

Severity: BLOCKING_HITS for `SystemRewriteReplaceAll`; DEGRADES_HITS for `EnsurePrefix`/`AppendAfter`

Evidence:

- `SystemRewriteReplaceAll` writes `root["system"] = PrefixText` at `backend/internal/gateway/system_rewrite.go:101-104`, losing any prior `system` array/object and its nested `cache_control`.
- `EnsurePrefix` on string system prepends `PrefixText + "\n\n" + original` at `backend/internal/gateway/system_rewrite.go:121-126` and `:238-247`.
- `EnsurePrefix` on system array prepends a new text block before existing raw blocks at `backend/internal/gateway/system_rewrite.go:128-136`.
- Single-object system is normalized into an array and then prefixed at `backend/internal/gateway/system_rewrite.go:138-147`.

Impact:

- No lowercase or trim was found; `strings.HasPrefix` is exact and does not normalize text.
- `ReplaceAll` is a hard cache break when existing cacheable system blocks are present.
- `EnsurePrefix` preserves old raw blocks, including old `cache_control`, but inserts new content before them. That changes the prefix hash up to the marker, so prior cache entries miss until the rewritten prefix is warmed.
- Object-to-array normalization changes prompt block shape and marker path, even when the original object block is preserved as raw content after the injected prefix.

One-line patch description:

- Add a cache-preserving mode that refuses `ReplaceAll` and prefix insertion before any existing `system.cache_control`; emit audit `cache_preserve_blocked_system_rewrite` instead.

### F3. `mimicry_compose.go` tool-name rewrite changes tool cache namespace

Severity: DEGRADES_HITS

Evidence:

- Step 4 calls `RewriteToolNames` at `backend/internal/gateway/mimicry_compose.go:193-203`.
- `RewriteToolNames` rewrites top-level `tools[].name`, message `tool_use.name`, and `tool_choice.name` at `backend/internal/gateway/tool_name_rewrite.go:87-100`.
- Top-level tools are traversed in existing order, but matching tool objects are reserialized after `name` mutation at `backend/internal/gateway/tool_name_rewrite.go:121-155`.
- Message order/content order is preserved, but matching `tool_use` blocks are reserialized after `name` mutation at `backend/internal/gateway/tool_name_rewrite.go:175-231`.

Impact:

- Anthropic cache prefix includes `tools` before `system` and `messages`; changing tool names changes the cache namespace and prevents reuse with unmodified client prompts.
- This does not delete `cache_control`; tests and raw-object logic preserve unknown fields such as `cache_control`, but the tool definition itself is intentionally changed.
- If mimicry is required, it can still warm a stable cache after the first rewritten request, but it will not reuse cache entries written by the original, unmimicked prompt.

One-line patch description:

- Gate tool-name rewrite behind `cache_preserve=false`, or require a stable per-tenant mapping and expose cache hit-rate deltas when this step is enabled.

### F4. `mimicry_compose.go` tools-tail cache breakpoint mutates the last tool object

Severity: DEGRADES_HITS when enabled against an already-warmed unmarked tools prefix; SAFE for preserving existing tail marker

Evidence:

- Step 6 calls `applyToolsTailCacheBreakpoint` at `backend/internal/gateway/mimicry_compose.go:227-236`.
- It refuses to overwrite `tools[-1].cache_control` at `backend/internal/gateway/mimicry_compose.go:329-332`.
- It adds `{"type":"ephemeral"}` and optional `ttl` at `backend/internal/gateway/mimicry_compose.go:339-347`, then reserializes the last tool and `tools` array at `:348-358`.
- It checks the existing cache-control count and refuses once at cap at `backend/internal/gateway/mimicry_compose.go:333-338`.

Impact:

- Existing tail `cache_control` is not silently dropped or overwritten.
- Adding a marker to the last tool mutates the prompt prefix and causes misses versus prior unmarked tool-cache entries, but the new shape can warm and hit if stable.
- The cap guard ignores `InspectCacheControl` errors; valid Anthropic Messages requests should pass, but malformed requests can skip the cap check.

One-line patch description:

- Treat `InspectCacheControl` errors as skip/fail-loud for step 6, and make tools-tail marker insertion conditional on cache strategy rather than unconditional mimicry.

### F5. `anthropic_sse.go` drops cache usage from typed accounting

Severity: SAFE for request cache markers; observability gap for hit-rate audit

Evidence:

- `anthropicEnvelope` parses `message.usage` and `delta.usage` into `CanonicalUsage` at `backend/internal/proto/anthropic_sse.go:33-40` and `:56-63`.
- `CanonicalUsage` only has `input_tokens`, `output_tokens`, and `total_tokens` at `backend/internal/proto/hcsf.go:95-100`.
- `message_delta` merges only those typed fields at `backend/internal/proto/anthropic_sse.go:138-141` and `:225-241`.
- `forwarder.go` copies only input/output tokens into `UsageRecordDraft`; cache creation/read fields remain zero at `backend/internal/gateway/forwarder.go:306-307`, while draft has cache fields at `backend/internal/gateway/forwarder_types.go:78-84`.
- Existing `cachemetrics` has global expvar counters but `rg` found no production call sites; package docs also explicitly reject per-tenant counters for now at `backend/internal/cachemetrics/cachemetrics.go:1-18`.

Impact:

- This does not break prompt-cache hits, because it is response-side parsing only.
- It does prevent reliable hit-rate measurement: Anthropic nested `usage.cache_creation_input_tokens`, `usage.cache_read_input_tokens`, and `usage.cache_creation` are not captured into typed usage/accounting.
- Existing field-matrix comments claiming passthrough preservation are too weak for metrics, because passthrough is envelope-level and does not promote nested usage cache fields into accounting counters.

One-line patch description:

- Extend `CanonicalUsage` with cache creation/read fields, parse nested Anthropic usage into them, update `UsageAccumulator`/`UsageRecordDraft`, and call a bounded metrics recorder from the forwarder.

## Per-file checklist

### `backend/internal/gateway/system_rewrite.go`

- Moves system prompt bytes: yes, when enabled and not already prefixed. `ReplaceAll` replaces the whole field; `EnsurePrefix` prepends; `AppendAfter` appends.
- Lowercase/trim: no. Prefix detection is exact `strings.HasPrefix`; no case folding or trim.
- Reorders messages / merges adjacent same role: no. It never decodes or writes `messages`.
- Changes tools order/fields: no. It never decodes or writes `tools`.
- Breaks/moves/deletes `cache_control`: `ReplaceAll` deletes existing system block markers; `EnsurePrefix` preserves existing raw marker but moves it later in the system array by prepending a new block; `AppendAfter` preserves existing array markers.
- Silently drops ephemeral: yes for `ReplaceAll` over a system object/array carrying `cache_control`; no for EnsurePrefix array/object paths because existing raw blocks are preserved.
- File verdict: BLOCKING_HITS for `ReplaceAll`; DEGRADES_HITS for other active rewrite modes; SAFE only when `PrefixText==""` or already prefixed no-op.

### `backend/internal/gateway/mimicry_compose.go`

- Moves system prompt bytes: yes through step 1 when `SystemRewrite` is non-nil.
- Lowercase/trim: no in this file.
- Reorders messages / merges adjacent same role: no message array reorder or merge found.
- Changes tools order/fields: yes. Step 4 can change tool names; step 6 can add `cache_control` to last tool.
- Breaks/moves/deletes `cache_control`: yes. Step 2 deletes `system[].cache_control`. Step 3 may add new markers. Step 6 adds marker to last tool but refuses to overwrite existing tail marker.
- Silently drops ephemeral: yes, step 2 deletes system ephemeral markers without preserving TTL.
- File verdict: BLOCKING_HITS when `StripSystemCacheControl=true`; DEGRADES_HITS for tool rewrite and new tools-tail breakpoint; SAFE only when mimicry disabled or all mutating steps are nil/false.

### `backend/internal/gateway/forwarder.go`

- Moves system prompt bytes: no; stream response forwarder only.
- Lowercase/trim: no request prompt manipulation.
- Reorders messages / merges adjacent same role: no.
- Changes tools order/fields: no.
- Breaks/moves/deletes `cache_control`: no.
- Silently drops ephemeral: no request marker path.
- Call-site check: no `ApplyMimicryPlan` or `RewriteSystem` call found in this file; repo-wide `rg` found production references only in implementation files and tests.
- File verdict: SAFE for cache-breaking patterns. Separate observability gap: cache creation/read tokens are not copied into draft accounting.

### `backend/internal/proto/anthropic_sse.go`

- Moves system prompt bytes: no; response event parsing only.
- Lowercase/trim: no request prompt manipulation.
- Reorders messages / merges adjacent same role: no.
- Changes tools order/fields: no.
- Breaks/moves/deletes `cache_control`: no request marker path.
- Silently drops ephemeral: no request marker path.
- Cache usage handling: drops nested cache usage from typed accounting because `CanonicalUsage` lacks fields.
- File verdict: SAFE for cache-breaking patterns; metrics/accounting gap.

### `backend/internal/provider/bedrock/anthropic_request_translator.go`

- Moves system prompt bytes: no nested mutation. It stores top-level values as `json.RawMessage` and only deletes `model`/`stream`, then injects `anthropic_version`.
- Lowercase/trim: no.
- Reorders messages / merges adjacent same role: no. Nested `messages` raw bytes are carried through.
- Changes tools order/fields: no. Nested `tools` raw bytes are carried through.
- Breaks/moves/deletes `cache_control`: no. Nested block-level markers and top-level `cache_control` are preserved unless they are inside deleted `model`/`stream`, which they are not.
- Silently drops ephemeral: no.
- Caveat: top-level JSON field order/spacing changes after map marshal, but nested prompt-bearing raw messages/system/tools bytes are preserved. Also, current Anthropic docs say automatic top-level caching support for Amazon Bedrock is not yet equivalent to Claude API; explicit block markers are the safer assumption on Bedrock.
- File verdict: SAFE.

## Metrics recommendation

Do expose cache hit metrics, but do not rely on the current global-only `cachemetrics` package as sufficient for a multi-tenant cache audit.

Recommended shape:

- Request/accounting storage: persist cache fields on the usage record, not only expvar.
  - Anthropic: `cache_creation_input_tokens`, `cache_read_input_tokens`, and if present `cache_creation.ephemeral_5m_input_tokens` / `cache_creation.ephemeral_1h_input_tokens`.
  - Gemini: `cachedContentTokenCount` as cache-read tokens for implicit/explicit caching; keep `promptTokenCount` as denominator context.
- Expvar MVP:
  - Keep global counters: `cache_creation_input_tokens_total`, `cache_read_input_tokens_total`, `cache_observed_requests_total`, `cache_hit_requests_total`.
  - Add bounded per-tenant maps only if tenant cardinality is capped/redacted: `cache_token_count_by_tenant[tenant_hash|provider|model]`.
  - Do not export raw tenant IDs or account credentials in `/debug/vars`.
- Production-preferred:
  - Use Prometheus/OpenTelemetry labels with cardinality controls, or derive per-tenant views from stored usage records.
  - Labels: `tenant_hash`, `provider_family`, `model_family`, `cache_mode` (`anthropic_explicit`, `anthropic_auto`, `gemini_implicit`, `gemini_explicit`), and `mimicry_enabled`.
- Alert ratios:
  - Anthropic token hit ratio: `cache_read / (cache_read + cache_creation + uncached_input_after_breakpoint)`.
  - Operational warm-cache ratio: `cache_read / (cache_read + cache_creation)`.
  - Gemini hit token ratio: `cachedContentTokenCount / promptTokenCount` when `promptTokenCount > 0`.
- Must-have dimensions for this audit:
  - `system_rewrite_reason`
  - `mimicry_step_applied`
  - `strip_system_cache_control_applied`
  - `tool_name_rewrite_applied`
  - `provider_family`
  - `upstream_model`

## Recommended next fixes

1. Block `StripSystemCacheControl` on cache-sensitive Anthropic routes, or remove it entirely and let `ApplyBreakpoints` skip already-marked blocks.
2. Add cache-preserving mode to `RewriteSystem` that refuses `ReplaceAll` and any prefix insertion before an existing system marker.
3. Treat tool-name rewrite as incompatible with cache-preserve mode unless a stable, documented mapping is intentionally selected.
4. Parse and account cache usage fields before debating hit-rate regressions; without this, regressions are invisible.
5. Add regression tests with a system array carrying `cache_control: {"type":"ephemeral","ttl":"1h"}` through each enabled mimicry plan.

## Owner summary

做了 reviewer-only 缓存命中率审计，没有改仓库代码，只写了 `/tmp/parallel-cache-audit-codex/report.md`。核心结论：当前 `forwarder.go` 没有调用 mimicry/system rewrite，所以已读路径本身不改请求 prompt；但一旦启用 mimicry，`stripSystemCacheControl` 会直接删除 `system[].cache_control`，属于 BLOCKING_HITS；`SystemRewriteReplaceAll` 会丢掉已有 system marker，也属于 BLOCKING_HITS；工具名混淆和 tools-tail marker 会改变 cache namespace，属于 DEGRADES_HITS。没有发现 Bedrock translator 会改 nested system/messages/tools 或丢 ephemeral。功能没有缩水，clean-room 风险无新增，安全风险主要是 `/debug/vars` per-tenant 指标需要 tenant hash/基数控制。需要 Owner 确认：是否把“cache-preserve mode”设为 Anthropic/Gemini 默认，以及是否禁止 mimicry step 2 进入生产路径。下一步建议先修 marker 保留和 cache usage 解析/指标，再评估真实命中率。
