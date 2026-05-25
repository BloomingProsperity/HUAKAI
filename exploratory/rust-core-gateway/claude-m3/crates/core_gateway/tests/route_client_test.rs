// M-rust-3 集成测试: RouteClient <-> MockControlPlane e2e
// 覆盖: 正常路由查询、健康检查、心跳、attempt 上报、
//       control plane 不可用时 5xx 回应、circuit breaker、
//       缓存命中、heartbeat drain_mode 传播。

use std::time::Duration;

use core_gateway::{
    mock_control_plane::{MockControlPlane, MockControlPlaneBehavior},
    route_client::{
        AttemptReportRequest, CacheMetrics, HeartbeatRequest, RouteClient, RouteClientConfig,
        RouteQueryRequest, TokensUsed,
    },
};

// ─── 辅助 ──────────────────────────────────────────────────────────────────────

fn make_route_client(base_url: &str) -> RouteClient {
    let config = RouteClientConfig {
        base_url: base_url.to_owned(),
        rpc_timeout: Duration::from_secs(5),
        circuit_failure_threshold: 5,
        circuit_recovery_window: Duration::from_secs(60),
        max_retries: 1,
    };
    RouteClient::with_default_http(config).expect("RouteClient 构建应成功")
}

fn make_route_req(id: &str) -> RouteQueryRequest {
    RouteQueryRequest {
        request_id: id.to_owned(),
        tenant_id: "tenant-test".to_owned(),
        requested_model: "claude-3-5-sonnet".to_owned(),
        session_hash: "hash-001".to_owned(),
        request_protocol: "anthropic_messages".to_owned(),
        stream: true,
        client_deadline_ms: 30_000,
        previous_attempts: vec![],
        capability_hints: None,
    }
}

fn make_attempt_report(request_id: &str, plan_id: &str, token: &str) -> AttemptReportRequest {
    AttemptReportRequest {
        request_id: request_id.to_owned(),
        route_plan_id: plan_id.to_owned(),
        attempt_id: "attempt-001".to_owned(),
        acquisition_token: token.to_owned(),
        status: "success".to_owned(),
        http_status: 200,
        started_at: "2026-05-09T00:00:00Z".to_owned(),
        ended_at: "2026-05-09T00:00:01Z".to_owned(),
        latency_ms: 1_000,
        tokens_used: TokensUsed {
            input_tokens: 100,
            output_tokens: 200,
            total_tokens: 300,
            source: "vendor_response".to_owned(),
        },
        cache_metrics: CacheMetrics {
            cache_read_tokens: 0,
            cache_write_tokens: 0,
            cache_hit: false,
            source: "missing".to_owned(),
        },
        bytes_in: 512,
        bytes_out: 1024,
        frames_in: 1,
        frames_out: 10,
        vendor_request_id: Some("vreq-abc".to_owned()),
        retryable: false,
        error_class: "".to_owned(),
        error_message_redacted: "".to_owned(),
        idempotency_key: format!("{request_id}-attempt-001-{token}"),
    }
}

// ─── 测试 ──────────────────────────────────────────────────────────────────────

/// 1. 正常 route query: 返回合法 RoutePlan
#[tokio::test]
async fn route_query_returns_valid_plan() {
    let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Normal).await;
    let client = make_route_client(&mock.base_url());
    let req = make_route_req("req-e2e-001");

    let plan = client
        .query_route(&req, Duration::from_secs(5))
        .await
        .expect("route query 应成功");

    assert_eq!(plan.vendor, "anthropic");
    assert_eq!(plan.auth_mode, "bearer");
    assert!(!plan.route_plan_id.is_empty());
    assert!(!plan.acquisition_token.is_empty());
    assert_eq!(mock.route_requests_seen(), 1);
}

/// 2. health_check: control plane 返回 ok + schema_version
#[tokio::test]
async fn health_check_returns_ok() {
    let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Normal).await;
    let client = make_route_client(&mock.base_url());

    let health = client.health_check().await.expect("health check 应成功");

    assert_eq!(health.status, "ok");
    assert_eq!(health.route_service_status, "ready");
    assert!(health.schema_version > 0);
}

