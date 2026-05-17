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
use crate::mimicry::{BuiltinProfile, FingerprintProfile, load_builtin_profile};

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
    BoringTlsConnector::new(Arc::new(profile))
}

#[cfg(feature = "mimicry-boring")]
pub fn build_http_client_with_profile(profile: Arc<FingerprintProfile>) -> GatewayHttpClient {
    build_http_client_with_connector(BoringTlsConnector::new(profile))
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
}
