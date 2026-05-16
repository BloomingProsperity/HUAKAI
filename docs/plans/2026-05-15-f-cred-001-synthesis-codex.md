# 2026-05-15 F-CRED-001 Synthesis Plan — Codex

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read HUAKAI self-repo files and preservation review files;
    sub2api source is NOT reread in this synthesis because the reviews already
    read and cited it.
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT:
  - Codex specifier, F-CRED-001 acquisition plan, 2026-05-15T17:39:58Z
  - Claude Sonnet reviewer, preservation review, 2026-05-15T17:30:00Z
  - Codex reviewer, preservation review, 2026-05-15T18:10:00Z

REFERENCE PROJECTS IN SCOPE: sub2api, citations reused from review files only

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

## Metadata

| Field | Value |
| --- | --- |
| Owner directive | "对了，怎么获取这个功能你们也要做! 看看sub2api" + "你要比他做的更简洁，更方便" |
| Artifact purpose | Synthesize F-CRED-001 acquisition plans after two preservation reviews blocked/failed current preservation. |
| Scope | Plan-only. No code, no schema, no tests, no commit. |
| Local/review files observed | Current repair reread the target synthesis plus both preservation reviews; sub2api source was not reopened. |
| Inferences | 10, all marked as HUAKAI design decisions from review evidence. |
| Open questions | 8 Owner OCAW points. |
| Preservation verdict before synthesis | BLOCK / FAIL_PRESERVATION until RF-1..RF-9 are mapped. |
| Synthesis verdict | Proceed to Owner OCAW; do not implement until approved decision points are resolved. |

## Scope Boundary

F-CRED-001 remains the credential acquisition feature: browser OAuth, cookie/session bootstrap, CLI auth-file content import, cloud/bootstrap inputs, API key paste, refresh-token endpoint exchange, and Antigravity special path. It ends by finalizing into F-AUTH-005 encrypted `account_credentials`.

F-AUTH-005 remains the credential management and refresh feature: stored credential state machine, encrypted storage, refresh scheduling, storm control, version/CAS discipline, redaction, and mode-specific refresh semantics.

F-AUTH-006 remains the high-risk login bootstrap / long-window automation roadmap row unless Owner explicitly merges it into F-CRED-001 after legal and UX decisions.

## Plan Conflict Resolution

Decision: **Codex safe boundary wins for default implementation.**

Claude's acquisition plan proposed server-side automatic reads from local CLI/cloud auth paths for one-click convenience. Codex's acquisition plan proposed upload/paste only by default and local-agent access only after Owner OCAW. This synthesis chooses Codex's boundary:

- Default F-CRED-001: admin uploads or pastes auth-file content; the server never scans workstation paths.
- Mandatory Roadmap: optional local-agent connector for one-click import after Owner approves security model, install boundary, consent UX, and audit.
- UX requirement preserved: the admin wizard may still show path hints and a one-click-looking flow when backed by upload/paste or a future approved local agent.

This preserves the convenience goal without granting the gateway process ambient filesystem authority.

## RF Decision Matrix

