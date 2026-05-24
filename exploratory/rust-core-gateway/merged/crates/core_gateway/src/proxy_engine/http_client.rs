#[cfg(feature = "mimicry-boring")]
use std::sync::Arc;
use std::time::Duration;

use axum::body::Body;
#[cfg(not(feature = "mimicry-boring"))]
use hyper_rustls::HttpsConnector;
use hyper_util::{client::legacy::Client, rt::TokioExecutor};

#[cfg(feature = "mimicry-boring")]
use super::boring_tls_connector::BoringTlsConnector;
#[cfg(not(feature = "mimicry-boring"))]
use hyper_util::client::legacy::connect::HttpConnector;

#[cfg(feature = "mimicry-boring")]
use crate::mimicry::{
    BuiltinProfile, FingerprintProfile, MimicryProductionCanaryError, load_builtin_profile,
    verify_profile_dispatchable_for_production,
};

#[cfg(feature = "mimicry-boring")]
pub type GatewayHttpConnector = BoringTlsConnector;
#[cfg(not(feature = "mimicry-boring"))]
pub type GatewayHttpConnector = HttpsConnector<HttpConnector>;
pub type GatewayHttpClient = Client<GatewayHttpConnector, Body>;

#[cfg(not(feature = "mimicry-boring"))]
fn build_gateway_connector() -> GatewayHttpConnector {
    // R-E-A 临时兜底: 用 hyper-rustls 保 HTTPS TLS; R-E-A+1 切到 rquest+BoringSSL 后真删
    // (burn-the-boats 阶段性放宽 — Owner OCAW 2026-05-16 已认可).
    hyper_rustls::HttpsConnectorBuilder::new()
        .with_webpki_roots()
        .https_or_http()
        .enable_http1()
        .enable_http2()
        .build()
}

#[cfg(feature = "mimicry-boring")]
fn build_gateway_connector() -> GatewayHttpConnector {
    let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("内置 Anthropic Claude Code mimicry profile 必须通过启动前校验");
    // W11-F P1-3+P1-4 fix 2026-05-24 (Codex round 1 P1 wiring): production canary —
    // 内置 profile 即使加载成功, 仍必须通过 decide_dispatch (BackendIntent + dispatch gate)
    // 才能进入 production HTTP client。旧实现跳过此校验, KnownGap / UnsupportedTemplate
    // 标记的 profile 会被静默接入 = L1/L2 守门形同虚设。
    verify_profile_dispatchable_for_production(&profile).expect(
        "内置 mimicry profile 必须通过 production dispatch canary; \
         KnownGap / UnsupportedTemplate -> fail-fast 让运维知道 profile 必须修复后才能上 production",
    );
    BoringTlsConnector::new(Arc::new(profile))
}

/// W11-F F-2.3 (synthesis Codex D-F2-1, 2026-05-24): fallible builder lets
/// startup paths + tests branch on profile dispatchability without panicking.
///
/// Returns:
///   - `Ok(client)` — profile passed the production dispatch canary gate
///     (`verify_profile_dispatchable_for_production`) AND the L1 preflight
///     classification (l1_preflight::preflight_status_from_intent) yields
///     `NotRequired` or `Pending` (runtime preflight will fire at first
///     connect via OpenSslAdapter or BoringSSL builder).
///   - `Err(MimicryProductionCanaryError)` — profile is KnownGap or
///     UnsupportedTemplate. Caller may surface as `GatewayError::Config`,
///     log + skip, or fail the startup.
///
/// The non-fallible [`build_http_client_with_profile`] retains the
/// fail-fast `.expect(...)` semantics so production main wiring keeps
/// loud-failure behavior; the fallible variant exists for:
///   - tests that exercise the gate path without panicking
///   - future startup paths that prefer structured `Result` propagation
///
/// Mutation: removing the call to `verify_profile_dispatchable_for_production`
/// here would let KnownGap profiles silently produce a working
/// `GatewayHttpClient`; the per-profile tests in this module catch that.
#[cfg(feature = "mimicry-boring")]
pub fn try_build_http_client_with_profile(
    profile: Arc<FingerprintProfile>,
) -> Result<GatewayHttpClient, MimicryProductionCanaryError> {
    // W11-F P1-3+P1-4 fix: 二次守门防漏 (caller may have bypassed dispatch).
    verify_profile_dispatchable_for_production(&profile)?;
    Ok(build_http_client_with_connector(BoringTlsConnector::new(profile)))
}

