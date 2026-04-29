# Codex Final Reviewer-Lane Report - F-GW-002 Streaming Forwarder Synthesis

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane |
| Review date | 2026-04-29 Asia/Shanghai; Owner-requested filename date retained as 2026-04-28 |
| Artifact reviewed | `docs/decompositions/_cross-cutting/streaming-forwarder-synthesis.md` |
| Gate | CL-001..CL-011 strict path review for F-GW-002 |
| Verdict | APPROVE-WITH-FIXES |
| Local Sub2API source | `.omc/reference-src/sub2api` at `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |
| Local one-api source | `.omc/reference-src/one-api` at `8df4a2670b98266bd287c698243fff327d9748cf` |
| Local Helicone source | `.omc/reference-src/helicone` at `548832f8e763a33732ead27d8b2dcaeccc665a39` |

## Review Protocol Notes

- Pre-commitment prediction 1: the synthesis would correctly carry forward the Bedrock drain correction but still leave TODO residue from the earlier v2 review.
- Actual: confirmed. It correctly distinguishes Anthropic-conversion no-drain vs Bedrock drain, but `TODO-3` still asks to re-check `bedrock_stream.go`, and the "Bedrock-only" wording is false against adjacent Sub2API passthrough paths.
- Pre-commitment prediction 2: atomic billing framing would be materially improved versus v1, but source traceability would depend on F-OBS-001 rather than direct local citation.
- Actual: confirmed. The claim is source-true, but the synthesis should add `observability-synthesis.md` / Sub2API observability input as explicit evidence.
- Pre-commitment prediction 3: CL-006 would be weak because cross-references to Helicone and one-api often omit E-LIC rows.
- Actual: confirmed. `Sources` names one-api and Helicone without E-LIC rows even though ledger rows exist.
- Pre-commitment prediction 4: CL-001 would still be partial because the synthesis keeps upstream function names for source review.
- Actual: confirmed. This is acceptable in reviewer evidence, but not in the final implementer-facing `docs/specs/streaming-forwarder.md`.
- Pre-commitment prediction 5: one-api would remain a low-confidence comparison unless `relay/controller/text.go` was checked.
- Actual: confirmed. The local source shows a simpler pre-consume / `DoResponse` / async post-consume path, but the synthesis keeps that as an open TODO.
- Review mode: escalated to ADVERSARIAL after finding the false "Bedrock-only drain" statement, because that is the same class of over-broad source generalization that caused the earlier F-GW-002 rejection.
- Self-audit result: no CRITICAL finding survived. All MAJOR findings below have direct artifact evidence plus source evidence.
- Realist check result: severity is MAJOR, not CRITICAL, because this is a pre-release document and the fixes are bounded text/spec corrections. If moved to `docs/specs/streaming-forwarder.md` without fixes, the verdict becomes REJECT.

## §1 - CL-001..011 Verdict Matrix

| Check | Verdict | One-line justification |
| --- | --- | --- |
| CL-001 | PARTIAL | The synthesis still carries upstream identifiers in release-facing prose: `detachStreamUpstreamContext`, `writeUsageLogBestEffort`, `parseSSEUsagePassthrough`, `mergeAnthropicUsage`, and `Apply`. Keep them only in evidence/provenance, not the Released spec body. |
| CL-002 | PASS | No upstream database table, column, or migration names are copied in the synthesis body. `Usage Record`, `Billing Ledger`, `Provider Account`, `Route`, and `Pool` are HUAKAI glossary/domain-model terms. |
| CL-003 | PASS | No upstream UI component names, frontend class names, or dashboard layout identifiers appear in the synthesis. |
| CL-004 | PASS | No upstream documentation sentence longer than common technical phrases is copied. The source-shaped items are identifiers/citations, not copied prose. |
| CL-005 | PASS | The HUAKAI algorithm is expressed as local guarantees and design phases, not a line-by-line translation of Sub2API source. The release spec still needs identifier cleanup under CL-001. |
| CL-006 | PARTIAL | Sub2API is tied to `E-LIC-001`, but the same `Sources` field also names one-api and Helicone without `E-LIC-004` and `E-LIC-007`. |
| CL-007 | PASS | `Lane mode` is explicitly `Option C` at `streaming-forwarder-synthesis.md:7`; F-GW-002 intersects gateway hot path, account failover/health, and billing reconciliation. |
| CL-008 | PASS | `Feature ID` is `F-GW-002` at `streaming-forwarder-synthesis.md:6`, and the parity matrix has `F-GW-002` at `docs/03_FEATURE_PARITY_MATRIX.md:38`. |
| CL-009 | FAIL | `Open TODOs` remain at `streaming-forwarder-synthesis.md:206-208`, and the file itself says they "DO block Released spec" at line 210. |
| CL-010 | PASS | No upstream source URL is embedded in implementer-relevant sections. The synthesis uses local doc links and local source path references. |
| CL-011 | FAIL | Synthesis files are exempt only when they inherit correct citations. Spot-checking found one false inherited generalization: `Bedrock-only` drain is contradicted by Sub2API OpenAI and Anthropic passthrough drains. |

Detailed CL notes:

- CL-001 pressure is not a current license-contamination finding. The identifiers are used for reviewer verification.
- Before `docs/specs/streaming-forwarder.md` is Released, implementer-facing sections must use HUAKAI vocabulary only.
- CL-006 is mechanical: add one-api and Helicone license rows to `Sources`, and list the F-OBS evidence file used for atomic-billing framing.
- CL-009 is a hard release blocker. The file says the TODOs block Released spec; the reviewer should not overrule that.
- CL-011 is the substantive source issue. The document corrected Bedrock but overcorrected into "Bedrock-only", missing other Sub2API passthrough drain paths.

## §2 - Spot-Check Log

Spot-check method:

- I sampled source claims from the synthesis and its stated inputs.
- I used `rg -n` against local clones under `.omc/reference-src`.
- Verdict meanings:
- PASS: cited source matches the claim.
- FAIL: cited source exists but contradicts or does not support the claim.
- MISSING: the synthesis makes or depends on the claim but lacks adequate release-ready source citation.

### Spot-check 01 - Sub2API pinned commit

- Synthesis claim: Sub2API source is commit `b0a2252...`.
- Grep/command evidence: `git -C .omc/reference-src/sub2api rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`.
- Verdict: PASS.

### Spot-check 02 - Default scanner buffer is 500 MiB

- Synthesis claim: Sub2API default scanner buffer is 500 MiB.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/gateway_service.go:46` defines `defaultMaxLineSize = 500 * 1024 * 1024`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/service/gateway_forward_as_chat_completions.go:367-372` creates a `bufio.Scanner`, reads `cfg.Gateway.MaxLineSize` when set, then calls `scanner.Buffer`.
- Verdict: PASS.

### Spot-check 03 - Chat-completions streaming uses line-based SSE parsing

- Synthesis claim: Anthropic-conversion paths use `bufio.Scanner` line-based SSE parsing.
- Grep evidence: `gateway_forward_as_chat_completions.go:367-372` initializes the scanner and buffer.
- Grep evidence: `gateway_forward_as_chat_completions.go:430-443` reads `event:` then `data:` lines before JSON decode.
- Verdict: PASS.

### Spot-check 04 - Per-event flush in chat-completions streaming

- Synthesis claim: per-event flush exists for real-time client visibility.
- Grep evidence: `gateway_forward_as_chat_completions.go:426` calls `c.Writer.Flush()` after event processing.
- Grep evidence: `gateway_forward_as_chat_completions.go:477-479` emits `[DONE]` and flushes on normal end.
- Verdict: PASS.

### Spot-check 05 - Inline usage extraction from `message_start` / `message_delta`

- Synthesis claim: Anthropic SSE usage is extracted inline from `message_start` and `message_delta`.
- Grep evidence: `gateway_forward_as_chat_completions.go:407-413` merges usage from `message_delta` and `message_start`.
- Grep evidence: `gateway_forward_as_responses.go:408-413` mirrors the same extraction in Responses conversion.
- Verdict: PASS.

### Spot-check 06 - First-token latency tracking

- Synthesis claim: first-token latency is tracked on the first emitted chunk.
- Grep evidence: `gateway_forward_as_chat_completions.go:400-405` sets `firstTokenMs` the first time `processAnthropicEvent` runs.
- Grep evidence: `gateway_forward_as_responses.go:405` sets `firstTokenMs` in the streaming response path.
- Verdict: PASS.

### Spot-check 07 - Chat-completions disconnect does not drain

- Synthesis claim: Anthropic-conversion chat-completions path exits immediately on client disconnect.
- Grep evidence: `gateway_forward_as_chat_completions.go:394-395` returns `true` on downstream write error.
- Grep evidence: `gateway_forward_as_chat_completions.go:450-451` returns `resultWithUsage()` immediately when `processAnthropicEvent` reports disconnect.
- Verdict: PASS.

### Spot-check 08 - `detachStreamUpstreamContext` decouples cancellation but is not a drain loop

- Synthesis claim: `detachStreamUpstreamContext` returns `context.WithoutCancel(ctx)` for streams and is not itself a drain primitive.
- Grep evidence: `gateway_service.go:7781-7789` defines `detachStreamUpstreamContext`; `gateway_service.go:7787` returns `context.WithoutCancel(ctx)`.
- Grep evidence: chat-completions still returns on disconnect at `gateway_forward_as_chat_completions.go:450-451`.
- Verdict: PASS.

### Spot-check 09 - Hardcoded failover status list

- Synthesis claim: pre-stream failover list is 401, 403, 429, 529, plus all 5xx.
- Grep evidence: `gateway_service.go:3669-3674` implements `shouldFailoverUpstreamError` with cases `401, 403, 429, 529` and default `statusCode >= 500`.
- Verdict: PASS.

### Spot-check 10 - Sub2API atomic billing claim/effects

- Synthesis claim: Sub2API billing is atomic; `Apply` runs claim plus effects in one PostgreSQL transaction.
- Grep evidence: `usage_billing_repo.go:35` starts a SQL transaction.
- Grep evidence: `usage_billing_repo.go:45` claims the billing key.
- Grep evidence: `usage_billing_repo.go:54` applies billing effects before `tx.Commit()` at line 58.
- Verdict: PASS.

### Spot-check 11 - Usage Record write is detached from billing transaction

- Synthesis claim: Usage Record write is detached/best-effort and happens outside the billing `Apply`.
- Grep evidence: `gateway_service.go:8023-8033` calls `applyUsageBilling`.
- Grep evidence: `gateway_service.go:8038` calls `writeUsageLogBestEffort` after billing succeeds.
- Grep evidence: `gateway_service.go:7812-7820` defines `writeUsageLogBestEffort` and invokes `CreateBestEffort`.
- Verdict: PASS.

### Spot-check 12 - Bedrock drain after downstream disconnect

- Synthesis claim: Bedrock keeps reading upstream after downstream write failure, suppressing client writes and extracting usage.
- Grep evidence: `bedrock_stream.go:136` calls `parseSSEUsagePassthrough` before write.
- Grep evidence: `bedrock_stream.go:142-153` writes only when `!clientDisconnected`, sets `clientDisconnected = true` on write error, and logs continued draining.
- Grep evidence: `bedrock_stream.go:157-163` returns on interval timeout when disconnected.
- Verdict: PASS.

### Spot-check 13 - "Bedrock-only drain" generalization

- Synthesis claim: `Sub2API has Bedrock-only unbounded drain + zero drain in Anthropic-conversion paths` at `streaming-forwarder-synthesis.md:63`.
- Grep evidence: `openai_gateway_service.go:3373-3385` has OpenAI passthrough `clientDisconnected` and logs continued upstream drain for usage.
- Grep evidence: `openai_gateway_service.go:4108` says the client-disconnected path continues draining upstream for usage.
- Grep evidence: `openai_gateway_service.go:4244-4260` continues processing upstream lines while downstream writes are suppressed after disconnect.
- Grep evidence: `gateway_service.go:5039-5147` shows Anthropic passthrough client disconnect and continued drain for usage.
- Grep evidence: `gateway_service.go:6886-7070` shows another streaming path continuing to drain after client disconnect.
- Verdict: FAIL.
- Required release action: replace `Bedrock-only` with a path-specific matrix covering Anthropic conversion no-drain, Bedrock drain, OpenAI passthrough drain, and Anthropic passthrough drain.

### Spot-check 14 - one-api `relay/controller/text.go` streaming baseline

- Synthesis TODO: cross-check one-api `relay/controller/text.go`.
- Grep evidence: `one-api/relay/controller/text.go:46-49` pre-consumes estimated quota.
- Grep evidence: `one-api/relay/controller/text.go:79` delegates response handling to `adaptor.DoResponse`.
- Grep evidence: `one-api/relay/controller/text.go:85-86` launches async `postConsumeQuota`.
- Grep evidence: `one-api/relay/adaptor/openai/main.go:27-96` uses a scanner, forwards SSE lines, stores provider usage when present, emits `[DONE]` if missing, and returns accumulated text plus usage.
- Verdict: PASS as a reviewer-resolved TODO; MISSING in synthesis because it remains an open TODO instead of a closed cited comparison.

### Spot-check 15 - `AcquireAccountSlotWithWait` is not atomic with usage

- Synthesis TODO: verify whether `gateway_helper.go` `AcquireAccountSlotWithWait` makes Pool slot atomic with usage.
- Grep evidence: `gateway_helper.go:267-271` calls `TryAcquireAccountSlot`.
- Grep evidence: `gateway_helper.go:295-299` defines `acquireSlot` as only user/account concurrency-slot acquisition.
- Grep evidence: `gateway_helper.go:360-365` retries only `acquireSlot` before returning the release function.
- Verdict: PASS as a verified negative; MISSING in synthesis because it remains open.

### Spot-check 16 - Helicone analytics decoupling

- Synthesis claim: Helicone is used only for analytics-decoupling discipline.
- Grep evidence: `ProxyForwarder.ts:437-543` schedules logging via `ctx.waitUntil` and calls a logging path with `HeliconeProducer`.
- Grep evidence: `LoggingHandler.ts:292-316` persists logging batches across Postgres/S3/ClickHouse style tiers with `Promise.all`.
- Verdict: PASS, but CL-006 requires the synthesis `Sources` row to cite `E-LIC-007` for this GPL-3.0 behavior-only source.

### Spot-check 17 - Feature and license rows

- Synthesis claim: feature is `F-GW-002`; Sub2API source has `E-LIC-001`; one-api and Helicone are cross-referenced.
- Grep evidence: `docs/03_FEATURE_PARITY_MATRIX.md:38` has `F-GW-002`.
- Grep evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:15` has Sub2API `E-LIC-001`.
- Grep evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:18` has one-api `E-LIC-004`.
- Grep evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:21` has Helicone `E-LIC-007`.
- Verdict: PASS for ledger existence; FAIL/PARTIAL for synthesis `Sources` because line 10 omits `E-LIC-004` and `E-LIC-007`.

