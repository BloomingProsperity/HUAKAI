// M-rust-9 observability 集成测试
// 覆盖: /metrics endpoint, heartbeat drain_mode 切换, redaction log 守门

use axum::{body, body::Body, http::Request};
use core_gateway::{
    build_router,
    config::StartupConfig,
    heartbeat::{is_drain_mode, set_drain_mode},
    metrics::{encode_metrics, registry},
    mock_control_plane::{MockControlPlane, mock_route_plan},
    redaction::{is_sensitive_header, redact_header_value},
    route_client::{RouteClient, RouteClientOptions},
    route_proto::v1::HeartbeatRequest,
};
use tower::ServiceExt;

// ─── 辅助函数 ────────────────────────────────────────────────────────────────

fn env_pairs_with_cp(cp_endpoint: &str) -> Vec<(String, String)> {
    [
        ("HUAKAI_LISTEN_ADDR", "127.0.0.1:0"),
        ("HUAKAI_CONTROL_PLANE_ENDPOINT", cp_endpoint),
        ("HUAKAI_TRANSPORT_BASELINE", "http"),
        ("HUAKAI_LOG_LEVEL", "debug"),
        ("HUAKAI_JSON_LOGS", "false"),
        ("HUAKAI_WORKER_THREADS", "2"),
    ]
    .into_iter()
    .map(|(k, v)| (k.to_owned(), v.to_owned()))
    .collect()
}

fn env_pairs() -> Vec<(String, String)> {
    env_pairs_with_cp("http://127.0.0.1:48080")
}

// ─── 测试 1: /metrics endpoint 返回 Prometheus 文本格式 ──────────────────────

#[tokio::test]
async fn metrics_endpoint_returns_prometheus_text_format() {
    let config = StartupConfig::from_env_iter(env_pairs()).expect("config 解析应成功");
    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .uri("/metrics")
                .body(Body::empty())
                .expect("request 构建应成功"),
        )
        .await
        .expect("router 应处理请求");

    assert_eq!(response.status(), 200, "/metrics 应返回 200");

    let ct = response
        .headers()
        .get("content-type")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("");
    assert!(
        ct.contains("text/plain"),
        "content-type 应含 text/plain, 实际: {ct}"
    );

    let bytes = body::to_bytes(response.into_body(), 64 * 1024)
        .await
        .expect("body 读取应成功");
    let body_str = std::str::from_utf8(&bytes).expect("body 应为 UTF-8");

    assert!(
        body_str.contains("huakai_rust_"),
        "/metrics 响应应含 huakai_rust_ metric 名称"
    );
}

// ─── 测试 2: /metrics 包含所有必需 metric 名称 ───────────────────────────────

#[tokio::test]
async fn metrics_endpoint_contains_all_required_metric_names() {
    let config = StartupConfig::from_env_iter(env_pairs()).expect("config 解析应成功");
    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .uri("/metrics")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();

    let _bytes = body::to_bytes(response.into_body(), 64 * 1024)
        .await
        .unwrap();

    // CounterVec 在无 label 观测时不输出数据行, 但 HELP 行始终存在 (通过 encode_metrics 中 gather)
    // 用 HELP 行检测确保所有 metric 名称都已注册
    let required_metrics = [
        "huakai_rust_rpc_latency_ms",
        "huakai_rust_route_query_latency_ms",
        "huakai_rust_stream_frames_in_total",
        "huakai_rust_stream_frames_out_total",
        "huakai_rust_cancel_total",
        "huakai_rust_upstream_error_total",
        "huakai_rust_active_connections",
        "huakai_rust_queue_depth",
        "huakai_rust_open_upstream_connections",
    ];

    // 先触发 upstream_error_total 有一个 label 观测, 让其出现在数据行
    core_gateway::metrics::registry();
    core_gateway::metrics::upstream_error_total()
        .with_label_values(&["test"])
        .inc();

    // 重新请求 /metrics 确保数据行出现
    let config2 = StartupConfig::from_env_iter(env_pairs()).expect("config 解析应成功");
    let response2 = build_router(config2)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .uri("/metrics")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    let bytes2 = body::to_bytes(response2.into_body(), 64 * 1024)
        .await
        .unwrap();
    let body_str2 = std::str::from_utf8(&bytes2).unwrap();

    for name in required_metrics {
        assert!(
            body_str2.contains(name),
            "/metrics 应含 {name}, 实际输出前512字节:\n{}",
            &body_str2[..body_str2.len().min(512)]
        );
    }
}

// ─── 测试 3: heartbeat drain_mode 触发与恢复 ─────────────────────────────────

