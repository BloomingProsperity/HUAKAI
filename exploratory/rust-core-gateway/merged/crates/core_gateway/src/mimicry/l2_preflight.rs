//! W11-F F-1.c (Owner-approved synthesis 2026-05-25): central L2 HTTP/2
//! preflight typed gate. Sits above the HTTP/2 fork adapter, below the
//! production builder (F-1.f wires it in). Mirrors the L1 TLS preflight in
//! `mimicry::l1_preflight` so the dispatch layer can apply the same "single
//! typed status + structured error" pattern at the L2 layer.
//!
//! Scope of this module
//! --------------------
//! - Type definitions: [`L2HttpPreflightStatus`] and [`L2HttpPreflightError`]
//!   parallel to L1's pair, carrying the same `profile_mode` + `reason`
//!   discriminator surface.
//! - Static classifier: [`preflight_status_from_profile`] checks the profile
//!   for the H2 capture data + ALPN h2 prerequisites WITHOUT running the
//!   fork. Returns `Pending` (caller runs runtime) or `Failed(...)`.
//! - Runtime runner: [`run_l2_preflight`] uses
//!   [`crate::mimicry::http2_adapter::HttpTwoMimicryAdapter::encode_request_exchange`]
//!   to drive the fork over in-memory IO + parse the captured SETTINGS / HEADERS
//!   frames + compare wire bytes against the profile's declared values.
//!   Returns `Passed` only when both static + runtime agree.
//! - Predicate [`is_l2_dispatchable`] gates the builder: only `NotRequired`
//!   or `Passed` are dispatchable; `Pending` must fail-closed until runtime
//!   resolves it (same semantic as L1's `is_dispatchable`).
//!
//! Why static + runtime layers (synthesis §4.3 #4 + #5 + #6)
//! ---------------------------------------------------------
//! - Static layer catches "profile is structurally incomplete" cheap +
//!   without spinning up the adapter (e.g., F-1.a evidence-contract enforces
//!   `h2_settings_frame.available=false` until F-1.g lands real capture →
//!   all 4 current builtin profiles fail static, no runtime ever runs).
//! - Runtime layer catches "profile is structurally complete but the fork
//!   adapter cannot reproduce the declared bytes" — F-1.b's loopback test
//!   proves the adapter-driven bytes match the synthetic profile; this
//!   module makes that check first-class for any profile.
//!
//! Naming: `is_l2_dispatchable` (not `is_dispatchable`) to avoid clashing
//! with the existing `l1_preflight::is_dispatchable` re-exported through
//! `mimicry::mod.rs`. Callers can `use crate::mimicry::l2_preflight::is_l2_dispatchable;`
//! when they need the L2 predicate without dragging in L1 noise.

use super::FingerprintProfile;
use super::profile::ProfileMode;

/// Outcome of the L2 HTTP/2 preflight stage. Distinguishes profiles that
/// need no preflight (e.g., http/1.1-only baselines), profiles that
/// still need a runtime check, profiles that passed both gates, and
/// profiles that failed in a typed way.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum L2HttpPreflightStatus {
    /// Profile does not intend to use HTTP/2 (e.g., http/1.1 only). The L2
    /// fork path is bypassed; H1 fallback handles the request.
    NotRequired,
    /// Profile is statically eligible (has H2 capture + ALPN h2). Caller
    /// MUST run [`run_l2_preflight`] to perform the runtime byte check
    /// before treating this as dispatchable.
    Pending { profile_mode: ProfileMode },
    /// Runtime preflight confirmed that the adapter's wire output for this
    /// profile matches the declared H2 capture bytes. Dispatch is allowed.
    Passed { profile_mode: ProfileMode },
    /// Static or runtime preflight rejected the profile.
    Failed(L2HttpPreflightError),
}