## §3 - Findings

### Critical Findings

None.

### Major Finding 1 - "Bedrock-only drain" is false and hides other Sub2API drain paths

- Evidence: `streaming-forwarder-synthesis.md:63` says `Sub2API has Bedrock-only unbounded drain + zero drain in Anthropic-conversion paths`.
- Evidence: `openai_gateway_service.go:3373-3385` has OpenAI passthrough `clientDisconnected` and logs continued drain for usage.
- Evidence: `openai_gateway_service.go:4108` explicitly says client disconnect continues upstream drain for usage.
- Evidence: `openai_gateway_service.go:4244-4260` suppresses downstream writes after disconnect while continuing upstream processing and usage parsing.
- Evidence: `gateway_service.go:5039-5147` shows Anthropic passthrough continuing upstream drain after client disconnect.
- Evidence: `gateway_service.go:6886-7070` shows another native streaming path continuing to drain after client disconnect.
- Confidence: HIGH.
- Why this matters: the synthesis fixed the old "no drain anywhere" hallucination but overcorrected into another false global claim. Implementers would design tests around only two paths and miss passthrough drains that affect quota burn, runtime occupancy, and failure classification.
- Fix: replace the H3 sentence and the section 1 table with a path-specific matrix:
- `Anthropic conversion (chat-completions/responses): no post-disconnect drain; loop returns immediately with accumulated usage.`
- `Bedrock passthrough: continues upstream read after downstream write failure until upstream closes/errors or stream-data interval timeout; no byte/cost budget.`
- `OpenAI passthrough / Responses passthrough: continues upstream read after downstream write failure to collect usage; timeout behavior depends on stream interval / keepalive configuration; no byte/cost budget.`
- `Anthropic passthrough/native: continues upstream read after downstream write failure; timeout behavior depends on stream interval; no byte/cost budget.`
- `HUAKAI design: one uniform bounded drain policy with byte, seconds, and estimated-cost budgets across all streaming paths.`

