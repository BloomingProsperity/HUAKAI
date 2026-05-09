use std::collections::HashSet;

use axum::{body, body::Body, http::Request};
use core_gateway::{build_router, config::StartupConfig, request_id::RequestId};
use serde_json::Value;
use tower::ServiceExt;
use uuid::Uuid;

#[test]
fn config_parse_uses_required_env_fields() {
    let config = StartupConfig::from_env_iter(env_pairs()).expect("config parses");

    assert_eq!(config.listen_addr.to_string(), "127.0.0.1:0");
    assert_eq!(
        config.control_plane_endpoint.to_string(),
        "http://127.0.0.1:48080/"
    );
    assert_eq!(
        config.tracing_endpoint.to_string(),
        "http://127.0.0.1:4317/"
    );
    assert_eq!(config.worker_threads, 2);
    assert!(config.json_logs);
}

#[tokio::test]
async fn health_endpoint_returns_200() {
    let config = StartupConfig::from_env_iter(env_pairs()).expect("config parses");
    let response = build_router(config)
        .oneshot(
            Request::builder()
                .uri("/healthz")
                .body(Body::empty())
                .expect("request builds"),
        )
        .await
        .expect("router handles request");

    assert_eq!(response.status(), 200);

    let bytes = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("body reads");
    let payload: Value = serde_json::from_slice(&bytes).expect("health json parses");

    assert_eq!(payload["status"], "ok");
}

#[test]
fn request_id_generation_is_unique_and_uuid_v7() {
    let mut seen = HashSet::with_capacity(4096);

    for _ in 0..4096 {
        let request_id = RequestId::generate();
        let uuid = Uuid::parse_str(request_id.as_str()).expect("generated uuid parses");

        assert_eq!(uuid.get_version_num(), 7);
        assert!(seen.insert(request_id.as_str().to_owned()));
    }

    let propagated = RequestId::from_candidate(Some("client-request-42"));
    assert_eq!(propagated.as_str(), "client-request-42");
}

fn env_pairs() -> Vec<(String, String)> {
    [
        ("HUAKAI_LISTEN_ADDR", "127.0.0.1:0"),
        ("HUAKAI_CONTROL_PLANE_ENDPOINT", "http://127.0.0.1:48080"),
        ("HUAKAI_LOG_LEVEL", "debug"),
        ("HUAKAI_TRACING_ENDPOINT", "http://127.0.0.1:4317"),
        ("HUAKAI_JSON_LOGS", "true"),
        ("HUAKAI_WORKER_THREADS", "2"),
    ]
    .into_iter()
    .map(|(key, value)| (key.to_owned(), value.to_owned()))
    .collect()
}
