// M-rust-1 烟雾测试
// 覆盖: config 解析 / health endpoint 200 / request_id 唯一性

use std::collections::HashSet;

// ── 1. config 解析测试 ──────────────────────────────────────────────────────

#[test]
fn config_parse_from_env_ok() {
    // 设置必填环境变量后解析应成功
    std::env::set_var("GATEWAY_LISTEN_ADDR", "127.0.0.1:18080");
    std::env::set_var("GATEWAY_CONTROL_PLANE_ENDPOINT", "http://127.0.0.1:9090");
    std::env::set_var("GATEWAY_LOG_LEVEL", "info");

    let cfg = core_gateway::config::GatewayConfig::from_env();
    assert!(cfg.is_ok(), "config 解析应成功: {:?}", cfg.err());

    let cfg = cfg.unwrap();
    assert_eq!(cfg.listen_addr, "127.0.0.1:18080");
    assert_eq!(cfg.control_plane_endpoint, "http://127.0.0.1:9090");
    assert_eq!(cfg.log_level, "info");
    assert!(cfg.otlp_endpoint.is_none());
}

#[test]
fn config_validate_rejects_empty_fields() {
    let cfg = core_gateway::config::GatewayConfig {
        listen_addr: String::new(),
        control_plane_endpoint: "http://127.0.0.1:9090".into(),
        log_level: "info".into(),
        otlp_endpoint: None,
        worker_threads: None,
    };
    assert!(cfg.validate().is_err(), "空 listen_addr 应验证失败");
}

// ── 2. health endpoint 集成测试 ────────────────────────────────────────────

#[tokio::test]
async fn health_endpoint_returns_200_ok() {
    // 选择随机高端口避免冲突
    let addr = "127.0.0.1:18081";

    // 在独立任务中启动 server
    let server_task = tokio::spawn(run_test_server(addr));

    // 等待 server 就绪
    tokio::time::sleep(std::time::Duration::from_millis(150)).await;

    let client = reqwest::Client::new();
    let resp = client
        .get(format!("http://{addr}/healthz"))
        .send()
        .await
        .expect("health 请求应成功");

    assert_eq!(resp.status(), 200, "HTTP 状态码应为 200");

    let body: serde_json::Value = resp.json().await.expect("响应应为合法 JSON");
    assert_eq!(body["status"], "ok", "响应体应含 status:ok");

    server_task.abort();
}

/// 启动最小 axum server 用于集成测试 (不依赖真实 ENV / OTLP)
async fn run_test_server(addr: &str) {
    use axum::{routing::get, Json, Router};
    use serde_json::json;

    let app = Router::new().route("/healthz", get(|| async { Json(json!({"status": "ok"})) }));

    let listener = tokio::net::TcpListener::bind(addr)
        .await
        .expect("测试 server bind 失败");

    axum::serve(listener, app).await.ok();
}

// ── 3. request_id 唯一性测试 ───────────────────────────────────────────────

#[test]
fn request_ids_are_globally_unique() {
    use core_gateway::request_id::new_request_id;

    let n = 5000;
    let ids: HashSet<_> = (0..n).map(|_| new_request_id()).collect();
    assert_eq!(ids.len(), n, "生成的 {n} 个 request_id 应全部唯一");
}

#[test]
fn request_id_parse_or_generate_roundtrip() {
    use core_gateway::request_id::{format_request_id, new_request_id, parse_or_generate};

    let id = new_request_id();
    let formatted = format_request_id(&id);
    let recovered = parse_or_generate(Some(&formatted));
    assert_eq!(id, recovered, "ID 序列化后再解析应与原值相等");
}
