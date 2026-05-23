# Citation Cleanup Backlog

Per CLAUDE.md #8 termination criteria (added 2026-05-23): when Codex per-commit review iteration exceeds 2 rounds and remaining findings are only P2/P3 citation form / path detail / absence-claim form, freeze the artifact, commit it, and surface deferred items here for batched later cleanup.

## Purpose

Track citation/path/form findings deferred from Codex review rounds where the underlying claim is substantively correct but presentation form has room for polish. These are **not blockers** under the new termination criteria. Real safety / clean-room / architecture violations are NEVER deferred here — they are always fixed before commit.

## Format

For each entry: `file:section` — finding summary — reason for deferral — target cleanup window.

## Current Deferred Items

### From `docs/process/plans/2026-05-22-rust-hardening-plan*.md` Codex review iterations 1–10 (2026-05-22 → 2026-05-23)

#### Plan synthesis (`2026-05-22-rust-hardening-plan.md`)

- **§2/§3/§4 in-prose short citation form** (`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:...` is sometimes used in long form, sometimes truncated) — deferred per CLAUDE.md #11 relaxation 2026-05-23; metadata records full `repo + SHA + fetch date`; in-prose short form `path:line` or source-map ID is acceptable. Implementer sessions should not be blocked by form variance.

- **D-4 sub2api/smg cite anchors at file granularity** (`backend/internal/service/usage_record_worker_pool.go` without explicit line range) — deferred; the cited file is correct evidence for "overflow sampling behavior" and "no spool/WAL" claims; line-level precision was not achievable from a quick scoped search and would require sustained re-reading. Tighten when next touched.

- **Absence claims supported by general statements** (e.g. "smg does not parse Retry-After header") — deferred; will be tightened during W12-D8 implementation when concrete grep commands + commit SHA are recorded inline.

#### Plan -claude.md (`2026-05-22-rust-hardening-plan-claude.md`)

- Same form variances as synthesis (this is the historical progenitor; less actively polished since synthesis took over as authoritative).
- This file is **historical archive only** — implementer reads synthesis, not -claude.md. Form polish here is purely audit-trail completeness.

#### Plan -codex.md (`2026-05-22-rust-hardening-plan-codex.md`)

- Inherits Codex's own citation style choices; not edited beyond a single corrective note (90-day → 30-day staleness clarification, line 13).
- This file is **Codex parallel-draft evidence** preserved per CLAUDE.md #10 dual-draft requirement. Form deferred.

## Target Cleanup Window

After **W11 implementation completes** (before W12 kickoff): batched citation polish pass on all plans + spec docs. Estimated 0.5–1 codex-day for the entire pass.

If implementer sessions surface a specific citation form that genuinely impedes verification (rather than just being stylistically imperfect), it gets bumped to P1 and fixed immediately rather than waiting for the batched pass.

## Change Log

- **2026-05-23**: Backlog created concurrent with CLAUDE.md #8 termination criteria addition. Initial entries seeded from Codex review iterations 1–10 on the W11+W12+指纹 plan synthesis.