### Major Finding 2 - Open TODOs explicitly block Released status

- Evidence: `streaming-forwarder-synthesis.md:204-208` lists `TODO-1`, `TODO-2`, and `TODO-3`.
- Evidence: `streaming-forwarder-synthesis.md:210` says `These do NOT block synthesis sign-off; they DO block Released spec (per CL-009).`
- Evidence: TODO-1 is now source-resolved: `gateway_helper.go:267-271`, `295-299`, and `360-365` only acquire/retry concurrency slots, not usage/billing atomicity.
- Evidence: TODO-2 is now source-resolved enough for the comparison: `one-api/relay/controller/text.go:46-86` pre-consumes quota, delegates response handling, then launches async post-consume; `one-api/relay/adaptor/openai/main.go:27-96` handles basic streaming usage.
- Evidence: TODO-3 is stale and incomplete: `bedrock_stream.go:136-163` verifies Bedrock drain, but adjacent `openai_gateway_service.go` and `gateway_service.go` show additional drain paths.
- Confidence: HIGH.
- Why this matters: CL-009 is not optional. A Released strict spec cannot carry source-verification TODOs that the same file labels as release blockers.
- Fix: remove the Open TODO section before release. Convert each TODO into a closed, cited note or into an explicit Open Question that keeps the verdict below Released.