| RF | Review severity | Behavior to preserve | Evidence anchor from reviews | Decision | Landing location | Estimate | OCAW? | Acceptance ID |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| RF-1 | HIGH | ChatGPT post-acquisition account metadata check plus privacy/training preference handling. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:255`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:276` | **Safe Equivalent**: implement metadata enrichment and audited privacy action, but make privacy mutation tenant-configured, redacted, and non-blocking unless Owner selects strict mode. | F-CRED-001 acquisition enrichment; F-AUTH-005 refresh metadata refresh; F-TRUST audit payload. | 1.0-1.5 backend days + 0.5 UI day. | **Yes**: privacy mutation default, failure policy, and operator wording. | `AT-CRED-001-016` |
| RF-2 | HIGH | Gemini subscription tier canonicalization, including 8 current canonical outputs and 7 legacy inputs, so old accounts do not lose rate-limit meaning. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:211`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:363` | **Implement**: HUAKAI-owned tier normalizer with explicit unknown-tier state and tests for 15 input labels. | F-CRED-001 initial tier capture; F-AUTH-005 refresh/update path; admin UI metadata display. | 0.75-1.0 backend day + 0.25 UI day. | No for canonicalization; **Yes** only if tier drives commercial rate-limit defaults. | `AT-CRED-001-017` |
| RF-3 | MED | Gemini refresh should recover from OAuth-client mismatch by trying an allowed alternate client path instead of immediately forcing reauthorization. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:675`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:733` | **Safe Equivalent**: bounded cross-client retry only when the stored mode allows that compatibility pair and storm controller permits it; audit both attempts. | F-AUTH-005 Gemini refresh adapter; F-CRED-001 records selected client family during acquisition. | 0.75-1.0 backend day. | **Yes**: approved client compatibility matrix and default enablement. | `AT-CRED-001-018` |
| RF-4 | MED | Gemini Drive-derived tier metadata should be cached for 24 hours during background refresh to avoid unnecessary upstream calls. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:733` | **Implement**: TTL cache field in credential metadata or refresh sidecar; expired cache triggers refresh, fresh cache skips tier probe. | F-AUTH-005 Gemini refresh adapter; F-CRED-001 initializes tier timestamp after acquisition. | 0.5 backend day. | No. | `AT-CRED-001-019` |
| RF-5 | HIGH | Antigravity project/plan metadata lookup must retry during every background refresh, not only initial acquisition. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:170`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:277`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:442` | **Implement**: dedicated Antigravity refresh adapter, preserving prior project metadata when lookup fails and marking operator attention when stale. | F-AUTH-005 dedicated Antigravity adapter; F-CRED-001 special acquisition adapter. | 1.0-1.5 backend days. | **Yes**: which metadata absence blocks traffic versus only degrades observability. | `AT-CRED-001-020` |
| RF-6 | MED | Antigravity refresh-token-only onboarding must validate refresh token, fetch user metadata, discover project/plan, and apply privacy policy without a full browser flow. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:214`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:337` | **Implement**: F-CRED-001 refresh-token acquisition path with dry-run validation and redacted preview before final save. | F-CRED-001 admin endpoint and adapter; F-AUTH-005 handles subsequent refresh. | 0.75-1.25 backend days + 0.25 UI day. | **Yes**: raw refresh-token admin endpoint exposure and preview/finalize policy. | `AT-CRED-001-021` |
| RF-7 | LOW | Claude long-lived setup-token branch supports 1-year automation-style token behavior. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:174` | **Mandatory Roadmap**: preserve as F-AUTH-006 / automation-bootstrap item behind feature flag, legal review, warning UI, and explicit expiry/audit controls. Do not include in F-CRED-001 L1 default. | F-AUTH-006 existing roadmap row; possible future `F-CRED-002` if Owner wants separate automation-token feature. | 0.75-1.0 backend day later + legal/OCAW. | **Yes**: ToS/legal posture, maximum expiry, and default disabled policy. | `AT-CRED-001-022` |
| RF-8 | LOW | OpenAI refresh eligibility should include a rate-limit recovery heuristic when expiry metadata is missing, without creating refresh storms. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/token_refresher.go:92`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:286` | **Safe Equivalent**: refresh-on-account-state predicate gated by F-AUTH-005 three-scope storm controller, with provider-specific no-op for static API-key modes. | F-AUTH-005 OpenAI/ChatGPT/Codex refresh adapter; F-CRED-001 stores enough mode metadata. | 0.5 backend day. | No. | `AT-CRED-001-023` |
| RF-9 | MED | Claude multi-organization bootstrap should prefer team workspace semantics but avoid silent wrong-org selection. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:35` | **Safe Equivalent**: default to team-preferred candidate, but show candidate list and require admin confirmation when multiple orgs exist; audit selected org label/hash. | F-CRED-001 Claude acquisition UI/backend; F-AUTH-005 stores selected org metadata for refresh. | 0.75-1.0 backend day + 0.5 UI day. | No for confirmation UX; **Yes** only if Owner wants auto-select without confirmation. | `AT-CRED-001-024` |

