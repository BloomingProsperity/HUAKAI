# HUAKAI release-notes template

> Source of truth for project-wide release notes. When cutting a release
> that includes a section listed under "Known reusable blocks" below, copy
> the corresponding block into the release notes verbatim (or with the
> minimal substitutions noted in the block).

## Skeleton

Use this skeleton as the starting point for each release.

```markdown
# HUAKAI <version> — <release date YYYY-MM-DD>

## Summary
<1-3 sentences on the release's headline outcome.>

## Included epics
- <epic id + short title + status (Released / dormant / Mandatory Roadmap)>

## Reusable blocks copied below
- <list which "Known reusable blocks" from release-notes-template.md were
  pulled in for this release>

## New features
- <user-facing new behavior>

## Changed behavior
- <user-visible changes vs prior release>

## Known limitations / pending work
- <Mandatory Roadmap items not in this release; Feature Flags shipping
  off; etc.>

## Security notes
- <secret rotations required, behavior gated on env, audit ledger notes>

## Upgrade procedure
- <db migrations, config changes, etc.>
```

## Known reusable blocks

### Block: W11-F F-1 (no h2 outbound; dormant h2 infrastructure)

Required verbatim (or equivalent) when a release ships any W11-F F-1
sub-phase code as part of HUAKAI. Source: `W11-F-F1-status.md` §11 Gate 5
(Owner-approved 2026-05-26 post Codex consult).

```markdown
### W11-F F-1 release-status disclosure

No currently-deployed profile uses HTTP/2 on the wire for business
requests. The F-1 epic ships h2 outbound infrastructure as a dormant
capability, gated on real first-party capture per `AGENTS.md` §"Dormant
h2 outbound infrastructure gate". The earlier F-1 Released criterion
"≥1 profile h2 fixture non-vacuous" is intentionally **N/A** for this
release, not silently skipped — see `docs/process/release-readiness/W11-F-F1-status.md`
§11 (5 dormancy gates) for the replacement criteria and §10 for the
direct capture evidence that justifies dormancy.

F-1.e (HTTP/2 fork outbound client real connection) state: Mandatory
Roadmap behind a Feature Flag (default OFF, per-profile opt-in). Activation
of any profile's h2 path requires real first-party h2 capture and
satisfaction of Gates 1+2+3+4 of §11 for that profile. Implementation-
before-capture is forbidden.
```

### Block: clean-room attestation (every release)

Required verbatim on every HUAKAI release (per AGENTS.md / CLAUDE.md
clean-room policy).

```markdown
### Clean-room attestation

Every commit in this release carries `Clean-room-attestation: original
HUAKAI implementation; no copied source/comments/tests/schemas from
non-permissive references.` per CLAUDE.md #11. The reference projects
that informed HUAKAI's design (paraphrased behavior summaries only, never
copied code) are tracked in `docs/tracking/` per
`24_REFERENCE_TRACKING_POLICY.md`.
```

## Usage

When cutting a release:

1. Copy the **Skeleton** into a new file (e.g.,
   `docs/process/release-notes/<version>.md`).
2. For each "Reusable block" relevant to the release, copy the block
   content verbatim into the appropriate skeleton section. List the
   blocks copied under "Reusable blocks copied below".
3. Fill in version-specific content (summary, new features, etc.).
4. Run the release through `docs/15_RELEASE_GATES.md` gate checklist
   before publishing.

## Audit / mutation discriminator

Per `W11-F-F1-status.md` §11.5 Gate 5: any HUAKAI release that includes
W11-F F-1 sub-phase code in its commit range MUST have the
"W11-F F-1 release-status disclosure" block in its release notes
(verbatim or equivalent — substantive parity, not literal copy). A
release note that omits the block while shipping F-1 code violates Gate 5
and is a Release Decision Gate failure per
`docs/15_RELEASE_GATES.md`. Codex per-release review must HIGH-block.