### Major Finding 3 - `Sources` omits license rows for cross-referenced references

- Evidence: `streaming-forwarder-synthesis.md:10` says `cross-references to Helicone ... and one-api ...` but only links Sub2API to `E-LIC-001`.
- Evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:18` has one-api `E-LIC-004`.
- Evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:21` has Helicone `E-LIC-007`.
- Confidence: HIGH.
- Why this matters: CL-006 requires every source in the `Sources` field to point to a verified license tier. Helicone is GPL-3.0 behavior-only, so this omission matters.
- Fix: rewrite `Sources` as:
- `Sub2API (E-LIC-001, LGPL-3.0, commit b0a2252ed19c3720e6adafde6083e64fbac2efa9); one-api (E-LIC-004, MIT, commit 8df4a2670b98266bd287c698243fff327d9748cf, limited comparison); Helicone AI Gateway (E-LIC-007, GPL-3.0-or-later, commit 548832f8e763a33732ead27d8b2dcaeccc665a39, behavior-only analytics-decoupling cross-reference).`

### Major Finding 4 - Atomic-billing correction depends on F-OBS evidence not listed as an input/source

- Evidence: `streaming-forwarder-synthesis.md:38-41` says the corrected framing comes from the F-OBS-001 atomic-billing finding.
- Evidence: `streaming-forwarder-synthesis.md:68` and `113-114` depend on F-OBS-001 Tx2 / O2 framing.
- Evidence: `docs/decompositions/_cross-cutting/observability-synthesis.md:13`, `97-104`, and `172` define the corrected atomic-billing / Usage Record-in-Tx2 decision.
- Evidence: Sub2API source supports the baseline: `usage_billing_repo.go:35-58` wraps billing claim/effects in one transaction, while `gateway_service.go:8038` writes Usage Record after billing.
- Confidence: HIGH.
- Why this matters: the claim is correct, but the synthesis source graph is incomplete. A future implementer or reviewer cannot tell which F-OBS artifact is authoritative for Tx2 semantics.
- Fix: add `observability-synthesis.md` and `sub2api/observability-source-verified.md` to `Inputs` or `Sources`, and include the exact local source evidence in a source appendix.