Decision distribution: **Implement = 4** (RF-2, RF-4, RF-5, RF-6); **Safe Equivalent = 4** (RF-1, RF-3, RF-8, RF-9); **Mandatory Roadmap = 1** (RF-7); **Defer-with-justification = 0**.

## Codex Preservation RF Crosswalk

The table above is the original Sonnet-numbered RF matrix and stays intact. This crosswalk uses the Codex preservation review as the numbering source of truth (`HUAKAI@working-tree:docs/reviews/2026-05-15-f-cred-001-preservation-codex-review.md:91`; `HUAKAI@working-tree:docs/reviews/2026-05-15-f-cred-001-preservation-codex-review.md:107`) and maps it against the Sonnet RF list (`HUAKAI@working-tree:docs/reviews/2026-05-15-f-cred-001-preservation-sonnet-review.md:137`; `HUAKAI@working-tree:docs/reviews/2026-05-15-f-cred-001-preservation-sonnet-review.md:162`). The preserved matrix starts at `HUAKAI@working-tree:docs/plans/2026-05-15-f-cred-001-synthesis-codex.md:82`.

| Codex RF | Content | Maps to (Sonnet RF / new row / Safe Equivalent / Mandatory Roadmap) | Acceptance ID |
| --- | --- | --- | --- |
| Codex RF-1 | OpenAI post-acquisition metadata plus privacy outcome. | Sonnet RF-1; **Safe Equivalent** with tenant policy, redaction, and audit. | `AT-CRED-001-016` |
| Codex RF-2 | Gemini tier canonicalization, Drive-derived tier, and tier refresh cache. | Sonnet RF-2 plus Sonnet RF-4; **Implemented** as canonical tier normalizer plus TTL cache. | `AT-CRED-001-017`, `AT-CRED-001-019` |
| Codex RF-3 | Gemini Code Assist / Google One project discovery fallback and onboarding path. | **MISSING — newly added below** as Codex-added row C-RF-3; **Safe Equivalent** unless Owner approves automatic onboarding side effects. | `AT-CRED-001-025` |
| Codex RF-4 | Antigravity must use a dedicated adapter instead of generic Gemini handling. | Sonnet RF-5 plus Sonnet RF-6; **Implemented** as dedicated acquisition/refresh adapter and refresh-token-only dry run. | `AT-CRED-001-020`, `AT-CRED-001-021` |
| Codex RF-5 | Claude/Anthropic cookie bootstrap details: team org, setup-token branch, org metadata. | Sonnet RF-9 plus Sonnet RF-7; **Safe Equivalent** for org confirmation and **Mandatory Roadmap** for long-window automation token. | `AT-CRED-001-024`, `AT-CRED-001-022` |
| Codex RF-6 | OpenAI refresh edge cases: preserve previous refresh material, client identity, account-state refresh. | Sonnet RF-8 plus expanded F-AUTH-005 writeback rules; **Safe Equivalent** through versioned merge and storm-controlled refresh predicate. | `AT-CRED-001-023` |
| Codex RF-7 | Common refresh reread/recovery and race protection. | **MISSING — newly added below** as Codex-added row C-RF-7; **Implemented Better** in F-AUTH-005 shared refresh pipeline. | `AT-CRED-001-026` |
| Codex RF-8 | User auth refresh-token cache plus OAuth email local-account flow. | **MISSING — newly added below** as Codex-added row C-RF-8; **Mandatory Roadmap** outside F-CRED-001. | `AT-AUTH-SESSION-001` |
| Codex RF-9 | Local file auto-detect conflict between Claude plan and Codex plan. | Plan Conflict Resolution plus OCAW-F-CRED-001-S2; **Safe Equivalent** upload/paste default, local-agent import as **Mandatory Roadmap**. | `AT-CRED-001-004`, `AT-CRED-001-005`, `AT-CRED-001-015` |

## Newly Added Rows

