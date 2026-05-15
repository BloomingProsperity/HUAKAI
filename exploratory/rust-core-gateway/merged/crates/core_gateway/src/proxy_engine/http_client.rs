use std::time::Duration;

use axum::body::Body;
use hyper_rustls::{HttpsConnector, HttpsConnectorBuilder};
use hyper_util::{
    client::legacy::{Client, connect::HttpConnector},
    rt::TokioExecutor,
};

pub type GatewayHttpConnector = HttpsConnector<HttpConnector>;
pub type GatewayHttpClient = Client<GatewayHttpConnector, Body>;

pub fn build_http_client() -> GatewayHttpClient {
    let https = HttpsConnectorBuilder::new()
        .with_webpki_roots()
        .https_or_http()
        .enable_http1()
        .enable_http2()
        .build();

    let mut builder = Client::builder(TokioExecutor::new());
    builder.pool_idle_timeout(Duration::from_secs(90));
    builder.pool_max_idle_per_host(128);
    builder.build(https)
}