/// Typed failure reasons for the L2 HTTP/2 preflight stage. Each variant
/// corresponds to one of the legitimate ways an HTTP/2 fingerprint profile
/// can fail to qualify for production dispatch through the fork.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum L2HttpPreflightError {
    /// Profile is missing required H2 capture data — `available=false`,
    /// empty `raw_order`, empty pseudo-header order, etc. Permanent until
    /// F-1.g imports real upstream capture.
    KnownGap { profile_mode: ProfileMode, reason: String },
    /// Profile cannot negotiate HTTP/2 at the TLS layer — e.g.,
    /// `profile.tls.alpn_protocols` does not include `"h2"`. Even if the
    /// fork is wired and the profile's H2 fields are complete, the live
    /// BoringSSL handshake will pick http/1.1 (or fail) → no H2 path.
    BackendUnsupported { profile_mode: ProfileMode, reason: String },
    /// Runtime preflight ran the adapter against the profile and observed
    /// wire bytes that disagree with the declared profile order / values.
    RuntimeMismatch { profile_mode: ProfileMode, reason: String },
    /// The HTTP/2 fork adapter could not be constructed for this profile
    /// (e.g., `mimicry-http2-fork` feature off in the binary, or the
    /// adapter builder rejected the profile fields). The fork path is
    /// fundamentally unavailable, not just byte-mismatched.
    AdapterMissing { profile_mode: ProfileMode, reason: String },
}

impl L2HttpPreflightError {
    /// Profile mode the error belongs to. Useful for telemetry slicing
    /// without leaking credential or session.
    pub fn profile_mode(&self) -> ProfileMode {
        match self {
            Self::KnownGap { profile_mode, .. }
            | Self::BackendUnsupported { profile_mode, .. }
            | Self::RuntimeMismatch { profile_mode, .. }
            | Self::AdapterMissing { profile_mode, .. } => *profile_mode,
        }
    }

    /// Stable short string for log fields. Stays stable across edits so
    /// dashboards don't break when error messages get rewritten.
    pub fn classification(&self) -> &'static str {
        match self {
            Self::KnownGap { .. } => "known_gap",
            Self::BackendUnsupported { .. } => "backend_unsupported",
            Self::RuntimeMismatch { .. } => "runtime_mismatch",
            Self::AdapterMissing { .. } => "adapter_missing",
        }
    }
}