| Added row | Codex RF | Decision | Landing | Estimate | OCAW | Acceptance ID |
| --- | --- | --- | --- | --- | --- | --- |
| C-RF-3 | Codex RF-3 | **Safe Equivalent**: implement HUAKAI-owned Gemini project discovery fallback as an explicit state machine. Try only approved discovery/registration paths, keep side effects bounded, and return operator-action status when project metadata remains unavailable. | F-CRED-001 Gemini Code Assist / Google One acquisition adapter; F-AUTH-005 may reuse the same project probe on refresh only after storm-control budget allows it. | 0.75-1.25 backend days + 0.25 UI day. | **Yes**: whether automatic onboarding/registration side effects are allowed by default or require Manual First. | `AT-CRED-001-025` |
| C-RF-7 | Codex RF-7 | **Implemented Better**: extend the shared F-AUTH-005 refresh pipeline with per-credential lock discipline, reread-before-write, versioned merge of returned credential material, and recovery by reread when a stale worker sees a permanent-token error after another worker already rotated the credential. | F-AUTH-005 common refresh writer and mode-adapter contract; F-CRED-001 records enough acquisition metadata for the shared writer to decide merge rules. | 0.75-1.25 backend days. | No for the safety rule; **Yes** only if Owner needs to choose lock backend, timeout, or retry budget before deployment. | `AT-CRED-001-026` |
| C-RF-8 | Codex RF-8 | **Mandatory Roadmap**: explicitly preserve user-session refresh-token family invalidation and OAuth email local-account creation/recovery as a separate HUAKAI user-auth slice, not as upstream-provider credential acquisition. | Future F-AUTH/session roadmap, likely after F-CRED-001 and F-AUTH-005 parity work; not counted in F-CRED backend implementation unless Owner pulls user-login work into the same milestone. | 0 F-CRED backend days now; 1.5-2.5 backend days later for the user-auth/session slice. | **Yes**: decide whether HUAKAI needs local OAuth email signup/invitation/rollback, Redis-like family revocation, and signup-source audit in this release train. | `AT-AUTH-SESSION-001` |

## OCAW Decision Points

1. **OCAW-F-CRED-001-S1 — ChatGPT privacy action policy.** Choose default: disabled until tenant opts in, enabled by default with audit, or Manual First. Also decide whether privacy-action failure blocks credential finalization.
2. **OCAW-F-CRED-001-S2 — CLI/cloud local-agent connector.** Default is upload/paste only. Owner must approve any future local agent that reads CLI/cloud auth files from a device.
3. **OCAW-F-CRED-001-S3 — Acquisition session schema and OpenAPI surface.** New DB table and admin endpoints are high-risk implementation scope; approve before migration/code.
4. **OCAW-F-CRED-001-S4 — OAuth client and cross-client fallback matrix.** Decide which built-in or tenant-provided client identities can be used for Gemini/OpenAI/Claude flows and which fallback pairs are allowed.
5. **OCAW-F-CRED-001-S5 — Antigravity metadata blocking policy.** Decide whether missing project/plan metadata blocks use, marks degraded, or remains operator-attention only.
6. **OCAW-F-CRED-001-S6 — Long-lived Claude setup-token roadmap.** Decide whether 1-year automation token behavior is commercially necessary, legally acceptable, and default-off behind feature flag.
7. **OCAW-F-CRED-001-S7 — Gemini project fallback side effects.** Decide whether project discovery may attempt automatic registration/onboarding, or whether HUAKAI must stop at Manual First with operator instructions.
8. **OCAW-F-CRED-001-S8 — Refresh lock/retry deployment policy.** Decide lock backend, timeout, and retry budget only if the implementer cannot use existing F-AUTH-005 CAS/storm-control defaults.
9. **OCAW-F-CRED-001-S9 — User-auth roadmap ownership.** Decide whether Codex RF-8 belongs to a near-term F-AUTH/session slice or a later identity roadmap.

## Implementation Order