#[cfg(feature = "mimicry-boring")]
pub fn build_http_client_with_profile(profile: Arc<FingerprintProfile>) -> GatewayHttpClient {
    // Backward-compat wrapper. New code prefers `try_build_http_client_with_profile`
    // so failures are typed Results, not panics. Main wiring keeps this for now
    // to preserve loud startup failure semantics.
    try_build_http_client_with_profile(profile).expect(
        "build_http_client_with_profile: profile 必须通过 production dispatch canary",
    )
}

pub fn build_http_client() -> GatewayHttpClient {
    build_http_client_with_connector(build_gateway_connector())
}

fn build_http_client_with_connector(connector: GatewayHttpConnector) -> GatewayHttpClient {
    let mut builder = Client::builder(TokioExecutor::new());
    builder.pool_idle_timeout(Duration::from_secs(90));
    builder.pool_max_idle_per_host(128);
    builder.build(connector)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[cfg(not(feature = "mimicry-boring"))]
    #[test]
    fn forward_to_https_endpoint_uses_tls() {
        fn assert_tls_connector(_: &HttpsConnector<HttpConnector>) {}

        let connector = build_gateway_connector();
        assert_tls_connector(&connector);
        assert!(
            std::any::type_name::<GatewayHttpConnector>().contains("hyper_rustls"),
            "Gateway HTTPS connector 必须来自 hyper-rustls, 禁止退回裸 HttpConnector"
        );
    }

    #[cfg(feature = "mimicry-boring")]
    #[test]
    fn forward_to_https_endpoint_uses_boring_tls() {
        fn assert_boring_connector(_: &BoringTlsConnector) {}

        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode).unwrap();
        let connector = BoringTlsConnector::new(Arc::new(profile));
        assert_boring_connector(&connector);
        assert!(
            std::any::type_name::<GatewayHttpConnector>().contains("BoringTlsConnector"),
            "mimicry-boring build 必须使用 HUAKAI BoringTLS connector"
        );
    }

    /// W11-F F-2.3 (Codex D-F2-1): fallible builder Ok path — Anthropic
    /// builtin profile passes the canary gate AND yields a usable HTTP client.
    ///
    /// Mutation: removing the verify_profile_dispatchable_for_production call
    /// from try_build_http_client_with_profile lets KnownGap profiles through;
    /// the `try_build_http_client_with_profile_rejects_blocked_profile` test
    /// below goes red on that mutation.
    #[cfg(feature = "mimicry-boring")]
    #[test]
    fn try_build_http_client_with_profile_accepts_anthropic() {
        let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
            .expect("Anthropic profile should load");
        let result = try_build_http_client_with_profile(Arc::new(profile));
        assert!(
            result.is_ok(),
            "Anthropic baseline must build through try_ variant: {:?}",
            result.err()
        );
    }

    /// W11-F F-2.3 (Codex D-F2-1): fallible builder Err path — Kiro CLI
    /// profile is KnownGap (per F-2.2 + 2026-05-24 reason correction), so the
    /// try_ variant returns Err rather than panicking. The Err variant must
    /// be `KnownGap` carrying the corrected reason ("real_upstream_capture"
    /// or "pending"), not the obsolete "rustls cannot be replicated" wording.
    ///
    /// Mutation: removing the gate call OR returning Ok on KnownGap lets
    /// the test go red on the `expect_err` AND the reason substring check.
    #[cfg(feature = "mimicry-boring")]
    #[test]
    fn try_build_http_client_with_profile_rejects_blocked_kiro() {
        let profile = load_builtin_profile(BuiltinProfile::KiroCli)
            .expect("Kiro profile should load");
        let err = try_build_http_client_with_profile(Arc::new(profile))
            .expect_err("Kiro KnownGap must fail-closed through try_ variant");
        match err {
            MimicryProductionCanaryError::KnownGap(reason) => {
                assert!(
                    reason.contains("real_upstream_capture") || reason.contains("pending"),
                    "Kiro KnownGap reason must cite real-upstream capture gap (got: {reason})"
                );
            }
            other => panic!(
                "Kiro should be KnownGap (post F-2.2 correction), got {other:?}"
            ),
        }
    }

    /// W11-F F-2.3 (Codex D-F2-1): the eager `build_http_client_with_profile`
    /// must still panic on KnownGap so production main wiring keeps its
    /// loud-failure semantics. The try_ variant is the only structured-Result
    /// path.
    ///
    /// Mutation: changing the wrapper to swallow the Err and return a stub
    /// client would let the panic disappear; this test goes red.
    #[cfg(feature = "mimicry-boring")]
    #[test]
    #[should_panic(expected = "production dispatch canary")]
    fn build_http_client_with_profile_panics_on_blocked_profile() {
        let profile = load_builtin_profile(BuiltinProfile::KiroCli)
            .expect("Kiro profile should load");
        let _ = build_http_client_with_profile(Arc::new(profile));
    }
}