### Major Finding 5 - Release-facing text still exposes upstream identifiers beyond minimal citation needs

- Evidence: `streaming-forwarder-synthesis.md:28-30` contains `detachStreamUpstreamContext` and `writeUsageLogBestEffort`.
- Evidence: `streaming-forwarder-synthesis.md:53` keeps `detachStreamUpstreamContext` in a KEEP item.
- Evidence: `streaming-forwarder-synthesis.md:55` contains `Apply` as the Sub2API billing pattern.
- Evidence: `streaming-forwarder-synthesis.md:23` contains `parseSSEUsagePassthrough`.
- Confidence: MEDIUM.
- Why this matters: CL-011 makes source identifiers useful in reviewer/specifier evidence, but CL-001 bars upstream function/method/config names from released implementer specs. The synthesis itself says the eventual spec will be "cleaned of source identifiers"; that cleanup is required before Released.
- Fix: keep upstream identifiers only in a `Source Evidence Appendix`. In implementer-facing sections, use HUAKAI vocabulary: upstream context decoupling, best-effort Usage Record writer, atomic billing transaction, passthrough usage parser, last-non-zero usage merge.

### Minor Finding 1 - Rejected Codex pass is still listed under `Inputs`

- Evidence: `streaming-forwarder-synthesis.md:11` lists `streaming-forwarder-codex.md` in `Inputs` while saying it is rejected and historical only.
- Why this matters: the text says the file is not used, but placing it in `Inputs` invites future agents to treat it as source material.
- Fix: move the rejected Codex pass to `Provenance / rejected history`, not `Inputs`.

### Minor Finding 2 - Failure taxonomy count is inconsistent

- Evidence: `streaming-forwarder-synthesis.md:117` says `Failure taxonomy (15 reasons)`.
- Evidence: the table at `streaming-forwarder-synthesis.md:121-134` contains 14 reason rows.
- Why this matters: minor release hygiene, but acceptance-test numbering and implementation enums should not start from a mismatched count.
- Fix: either change the heading to `14 reasons` or add the missing reason with source/design status.

### Minor Finding 3 - one-api comparison is source-resolved but not folded back into the synthesis

- Evidence: `streaming-forwarder-synthesis.md:207` leaves one-api `relay/controller/text.go` as a TODO.
- Evidence: local source confirms the simple baseline at `text.go:46-86` and `relay/adaptor/openai/main.go:27-96`.
- Why this matters: the final spec should not carry a TODO for a check the reviewer can now close.
- Fix: add a short closed note or remove one-api from the source graph if it is not needed for Released F-GW-002.

## What's Missing