#[tokio::test]
async fn heartbeat_drain_mode_toggle_via_mock_cp() {
    // 启动 mock control plane
    let plan = mock_route_plan("http://127.0.0.1:1");
    let mock_cp = MockControlPlane::spawn(plan).await;

    // 初始: drain_mode=false
    mock_cp.set_drain_mode(false);

    let route_client = RouteClient::new(
        mock_cp.endpoint().parse().unwrap(),
        RouteClientOptions {
            rpc_timeout: std::time::Duration::from_millis(500),
            ..RouteClientOptions::default()
        },
    )
    .expect("route client 创建应成功");

    // 手动发送心跳, 确认 drain_mode=false
    let resp = route_client
        .heartbeat(HeartbeatRequest {
            node_id: "test-gw".to_owned(),
            schema_version: "route.v1".to_owned(),
            ..Default::default()
        })
        .await
        .expect("heartbeat 应成功");
    assert!(!resp.drain_mode, "初始 drain_mode 应为 false");

    // 切换 mock CP 到 drain_mode=true
    mock_cp.set_drain_mode(true);
    let resp2 = route_client
        .heartbeat(HeartbeatRequest {
            node_id: "test-gw".to_owned(),
            schema_version: "route.v1".to_owned(),
            ..Default::default()
        })
        .await
        .expect("heartbeat 应成功");
    assert!(resp2.drain_mode, "mock CP 切换后 drain_mode 应为 true");

    // 恢复
    mock_cp.set_drain_mode(false);
    let resp3 = route_client
        .heartbeat(HeartbeatRequest {
            node_id: "test-gw".to_owned(),
            schema_version: "route.v1".to_owned(),
            ..Default::default()
        })
        .await
        .expect("heartbeat 应成功");
    assert!(!resp3.drain_mode, "恢复后 drain_mode 应为 false");
}

// ─── 测试 4: heartbeat mock CP 计数验证 ──────────────────────────────────────

#[tokio::test]
async fn heartbeat_worker_sends_to_mock_cp() {
    let plan = mock_route_plan("http://127.0.0.1:1");
    let mock_cp = MockControlPlane::spawn(plan).await;

    let route_client = RouteClient::new(
        mock_cp.endpoint().parse().unwrap(),
        RouteClientOptions {
            rpc_timeout: std::time::Duration::from_millis(500),
            ..RouteClientOptions::default()
        },
    )
    .expect("route client 创建应成功");

    // 启动 heartbeat worker (5s 间隔, 但首次立即触发)
    let worker = core_gateway::heartbeat::HeartbeatWorker::spawn(route_client);

    // 等待至少一次心跳触发
    tokio::time::sleep(std::time::Duration::from_millis(200)).await;

    // worker 应至少发出一次心跳
    assert!(
        mock_cp.heartbeats_seen() >= 1,
        "HeartbeatWorker 应至少发出 1 次心跳, 实际: {}",
        mock_cp.heartbeats_seen()
    );

    worker.abort();
}

// ─── 测试 5: drain_mode 全局原子标志读写 ─────────────────────────────────────

#[test]
fn drain_mode_global_atomic_toggle() {
    // 确保从 false 开始
    set_drain_mode(false);
    assert!(!is_drain_mode());

    set_drain_mode(true);
    assert!(is_drain_mode());

    set_drain_mode(false);
    assert!(!is_drain_mode());
}

// ─── 测试 6: redaction log 不含 Authorization 值 ─────────────────────────────

#[test]
fn redaction_log_does_not_expose_authorization() {
    let secret = "Bearer sk-super-secret-key-12345";

    // 脱敏后的值不应含真实 token
    let redacted = redact_header_value("Authorization", secret);
    assert_ne!(redacted, secret, "脱敏后不应与原值相同");
    assert_eq!(redacted, "[REDACTED]");
    assert!(!redacted.contains("sk-super-secret-key-12345"));
}

// ─── 测试 7: redaction 对 X-Api-Key 生效 ─────────────────────────────────────

#[test]
fn redaction_covers_x_api_key() {
    assert!(is_sensitive_header("X-Api-Key"));
    let redacted = redact_header_value("x-api-key", "my-secret-api-key");
    assert_eq!(redacted, "[REDACTED]");
}

// ─── 测试 8: encode_metrics 线程安全 (并发调用不崩溃) ────────────────────────

#[tokio::test]
async fn encode_metrics_is_thread_safe() {
    let _ = registry();
    let handles: Vec<_> = (0..8)
        .map(|_| tokio::spawn(async { encode_metrics() }))
        .collect();
    for h in handles {
        let output = h.await.expect("task 应成功");
        assert!(
            output.contains("huakai_rust_"),
            "并发 encode 应含 metric 名称"
        );
    }
}
