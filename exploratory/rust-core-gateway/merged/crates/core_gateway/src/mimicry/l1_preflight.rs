//! W11-F F-2.2 (synthesis §6 + Codex D-F2-1, 2026-05-24): central L1 TLS
//! preflight abstraction.
//!
//! Why this module exists
//! ----------------------
//! Before F-2.2 the production dispatch path mixed two responsibilities:
//!   - policy classification (`backend_intent()` / `match_policy()` decide if
//!     a profile is dispatchable in principle), and
//!   - runtime verification (`OpenSslMimicryAdapter::run_profile_preflight()`
//!     checks that the actual ClientHello bytes the OpenSSL backend would
//!     emit match the profile template).
//!
//! Codex' parallel-draft pointed out that the runtime gate was implicit and
//! could regress silently: an adapter that compiled correctly but produced
//! wrong wire bytes might still be allowed through dispatch because
//! `DispatchDecision::AllowOpenSsl` is returned before the connection is
//! actually built. Synthesis D-S2 + D-S5 + Codex G-COX-2 accept the proposal
//! to introduce a typed status / error pair that wraps every runtime
//! preflight outcome so the dispatch layer can branch on a single value.
//!
//! Scope of THIS file (F-2.2 only)
//! -------------------------------
//!   - Define [L1TlsPreflightStatus] and [L1TlsPreflightError] (typed gate
//!     surfaces).
//!   - Provide [preflight_status_from_intent] — a static classification step
//!     that looks at a profile's policy and returns one of the early-exit
//!     states (NotRequired / KnownGap / BackendUnsupported) without touching
//!     a TLS adapter.
//!   - Do NOT yet wire the runtime adapter result here; that happens in
//!     F-2.3a / F-2.3c when the adapter call site is reachable from this
//!     module's caller. The Pending variant marks profiles that need that
//!     downstream runtime check.
//!
//! Stage-gate semantics
//! --------------------
//!   - `NotRequired` — the profile is the verified L1 baseline (Anthropic);
//!     dispatch may proceed without further L1 gating. Other gates (L2,
//!     canary policy) still apply independently.
//!   - `Pending` — the profile is policy-eligible but its runtime wire bytes
//!     have not yet been verified for this binary. Callers MUST run the
//!     backend-specific preflight (OpenSSL adapter or Boring builder) before
//!     allowing production traffic. Treat as fail-closed until verified.
//!   - `Failed` — runtime preflight ran and rejected the profile (wrong
//!     extension order, missing required extension, hash mismatch). NEVER
//!     dispatch this profile to production until F-2.3 closes the gap or
//!     Owner reclassifies it as KnownGap permanent.
//!
//! Mutation tests for this gate live in `mimicry_dispatch_test.rs` —
//! removing the `Pending`-arm wiring or treating `Failed` as `Passed` will
//! flip the discriminating assertions per CLAUDE.md #14.

use super::{FingerprintProfile, ProfileMode};
use super::backend::BackendIntent;

/// Outcome of the L1 TLS preflight stage. Distinguishes profiles that need
/// no preflight (the verified baseline) from those that must run a runtime
/// adapter check before dispatch is allowed.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum L1TlsPreflightStatus {
    /// Profile is the verified L1 baseline (Anthropic). Dispatch may proceed
    /// without further L1 gating. Other gates (L2, canary recency) still apply.
    NotRequired,
    /// Profile is policy-eligible but its runtime wire bytes have not yet
    /// been confirmed for this binary. Callers MUST invoke the
    /// backend-specific preflight runner before allowing dispatch.
    ///
    /// `profile_mode` is carried so the runtime runner can branch.
    Pending { profile_mode: ProfileMode },
    /// Runtime preflight ran and confirmed wire bytes match the profile
    /// template. Dispatch is allowed (subject to other gates).
    Passed { profile_mode: ProfileMode },
    /// Runtime preflight ran and rejected the profile, OR static
    /// classification rejected the profile before any runtime call.
    Failed(L1TlsPreflightError),
}