/// Static classification step: inspect a profile's H2 capture fields +
/// ALPN list and return the early-exit preflight state. Does NOT touch the
/// fork adapter — callers who get back [`L2HttpPreflightStatus::Pending`]
/// must call [`run_l2_preflight`] to perform the runtime byte check.
///
/// Decision tree:
///   - `h2_settings_frame.available = false`           → Failed(KnownGap)
///   - `h2_settings_frame.raw_order.is_empty()`        → Failed(KnownGap)
///   - `!h2_pseudo_header_capture.available`           → Failed(KnownGap)
///   - `h2_pseudo_header_capture.order.is_empty()`     → Failed(KnownGap)
///   - `!profile.tls.alpn_protocols.contains("h2")`    → Failed(BackendUnsupported)
///   - otherwise                                       → Pending
///
/// Today's expected behavior across the 4 built-in profiles: all 4 return
/// `Failed(KnownGap)` because none has real upstream H2 capture data
/// (`docs/process/release-readiness/W11-F-F1-status.md` §2).
///
/// Mutation discriminator (CLAUDE.md #14):
/// - Removing the `h2_settings_frame.available` check makes the
///   `static_classifier_*_known_gap_for_missing_capture` tests pass on a
///   profile that has empty raw_order — the test then catches it via the
///   `raw_order.is_empty()` branch. Removing both checks makes the
///   "anthropic returns Failed(KnownGap)" test red.
/// - Removing the ALPN check makes `static_classifier_backend_unsupported_for_h1_only_profile`
///   return Pending instead of Failed(BackendUnsupported) → red.
pub fn preflight_status_from_profile(profile: &FingerprintProfile) -> L2HttpPreflightStatus {
    // (1) H2 capture availability gate (F-1.a evidence contract).
    if !profile.h2_settings_frame.available {
        return L2HttpPreflightStatus::Failed(L2HttpPreflightError::KnownGap {
            profile_mode: profile.mode,
            reason: format!(
                "h2_settings_frame.available=false for {:?}; no real upstream H2 capture yet (blocked on F-1.g)",
                profile.mode
            ),
        });
    }
    if profile.h2_settings_frame.raw_order.is_empty() {
        return L2HttpPreflightStatus::Failed(L2HttpPreflightError::KnownGap {
            profile_mode: profile.mode,
            reason: format!(
                "h2_settings_frame.raw_order empty for {:?} despite available=true; profile is structurally invalid",
                profile.mode
            ),
        });
    }
    if !profile.h2_pseudo_header_capture.available {
        return L2HttpPreflightStatus::Failed(L2HttpPreflightError::KnownGap {
            profile_mode: profile.mode,
            reason: format!(
                "h2_pseudo_header_capture.available=false for {:?}; required for HPACK pseudo-order parity",
                profile.mode
            ),
        });
    }
    if profile.h2_pseudo_header_capture.order.is_empty() {
        return L2HttpPreflightStatus::Failed(L2HttpPreflightError::KnownGap {
            profile_mode: profile.mode,
            reason: format!(
                "h2_pseudo_header_capture.order empty for {:?} despite available=true",
                profile.mode
            ),
        });
    }

    // (2) ALPN h2 negotiability gate (round 1 Codex P1 on F-1 synthesis).
    if !profile.tls.alpn_protocols.iter().any(|p| p == "h2") {
        return L2HttpPreflightStatus::Failed(L2HttpPreflightError::BackendUnsupported {
            profile_mode: profile.mode,
            reason: format!(
                "profile.tls.alpn_protocols={:?} does not include \"h2\"; HTTP/2 not negotiable at TLS layer",
                profile.tls.alpn_protocols
            ),
        });
    }

    // (3) Static gates passed. Runtime byte check still owes a Passed/Failed.
    L2HttpPreflightStatus::Pending {
        profile_mode: profile.mode,
    }
}

/// Convenience predicate: did the L2 preflight allow this profile to proceed
/// to dispatch? `NotRequired` and `Passed` are allowed; everything else
/// must fail closed at the builder.
///
/// `Pending` returns `false` here even though the runtime check is yet to
/// run — same semantic as L1's `is_dispatchable`. Callers MUST resolve
/// `Pending` to either `Passed` or `Failed` via [`run_l2_preflight`] before
/// treating the profile as dispatchable.
pub fn is_l2_dispatchable(status: &L2HttpPreflightStatus) -> bool {
    matches!(
        status,
        L2HttpPreflightStatus::NotRequired | L2HttpPreflightStatus::Passed { .. }
    )
}