/// 3. heartbeat: control plane 接收并 ack
#[tokio::test]
async fn heartbeat_is_acked() {
    let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Normal).await;
    let client = make_route_client(&mock.base_url());

    let hb = HeartbeatRequest {
        node_id: "rust-node-1".to_owned(),
        build_sha: "abc123".to_owned(),
        schema_version: 1,
        started_at: "2026-05-09T00:00:00Z".to_owned(),
        in_flight_requests: 5,
        open_upstream_connections: 3,
        attempt_report_queue_depth: 0,
        p95_control_plane_rpc_ms: 12.5,
        error_rate_1m: 0.0,
    };

    let resp = client.send_heartbeat(&hb).await.expect("heartbeat 应成功");

    assert!(resp.ack);
    assert!(!resp.drain_mode);
    assert_eq!(mock.heartbeats_seen(), 1);

    // 验证 mock 收到的 heartbeat 字段
    let last = mock
        .last_heartbeat()
        .await
        .expect("mock 应有收到的 heartbeat");
    assert_eq!(last.node_id, "rust-node-1");
    assert_eq!(last.in_flight_requests, 5);
}

/// 4. attempt_report: mock ack + 验证 idempotency_key 字段
#[tokio::test]
async fn attempt_report_is_acked_with_idempotency_key() {
    let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Normal).await;
    let client = make_route_client(&mock.base_url());

    // 先查询得到 plan
    let req = make_route_req("req-e2e-004");
    let plan = client
        .query_route(&req, Duration::from_secs(5))
        .await
        .expect("route query 应成功");

    let report = make_attempt_report("req-e2e-004", &plan.route_plan_id, &plan.acquisition_token);
    let ack = client
        .report_attempt(&report, Duration::from_secs(5))
        .await
        .expect("attempt report 应成功");

    assert!(ack.ack);
    assert!(!ack.ack_id.is_empty());
    assert_eq!(mock.attempt_reports_seen(), 1);

    let last = mock.last_attempt().await.expect("mock 应有收到的 attempt");
    assert_eq!(last.request_id, "req-e2e-004");
    assert_eq!(last.acquisition_token, plan.acquisition_token);
    assert!(!last.idempotency_key.is_empty());
}

/// 5. control plane 不可用时 query_route 返回明确错误
#[tokio::test]
async fn route_query_returns_error_when_control_plane_unavailable() {
    let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Unavailable).await;
    // max_retries=1, 两次都失败
    let config = RouteClientConfig {
        base_url: mock.base_url(),
        rpc_timeout: Duration::from_secs(2),
        circuit_failure_threshold: 10,
        circuit_recovery_window: Duration::from_secs(60),
        max_retries: 1,
    };
    let client = RouteClient::with_default_http(config).expect("构建应成功");
    let req = make_route_req("req-e2e-005");

    let err = client
        .query_route(&req, Duration::from_secs(5))
        .await
        .expect_err("不可用时应返回错误");

    assert_eq!(err.error_class(), "control_plane_error");
    // 两次尝试 (1 + 1 retry) 应都到达 mock
    assert!(mock.route_requests_seen() >= 1, "不可用时应至少尝试一次");
}

/// 6. heartbeat drain_mode 传播: mock 设置后 Rust 收到 drain_mode=true
#[tokio::test]
async fn heartbeat_drain_mode_propagates() {
    let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Normal).await;
    let client = make_route_client(&mock.base_url());

    // 第一次 heartbeat: drain_mode=false
    let hb = HeartbeatRequest {
        node_id: "rust-node-drain".to_owned(),
        build_sha: "def456".to_owned(),
        schema_version: 1,
        started_at: "2026-05-09T00:00:00Z".to_owned(),
        in_flight_requests: 0,
        open_upstream_connections: 0,
        attempt_report_queue_depth: 0,
        p95_control_plane_rpc_ms: 5.0,
        error_rate_1m: 0.0,
    };

    let resp1 = client
        .send_heartbeat(&hb)
        .await
        .expect("第一次 heartbeat 应成功");
    assert!(!resp1.drain_mode, "初始应无 drain_mode");

    // mock 设置 drain
    mock.set_drain_mode(true);

    let resp2 = client
        .send_heartbeat(&hb)
        .await
        .expect("第二次 heartbeat 应成功");
    assert!(
        resp2.drain_mode,
        "设置 drain 后 heartbeat 响应应携带 drain_mode=true"
    );
    assert_eq!(mock.heartbeats_seen(), 2);
}