/// Typed failure reasons for the L1 TLS preflight stage. Each variant
/// corresponds to one of the legitimate ways a profile can fail to qualify
/// for production dispatch.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum L1TlsPreflightError {
    /// Profile declares wire-level fields the current backend implementation
    /// cannot reproduce. Permanent until a downstream sub-phase closes the
    /// gap or Owner moves the profile to F-3 roadmap.
    KnownGap { profile_mode: ProfileMode, reason: String },
    /// Profile's declared TLS backend has no production implementation in
    /// this binary (e.g. `tls_backend = rustls` for Kiro before D-S3, OR a
    /// new vendor template that hasn't been mapped yet).
    BackendUnsupported { profile_mode: ProfileMode, reason: String },
    /// Runtime preflight ran and saw a wire-byte mismatch — extension
    /// missing, wrong order, hash mismatch, etc. The string carries enough
    /// detail for log-only triage but never includes raw credential material
    /// (preflight runs on dummy handshake against fixture, not real upstream).
    RuntimeMismatch { profile_mode: ProfileMode, reason: String },
    /// Profile would otherwise pass, but the backend adapter required by
    /// `backend_intent()` is missing at compile time (mimicry-openssl /
    /// mimicry-boring feature off). The build is operating below its
    /// declared production capability.
    AdapterMissing { profile_mode: ProfileMode, reason: String },
}

