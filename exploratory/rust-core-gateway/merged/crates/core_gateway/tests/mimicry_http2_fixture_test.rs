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

        // F-1.a round 2 (Codex P2): enforce full README cross-check contract,
        // not just raw_order. Without these the fixture could carry matching
        // SETTINGS ids but wrong values, OR right SETTINGS but wrong pseudo-
        // header order, OR wrong ALPN negotiation — and the F-1 Released gate
        // would silently pass while production runtime still drifts from the
        // template.
        //
        // Cross-check #1 (round 3 Codex P2 fix): iterate profile.raw_order
        // instead of profile.values map. The adapter
        // (`http2_adapter::new_with_profile`) rejects profiles where any
        // raw_order id lacks a matching values entry ("order has no matching
        // value"), but the load-time profile.rs validation only checks that
        // values is non-empty. So a F-1.g fixture matching a partial
        // values-map could pass an iterate-values loop while the adapter
        // would still reject at runtime — leaving us with a "fixture matches
        // template" claim that the production code can't actually use.
        //
        // Iterating raw_order requires (a) every raw_order id is in
        // profile.values, (b) every raw_order id is in fixture.values, (c)
        // values agree.
        let fixture_values = &fixture_settings_frame["values"];
        let fixture_values_map = fixture_values.as_object().unwrap_or_else(|| {
            panic!(
                "fixture {path:?} missing h2_settings_frame.values object \
                 (required by F-1.a schema)"
            )
        });
        for &profile_id in &profile.h2_settings_frame.raw_order {
            let profile_value = profile.h2_settings_frame.values.get(&profile_id).unwrap_or_else(|| {
                panic!(
                    "profile {builtin:?} raw_order lists SETTINGS id \
                     {profile_id} but profile.values omits it; the adapter \
                     will reject this profile with \"order has no matching \
                     value\" at runtime. Fix the profile before promoting."
                )
            });
            let key = profile_id.to_string();
            let fixture_value = fixture_values_map.get(&key).unwrap_or_else(|| {
                panic!(
                    "profile {builtin:?} raw_order id {profile_id} but \
                     fixture {path:?} omits it (superset rule violated)"
                )
            });
            let fixture_u32 = u32::try_from(
                fixture_value
                    .as_u64()
                    .unwrap_or_else(|| panic!("fixture value for id {profile_id} not u64")),
            )
            .expect("u32 fits");
            assert_eq!(
                fixture_u32, *profile_value,
                "profile/fixture value mismatch for SETTINGS id {profile_id} \
                 on {builtin:?}. Profile says {profile_value}, fixture says \
                 {fixture_u32}. Re-capture upstream and align both."
            );
        }

        // Cross-check #2: fixture's h2_pseudo_header_order.order must match
        // profile's h2_pseudo_header_capture.order byte-for-byte. Pseudo-header
        // order is a fingerprintable wire detail — different orderings yield
        // different HPACK bytes.
        //
        // Round 3 Codex P2 fix: if pseudo-header capture is unavailable on
        // the profile but settings_frame is available, the profile's order is
        // a default empty vec, and a fixture with empty order would trivially
        // pass the equality — letting an "F-1.g promoted" profile through
        // without real pseudo-header evidence. Gate on
        // h2_pseudo_header_capture.available + non-empty order before the
        // equality assertion.
        assert!(
            profile.h2_pseudo_header_capture.available,
            "profile {builtin:?} has h2_settings_frame.available=true but \
             h2_pseudo_header_capture.available=false. The fork client needs \
             both — flip pseudo-header capture or revert settings_frame \
             availability."
        );
        assert!(
            !profile.h2_pseudo_header_capture.order.is_empty(),
            "profile {builtin:?} has h2_pseudo_header_capture.available=true \
             but empty .order; impossible to enforce wire-order parity."
        );
        let fixture_pseudo = &fixture["h2_pseudo_header_order"];
        let fixture_pseudo_order: Vec<String> = fixture_pseudo["order"]
            .as_array()
            .unwrap_or_else(|| {
                panic!(
                    "fixture {path:?} missing h2_pseudo_header_order.order \
                     array (required by F-1.a schema)"
                )
            })
            .iter()
            .map(|v| {
                v.as_str()
                    .unwrap_or_else(|| {
                        panic!("fixture pseudo-header name is not a string in {path:?}")
                    })
                    .to_owned()
            })
            .collect();
        assert!(
            !fixture_pseudo_order.is_empty(),
            "fixture {path:?} has empty h2_pseudo_header_order.order; \
             impossible to byte-match real upstream HEADERS frame."
        );
        assert_eq!(
            fixture_pseudo_order, profile.h2_pseudo_header_capture.order,
            "profile/fixture mismatch on pseudo-header order for {builtin:?}. \
             Profile says {:?}, fixture says {:?}. Re-capture upstream + align.",
            profile.h2_pseudo_header_capture.order, fixture_pseudo_order
        );

        // Cross-check #3: fixture's tls_alpn_negotiated MUST be "h2". The
        // synthesis §4.3 #5 acceptance criterion (round 1 Codex P1 fix)
        // requires ALPN h2 evidence for every Released profile; the fixture
        // is the canonical evidence carrier. A captured h1 negotiation has
        // no business in an H2 fingerprint fixture.
        let fixture_alpn = fixture["tls_alpn_negotiated"]
            .as_str()
            .unwrap_or_else(|| {
                panic!(
                    "fixture {path:?} missing tls_alpn_negotiated string \
                     (required by F-1.a schema + synthesis §4.3 #5)"
                )
            });
        assert_eq!(
            fixture_alpn, "h2",
            "fixture {path:?} records tls_alpn_negotiated={fixture_alpn:?}; \
             must be \"h2\" for an H2 fingerprint fixture. If real upstream \
             negotiates h1, that profile is not F-1 Released eligible — \
             do NOT promote it."
        );

        // Cross-check #4 (round 3 Codex P2 fix): the fixture's captured ALPN
        // is only half the story — the live BoringSSL handshake uses
        // `profile.tls.alpn_protocols` to ADVERTISE supported protocols.
        // If the profile advertises only `["http/1.1"]` (as anthropic_claude_code.json:99-100
        // currently does), the real upstream MUST negotiate h1 regardless of
        // what the fixture says was negotiated in capture, and the fork
        // client at boring_tls_connector.rs:176-178/249-258 will fail-closed
        // on every real Anthropic request. F-1.g promotion MUST refresh
        // profile.tls.alpn_protocols to include "h2".
        assert!(
            profile.tls.alpn_protocols.iter().any(|p| p == "h2"),
            "profile {builtin:?} has h2_settings_frame.available=true but \
             profile.tls.alpn_protocols={:?} does not include \"h2\". The \
             BoringSSL ALPN advertise list at runtime would not offer h2, so \
             the fork client would never negotiate h2 with real upstream — \
             every request fails-closed despite fixture evidence. F-1.g \
             promotion MUST refresh alpn_protocols from real capture.",
            profile.tls.alpn_protocols
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