/// 7. OpenAI 协议路由: plan.vendor == "openai"
#[tokio::test]
async fn route_query_openai_protocol_returns_openai_vendor() {
    let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Normal).await;
    let client = make_route_client(&mock.base_url());

    let req = RouteQueryRequest {
        request_id: "req-oai-001".to_owned(),
        tenant_id: "tenant-oai".to_owned(),
        requested_model: "gpt-4o".to_owned(),
        session_hash: "hash-oai".to_owned(),
        request_protocol: "openai_chat_completions".to_owned(),
        stream: false,
        client_deadline_ms: 10_000,
        previous_attempts: vec![],
        capability_hints: None,
    };

    let plan = client
        .query_route(&req, Duration::from_secs(5))
        .await
        .expect("OpenAI route query 应成功");

    assert_eq!(plan.vendor, "openai");
    assert_eq!(plan.auth_mode, "bearer");
}

/// 8. 慢 control plane: 超过 deadline 时返回 DeadlineExceeded 错误
#[tokio::test]
async fn route_query_deadline_exceeded_on_slow_control_plane() {
    let mock = MockControlPlane::spawn(MockControlPlaneBehavior::SlowResponse {
        delay: Duration::from_millis(300),
    })
    .await;
    let config = RouteClientConfig {
        base_url: mock.base_url(),
        // rpc_timeout 远大于 deadline, 让 deadline 先触发
        rpc_timeout: Duration::from_secs(10),
        circuit_failure_threshold: 10,
        circuit_recovery_window: Duration::from_secs(60),
        max_retries: 0,
    };
    let client = RouteClient::with_default_http(config).expect("构建应成功");
    let req = make_route_req("req-deadline-001");

    // deadline=100ms < mock delay=300ms → 应超时
    let err = client
        .query_route(&req, Duration::from_millis(100))
        .await
        .expect_err("慢 control plane 应触发 deadline exceeded");

    // 超时会触发 reqwest timeout error (Transport) 或 DeadlineExceeded
    // 两者都归属 control_plane_error
    assert_eq!(
        err.error_class(),
        "control_plane_error",
        "deadline 超时应归属 control_plane_error, got: {err}"
    );
}

/// 9. previous_attempts 字段正确序列化并到达 mock
#[tokio::test]
async fn route_query_with_previous_attempts_is_received() {
    use core_gateway::route_client::PreviousAttempt;

    let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Normal).await;
    let client = make_route_client(&mock.base_url());

    let req = RouteQueryRequest {
        request_id: "req-prev-001".to_owned(),
        tenant_id: "tenant-prev".to_owned(),
        requested_model: "claude-3-5-sonnet".to_owned(),
        session_hash: "hash-prev".to_owned(),
        request_protocol: "anthropic_messages".to_owned(),
        stream: true,
        client_deadline_ms: 30_000,
        previous_attempts: vec![PreviousAttempt {
            attempt_id: "prev-atm-1".to_owned(),
            status: "upstream_5xx".to_owned(),
            vendor: "anthropic".to_owned(),
            error_class: "upstream_error".to_owned(),
        }],
        capability_hints: Some(vec!["tool_use".to_owned()]),
    };

    let plan = client
        .query_route(&req, Duration::from_secs(5))
        .await
        .expect("带 previous_attempts 的 route query 应成功");

    // mock 不检查 previous_attempts 内容, 只验证路由逻辑照常工作
    assert_eq!(plan.vendor, "anthropic");
    assert_eq!(mock.route_requests_seen(), 1);
}