impl L1TlsPreflightError {
    /// Profile mode the error belongs to. Useful for telemetry slicing
    /// without leaking the credential or session.
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

/// Static classification step: inspect a profile's policy/intent and return
/// the early-exit preflight state. Does NOT touch any TLS adapter — callers
/// who get back [L1TlsPreflightStatus::Pending] must run the backend-specific
/// runtime preflight separately.
///
/// Two early-exit paths:
///   - The profile is the verified Anthropic baseline → `NotRequired`.
///   - `backend_intent()` returns `KnownGapBlocked` or `UnsupportedTemplate`
///     → `Failed(KnownGap | BackendUnsupported)`.
///
/// Everything else returns `Pending` and the caller is responsible for
/// dispatching to the backend-specific runtime runner.
///
/// Mutation check (mimicry_dispatch_test.rs::preflight_classifies_*):
/// returning `Passed` for a known-gap profile here would silently bypass
/// the gap; the discriminating assertion in those tests goes red.
pub fn preflight_status_from_intent(profile: &FingerprintProfile) -> L1TlsPreflightStatus {
    // Anthropic profile is the verified L1 baseline. Other gates (L2 wiring,
    // canary recency) still need to pass independently — this is L1-only.
    if profile.mode == ProfileMode::AnthropicClaudeCode {
        return L1TlsPreflightStatus::NotRequired;
    }

    match profile.backend_intent() {
        BackendIntent::OpenSslAdapter => L1TlsPreflightStatus::Pending {
            profile_mode: profile.mode,
        },
        BackendIntent::KnownGapBlocked { reason } => L1TlsPreflightStatus::Failed(
            L1TlsPreflightError::KnownGap {
                profile_mode: profile.mode,
                reason,
            },
        ),
        BackendIntent::UnsupportedTemplate { reason } => L1TlsPreflightStatus::Failed(
            L1TlsPreflightError::BackendUnsupported {
                profile_mode: profile.mode,
                reason,
            },
        ),
    }
}

/// Convenience predicate: did the L1 preflight allow this profile to proceed
/// to (other) dispatch gates? `NotRequired` and `Passed` are allowed;
/// everything else must fail closed.
///
/// Note: `Pending` returns `false` here even though the runtime check is yet
/// to run. This is intentional — callers MUST resolve `Pending` to either
/// `Passed` or `Failed` via the runtime runner before treating the profile
/// as dispatchable. Allowing `Pending` here would defeat the gate.
pub fn is_dispatchable(status: &L1TlsPreflightStatus) -> bool {
    matches!(
        status,
        L1TlsPreflightStatus::NotRequired | L1TlsPreflightStatus::Passed { .. }
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mimicry::{BuiltinProfile, load_builtin_profile};

    /// W11-F F-2.2 (mutation-resistant per CLAUDE.md #14):
    /// Anthropic profile MUST land in `NotRequired` — it is the verified
    /// baseline and zero-regression contract for the F-2 wave.
    ///
    /// Mutation: returning `Pending` for Anthropic would force every
    /// dispatch to wait for a runtime preflight that wasn't designed for
    /// it; this test goes red on that mutation.
    #[test]
    fn preflight_anthropic_is_not_required() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
            .expect("Anthropic builtin profile loads");
        let status = preflight_status_from_intent(&profile);
        assert!(
            matches!(status, L1TlsPreflightStatus::NotRequired),
            "Anthropic must be NotRequired (baseline), got {:?}",
            status
        );
        assert!(is_dispatchable(&status));
    }

    /// CodexCli profile has 4 hard-coded `known_gap_fields` —
    /// `backend_intent()` already returns `KnownGapBlocked` for it. Preflight
    /// must surface this as `Failed(KnownGap)` — the dispatch layer special-
    /// cases CodexCli back to OpenSslAdapter at the resolver level, but the
    /// preflight gate must record the known gap for telemetry / future
    /// `Passed` classification once F-2.3b closes it.
    ///
    /// Mutation: classifying CodexCli as `Passed` here would mask the gap
    /// and remove the discriminator for F-2.3b's "closure happened" test.
    #[test]
    fn preflight_codex_cli_is_failed_known_gap() {
        let profile = load_builtin_profile(BuiltinProfile::CodexCli)
            .expect("CodexCli builtin profile loads");
        let status = preflight_status_from_intent(&profile);
        match &status {
            L1TlsPreflightStatus::Failed(L1TlsPreflightError::KnownGap { profile_mode, reason }) => {
                assert_eq!(*profile_mode, ProfileMode::CodexCli);
                assert!(!reason.is_empty(), "KnownGap reason must be non-empty");
            }
            other => panic!("Codex must be Failed(KnownGap), got {:?}", other),
        }
        assert!(!is_dispatchable(&status));
    }

    /// `is_dispatchable` MUST reject `Pending` — otherwise the runtime check
    /// can be skipped silently.
    ///
    /// Mutation: returning `true` for `Pending` here would let every
    /// non-Anthropic profile flow through dispatch without runtime
    /// verification; this test goes red.
    #[test]
    fn is_dispatchable_rejects_pending_to_force_runtime_check() {
        let status = L1TlsPreflightStatus::Pending {
            profile_mode: ProfileMode::GeminiAdvanced,
        };
        assert!(
            !is_dispatchable(&status),
            "Pending must NOT be dispatchable until runtime preflight resolves it"
        );
    }

    /// `L1TlsPreflightError::classification` returns stable strings used by
    /// dashboards / SLO panels.
    ///
    /// Mutation: renaming any classification string would break the
    /// downstream label, this test pins the contract.
    #[test]
    fn error_classification_strings_are_stable() {
        let cases = [
            (
                L1TlsPreflightError::KnownGap {
                    profile_mode: ProfileMode::CodexCli,
                    reason: "x".to_owned(),
                },
                "known_gap",
            ),
            (
                L1TlsPreflightError::BackendUnsupported {
                    profile_mode: ProfileMode::KiroCli,
                    reason: "x".to_owned(),
                },
                "backend_unsupported",
            ),
            (
                L1TlsPreflightError::RuntimeMismatch {
                    profile_mode: ProfileMode::GeminiAdvanced,
                    reason: "x".to_owned(),
                },
                "runtime_mismatch",
            ),
            (
                L1TlsPreflightError::AdapterMissing {
                    profile_mode: ProfileMode::CodexCli,
                    reason: "x".to_owned(),
                },
                "adapter_missing",
            ),
        ];
        for (err, expected) in cases {
            assert_eq!(err.classification(), expected);
        }
    }
}