| Order | Work pack | RFs | Lane owner | Estimate | Reason |
| --- | --- | --- | --- | --- | --- |
| 0 | Owner OCAW + final spec release update | All | Claude PM + Codex reviewer | 0.5-1 day | Prevents implementation from beginning with unresolved privacy/schema/local-agent decisions. |
| 1 | Dedicated Antigravity refresh/acquisition parity and OpenAI metadata/privacy safe equivalent | RF-5, RF-1 | Clean implementer backend; Codex reviews only | 2.0-3.0 days | Highest production failure risk: wrong Antigravity metadata and missing ChatGPT account/privacy state. |
| 2 | Claude multi-org confirmation and Gemini tier normalizer | RF-9, RF-2 | Backend + Gemini UI | 1.5-2.0 days | High user-facing acquisition correctness and rate-limit metadata correctness. |
| 3 | Gemini refresh edge behavior, Gemini project discovery fallback, and OpenAI rate-limit-triggered refresh heuristic | RF-3, RF-4, RF-8 plus Codex RF-3 | Backend implementer | 2.25-3.25 days | Refresh and project-discovery reliability after basic metadata is correct; must pass storm-controller and fallback-path tests. |
| 4 | Antigravity refresh-token-only acquisition endpoint | RF-6 | Backend + Gemini UI | 1.0-1.5 days | Adds manual-first onboarding path after dedicated adapter exists. |
| 5 | Shared refresh reread/recovery semantics | Codex RF-7 | Backend implementer | 0.75-1.25 days | Closes the common refresh race gap before any mode-specific parity claim. |
| 6 | Mandatory roadmap packaging for long-lived Claude automation token, local-agent 1-click UX, and user-auth/session cache/email flow | RF-7 plus Codex RF-8/RF-9 | Claude PM + Owner | 0.5-1.0 planning day; user-auth implementation later | Keeps risky convenience and out-of-scope user-login features visible without blocking safer F-CRED L1. |
| 7 | Reviewer-lane preservation audit + acceptance matrix update | All | Codex reviewer lane, separate session | 0.5-1 day | Required before parity claim or implementation handoff. |

Estimate basis:

- Old backend estimate **6.0-8.5 days** was the **Sonnet RF-only redline delta**, not full F-CRED implementation and not the Codex RF union.
- Codex RF additions counted inside F-CRED/F-AUTH redline work: Codex RF-3 project discovery fallback **+0.75-1.25 backend days** and Codex RF-7 shared refresh reread/recovery **+0.75-1.25 backend days**, total **+1.5-2.5 backend days**.
- Codex RF-8 is out of F-CRED scope and is mapped to Mandatory Roadmap: **+0 backend days** for full F-CRED, but **+1.5-2.5 backend days later** if Owner pulls the user-auth/session slice into the same release train.
- Full F-CRED backend total after Codex RF union: **7.5-11.0 backend days** = 6.0-8.5 Sonnet RF-only redline delta + 1.5-2.5 Codex RF additions.

Lane estimate total after OCAW: backend clean implementer **7.5-11.0 days for full F-CRED redline parity**, Gemini UI 1.25-2.25 days, Codex review/test-spec 1.0-2.0 days, Claude PM/spec propagation 1.0-1.5 days. The later user-auth/session roadmap is tracked separately unless Owner expands scope.

## Fusion-Upgrade Taxonomy

| RF | Architecture upgrade | Algorithm upgrade | Ecosystem / UX upgrade |
| --- | --- | --- | --- |
| RF-1 | Metadata enrichment adapter separated from encrypted credential finalizer. | Privacy-action policy is explicit, audited, and retry-classified instead of hidden side effect. | Admin sees account plan/privacy outcome without token bytes. |
| RF-2 | Gemini tier metadata normalized into HUAKAI-owned canonical enum. | 8 canonical outputs + 7 legacy inputs tested; unknown tier becomes explicit state. | UI and rate-limit templates can show stable tier labels. |
| RF-3 | Refresh adapter records allowed client compatibility pairs. | Bounded alternate-client retry with storm-controller budget. | Operators avoid unnecessary reauthorization after client migration. |
| RF-4 | Tier cache timestamp lives with credential metadata or refresh sidecar. | 24h TTL suppresses expensive Drive-tier probes until stale. | Lower latency and fewer upstream quota surprises. |
| RF-5 | Antigravity gets a dedicated mode adapter, not generic Gemini handling. | Every refresh performs bounded metadata lookup and preserves previous metadata on lookup failure. | Operator dashboard shows stale/degraded project metadata instead of silent drift. |
| RF-6 | Refresh-token-only acquisition becomes a first-class F-CRED flow kind. | Dry-run validation requires token exchange + user metadata + project/plan probe before finalize. | Manual onboarding can be one-screen with redacted preview. |
| RF-7 | Long-lived automation token support remains outside default F-CRED path behind roadmap/flag. | Expiry cap and warning/audit policy are explicit before any implementation. | Commercial automation use case remains visible without default legal risk. |
| RF-8 | OpenAI-style refresh adapter includes account-state inputs, not only timestamp inputs. | Missing-expiry plus rate-limit state can trigger one bounded refresh attempt. | Operators recover quota windows without blind manual refresh. |
| RF-9 | Claude org selection is stored as acquisition metadata and confirmed by admin. | Team-preferred default is a ranking rule, not an irreversible auto-pick. | Multi-org users see and choose the intended workspace. |

