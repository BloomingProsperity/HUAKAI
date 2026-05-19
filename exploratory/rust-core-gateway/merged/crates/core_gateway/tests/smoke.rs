// M-rust-1 烟雾测试 (merged lane)
// 覆盖: config 解析 / health endpoint 200 / request_id 唯一性 / error_class

use std::collections::HashSet;

use axum::{body, body::Body, http::Request};
use core_gateway::{build_router, config::StartupConfig, request_id::RequestId};
use serde_json::Value;
use tower::ServiceExt;
use uuid::Uuid;

// ── 辅助函数 ────────────────────────────────────────────────────────────────

fn env_pairs() -> Vec<(String, String)> {
    [
        ("HUAKAI_LISTEN_ADDR", "127.0.0.1:0"),
        ("HUAKAI_CONTROL_PLANE_ENDPOINT", "http://127.0.0.1:48080"),
        ("HUAKAI_TRANSPORT_BASELINE", "http"),
        ("HUAKAI_LOG_LEVEL", "debug"),
        ("HUAKAI_JSON_LOGS", "true"),
        ("HUAKAI_WORKER_THREADS", "2"),
    ]
    .into_iter()
    .map(|(k, v)| (k.to_owned(), v.to_owned()))
    .collect()
}

// ── 1. config 解析测试 ───────────────────────────────────────────────────────

#[test]
fn config_parse_uses_required_env_fields() {
    let config = StartupConfig::from_env_iter(env_pairs()).expect("config 解析应成功");

    assert_eq!(config.listen_addr.to_string(), "127.0.0.1:0");
    assert_eq!(
        config.control_plane_endpoint.to_string(),
        "http://127.0.0.1:48080/"
    );
    assert_eq!(config.worker_threads, 2);
    assert!(config.json_logs);
    assert!(config.otlp_endpoint.is_none());
}

#[test]
fn config_validate_rejects_empty_fields() {
    // worker_threads=0 应触发 validate 错误 (from_raw 也会拒绝)
    let mut env = env_pairs();
    env.retain(|(k, _)| k != "HUAKAI_WORKER_THREADS");
    env.push(("HUAKAI_WORKER_THREADS".to_owned(), "0".to_owned()));
    let result = StartupConfig::from_env_iter(env);
    assert!(result.is_err(), "worker_threads=0 应解析失败");
}

#[test]
fn config_with_otlp_endpoint_accepted() {
    let mut env = env_pairs();
    env.push((
        "HUAKAI_OTLP_ENDPOINT".to_owned(),
        "http://127.0.0.1:4317".to_owned(),
    ));
    let config = StartupConfig::from_env_iter(env).expect("带 otlp_endpoint 的 config 应解析成功");
    assert!(config.otlp_endpoint.is_some());
}

// ── 2. health endpoint 集成测试 (tower oneshot, 无真实 TCP) ─────────────────

#[tokio::test]
async fn health_endpoint_returns_200() {
    let config = StartupConfig::from_env_iter(env_pairs()).expect("config 解析应成功");
    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .uri("/healthz")
                .body(Body::empty())
                .expect("request 构建应成功"),
        )
        .await
        .expect("router 应处理请求");

    assert_eq!(response.status(), 200);

    let bytes = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("body 读取应成功");
    let payload: Value = serde_json::from_slice(&bytes).expect("health json 应可解析");

    assert_eq!(payload["status"], "ok");
}

#[tokio::test]
async fn health_endpoint_content_type_is_json() {
    let config = StartupConfig::from_env_iter(env_pairs()).expect("config 解析应成功");
    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .uri("/healthz")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();

    let ct = response
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("");
    assert!(
        ct.contains("application/json"),
        "content-type 应含 application/json, 实际: {ct}"
    );
}

#[tokio::test]
async fn unknown_path_returns_404() {
    let config = StartupConfig::from_env_iter(env_pairs()).expect("config 解析应成功");
    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .uri("/not_found")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();

    assert_eq!(response.status(), 404);
}

// ── 3. request_id 测试 ───────────────────────────────────────────────────────

#[test]
fn request_id_generation_is_unique_and_uuid_v7() {
    let mut seen = HashSet::with_capacity(4096);

    for _ in 0..4096 {
        let rid = RequestId::generate();
        let uuid = Uuid::parse_str(rid.as_str()).expect("生成的 UUID 应可解析");
        assert_eq!(uuid.get_version_num(), 7);
        assert!(
            seen.insert(rid.as_str().to_owned()),
            "request_id 应全局唯一"
        );
    }
}

#[test]
fn request_id_propagates_valid_candidate() {
    let rid = RequestId::from_candidate(Some("client-request-42"));
    assert_eq!(rid.as_str(), "client-request-42");
}

#[test]
fn request_id_generates_on_empty_candidate() {
    let rid = RequestId::from_candidate(Some(""));
    let uuid = Uuid::parse_str(rid.as_str()).expect("fallback 应为合法 UUID");
    assert_eq!(uuid.get_version_num(), 7);
}

#[test]
fn request_id_rejects_overlong_candidate() {
    let long = "x".repeat(129);
    let rid = RequestId::from_candidate(Some(&long));
    assert!(
        Uuid::parse_str(rid.as_str()).is_ok(),
        "超长 candidate 应触发 generate"
    );
}

// ── 4. error_class 测试 ──────────────────────────────────────────────────────

#[test]
fn error_class_labels_match_spec() {
    use core_gateway::error::GatewayError;

    assert_eq!(GatewayError::Config("x".into()).error_class(), "config");
    assert_eq!(
        GatewayError::Network(std::io::Error::other("x")).error_class(),
        "network_error"
    );
    assert_eq!(
        GatewayError::Upstream("x".into()).error_class(),
        "upstream_error"
    );
    assert_eq!(
        GatewayError::ControlPlane("x".into()).error_class(),
        "control_plane_error"
    );
    assert_eq!(
        GatewayError::Stream("x".into()).error_class(),
        "protocol_error"
    );
    assert_eq!(
        GatewayError::Internal("x".into()).error_class(),
        "internal_error"
    );
}

// ── 5. TCP 端到端烟雾测试 ────────────────────────────────────────────────────

#[tokio::test]
async fn health_endpoint_tcp_e2e() {
    use core_gateway::run;

    // 用端口 0 让 OS 分配可用端口
    let mut env = env_pairs();
    env.retain(|(k, _)| k != "HUAKAI_LISTEN_ADDR");
    env.push(("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()));

    // 手动 bind 获取真实端口
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("bind 应成功");
    let addr = listener.local_addr().expect("获取本地地址应成功");
    drop(listener); // 释放端口后立即用相同端口重新 bind (可能有竞争, 仅作演示)

    // 用实际端口构建 config
    let mut env2 = env_pairs();
    env2.retain(|(k, _)| k != "HUAKAI_LISTEN_ADDR");
    env2.push(("HUAKAI_LISTEN_ADDR".to_owned(), addr.to_string()));
    let config = StartupConfig::from_env_iter(env2).expect("config 解析应成功");

    // 在后台任务中启动 server
    let server = tokio::spawn(async move {
        run(config).await.ok();
    });

    // 等待 server 就绪
    tokio::time::sleep(std::time::Duration::from_millis(100)).await;

    let client = reqwest::Client::new();
    let resp = client
        .get(format!("http://{addr}/healthz"))
        .send()
        .await
        .expect("HTTP 请求应成功");

    assert_eq!(resp.status(), 200);
    let body: Value = resp.json().await.expect("响应应为合法 JSON");
    assert_eq!(body["status"], "ok");

    server.abort();
}