- A complete path matrix for Sub2API streaming drain behavior beyond Anthropic conversion and Bedrock.
- Closed disposition for all `Open TODOs`.
- E-LIC rows for every named source in `Sources`.
- Explicit input/source link to the F-OBS-001 atomic-billing synthesis that supplies Tx2 semantics.
- A clear separation between source-evidence identifiers and implementer-facing HUAKAI vocabulary.
- A released-spec cleanup step that removes or isolates upstream function names.
- Acceptance-test wording for OpenAI/Anthropic passthrough drain behavior if HUAKAI wants to use those paths as anti-patterns.
- A final decision on whether one-api remains a source for this synthesis or is removed as nonessential comparison material.

## Multi-Perspective Notes

- Executor perspective: an implementer following H3 as written would likely test Bedrock drain and Anthropic-conversion no-drain only, missing OpenAI/Anthropic passthrough drain cases that affect cost and resource exposure.
- Stakeholder perspective: the synthesis preserves the correct product outcome: uniform bounded drain, usage taxonomy, atomic Tx2, and no mid-stream failover by default. There is no feature shrinkage in the required fixes.
- Skeptic perspective: the document still carries "source-verified" confidence while preserving unresolved TODOs. That is exactly the failure mode CL-011 was added to prevent.
- Security perspective: the 500 MiB scanner risk is correctly treated as a HUAKAI improvement target. The larger risk is post-disconnect provider quota burn and resource retention across more paths than the synthesis names.
- Ops perspective: path-specific drain behavior is operationally material. Different stream paths currently have different resource and billing exposure, so the Released spec must define a single HUAKAI behavior and tests.
- New-hire perspective: current synthesis is understandable as a review memo, but not yet self-contained as a Released strict spec. It depends on input files and F-OBS artifacts that are not fully listed.

## Self-Audit

| Finding | Confidence | Could author refute with missing context? | Flaw or preference | Disposition |
| --- | --- | --- | --- | --- |
| Major 1 - Bedrock-only false | HIGH | No; local source shows other drain paths | Flaw | Kept |
| Major 2 - Open TODOs block release | HIGH | No; artifact states this directly | Flaw | Kept |
| Major 3 - missing E-LIC rows | HIGH | No; ledger rows exist and Sources omits them | Flaw | Kept |
| Major 4 - F-OBS evidence not listed | HIGH | Partially, if release process treats F-OBS as implicit | Flaw | Kept as Major because Tx2 is load-bearing |
| Major 5 - upstream identifiers | MEDIUM | Yes, for decomposition artifacts | Flaw for Released spec, acceptable for evidence | Kept as release-facing cleanup |
| Minor 1 - rejected input listed | HIGH | No | Flaw | Kept |
| Minor 2 - taxonomy count | HIGH | No | Flaw | Kept |
| Minor 3 - one-api TODO unresolved | HIGH | No | Flaw | Kept |

## Realist Check

- Finding 1 stays MAJOR: the realistic failure is incomplete implementation/tests for stream drain behavior, causing quota/resource exposure under disconnects. It is not CRITICAL because no code has shipped and the repair is a bounded text correction.
- Finding 2 stays MAJOR: open TODOs block release by rule. It is not CRITICAL because all three TODOs are resolvable with existing local source evidence.
- Finding 3 stays MAJOR: missing license rows are a strict clean-room gate defect, especially for GPL Helicone. It is bounded to the `Sources` field.
- Finding 4 stays MAJOR: Tx2 semantics are central to billing correctness. Mitigated by existing F-OBS synthesis and source evidence, but the F-GW synthesis must cite the dependency.
- Finding 5 stays MAJOR/PARTIAL: upstream identifiers are acceptable in evidence sections, but must not flow into the implementer spec body. Mitigated by the synthesis line saying the Released file will be cleaned.

## §4 - FINAL VERDICT

Verdict: APPROVE-WITH-FIXES.

Meaning:

- Do not move `streaming-forwarder-synthesis.md` to `docs/specs/streaming-forwarder.md` as Released yet.
- The v1 rejection class is mostly remediated: bounded buffer, drain, atomic billing, and HUAKAI design improvements are no longer blindly attributed to Sub2API.
- The remaining issues are bounded, but they are real release blockers under CL-006, CL-009, and CL-011.
- If the required fixes are not applied exactly, the verdict becomes REJECT.

### Required Fixes Before Released

1. Replace the H3 "Bedrock-only" statement at `streaming-forwarder-synthesis.md:63`.
   - Recommended replacement:
   - `Sub2API has path-specific post-disconnect behavior: Anthropic-conversion chat-completions/responses exits immediately on downstream disconnect; Bedrock passthrough, OpenAI passthrough, and Anthropic passthrough continue reading upstream to collect usage after downstream write failure, bounded only by upstream end/error and configured stream interval behavior, not by byte/cost budgets. HUAKAI replaces all variants with one uniform bounded drain policy: drain_max_bytes, drain_max_seconds, and drain_max_estimated_cost.`