## Acceptance Test Plan

Existing `AT-CRED-001-001..015` from the Codex acquisition plan stay valid. Add the following test IDs before implementation:

| Test ID | RF | Scenario | Expected result |
| --- | --- | --- | --- |
| `AT-CRED-001-016` | RF-1 | ChatGPT OAuth finalization performs account metadata enrichment and privacy action according to tenant policy. | Metadata is stored redacted; privacy action outcome is audited; configured non-blocking failure finalizes with operator attention, strict failure blocks finalize. |
| `AT-CRED-001-017` | RF-2 | Gemini acquisition/refresh receives 8 canonical and 7 legacy tier labels. | Every known label maps to one HUAKAI canonical tier; unknown labels become explicit `unknown` metadata and never default to a higher tier. |
| `AT-CRED-001-018` | RF-3 | Gemini refresh gets a client-mismatch error for the stored client family. | Only approved alternate family is tried once; both attempts are audited; disallowed pairs fail with reauthorization-needed status. |
| `AT-CRED-001-019` | RF-4 | Gemini background refresh runs twice within 24 hours after tier probe. | First refresh may call tier probe; second refresh uses cached tier and does not call Drive-tier probe; stale cache refreshes after TTL. |
| `AT-CRED-001-020` | RF-5 | Antigravity background refresh succeeds but project metadata lookup fails transiently. | Access material refreshes; previous project metadata remains; account is marked metadata-stale/operator-attention per OCAW policy. |
| `AT-CRED-001-021` | RF-6 | Admin imports Antigravity refresh token without browser OAuth. | Dry-run validates token, user metadata, project/plan, and privacy policy; finalization stores encrypted payload with redacted audit. |
| `AT-CRED-001-022` | RF-7 | Long-lived Claude automation-token feature flag is disabled by default. | Attempt is rejected with roadmap/feature-flag message; when future flag is enabled, test must verify expiry cap, audit, and warning. |
| `AT-CRED-001-023` | RF-8 | OpenAI-style OAuth account has missing expiry metadata and rate-limit account state. | One bounded refresh attempt is scheduled through F-AUTH-005 storm controller; static API-key mode remains no-op. |
| `AT-CRED-001-024` | RF-9 | Claude bootstrap returns multiple org candidates including team and personal contexts. | UI/backend default highlights team-preferred candidate, requires confirmation, stores selected org metadata redacted, and audits the choice. |
| `AT-CRED-001-025` | Codex RF-3 | Gemini project discovery cannot find project metadata through the first lookup path. | Approved fallback paths are attempted in configured order; automatic side effects require OCAW-approved policy; unresolved project metadata returns operator-action status with no silent success. |
| `AT-CRED-001-026` | Codex RF-7 | Two refresh workers race on the same OAuth credential and one sees stale credential material. | The worker rereads current credential version before write, merges only safe returned material, and recovers by reread when another worker already rotated the credential. |
| `AT-AUTH-SESSION-001` | Codex RF-8 | Project-level user auth roadmap is checked for refresh-token family invalidation and OAuth email local-account recovery. | F-CRED-001 marks this out of scope with Mandatory Roadmap ownership; future user-auth slice must test token-family invalidation, email verification/invitation, rollback, and login audit before parity is claimed. |

Minimum checks after implementation:

