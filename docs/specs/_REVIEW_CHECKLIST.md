This file is agent-facing and authoritative.

# Spec-Leakage Review Checklist

Per [DR-000](../decisions/DR-000-clean-room-methodology.md), every spec produced by the specifier lane MUST pass this checklist before it is released to the implementer lane. The reviewer must be a different agent session from the specifier.

## Reviewer Workflow

1. Open the spec file.
2. Run through every check below.
3. If any check fails, return the spec to the specifier with the failing item ID. Do **not** rewrite the spec yourself unless you are switching into a new specifier session.
4. If all checks pass, set `Status: Reviewed`, fill `Reviewer` and `Review date`, and commit.
5. The implementer lane (which may be a different agent or a new session of the same agent) flips `Status: Released` once it confirms the spec is consumable.

## Checks

### CL-001 — No upstream function, method, or configuration-constant names

The spec must not contain function, method, handler, or **configuration-constant names with values** verbatim from non-MIT references (e.g. `OpenAIController.handleChat`, `ratio_billing_calculate`, or specific `CONFIG_FLAG=value` strings drawn from a non-MIT source). Use behavior verbs from this project's glossary instead.

Sub-clause **CL-001a** (added 2026-04-28 after a real leak at E-S2A-005): a configuration constant's *name plus value pair* (e.g. `RUN_MODE=simple`) is a fingerprint of the upstream project even if either piece alone is generic. Specs and evidence rows must paraphrase as "an edition-mode flag", "a deployment-profile selector", etc.

### CL-002 — No upstream schema column names

No column names, table names, or migration filenames borrowed from non-MIT reference databases. Refer to entities by the names defined in [18_GLOSSARY.md](../18_GLOSSARY.md) and [19_DOMAIN_MODEL.md](../19_DOMAIN_MODEL.md).

### CL-003 — No upstream UI component or class names

No copied React/Vue/HTML class names, component file paths, or distinctive layout terminology from reference admin dashboards.

### CL-004 — No verbatim sentences from upstream docs

Quoted sentences longer than ~10 words from non-MIT reference docs / READMEs / wiki pages must be paraphrased. Short technical phrases (e.g. "rate limit", "circuit breaker") are common-vocabulary and are fine.

### CL-005 — No algorithmic pseudocode that reads as direct translation

If the spec describes an algorithm, the wording must be in this project's terms. A reader who later sees the upstream source must not be able to point to a line-by-line correspondence. If the algorithm is intricate (e.g. weighted account selection with health backoff), the spec should describe **what guarantee is preserved** rather than **what steps to execute**.

### CL-006 — Every reference cited has a verified license tier

Every entry in the `Sources` field must point to a row in [07_REFERENCE_EVIDENCE_LEDGER.md](../07_REFERENCE_EVIDENCE_LEDGER.md) with an `E-LIC-NNN` reference. Sources without verified license tier must be removed before release.

### CL-007 — Lane mode matches feature carve-out

If the feature is one of the Option C carve-outs (billing ledger, account-pool routing, provider failover/account-health) the `Lane mode` field must be `Option C`. Otherwise it must be `Option B`. Mismatch is a structural defect.

### CL-008 — Capability ID exists in parity matrix

The `Feature ID` field must match an existing row in [03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md). If the row does not yet exist, the specifier must add it before the spec is released.

### CL-009 — Open Questions section is honest

If the specifier could not resolve a behavior from the references, it must appear in `Open Questions`. A spec that pretends to have all answers is more dangerous than one that admits gaps. Implementer lane treats Open Questions as a hold signal.

### CL-010 — No source URL embedded in implementer-relevant sections

Sources may appear in the `Sources` field. They must NOT appear in `Normal Path`, `Failure Path`, `Audit / Usage / Log Evidence`, or `Acceptance Test Direction`. The implementer lane reads only the latter sections, and a stray URL would invite the implementer to click through.

### CL-011 — Every behavior claim has a source citation (added 2026-04-28)

Added 2026-04-28 after a real leak in F-POOL-001 / F-GW-002 cycles 1-2 where Claude's specifier passes were paraphrased from prior prose decompositions instead of source-verified, producing 18 hallucinated claims. See [docs/reviews/2026-04-28-source-truth-corrections.md](../reviews/2026-04-28-source-truth-corrections.md) for the catalogue.

The rule:

- **Specifier-lane pass**: every claim about a reference's behavior MUST cite a specific source location — a relative path under `.omc/reference-src/<ref>/...` plus a line range or a function name with verifiable existence. "Behavior basis = existing prose decomposition" is NOT a source citation; the prose itself may be paraphrased.
- **Acceptable citation forms**:
  - File path + line range: `backend/internal/service/gateway_service.go:1376–1928`
  - File path + function name (function must exist when grepped): `backend/internal/service/gateway_service.go::SelectAccountWithLoadAwareness`
  - Verified commit hash pinned in the file's header for reproducibility against [docs/24 reference tracking policy](../24_REFERENCE_TRACKING_POLICY.md).
- **Unverified claims must be marked**: if the specifier could not locate a behavior in source but believes it exists (e.g. heard from another agent), the claim must carry a `**TODO/UNVERIFIED**` tag and appear in `Open Questions` (CL-009 cross-link).
- **HUAKAI design improvements (KEEP/IMPROVE/AVOID outputs) are exempt from CL-011**: when the spec proposes a behavior NOT in any reference, the claim must be labeled `(HUAKAI design — not in source)` so reviewer can't confuse design proposal with source extraction.
- **Reviewer-lane responsibility**: spot-check at least 5 random claims by grepping the source file at the cited path. If any cited location does not contain the claimed behavior, CL-011 fails.

CL-011 applies to all specifier-lane decomposition files under `docs/decompositions/` and all Option C strict specs under `docs/specs/`. It does NOT apply to synthesis files (which integrate two specifier passes) but the synthesis must inherit citations from the input passes.

Sub-clause **CL-011a — Reference source must be locally cloned**: every reference cited in a specifier pass MUST be locally cloned under `.omc/reference-src/<ref>/` before the pass is authored. Web-fetch-only specifier passes are CL-011a fail. Clone command must be reproducible (specifier records `git clone --depth 1 <url> <dir>` in the pass's Provenance).

Sub-clause **CL-011b — KEEP / IMPROVE / AVOID separation is mandatory**: every specifier pass that proposes HUAKAI changes must structure its output as three sections: KEEP (verified-in-source behaviors to inherit), IMPROVE (HUAKAI-design improvements clearly labeled "not in source"), AVOID (verified-in-source anti-patterns to NOT inherit). Mixing HUAKAI design into KEEP without the label is CL-011b fail.

## Reviewer Sign-Off

When all 11 checks pass, append a sign-off block to the spec:

```markdown
## Review Sign-Off

| Field | Value |
| --- | --- |
| Reviewer | <agent + session> |
| Review date | YYYY-MM-DD |
| Checks passed | CL-001 through CL-011 |
| Notes | <optional, e.g. "CL-005 had a borderline pseudocode block; rewritten to guarantee form."> |
```

Then flip `Status: Reviewed` in the header.

## When the Reviewer Is the Specifier

Not allowed. A specifier must not self-review. Either a different agent or a fresh session of the same agent (with no spec context loaded) does the review. The reviewer must be able to read the spec cold and not recognize its phrasing as upstream.
