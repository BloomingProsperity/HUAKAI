use std::time::Duration;

use axum::body::Body;
use hyper_rustls::HttpsConnector;
use hyper_util::{
    client::legacy::{Client, connect::HttpConnector},
    rt::TokioExecutor,
};

pub type GatewayHttpConnector = HttpsConnector<HttpConnector>;
pub type GatewayHttpClient = Client<GatewayHttpConnector, Body>;

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

pub fn build_http_client() -> GatewayHttpClient {
    let mut builder = Client::builder(TokioExecutor::new());
    builder.pool_idle_timeout(Duration::from_secs(90));
    builder.pool_max_idle_per_host(128);
    builder.build(build_gateway_connector())
}

#[cfg(test)]
mod tests {
    use super::*;

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
}