2. Expand the section 1 path matrix at `streaming-forwarder-synthesis.md:20-23`.
   - Add rows for `OpenAI passthrough / Responses passthrough` and `Anthropic passthrough/native`.
   - Cite: `openai_gateway_service.go:3373-3385`, `openai_gateway_service.go:4108-4260`, `gateway_service.go:5039-5147`, and `gateway_service.go:6886-7070`.

3. Close TODO-1 at `streaming-forwarder-synthesis.md:206`.
   - Recommended replacement:
   - `Verified negative: Sub2API's helper-level account-slot wait retries only concurrency-slot acquisition, not Usage Record or billing atomicity. Evidence: backend/internal/handler/gateway_helper.go:267-271, 295-299, 360-365. HUAKAI keeps S2 as HUAKAI-DESIGN tied to F-OBS-001 Tx2.`

4. Close TODO-2 at `streaming-forwarder-synthesis.md:207`.
   - Recommended replacement:
   - `one-api comparison closed: relay/controller/text.go pre-consumes quota, delegates streaming/non-streaming response handling to adaptor.DoResponse, then launches async postConsumeQuota; openai StreamHandler scans SSE lines, forwards valid chunks, records provider usage if present, emits [DONE] if missing, and returns accumulated text/usage. Evidence: relay/controller/text.go:46-86; relay/adaptor/openai/main.go:27-96. This is a simpler baseline than HUAKAI Tx2.`

5. Close TODO-3 at `streaming-forwarder-synthesis.md:208`.
   - Recommended replacement:
   - `Bedrock drain verified: bedrock_stream.go:136-163 extracts usage before downstream write and continues after downstream disconnect until upstream close/error or interval timeout. Additional passthrough drain paths also exist; see the expanded path matrix.`

6. Replace `Sources` at `streaming-forwarder-synthesis.md:10`.
   - Recommended replacement:
   - `Sources | Sub2API (E-LIC-001, LGPL-3.0, commit b0a2252ed19c3720e6adafde6083e64fbac2efa9); one-api (E-LIC-004, MIT, commit 8df4a2670b98266bd287c698243fff327d9748cf, limited streaming baseline comparison); Helicone AI Gateway (E-LIC-007, GPL-3.0-or-later, commit 548832f8e763a33732ead27d8b2dcaeccc665a39, behavior-only analytics-decoupling cross-reference); F-OBS-001 synthesis inputs for Tx2 atomicity framing.`

7. Add F-OBS evidence to `Inputs` at `streaming-forwarder-synthesis.md:11` or Provenance at `streaming-forwarder-synthesis.md:212-218`.
   - Recommended addition:
   - `F-OBS-001 Tx2 framing: docs/decompositions/_cross-cutting/observability-synthesis.md plus docs/decompositions/sub2api/observability-source-verified.md; source evidence confirms billing Apply transaction and detached Usage Record write.`

8. Clean CL-001 identifiers before creating `docs/specs/streaming-forwarder.md`.
   - Recommended method:
   - Move upstream function/method names into a non-implementer `Source Evidence Appendix`.
   - In the released spec body, replace them with HUAKAI vocabulary: upstream-context decoupling, best-effort Usage Record writer, atomic billing transaction, passthrough usage parser, field-level usage merge.

9. Fix the taxonomy count at `streaming-forwarder-synthesis.md:117`.
   - Recommended replacement:
   - `### 5.4 Failure taxonomy (14 reasons)` unless a missing fifteenth reason is added with source/design status.

10. Move rejected `streaming-forwarder-codex.md` out of `Inputs` at `streaming-forwarder-synthesis.md:11`.
   - Recommended replacement:
   - Keep `streaming-forwarder-claude-v2.md`, Helicone cross-verify, one-api closed comparison, and F-OBS inputs in `Inputs`.
   - Move `streaming-forwarder-codex.md` to `Provenance: rejected historical pass, not source material`.

### Upgrade Conditions

- Upgrade to APPROVE-FOR-RELEASED after all 10 fixes are applied and the final spec body is cleaned of upstream identifiers.
- If any TODO remains in the Released artifact, downgrade to REJECT.
- If "Bedrock-only" remains or the path matrix omits OpenAI/Anthropic passthrough drain, downgrade to REJECT.
- If Helicone remains in `Sources` without `E-LIC-007`, downgrade to REJECT under CL-006.

## Appendix A - Assumptions, Pre-Mortem, and Dependency Audit

### Key Assumptions Extracted

