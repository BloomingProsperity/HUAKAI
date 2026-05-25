//! W11-F F-1.a (Owner-approved synthesis 2026-05-25, D1=A evidence first):
//! cross-profile consistency tests for the HTTP/2 fingerprint fixtures under
//! `tests/fixtures/http2_fingerprint/`.
//!
//! Why this file exists
//! --------------------
//! F-2.5 status doc proved (and Codex's parallel F-1 plan-trio draft caught,
//! see `docs/process/plans/2026-05-25-w11f-f1-l2-http2-jiexian-synthesis.md`
//! §2 C-1) that HUAKAI currently holds **zero** real-upstream HTTP/2 capture
//! data for any of the 4 built-in profiles. Without an explicit guard, a
//! future implementer could:
//!   - flip `h2_settings_frame.available = true` on a profile without adding
//!     the backing fixture file → mimicry runtime would silently produce
//!     synthetic / arbitrary HTTP/2 bytes claiming to be "real-captured", OR
//!   - leave a stale fixture file behind after a profile rolled back to
//!     `available = false` → next time the profile is restored, the stale
//!     fixture's wrong bytes would be picked up and claimed as evidence.
//!
//! The two tests below close those holes by enforcing fixture-presence ↔
//! profile-availability invariants.
//!
//! Mutation discipline (CLAUDE.md #14)
//! -----------------------------------
//! `fixture_exists_when_profile_marks_available`:
//!   - The interior assertion body is silent when no profile is currently
//!     `available = true` (today: all 4 profiles `available = false`).
//!   - Mutation: flip any profile's `h2_settings_frame.available = true`
//!     without adding the matching fixture file → `path.exists()` assertion
//!     red. Mutation: add fixture with wrong `raw_order` → equality
//!     assertion red.
//!
//! `fixture_absent_when_profile_marks_unavailable`:
//!   - Today: all 4 profiles `available = false`, asserts file does NOT
//!     exist for all 4 (real, non-vacuous assertions today).
//!   - Mutation: create any fixture file under `tests/fixtures/http2_fingerprint/`
//!     while the corresponding profile still has `available = false` → red.

use std::path::PathBuf;

use core_gateway::mimicry::{BuiltinProfile, load_builtin_profile};

/// Fixture filename = `template_name()` with `.json` stripped + `-h2.json`.
/// See `tests/fixtures/http2_fingerprint/README.md` filename convention table.
fn fixture_path(profile: BuiltinProfile) -> PathBuf {
    let template = profile.template_name();
    let base = template.strip_suffix(".json").unwrap_or(template);
    let mut path = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    path.push("tests");
    path.push("fixtures");
    path.push("http2_fingerprint");
    path.push(format!("{base}-h2.json"));
    path
}

/// Invariant: if a built-in profile declares `h2_settings_frame.available =
/// true`, the matching fixture file MUST exist under
/// `tests/fixtures/http2_fingerprint/` AND its `raw_order` field MUST match
/// the profile's `h2_settings_frame.raw_order` byte-for-byte.
///
/// Today's expected behavior: 0 of 4 built-in profiles have
/// `available = true`, so the body inside the `if` branch never fires. The
/// test is still discriminating for future mutations (see module-level
/// mutation-discipline comment above).
#[test]
fn fixture_exists_when_profile_marks_available() {
    for builtin in BuiltinProfile::ALL {
        let profile = load_builtin_profile(builtin).expect("builtin profile loads");
        if !profile.h2_settings_frame.available {
            continue;
        }

        let path = fixture_path(builtin);
        assert!(
            path.exists(),
            "profile {builtin:?} marks h2_settings_frame.available=true but \
             the matching fixture is missing at {path:?}. Either add the \
             real-upstream capture fixture (see \
             tests/fixtures/http2_fingerprint/README.md) or flip \
             available=false until capture lands."
        );

        let bytes = std::fs::read(&path).expect("fixture file readable");
        let fixture: serde_json::Value =
            serde_json::from_slice(&bytes).expect("fixture is valid JSON");
        let fixture_settings_frame = &fixture["h2_settings_frame"];
        let fixture_raw_order: Vec<u16> = fixture_settings_frame["raw_order"]
            .as_array()
            .unwrap_or_else(|| {
                panic!(
                    "fixture {path:?} missing h2_settings_frame.raw_order \
                     array (required by F-1.a schema)"
                )
            })
            .iter()
            .map(|v| {
                u16::try_from(v.as_u64().unwrap_or_else(|| {
                    panic!("fixture {path:?} raw_order contains non-integer")
                }))
                .expect("u16 fits")
            })
            .collect();

        assert_eq!(
            fixture_raw_order, profile.h2_settings_frame.raw_order,
            "profile/fixture mismatch on h2_settings_frame.raw_order for \
             {builtin:?}. Profile says {:?}, fixture at {path:?} says {:?}. \
             Re-capture upstream and align both, do NOT hand-edit one to \
             match the other.",
            profile.h2_settings_frame.raw_order, fixture_raw_order
        );
    }
}

/// Invariant: if a built-in profile declares `h2_settings_frame.available =
/// false` (or omits the field, which treats as false), NO fixture file
/// should exist for it. Protects against stale fixtures lingering across
/// profile regressions.
///
/// Today: all 4 built-in profiles `available = false` and no fixture files
/// exist, so all 4 path-checks actively fire and pass. Strong mutation
/// discriminator today.
#[test]
fn fixture_absent_when_profile_marks_unavailable() {
    for builtin in BuiltinProfile::ALL {
        let profile = load_builtin_profile(builtin).expect("builtin profile loads");
        if profile.h2_settings_frame.available {
            continue;
        }

        let path = fixture_path(builtin);
        assert!(
            !path.exists(),
            "profile {builtin:?} marks h2_settings_frame.available=false but \
             a stale fixture file exists at {path:?}. Delete the stale \
             fixture or flip available=true (and align raw_order) once a \
             real upstream re-capture lands."
        );
    }
}

/// Sanity check that the fixture-path derivation matches what the
/// README documents. This test is a fixed-point against the README's
/// filename table: if `template_name()` ever changes for a builtin, the
/// README and the cross-check tests must be updated together.
///
/// Mutation: changing `BuiltinProfile::AnthropicClaudeCode::template_name()`
/// to e.g. `"anthropic_claude_code.json"` (underscored) would make this
/// test fail and force the README update — without that signal, F-1.g could
/// drop a fixture at the wrong path and the cross-check tests would silently
/// stop discriminating.
#[test]
fn fixture_path_matches_documented_filename_table() {
    let cases: &[(BuiltinProfile, &str)] = &[
        (
            BuiltinProfile::AnthropicClaudeCode,
            "anthropic-claude-code-h2.json",
        ),
        (BuiltinProfile::CodexCli, "codex-cli-h2.json"),
        (BuiltinProfile::KiroCli, "kiro-cli-h2.json"),
        (BuiltinProfile::GeminiAdvanced, "gemini-advanced-h2.json"),
    ];
    for (builtin, expected_basename) in cases {
        let path = fixture_path(*builtin);
        let actual = path
            .file_name()
            .and_then(|s| s.to_str())
            .expect("fixture path has valid filename component");
        assert_eq!(
            actual, *expected_basename,
            "fixture path basename drifted for {builtin:?} — update either \
             BuiltinProfile::template_name() OR \
             tests/fixtures/http2_fingerprint/README.md filename table to \
             stay in sync"
        );
    }
}