- `go test ./backend/internal/credentialacq/...`
- `go test ./backend/internal/credentialworker -run 'Gemini|OpenAI|Antigravity|Claude'`
- `go test ./backend/internal/gatewayhttp -run CredentialAcquisition`
- OpenAPI parse/codegen check after admin endpoint changes.
- Spec-leakage review before implementer lane consumes the final spec.

## Risk And Preservation Notes

- No RF is dropped. Legal/security risk changes default, OCAW, feature flag, or roadmap placement only.
- No RF is dropped across the Sonnet RF matrix and the Codex RF crosswalk. Codex RF-3, RF-7, and RF-8 were previously missing from this synthesis and are now mapped.
- Mandatory Roadmap now contains Sonnet RF-7 plus Codex RF-8; the local-agent portion of Codex RF-9 is also roadmap-only unless Owner approves a local connector.
- RF-1, Sonnet RF-3, Sonnet RF-8, Sonnet RF-9, Codex RF-3, and Codex RF-9 are Safe Equivalent because the user outcome is preserved with stronger HUAKAI controls: tenant policy, bounded fallback, storm control, confirmation UX, and audit.
- Direct server-side reads of local CLI/cloud auth files are not allowed in the default plan. The 1-click UX is preserved as a future local-agent roadmap item.
- Refresh-specific RFs must land in F-AUTH-005 even when their acceptance IDs are listed under AT-CRED, because acquisition correctness depends on subsequent refresh behavior.

## Open Questions

1. Should F-AUTH-006 be merged into F-CRED-001 after this synthesis, or remain a separate roadmap row for high-risk login bootstrap and long-window automation?
2. Which ChatGPT privacy behavior should HUAKAI default to: Manual First, tenant opt-in default off, or default-on with audit?
3. Which OAuth client identities can HUAKAI ship, and which must be tenant/operator-supplied?
4. Should Antigravity missing project/plan metadata block route eligibility, or only surface degraded/operator-attention?
5. What is the legal boundary for long-lived Claude automation tokens and client-identity mimicry?
6. Does Owner want local-agent one-click import in the same commercial milestone, or later after upload/paste F-CRED-001 is stable?
7. May Gemini project discovery perform automatic registration/onboarding side effects, or must those steps be Manual First?
8. Should user auth refresh-token cache and OAuth email local-account flow be pulled into the same release train, or remain a separate F-AUTH/session roadmap slice?

## OWNER 中文摘要

本次修复保留原 Sonnet 编号 9 行矩阵，并新增 Codex Preservation RF Crosswalk，把 Codex RF-1..RF-9 全部映射到既有 Sonnet 行、安全等价或 Mandatory Roadmap；另外新增 C-RF-3、C-RF-7、C-RF-8 三行，分别覆盖 Gemini project fallback、common refresh reread/recovery、user auth cache + OAuth email roadmap。backend 估算从旧的 Sonnet RF-only redline delta 6.0-8.5 天，重算为 Codex RF additions +1.5-2.5 天，full F-CRED total 7.5-11.0 天；Codex RF-8 不计入 F-CRED backend，另列未来 user-auth/session 1.5-2.5 天。当前 Sonnet+Codex RF union 没有已知漏项；无功能缩水，clean-room 风险仍通过复用 review citation、行为释义和不重读 sub2api 源控制。

Source files read: .agents/skills/pm-orchestrator/SKILL.md; .agents/skills/clean-room-license-guard/SKILL.md; .agents/skills/feature-parity-auditor/SKILL.md; docs/RULES.md; docs/01_PROJECT_BRIEF.md; docs/05_CLEAN_ROOM_POLICY.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/10_RISK_REGISTER.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/12_AGENT_WORKFLOW.md; docs/plans/2026-05-15-f-cred-001-synthesis-codex.md; docs/plans/2026-05-15-f-cred-001-acquisition-claude.md; docs/plans/2026-05-15-f-cred-001-acquisition-codex.md; docs/reviews/2026-05-15-f-cred-001-preservation-codex-review.md; docs/reviews/2026-05-15-f-cred-001-preservation-sonnet-review.md
Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T04:08:35Z