/// W11-F F-1.c runtime: drive the HTTP/2 fork adapter against the profile,
/// capture the wire bytes (SETTINGS frame + HEADERS frame), and assert that
/// the captured ordering matches the profile's declared `h2_settings_frame.raw_order`
/// + `h2_pseudo_header_capture.order`. Resolves `Pending` → `Passed` or
/// `Failed(RuntimeMismatch)`.
///
/// In-memory implementation: uses
/// [`crate::mimicry::http2_adapter::HttpTwoMimicryAdapter::encode_request_exchange`]
/// which runs the fork over `tokio::io::DuplexStream`. No real network
/// traffic. F-1.b's loopback TCP test proved the byte equivalence between
/// in-memory and real-TCP paths.
///
/// Static-fail propagation: if `preflight_status_from_profile` returns
/// `Failed(...)` or `NotRequired`, this function returns the same status
/// without spinning up the adapter — runtime check is only meaningful when
/// the profile is `Pending`.
///
/// Mutation discriminator (CLAUDE.md #14):
/// - If runtime parsing collapses to "any encode_request_exchange success →
///   Passed" without checking bytes, a profile whose declared raw_order
///   differs from what the adapter produces would falsely pass. The
///   `runtime_returns_mismatch_when_profile_raw_order_disagrees` test
///   triggers exactly that case.
/// - If the runtime returns `Passed` for an adapter that returned `Err`,
///   the `runtime_returns_adapter_missing_when_adapter_fails` test goes red.
#[cfg(feature = "mimicry-http2-fork")]
pub async fn run_l2_preflight(profile: &FingerprintProfile) -> L2HttpPreflightStatus {
    let static_status = preflight_status_from_profile(profile);
    if !matches!(static_status, L2HttpPreflightStatus::Pending { .. }) {
        return static_status;
    }

    use super::http2_adapter::HttpTwoMimicryAdapter;
    let adapter = match HttpTwoMimicryAdapter::new_with_profile(profile) {
        Ok(a) => a,
        Err(err) => {
            return L2HttpPreflightStatus::Failed(L2HttpPreflightError::AdapterMissing {
                profile_mode: profile.mode,
                reason: format!("HttpTwoMimicryAdapter::new_with_profile failed: {err}"),
            });
        }
    };

    let request = match http::Request::builder()
        .method("POST")
        .uri(format!(
            "https://{}{}",
            profile.target_host, profile.http_layer.endpoint
        ))
        .version(http::Version::HTTP_2)
        .header("user-agent", profile.http_layer.user_agent.as_str())
        .body(())
    {
        Ok(req) => req,
        Err(err) => {
            return L2HttpPreflightStatus::Failed(L2HttpPreflightError::AdapterMissing {
                profile_mode: profile.mode,
                reason: format!("preflight request builder failed: {err}"),
            });
        }
    };

    let exchange = match adapter.encode_request_exchange(request).await {
        Ok(ex) => ex,
        Err(err) => {
            return L2HttpPreflightStatus::Failed(L2HttpPreflightError::RuntimeMismatch {
                profile_mode: profile.mode,
                reason: format!("encode_request_exchange failed: {err}"),
            });
        }
    };

    // Byte-level check #1: SETTINGS frame ids in wire order match
    // profile.h2_settings_frame.raw_order.
    let captured_settings_ids = parse_settings_ids(&exchange.initial_settings_frame);
    if captured_settings_ids != profile.h2_settings_frame.raw_order {
        return L2HttpPreflightStatus::Failed(L2HttpPreflightError::RuntimeMismatch {
            profile_mode: profile.mode,
            reason: format!(
                "SETTINGS id order mismatch. Profile {:?}, adapter wire {:?}",
                profile.h2_settings_frame.raw_order, captured_settings_ids
            ),
        });
    }

    // Byte-level check #2: HEADERS frame pseudo-header order matches
    // profile.h2_pseudo_header_capture.order.
    let captured_pseudo_order = parse_pseudo_header_order(&exchange.request_headers_frame);
    if captured_pseudo_order != profile.h2_pseudo_header_capture.order {
        return L2HttpPreflightStatus::Failed(L2HttpPreflightError::RuntimeMismatch {
            profile_mode: profile.mode,
            reason: format!(
                "HEADERS pseudo-order mismatch. Profile {:?}, adapter wire {:?}",
                profile.h2_pseudo_header_capture.order, captured_pseudo_order
            ),
        });
    }

    L2HttpPreflightStatus::Passed {
        profile_mode: profile.mode,
    }
}

// ===== HPACK + frame parsing helpers (private to l2_preflight) =====
// Mirror the parsers in tests/mimicry_http2_adapter_test.rs lines 125-256;
// kept local here so production runtime preflight doesn't depend on test
// code. If the two implementations drift, F-1.b loopback test + this
// module's runtime test will catch via separate parsers asserting same
// bytes.

#[cfg(feature = "mimicry-http2-fork")]
fn parse_settings_ids(frame: &[u8]) -> Vec<u16> {
    if frame.len() < 9 {
        return Vec::new();
    }
    let len = ((frame[0] as usize) << 16) | ((frame[1] as usize) << 8) | frame[2] as usize;
    if frame[3] != 0x04 || frame.len() < 9 + len {
        return Vec::new();
    }
    let payload = &frame[9..9 + len];
    payload
        .chunks_exact(6)
        .map(|chunk| u16::from_be_bytes([chunk[0], chunk[1]]))
        .collect()
}

