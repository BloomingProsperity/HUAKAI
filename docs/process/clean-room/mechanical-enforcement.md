# HUAKAI Clean-Room Mechanical Enforcement

**Status**: SPEC — implementation TBD before W11-D2 commit.

Per CLAUDE.md #11 relaxation (2026-05-23 Owner approval + Codex independent consult), the clean-room rule moved from blanket hard isolation to risk-stratified (L0–L4). To compensate for the relaxation, mandatory mechanical enforcement backstops are required. This document specifies them.

## Purpose

Provide automated defense layers that complement Codex per-commit review (CLAUDE.md #8). Mechanical layers catch what human/AI judgment might miss in:
- L2 verbatim copying detection
- L3 vendor contamination prevention
- L4 AGPL service contamination prevention

L0 (behavior parity) and L1 (algorithm mimicry) remain procedural — the mechanical layers cannot detect "did you read source in this specifier session", only their output traces.

## Defense Mechanisms

### M1 — Pre-commit reference/license name scan

- **Script**: `tools/clean-room/scan-references.sh`
- **Trigger**: pre-commit git hook on staged files
- **Patterns**: `AGPL`, `LGPL`, `GNU Affero`, `GNU Lesser`, `sub2api`, `clewdr`
- **Allowlist paths**: `docs/`, `.gitignore`, `CLAUDE.md`, `AGENTS.md`, `docs/process/clean-room/`, `LICENSE`, `NOTICE`
- **Action on match in disallowed path**: abort commit with red message naming file + line + matched pattern; require manual `--no-verify` override + Owner approval to bypass

### M2 — License deny via cargo-deny

- **File**: `deny.toml` at repository root
- **Deny list**: `AGPL-3.0`, `AGPL-3.0-only`, `AGPL-3.0-or-later`, `GPL-2.0`, `GPL-2.0-only`, `GPL-2.0-or-later`, `GPL-3.0`, `GPL-3.0-only`, `GPL-3.0-or-later`, `LGPL-2.1`, `LGPL-2.1-only`, `LGPL-2.1-or-later`, `LGPL-3.0`, `LGPL-3.0-only`, `LGPL-3.0-or-later`
- **Allow list**: `MIT`, `MIT-0`, `Apache-2.0`, `Apache-2.0 WITH LLVM-exception`, `BSD-2-Clause`, `BSD-3-Clause`, `ISC`, `MPL-2.0`, `Unicode-DFS-2016`, `Unlicense`, `CC0-1.0`, `Zlib`
- **Run**: `cargo deny check licenses` in pre-commit (after Rust changes) and in CI

### M3 — Similarity scan (HUAKAI ⟷ reference repos)

- **Script**: `tools/clean-room/similarity-scan.sh`
- **Trigger**: pre-commit hook for staged Rust source files
- **Mechanism**: token-level shingle hash (e.g., 10-token windows) cross-check between staged Rust files and `~/refs/sub2api/`, `~/refs/clewdr/`
- **Threshold**: any matching shingle ≥ 5 consecutive identical 10-token windows (~50 distinctive tokens) → flag
- **Action on flag**: abort commit, surface match (HUAKAI file:lines ⟷ reference file:lines) for paraphrasing
- **Tool option**: `simian`, `cpd` (PMD's copy-paste detector), or custom Go/Rust util with shingle hashing

### M4 — Distinctive identifier denylist

- **File**: `tools/clean-room/identifier-denylist.txt`
- **Format**: one identifier per line (function name, type name, distinctive field name, distinctive test name), with `# source: <repo>:<file>:<line>` comment
- **Seed source**: extract distinctive identifiers from existing specifier sessions' reading lists for clewdr/smg/litellm-rs/sub2api/cliproxyapi
- **Pre-commit grep**: any staged source file containing a denylist identifier (case-sensitive, word boundary) → abort
- **Maintenance**: Owner-only edits (manual review for false positives — common Rust patterns like `new`, `default`, `from` are NOT in denylist; only distinctive names)

### M5 — Commit attestation enforcement

- **Pre-commit hook checks**: commit message body MUST contain the line `Clean-room-attestation: original HUAKAI implementation; no copied source/comments/tests/schemas from non-permissive references.`
- **Missing or modified attestation**: abort commit
- **Rationale**: attestation (not attribution — attribution implies derivation, attestation declares independent creation) provides legal evidence of independent work intent. Codex 2026-05-23 consult specifically corrected "attribution" to "attestation".

## Implementation Sequence (target: before W11-D2 commit)

1. Write 5 scripts + `deny.toml` + denylist seed file
2. Install pre-commit hook (`.git/hooks/pre-commit` calling each script in sequence)
3. Verify by attempting a deliberate violation commit (each mechanism should abort)
4. Document operational details (false positive handling, override path) in AGENTS.md
5. Apply to current uncommitted plans before pushing

## What These Mechanisms Do NOT Catch

- **L0 / L1 lane discipline**: cannot detect "did this session read non-MIT source first?" — procedural, relies on Claude/Codex prompt hygiene
- **Conceptual derivation**: cannot detect "you internalized clewdr's mental model and wrote a structurally similar Rust" — relies on L1 lane split + paraphrase discipline
- **License compatibility nuances** beyond cargo-deny's coverage (e.g., dual-license interpretation, transitive license escalation): manual legal review on case-by-case
- **Documentation-only verbatim**: M1 grep catches license markers and project names but not arbitrary doc text — relies on Codex review

## Owner Escalation Path

If a legitimate need to use a denylisted identifier or pattern arises (e.g., the identifier is actually generic and the original source merely happens to use it):
1. Document the case in a `tools/clean-room/exemptions.md` PR
2. Owner reviews + approves the exemption
3. Add to `identifier-denylist.txt` allowlist section with rationale
4. The PR itself uses `--no-verify` only with Owner explicit approval recorded in commit message

## Cross-References

- CLAUDE.md #8: per-commit Codex review with termination criteria
- CLAUDE.md #11: clean-room risk stratification (L0–L4)
- CLAUDE.md #12: source-must-read + citation discipline
- AGENTS.md §Clean-Room Codex Prompt Template: L1 specifier prompt format

## Change Log

- **2026-05-23**: SPEC created concurrent with CLAUDE.md #11 relaxation (Owner "同意" + Codex consult).