| Assumption | Rating | Evidence / concern |
| --- | --- | --- |
| F-GW-002 is an Option C strict feature. | VERIFIED | `streaming-forwarder-synthesis.md:7`; gateway hot path plus billing/health intersections. |
| F-GW-002 parity row exists. | VERIFIED | `docs/03_FEATURE_PARITY_MATRIX.md:38`. |
| Sub2API local clone is available at the pinned commit. | VERIFIED | `git rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`. |
| The synthesis is ready for Released if reviewed. | FRAGILE / FALSE | Line 210 says TODOs block Released spec. |
| Bedrock is the only Sub2API drain path. | FRAGILE / FALSE | OpenAI and Anthropic passthrough paths also drain after disconnect. |
| one-api can remain a cross-reference without direct closure. | FRAGILE | It is named in `Sources`; CL-006/CL-011 need explicit closure or removal. |
| F-OBS Tx2 semantics are implicit enough. | FRAGILE | The synthesis uses F-OBS framing but does not list the input/source artifacts. |
| HUAKAI design additions are allowed if labeled. | VERIFIED | CL-011 exempts HUAKAI design improvements when clearly labeled. |

### Pre-Mortem

Assume this synthesis was moved to Released exactly as written and failed. Specific failure scenarios:

1. Implementer tests only Bedrock drain and Anthropic-conversion no-drain, missing OpenAI/Anthropic passthrough drain behavior.
   - Covered by synthesis? No.
   - Finding: Major Finding 1.

2. Release manager ignores TODOs because line 228 says "none blocking synthesis", while line 210 says they block Released spec.
   - Covered by synthesis? Contradictory.
   - Finding: Major Finding 2.

3. Clean-room reviewer blocks the Released spec because Helicone is GPL-3.0 but lacks an E-LIC row in `Sources`.
   - Covered by synthesis? No.
   - Finding: Major Finding 3.

4. Implementer cannot trace Tx2 semantics because F-GW-002 references F-OBS O2 without listing the actual F-OBS synthesis/source file.
   - Covered by synthesis? Partially.
   - Finding: Major Finding 4.

5. Implementer copies source function names from the Released spec body into local implementation names.
   - Covered by synthesis? It says the final file will be cleaned, but it is not cleaned yet.
   - Finding: Major Finding 5.

6. Acceptance test authors create 15 enum cases because the heading says 15 reasons, but only 14 are listed.
   - Covered by synthesis? No.
   - Finding: Minor Finding 2.

7. A future agent treats rejected `streaming-forwarder-codex.md` as an active source input because it remains in `Inputs`.
   - Covered by synthesis? Partially, but the placement is misleading.
   - Finding: Minor Finding 1.

### Dependency Audit

| Dependency | Status | Notes |
| --- | --- | --- |
| `docs/specs/_REVIEW_CHECKLIST.md` | PASS | Read; CL-011 synthesis nuance applied. |
| `streaming-forwarder-synthesis.md` | PASS | Read with line numbers. |
| `streaming-forwarder-claude-v2.md` | PASS | Read; main source-verified Sub2API input. |
| `streaming-forwarder-codex.md` | PASS / rejected | Read; correctly marked rejected and should not remain in active Inputs. |
| `helicone/observability-source-verified.md` | PASS | Read; behavior-only GPL cross-reference. |
| Sub2API clone | PASS | Pinned commit present. |
| one-api clone | PASS | Pinned commit present. |
| Helicone clone | PASS | Pinned commit present. |
| License ledger rows | PARTIAL in synthesis | Ledger has rows; synthesis omits one-api/Helicone rows in `Sources`. |
| Parity matrix row | PASS | `F-GW-002` exists. |
| Released-readiness | FAIL until fixes | Open TODOs and false path generalization block Released. |

## §5 - Owner-Facing Chinese Summary

最终结论：`streaming-forwarder-synthesis.md` 可以 `APPROVE-WITH-FIXES`，但不能 as-is 移到 `docs/specs/streaming-forwarder.md` 并标为 Released。
主要 blocker 是三类：CL-009 的 Open TODO 仍存在，CL-006 的 one-api/Helicone license row 没写进 Sources，以及 CL-011 抽查发现 `Bedrock-only drain` 这个归纳不成立，因为 Sub2API 的 OpenAI passthrough / Anthropic passthrough 也会在客户端断开后继续 drain 上游。
没有发现需要删除功能；HUAKAI 的统一 bounded drain、Usage Record taxonomy、Tx2 atomicity 和默认禁止 mid-stream failover 的方向都应保留，只需要把引用和路径矩阵修准。
clean-room 风险是可控的，但 Released spec 必须把上游函数名移到证据附录或改写成 HUAKAI 术语；安全/运营风险主要是断连后继续消耗 Provider Account quota 的路径比 synthesis 当前描述更多。
Owner 不需要做产品取舍确认；下一步建议让 specifier 精确应用 10 条 required fixes，然后重新跑一次 8+ citation spot-check。