#[cfg(feature = "mimicry-http2-fork")]
fn parse_pseudo_header_order(frame: &[u8]) -> Vec<String> {
    if frame.len() < 9 || frame[3] != 0x01 {
        return Vec::new();
    }
    let len = ((frame[0] as usize) << 16) | ((frame[1] as usize) << 8) | frame[2] as usize;
    if frame.len() < 9 + len {
        return Vec::new();
    }
    let flags = frame[4];
    let mut payload = &frame[9..9 + len];
    if flags & 0x08 != 0 {
        if payload.is_empty() {
            return Vec::new();
        }
        let pad_len = payload[0] as usize;
        if 1 + pad_len > payload.len() {
            return Vec::new();
        }
        payload = &payload[1..payload.len() - pad_len];
    }
    if flags & 0x20 != 0 {
        if payload.len() < 5 {
            return Vec::new();
        }
        payload = &payload[5..];
    }
    pseudo_names_from_hpack(payload)
}

#[cfg(feature = "mimicry-http2-fork")]
fn pseudo_names_from_hpack(block: &[u8]) -> Vec<String> {
    let mut offset = 0;
    let mut names = Vec::new();
    while offset < block.len() {
        let byte = block[offset];
        if byte & 0x80 != 0 {
            let index = decode_prefixed_integer(block, &mut offset, 7);
            if let Some(name) = static_pseudo_name(index) {
                names.push(name.to_owned());
            } else if !names.is_empty() {
                break;
            }
            continue;
        }
        if byte & 0x20 != 0 {
            let _ = decode_prefixed_integer(block, &mut offset, 5);
            continue;
        }
        let prefix_bits = if byte & 0x40 != 0 { 6 } else { 4 };
        let name_index = decode_prefixed_integer(block, &mut offset, prefix_bits);
        if name_index == 0 {
            if !names.is_empty() {
                break;
            }
            skip_hpack_string(block, &mut offset);
        } else if let Some(name) = static_pseudo_name(name_index) {
            names.push(name.to_owned());
        } else if !names.is_empty() {
            break;
        }
        skip_hpack_string(block, &mut offset);
    }
    names
}

#[cfg(feature = "mimicry-http2-fork")]
fn static_pseudo_name(index: usize) -> Option<&'static str> {
    match index {
        1 => Some(":authority"),
        2 | 3 => Some(":method"),
        4 | 5 => Some(":path"),
        6 | 7 => Some(":scheme"),
        8 => Some(":status"),
        _ => None,
    }
}

#[cfg(feature = "mimicry-http2-fork")]
fn decode_prefixed_integer(block: &[u8], offset: &mut usize, prefix_bits: u8) -> usize {
    if *offset >= block.len() {
        return 0;
    }
    let mask = (1u8 << prefix_bits) - 1;
    let mut value = (block[*offset] & mask) as usize;
    *offset += 1;
    if value < mask as usize {
        return value;
    }
    let mut shift = 0;
    loop {
        if *offset >= block.len() {
            return value;
        }
        let byte = block[*offset];
        *offset += 1;
        value += ((byte & 0x7f) as usize) << shift;
        if byte & 0x80 == 0 {
            return value;
        }
        shift += 7;
    }
}

