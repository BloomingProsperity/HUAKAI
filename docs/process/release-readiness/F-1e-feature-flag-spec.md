# W11-F F-1.e Feature Flag spec

> Owner-approved 2026-05-26 (post Codex consult on W11-F F-1 scope; §11
> Gate 4). This document specifies the Feature Flag that MUST gate any
> future F-1.e (HTTP/2 fork outbound client real connection) implementation.
> F-1.e is on **Mandatory Roadmap** — not dropped, not implemented. When
> the implementation lands, it must conform to this spec or fail Codex
> per-commit review.

## Flag name + scope

| Property | Value |
|---|---|
| Flag name | `mimicry.h2_outbound_per_profile` |
| Type | `HashMap<ProfileName: String, EnableH2Outbound: bool>` |
| Config home | Rust gateway runtime config (`core_gateway` crate config), AND Go control-plane proto field (for distribution to the Rust runtime via the existing control-plane reconciliation path) |
| Default value | empty map (≡ all profiles OFF) |
| Scope | per-profile, NOT global — global enable is forbidden by Gate 4 |
| Persistence | persisted with the rest of gateway config (no in-memory-only mode) |

## Semantics

- **OFF for profile X (map missing key OR `false`)**: any outbound request
  routed to profile X uses the existing h1.1 / dormant transport path.
  The h2 fork outbound code path is unreachable. This is the default and
  the only allowed steady state for HUAKAI as shipped 2026-05-26.
- **ON for profile X (map has `X → true`)**: outbound requests routed to
  profile X may use the F-1.e h2 fork outbound code path, subject to the
  L1+L2 preflight gates (`mimicry::l1_preflight` + `mimicry::l2_preflight`)
  that the F-1.b/c/d/f infrastructure already enforces. ON is only allowed
  for profile X if all of:
  - Gate 1 (provenance) is PASS for X
  - Gate 2 (per-profile ALPN assertion) is PASS for X
  - Gate 3 (hard-unreachable without captured h2 profile) is satisfied for X
    (i.e., X's `h2_settings_frame.available=true` AND fixture exists +
    cross-check non-vacuous AND `_field_sources.h2_settings` traces to
    real first-party h2 capture jsonl with `alpn_negotiated=h2`)

## Activation rule (verbatim Gate 4 mandate)

> **Activation requires real first-party h2 capture for the target profile;
> implementation precedes capture is forbidden.**

Step-by-step (mirrors AGENTS.md §"Dormant h2 outbound infrastructure gate"
Activation rule):

1. Take real first-party h2 capture of the target vendor's CLI/desktop
   app. Evidence: jsonl under `tools/fingerprint-collector/captures/`
   with `alpn_negotiated=h2`, SETTINGS frame bytes, pseudo-header order.
2. Flip target profile JSON `h2_settings.available=true` + add fixture
   under `tests/fixtures/http2_fingerprint/<profile>-h2.json` + cross-
   check test (`mimicry_http2_fixture_test::fixture_exists_when_profile_marks_available`)
   runs non-vacuously.
3. ONLY THEN: F-1.e implementation may be written and gated by this flag.
4. Operator separately flips `mimicry.h2_outbound_per_profile[X]=true` to
   activate at runtime.

**Reverse order is forbidden.** Implementation before capture is a
Gate 3 / Gate 4 / `AGENTS.md` §"Per-Vendor Fingerprint Capture
Discipline" violation. Codex per-commit review must HIGH-block.

## Codex enforcement (when F-1.e implementation lands)

Codex per-commit review must HIGH-block a commit that introduces F-1.e
code (any wiring that makes `http2_adapter::drive_request<T>` or the h2
branch of `try_build_gateway_transport_with_profile` reachable from
production ProxyEngine) unless ALL of:

1. The commit ALSO registers the `mimicry.h2_outbound_per_profile` config
   field per this spec (default empty / OFF).
2. The commit ALSO adds runtime gate logic that returns the dormant h1.1
   path for any profile where the flag is missing/false.
3. The commit message cites this spec file by path.
4. The commit's tests assert `mimicry.h2_outbound_per_profile=Empty`
   default → ProxyEngine never reaches F-1.e code paths (mutation
   discriminator: removing the gate logic must turn the test red).

Codex per-commit review must ALSO HIGH-block a commit that flips a profile's
`mimicry.h2_outbound_per_profile[X]=true` in any committed config /
config-sample unless ALL of Gates 1+2+3 are PASS for profile X (cite the
status doc §12.2 verdict).

## Mutation discriminator for this spec

If a future commit silently weakens any of:
- The "empty map = all OFF" default,
- The per-profile (not global) constraint,
- The Gates-1+2+3-PASS prerequisite for any ON flip,
- The "implementation precedes capture is forbidden" rule,

then the activation rule is broken. Codex per-commit review must
HIGH-block any such weakening; the discriminating signal is:

- Commit changes default to a non-empty map → red.
- Commit introduces a `mimicry.h2_outbound_global=bool` field → red.
- Commit flips ON for a profile whose §12.2 verdict is not PASS → red.
- Commit implements F-1.e BEFORE that profile's `h2_settings.available=true`
  promotion lands → red.

## What this spec is NOT

- Not an F-1.e implementation. F-1.e remains NOT STARTED.
- Not a Cargo feature flag (those are compile-time; this is runtime
  per-profile). The existing Cargo features `mimicry-boring`,
  `mimicry-openssl`, `mimicry-http2-fork` continue to gate **availability**
  of the code at build time. This runtime flag is a SEPARATE concern that
  gates **activation per profile at request time**, on top of the Cargo
  feature being enabled.
- Not a license / clean-room concern (those are §"Per-Vendor Fingerprint
  Capture Discipline" + clean-room L0/L1/L2/L3 layers in CLAUDE.md #11).

## Cross-references

- AGENTS.md §"Per-Vendor Fingerprint Capture Discipline" → "Dormant h2
  outbound infrastructure gate" — the structural enforcement rule.
- W11-F-F1-status.md §11 — the 5 dormancy gates. Gate 4 (this flag) is
  acceptance-tracked there.
- W11-F-F1-status.md §10 — the option (b) capture evidence that justifies
  dormancy.
- W11-F-F1g-h2-stack-divergence-finding.md — the cross-library h2
  fingerprint divergence evidence that justifies per-vendor (not generic)
  h2 mimicry.
- docs/process/plans/2026-05-26-w11f-f1-scope-decision-codex-consult.md —
  the Codex consult that produced Gate 4.