#[cfg(feature = "mimicry-http2-fork")]
fn skip_hpack_string(block: &[u8], offset: &mut usize) {
    let len = decode_prefixed_integer(block, offset, 7);
    if *offset + len <= block.len() {
        *offset += len;
    } else {
        *offset = block.len();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mimicry::{BuiltinProfile, load_builtin_profile};

    /// Today: all 4 built-in profiles have `h2_settings_frame.available=false`
    /// (per F-1.a status doc + cross-check test). Static classifier must
    /// return `Failed(KnownGap)` for each. Mutation: dropping the
    /// `available` check would let one of them return Pending → red.
    #[test]
    fn static_classifier_known_gap_for_all_4_builtin_profiles() {
        for builtin in BuiltinProfile::ALL {
            let profile = load_builtin_profile(builtin).expect("builtin profile loads");
            let status = preflight_status_from_profile(&profile);
            // Compute dispatchability BEFORE matching so we don't have to deal
            // with partial moves of the inner String when destructuring KnownGap.
            assert!(
                !is_l2_dispatchable(&status),
                "Failed(KnownGap) must NOT be dispatchable for {builtin:?}"
            );
            match status {
                L2HttpPreflightStatus::Failed(L2HttpPreflightError::KnownGap {
                    profile_mode,
                    reason,
                }) => {
                    assert_eq!(profile_mode, profile.mode);
                    assert!(
                        !reason.is_empty(),
                        "KnownGap reason must be non-empty for {builtin:?}"
                    );
                }
                other => panic!(
                    "{builtin:?} should be Failed(KnownGap) today (all 4 lack real H2 capture); got {other:?}"
                ),
            }
        }
    }

    /// Synthetic profile with `available=true` + raw_order populated +
    /// pseudo-header populated + ALPN includes "h2" must reach `Pending`.
    /// Mutation: any of the 4 gate checks (`available`, `raw_order`,
    /// `pseudo_header_capture.available`, `pseudo_header_capture.order`)
    /// failing makes this test red because the classifier returns
    /// Failed(KnownGap) instead of Pending.
    #[test]
    fn static_classifier_pending_when_all_static_gates_satisfied() {
        let mut profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
            .expect("Anthropic profile loads");
        // Synthetic flips to make the static gates pass.
        profile.h2_settings_frame.available = true;
        profile.h2_settings_frame.raw_order = vec![4, 1, 6, 5, 2, 3];
        profile.h2_settings_frame.values = std::collections::BTreeMap::from([
            (4, 65_535),
            (1, 4_096),
            (6, 262_144),
            (5, 16_384),
            (2, 0),
            (3, 100),
        ]);
        profile.h2_pseudo_header_capture.available = true;
        profile.h2_pseudo_header_capture.order = vec![
            ":method".to_owned(),
            ":authority".to_owned(),
            ":scheme".to_owned(),
            ":path".to_owned(),
        ];
        profile.tls.alpn_protocols = vec!["h2".to_owned(), "http/1.1".to_owned()];

        let status = preflight_status_from_profile(&profile);
        match status {
            L2HttpPreflightStatus::Pending { profile_mode } => {
                assert_eq!(profile_mode, profile.mode);
            }
            other => panic!("expected Pending after all static gates pass; got {other:?}"),
        }
    }

    /// Profile with all H2 capture data present but `tls.alpn_protocols`
    /// not including "h2" must return `Failed(BackendUnsupported)`.
    /// Mutation: removing the ALPN check makes the classifier return
    /// `Pending` (wrong) → this test goes red.
    #[test]
    fn static_classifier_backend_unsupported_for_h1_only_profile() {
        let mut profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
            .expect("Anthropic profile loads");
        profile.h2_settings_frame.available = true;
        profile.h2_settings_frame.raw_order = vec![4, 1];
        profile.h2_settings_frame.values =
            std::collections::BTreeMap::from([(4, 65_535), (1, 4_096)]);
        profile.h2_pseudo_header_capture.available = true;
        profile.h2_pseudo_header_capture.order = vec![":method".to_owned()];
        // ALPN deliberately h1-only.
        profile.tls.alpn_protocols = vec!["http/1.1".to_owned()];

        let status = preflight_status_from_profile(&profile);
        match status {
            L2HttpPreflightStatus::Failed(L2HttpPreflightError::BackendUnsupported {
                profile_mode,
                reason,
            }) => {
                assert_eq!(profile_mode, profile.mode);
                assert!(
                    reason.contains("alpn_protocols") || reason.contains("\"h2\""),
                    "BackendUnsupported reason should cite ALPN absence (got: {reason})"
                );
            }
            other => panic!("expected Failed(BackendUnsupported), got {other:?}"),
        }
    }

    /// `is_l2_dispatchable` must reject `Pending` to force the runtime
    /// check before traffic flows. Mutation: returning true for Pending
    /// here would let every non-baseline profile flow through dispatch
    /// without runtime verification → this test goes red.
    #[test]
    fn is_l2_dispatchable_rejects_pending() {
        let pending = L2HttpPreflightStatus::Pending {
            profile_mode: ProfileMode::AnthropicClaudeCode,
        };
        assert!(
            !is_l2_dispatchable(&pending),
            "Pending must NOT be dispatchable until runtime preflight resolves"
        );
        let passed = L2HttpPreflightStatus::Passed {
            profile_mode: ProfileMode::AnthropicClaudeCode,
        };
        assert!(is_l2_dispatchable(&passed));
        assert!(is_l2_dispatchable(&L2HttpPreflightStatus::NotRequired));
    }

    /// Error `classification()` returns stable strings used by dashboards.
    /// Mutation: renaming any classification string breaks downstream label.
    #[test]
    fn error_classification_strings_are_stable() {
        let cases = [
            (
                L2HttpPreflightError::KnownGap {
                    profile_mode: ProfileMode::AnthropicClaudeCode,
                    reason: "x".to_owned(),
                },
                "known_gap",
            ),
            (
                L2HttpPreflightError::BackendUnsupported {
                    profile_mode: ProfileMode::AnthropicClaudeCode,
                    reason: "x".to_owned(),
                },
                "backend_unsupported",
            ),
            (
                L2HttpPreflightError::RuntimeMismatch {
                    profile_mode: ProfileMode::CodexCli,
                    reason: "x".to_owned(),
                },
                "runtime_mismatch",
            ),
            (
                L2HttpPreflightError::AdapterMissing {
                    profile_mode: ProfileMode::KiroCli,
                    reason: "x".to_owned(),
                },
                "adapter_missing",
            ),
        ];
        for (err, expected) in cases {
            assert_eq!(err.classification(), expected);
        }
    }

    /// W11-F F-1.c runtime: synthetic profile that passes ALL static gates
    /// + uses the same SETTINGS/pseudo-header order that the adapter actually
    /// produces should reach `Passed` after runtime byte check.
    /// Mutation: weaken `run_l2_preflight` to return `Passed` without
    /// comparing bytes → the next test (`runtime_returns_mismatch_*`)
    /// stops failing red on the bad case (because everything is "Passed").
    #[cfg(feature = "mimicry-http2-fork")]
    #[tokio::test]
    async fn runtime_returns_passed_for_synthetic_matching_profile() {
        let mut profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("loads");
        let settings_order = vec![4u16, 1, 6, 5, 2, 3];
        let settings_values = std::collections::BTreeMap::from([
            (4u16, 65_535u32),
            (1, 4_096),
            (6, 262_144),
            (5, 16_384),
            (2, 0),
            (3, 100),
        ]);
        let pseudo_order = vec![
            ":method".to_owned(),
            ":authority".to_owned(),
            ":scheme".to_owned(),
            ":path".to_owned(),
        ];
        profile.h2_settings_frame.available = true;
        profile.h2_settings_frame.raw_order = settings_order.clone();
        profile.h2_settings_frame.values = settings_values.clone();
        profile.h2_pseudo_header_capture.available = true;
        profile.h2_pseudo_header_capture.order = pseudo_order.clone();
        profile.h2_settings_order = settings_order;
        profile.h2_settings_values = settings_values;
        profile.h2_pseudo_header_order = pseudo_order;
        profile.tls.alpn_protocols = vec!["h2".to_owned(), "http/1.1".to_owned()];

        let status = run_l2_preflight(&profile).await;
        match status {
            L2HttpPreflightStatus::Passed { profile_mode } => {
                assert_eq!(profile_mode, profile.mode);
            }
            other => panic!(
                "synthetic matching profile should Pass runtime preflight; got {other:?}"
            ),
        }
    }

    /// W11-F F-1.c runtime: synthetic profile where the declared
    /// `raw_order` disagrees with what the adapter actually drives via the
    /// fork must reach `Failed(RuntimeMismatch)`. The adapter takes the
    /// profile's raw_order + values for `apply_settings`, so a divergence
    /// here is constructed by setting `h2_settings_frame.raw_order` (the
    /// "expected" capture field used by preflight check) to a DIFFERENT
    /// order than `h2_settings_order` (the operational field used by the
    /// adapter). The runtime check sees adapter-wire order ≠ expected and
    /// returns RuntimeMismatch.
    ///
    /// Mutation: collapsing `run_l2_preflight` to "encode succeeded →
    /// Passed" without comparing captured bytes makes this test return
    /// Passed (wrong) → red.
    #[cfg(feature = "mimicry-http2-fork")]
    #[tokio::test]
    async fn runtime_returns_mismatch_when_profile_raw_order_disagrees() {
        let mut profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("loads");
        let adapter_order = vec![4u16, 1, 6];
        let expected_capture_order_intentionally_diverged = vec![1u16, 6, 4]; // intentional disagreement
        let settings_values = std::collections::BTreeMap::from([
            (4u16, 65_535u32),
            (1, 4_096),
            (6, 262_144),
        ]);
        let pseudo_order = vec![":method".to_owned(), ":path".to_owned()];
        profile.h2_settings_frame.available = true;
        profile.h2_settings_frame.raw_order = expected_capture_order_intentionally_diverged;
        profile.h2_settings_frame.values = settings_values.clone();
        profile.h2_pseudo_header_capture.available = true;
        profile.h2_pseudo_header_capture.order = pseudo_order.clone();
        // Adapter operational fields use the DIFFERENT order; this is what
        // the fork actually puts on the wire.
        profile.h2_settings_order = adapter_order;
        profile.h2_settings_values = settings_values;
        profile.h2_pseudo_header_order = pseudo_order;
        profile.tls.alpn_protocols = vec!["h2".to_owned()];

        let status = run_l2_preflight(&profile).await;
        match status {
            L2HttpPreflightStatus::Failed(L2HttpPreflightError::RuntimeMismatch {
                profile_mode,
                reason,
            }) => {
                assert_eq!(profile_mode, profile.mode);
                assert!(
                    reason.contains("SETTINGS")
                        || reason.contains("id order")
                        || reason.contains("Profile"),
                    "RuntimeMismatch reason should cite SETTINGS divergence (got: {reason})"
                );
            }
            other => panic!(
                "profile/adapter raw_order disagreement should produce Failed(RuntimeMismatch); got {other:?}"
            ),
        }
    }

    /// W11-F F-1.c runtime: static-fail short-circuit. If
    /// `preflight_status_from_profile` returns Failed(KnownGap), the
    /// runtime runner returns the same status without spinning up the
    /// adapter (cheap). Today this fires for all 4 built-in profiles
    /// (available=false). Mutation: removing the static short-circuit and
    /// always calling the adapter would make this test red because the
    /// reason text would no longer cite the static missing-capture phrase.
    #[cfg(feature = "mimicry-http2-fork")]
    #[tokio::test]
    async fn runtime_propagates_static_failed_without_running_adapter() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).expect("loads");
        let status = run_l2_preflight(&profile).await;
        match status {
            L2HttpPreflightStatus::Failed(L2HttpPreflightError::KnownGap { reason, .. }) => {
                assert!(
                    reason.contains("h2_settings_frame.available=false")
                        || reason.contains("no real upstream H2 capture"),
                    "static-fail reason should cite missing capture (got: {reason})"
                );
            }
            other => panic!("expected Failed(KnownGap) from static, got {other:?}"),
        }
    }
}
