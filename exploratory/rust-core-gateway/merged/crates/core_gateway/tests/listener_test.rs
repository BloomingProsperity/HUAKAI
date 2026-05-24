// M-rust-2 listener 集成测试
// 覆盖: echo、body limit、client cancel、slow client、SSE pass-through、request_id 透传。
//
// **2026-05-24 HybridStream Option b 后已不再 cfg-gate**: 早期 commit 试图用
// `#[cfg(not(feature = "mimicry-boring"))]` gate 全部 mock-upstream 测试 (避免 BoringTlsConnector
// 按 https-only 拒 http://127.0.0.1 mock), 但那是 whack-a-mole 维护负担。本提交把
// BoringTlsConnector 升级成 HybridStream (TLS for https, plain TCP for http), 这些
// 测试在 mimicry-boring feature 下 也能正常跑, gate 已全部撤除, cfg_attr 临时
// allow 也一并删。

mod common;

use std::{
    net::SocketAddr,
    sync::{Mutex, MutexGuard},
    time::Duration,
};

use axum::{
    body::{self, Body},
    http::{Request, StatusCode, header},
};
use bytes::Bytes;
use common::mock_upstream::{MockBehavior, MockUpstream};
use core_gateway::{
    GatewayState, build_router,
    config::StartupConfig,
    heartbeat::set_drain_mode,
    listener as core_listener,
    request_id::REQUEST_ID_HEADER,
    server_runtime::{self, ServerTimeouts},
};
use futures_util::StreamExt;
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
    sync::oneshot,
    task::JoinHandle,
};
use tower::ServiceExt;

static DRAIN_TEST_LOCK: Mutex<()> = Mutex::new(());

struct DrainModeReset {
    _guard: MutexGuard<'static, ()>,
}

impl DrainModeReset {
    fn set(drain: bool) -> Self {
        let guard = DRAIN_TEST_LOCK.lock().expect("drain test lock poisoned");
        set_drain_mode(drain);
        Self { _guard: guard }
    }
}

impl Drop for DrainModeReset {
    fn drop(&mut self) {
        set_drain_mode(false);
    }
}

fn test_config(max_body_bytes: usize, mock_upstream_endpoint: Option<String>) -> StartupConfig {
    test_config_with_resource_limits_and_timeouts(
        max_body_bytes,
        mock_upstream_endpoint,
        0,
        0,
        1,
        TestTimeouts::default(),
    )
}

fn test_config_with_resource_limits(
    max_body_bytes: usize,
    mock_upstream_endpoint: Option<String>,
    max_in_flight_requests: usize,
    max_connections: usize,
    overload_retry_after_secs: u64,
) -> StartupConfig {
    test_config_with_resource_limits_and_timeouts(
        max_body_bytes,
        mock_upstream_endpoint,
        max_in_flight_requests,
        max_connections,
        overload_retry_after_secs,
        TestTimeouts::default(),
    )
}

#[derive(Clone, Copy)]
struct TestTimeouts {
    upstream_body_idle_timeout_ms: u64,
    downstream_write_idle_timeout_ms: u64,
    request_body_idle_timeout_ms: u64,
    server_header_read_timeout_ms: u64,
}

impl Default for TestTimeouts {
    fn default() -> Self {
        Self {
            upstream_body_idle_timeout_ms: 300_000,
            downstream_write_idle_timeout_ms: 60_000,
            request_body_idle_timeout_ms: 30_000,
            server_header_read_timeout_ms: 30_000,
        }
    }
}

fn test_config_with_resource_limits_and_timeouts(
    max_body_bytes: usize,
    mock_upstream_endpoint: Option<String>,
    max_in_flight_requests: usize,
    max_connections: usize,
    overload_retry_after_secs: u64,
    timeouts: TestTimeouts,
) -> StartupConfig {
    let mut env = vec![
        ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
        (
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            "http://127.0.0.1:48080".to_owned(),
        ),
        ("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()),
        ("HUAKAI_LOG_LEVEL".to_owned(), "debug".to_owned()),
        ("HUAKAI_JSON_LOGS".to_owned(), "true".to_owned()),
        ("HUAKAI_WORKER_THREADS".to_owned(), "2".to_owned()),
        (
            "HUAKAI_MAX_BODY_BYTES".to_owned(),
            max_body_bytes.to_string(),
        ),
        (
            "HUAKAI_MAX_IN_FLIGHT_REQUESTS".to_owned(),
            max_in_flight_requests.to_string(),
        ),
        (
            "HUAKAI_MAX_CONNECTIONS".to_owned(),
            max_connections.to_string(),
        ),
        (
            "HUAKAI_OVERLOAD_RETRY_AFTER_SECS".to_owned(),
            overload_retry_after_secs.to_string(),
        ),
        (
            "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS".to_owned(),
            timeouts.upstream_body_idle_timeout_ms.to_string(),
        ),
        (
            "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS".to_owned(),
            timeouts.downstream_write_idle_timeout_ms.to_string(),
        ),
        (
            "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS".to_owned(),
            timeouts.request_body_idle_timeout_ms.to_string(),
        ),
        (
            "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS".to_owned(),
            timeouts.server_header_read_timeout_ms.to_string(),
        ),
    ];

    if let Some(endpoint) = mock_upstream_endpoint {
        env.push(("HUAKAI_MOCK_UPSTREAM_ENDPOINT".to_owned(), endpoint));
    }
    // W11-D2 + W12-A D-4 第三方 P1 finding 2026-05-24: listener 集成测试一律
    // dev 模式 — 既允许可选 mock 上游, 又免去强制 spool 配置 (production validate
    // 要求 HUAKAI_SPOOL_ENABLED + DIR, dev/test 默认 disabled)。production-mode
    // 行为由 config.rs 单元测试覆盖, 此处不重复。
    env.push(("HUAKAI_RUNTIME_MODE".to_owned(), "development".to_owned()));

    StartupConfig::from_env_iter(env).expect("listener 测试配置应可解析")
}

async fn spawn_listener(config: StartupConfig) -> (SocketAddr, JoinHandle<()>) {
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("listener bind 应成功");
    let addr = listener.local_addr().expect("listener addr 应存在");
    let app = build_router(config).expect("build_router");
    let task = tokio::spawn(async move {
        let _ = axum::serve(listener, app).await;
    });
    (addr, task)
}

#[tokio::test]
async fn normal_messages_request_echoes_body_through_mock_upstream() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let config = test_config(4 * 1024 * 1024, Some(mock.endpoint()));
    let payload = Bytes::from_static(br#"{"model":"claude-test","messages":[]}"#);

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .body(Body::from(payload.clone()))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::OK);
    let body = body::to_bytes(response.into_body(), 1024)
        .await
        .expect("响应 body 应可读取");
    assert_eq!(body, payload);
    assert_eq!(mock.bytes_seen(), payload.len());
}

/// W11-D2 B2: mock 上游分支必须显式 emit 一个 attempt event，让控制面/审计能
/// 看出此次流量是演练 (route_plan_id="mock-upstream-drill" + attempt_id 前缀
/// "attempt-mock-")，账本不再为 mock drill 留空洞。
///
/// 判别性 + mutation：调用前后取 `enqueued_count()` 必须严格 +1；删 listener.rs
/// 里 reporter.report 调用 → 此测试断言失败。
#[tokio::test]
async fn mock_upstream_emits_explicit_mock_attempt_event() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let config = test_config(4 * 1024 * 1024, Some(mock.endpoint()));

    // 直接构造 GatewayState 以便测试可读 attempt_reporter 的 enqueued_count。
    // 跳过 build_router 顶层中间件 (drain/overload/body_limit) 不影响 mock 分支记账。
    let state = GatewayState::new(config).expect("state 构建应成功");
    let baseline = state.attempt_reporter().enqueued_count();

    let router = core_listener::build_router().with_state(state.clone());
    let payload = Bytes::from_static(br#"{"model":"claude-test","messages":[]}"#);

    let response = router
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .body(Body::from(payload))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::OK);

    let after = state.attempt_reporter().enqueued_count();
    assert_eq!(
        after,
        baseline + 1,
        "mock 上游路径必须发恰好 1 个 attempt event 让审计可见 (baseline={baseline}, after={after})"
    );
}

/// W12-A D-4 第三方 P1 finding 2026-05-24: production 模式启动 + 显式 spool 配置时,
/// GatewayState 必须真把 spool 接到 AttemptReporter — `has_durable_spool() == true`。
///
/// 判别性 + mutation 设计:
/// 1) 跳过 mock_upstream_endpoint (production validate 拒绝), 显式提供 spool dir 通过校验。
/// 2) `state.attempt_reporter().has_durable_spool()` 必须 == true (D-4 真接入)。
///
/// mutation:
/// - 在 lib.rs::GatewayState::new 退回到 AttemptReporter::spawn(route_client) 不传 options ->
///   reporter 内 spool 永远 None -> has_durable_spool() == false -> 红。
/// - 把 attempt_spool_options() 的 enabled 字段固定写 false -> 同样红。
#[tokio::test]
async fn production_gateway_state_wires_durable_spool_into_attempt_reporter() {
    let _drain_guard = DrainModeReset::set(false);

    // 用 tempdir 防与其它测试争抢同一 spool 目录
    let tmp_dir = std::env::temp_dir().join(format!(
        "huakai-prod-spool-{}-{}",
        std::process::id(),
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos()
    ));

    let env: Vec<(String, String)> = vec![
        ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
        (
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            "http://127.0.0.1:48080".to_owned(),
        ),
        ("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()),
        ("HUAKAI_LOG_LEVEL".to_owned(), "warn".to_owned()),
        ("HUAKAI_JSON_LOGS".to_owned(), "true".to_owned()),
        ("HUAKAI_WORKER_THREADS".to_owned(), "1".to_owned()),
        ("HUAKAI_MAX_BODY_BYTES".to_owned(), "4194304".to_owned()),
        ("HUAKAI_MAX_IN_FLIGHT_REQUESTS".to_owned(), "0".to_owned()),
        ("HUAKAI_MAX_CONNECTIONS".to_owned(), "0".to_owned()),
        ("HUAKAI_OVERLOAD_RETRY_AFTER_SECS".to_owned(), "1".to_owned()),
        (
            "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS".to_owned(),
            "300000".to_owned(),
        ),
        (
            "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS".to_owned(),
            "60000".to_owned(),
        ),
        (
            "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS".to_owned(),
            "30000".to_owned(),
        ),
        (
            "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS".to_owned(),
            "30000".to_owned(),
        ),
        // 关键: production 模式必须有 spool config
        ("HUAKAI_RUNTIME_MODE".to_owned(), "production".to_owned()),
        ("HUAKAI_SPOOL_ENABLED".to_owned(), "true".to_owned()),
        (
            "HUAKAI_SPOOL_DIR".to_owned(),
            tmp_dir.to_string_lossy().into_owned(),
        ),
        // production validate 拒绝 fsync=false (Codex round 2 P2 fix), 必须 true
        ("HUAKAI_SPOOL_FSYNC_ON_WRITE".to_owned(), "true".to_owned()),
    ];

    let config = StartupConfig::from_env_iter(env).expect("production spool 配置应解析成功");
    assert!(
        config.spool_enabled,
        "config.spool_enabled 必须 true 才能让 GatewayState 接 spool"
    );

    let state = GatewayState::new(config).expect("production GatewayState 应可构建");
    assert!(
        state.attempt_reporter().has_durable_spool(),
        "production GatewayState 启动后 attempt_reporter 必须 has_durable_spool=true \
         (lib.rs::GatewayState::new 调 spawn_with_options 时把 spool 接进去); \
         实际 false 说明 mutation 复现第三方 P1 finding (spool 形同虚设)"
    );

    let _ = std::fs::remove_dir_all(&tmp_dir);
}

/// P1-7 (W11 同源) 2026-05-24: mock 分支 forward 前必须主动 strip 客户端凭据头
/// (Authorization / x-api-key / cookie / proxy-authorization / x-auth-token /
/// x-access-token, 与 redaction::SENSITIVE_REQUEST_CREDENTIAL_HEADERS 同源),
/// 防止 dev/test mock 上游 log / 落盘真实 vendor 凭据。
///
/// 判别性 + mutation 设计:
/// 1) 客户端发全部 6 个 redaction 名单上的凭据头, 其中 cookie 故意发 2 行 →
///    `mock_credential_strip_count()` 增量恰好 = **7** (5 个单值头 + cookie 两行)。
/// 2) `mock.last_credential_headers()` 必须为空: 任一头被泄露则 vec 非空 → 红。
///
/// mutation:
/// - 删 listener.rs strip_client_credentials_for_mock 调用 → counter 增 0 + mock 看到全部 → 红 (1+2)。
/// - 缩 redaction::SENSITIVE_REQUEST_CREDENTIAL_HEADERS 任一项 → counter 增 ≤6 而非 7 → 红 (1)
///   且 mock 看到被删的那条 → 红 (2)。
/// - 把计数改回旧的 "+1 per header key" 而非 "+1 per value" → cookie 双行只计 1
///   → counter 增 6 而非 7 → 红 (1)。
/// - 把 authorization 加回 proxy_engine/headers.rs 白名单 → mock 看到 Authorization → 红 (2)。
#[tokio::test]
async fn mock_upstream_strips_authorization_x_api_key_and_cookie_with_counter() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let config = test_config(4 * 1024 * 1024, Some(mock.endpoint()));

    let baseline = core_listener::mock_credential_strip_count();

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .header("authorization", "Bearer real-vendor-key-must-not-leak")
                .header("x-api-key", "sk-ant-leak-canary")
                .header("cookie", "session=stolen-vendor-session")
                // 同名头第二行覆盖 "按 value 数累加" 语义 (Codex round 1 P2 fix)
                .header("cookie", "csrf=stolen-csrf-token")
                .header("proxy-authorization", "Basic stolen-proxy-creds")
                // x-auth-token / x-access-token: redaction 名单识别但旧 strip 名单漏 (round 2 fix)
                .header("x-auth-token", "vendor-session-jwt")
                .header("x-access-token", "stolen-oauth-access")
                .body(Body::from(Bytes::from_static(br#"{"messages":[]}"#)))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::OK);

    let after = core_listener::mock_credential_strip_count();
    assert_eq!(
        after,
        baseline + 7,
        "mock 分支必须按 value 累加 strip 所有 6 个凭据头 key (含 cookie 双行 = 7 个 value); \
         baseline={baseline}, after={after}; 任一头漏掉或计数按 key 而非 value 都会红"
    );
    let leaked = mock.last_credential_headers().await;
    assert!(
        leaked.is_empty(),
        "mock 上游不应看到任何客户端凭据头, 实际泄露 = {leaked:?}"
    );
}

#[tokio::test]
async fn overload_limit_one_rejects_second_concurrent_business_request() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::Sse {
        chunks: vec![
            Bytes::from_static(b"data: hold\n\n"),
            Bytes::from_static(b"data: [DONE]\n\n"),
        ],
        delay: Duration::from_secs(5),
    })
    .await;
    let config = test_config_with_resource_limits(4 * 1024 * 1024, Some(mock.endpoint()), 1, 0, 7);
    let (addr, task) = spawn_listener(config).await;
    let client = reqwest::Client::new();

    let first = client
        .post(format!("http://{addr}/v1/chat/completions"))
        .header("content-type", "application/json")
        .body("{}")
        .send()
        .await
        .expect("首个流式请求应建立");
    assert_eq!(first.status().as_u16(), 200);

    let second = client
        .post(format!("http://{addr}/v1/chat/completions"))
        .header("content-type", "application/json")
        .header(REQUEST_ID_HEADER, "overload-second")
        .body("{}")
        .send()
        .await
        .expect("过载请求应快速得到响应");

    assert_eq!(second.status().as_u16(), 503);
    assert_eq!(second.headers().get(header::RETRY_AFTER).unwrap(), "7");
    let body = second.text().await.expect("overload body 应可读");
    assert!(body.contains(r#""error":"overloaded""#), "body={body}");
    assert!(
        body.contains(r#""request_id":"overload-second""#),
        "body={body}"
    );
    assert_eq!(mock.requests_seen(), 1, "第二个请求不应进入上游");

    drop(first);
    task.abort();
}

#[tokio::test]
async fn healthz_and_metrics_bypass_overload_limit() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::Sse {
        chunks: vec![Bytes::from_static(b"data: hold\n\n")],
        delay: Duration::from_secs(5),
    })
    .await;
    let config = test_config_with_resource_limits(4 * 1024 * 1024, Some(mock.endpoint()), 1, 0, 1);
    let (addr, task) = spawn_listener(config).await;
    let client = reqwest::Client::new();

    let first = client
        .post(format!("http://{addr}/v1/messages"))
        .header("content-type", "application/json")
        .body("{}")
        .send()
        .await
        .expect("首个请求应占用唯一 in-flight permit");
    assert_eq!(first.status().as_u16(), 200);

    let health = client
        .get(format!("http://{addr}/healthz"))
        .send()
        .await
        .expect("healthz 不应被 overload 限制");
    assert_eq!(health.status().as_u16(), 200);

    let metrics = client
        .get(format!("http://{addr}/metrics"))
        .send()
        .await
        .expect("metrics 不应被 overload 限制");
    assert_eq!(metrics.status().as_u16(), 200);
    let metrics_body = metrics.text().await.expect("metrics body 应可读");
    assert!(metrics_body.contains("huakai_inflight_requests"));
    assert!(metrics_body.contains("huakai_inflight_limit"));

    drop(first);
    task.abort();
}

#[tokio::test]
async fn drain_guard_runs_before_overload_and_does_not_hold_permit() {
    let _drain_guard = DrainModeReset::set(true);
    let mock = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let config = test_config_with_resource_limits(4 * 1024 * 1024, Some(mock.endpoint()), 1, 0, 1);
    let router = build_router(config).expect("build_router");

    let drain_response = router
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("drain request 构建应成功"),
        )
        .await
        .expect("drain response 应返回");
    assert_eq!(drain_response.status(), StatusCode::SERVICE_UNAVAILABLE);

    set_drain_mode(false);
    let accepted = router
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("accepted request 构建应成功"),
        )
        .await
        .expect("drain 后业务请求应可进入");

    assert_eq!(accepted.status(), StatusCode::OK);
    assert_eq!(mock.requests_seen(), 1);
    drop(drain_response);
}

#[tokio::test]
async fn oversized_body_returns_413_payload_too_large() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let config = test_config(8, Some(mock.endpoint()));
    let payload = Bytes::from_static(b"0123456789abcdef");

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("content-type", "application/json")
                .header("content-length", payload.len().to_string())
                .body(Body::from(payload))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
    assert_eq!(mock.requests_seen(), 0, "超限请求不应到达 mock upstream");
}

#[tokio::test]
async fn client_cancel_mid_upload_stops_upstream_read_and_keeps_listener_alive() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::CountOnly {
        per_frame_delay: Duration::from_millis(5),
    })
    .await;
    let config = test_config(2 * 1024 * 1024, Some(mock.endpoint()));
    let (addr, task) = spawn_listener(config).await;

    let declared_len = 1024 * 1024usize;
    let mut stream = TcpStream::connect(addr).await.expect("tcp connect 应成功");
    let headers = format!(
        "POST /v1/messages HTTP/1.1\r\nHost: {addr}\r\nContent-Type: application/json\r\nContent-Length: {declared_len}\r\n\r\n"
    );
    stream
        .write_all(headers.as_bytes())
        .await
        .expect("headers 写入应成功");

    let chunk = vec![b'x'; 4096];
    for _ in 0..4 {
        stream
            .write_all(&chunk)
            .await
            .expect("body chunk 写入应成功");
        tokio::time::sleep(Duration::from_millis(20)).await;
    }

    let seen_before_close = mock
        .wait_for_bytes_at_least(4096, Duration::from_secs(2))
        .await;
    drop(stream);
    tokio::time::sleep(Duration::from_millis(250)).await;

    let seen_after_close = mock.bytes_seen();
    assert!(seen_before_close > 0, "mock upstream 应看到部分 body");
    assert!(
        seen_after_close < declared_len,
        "client cancel 后不应继续读完整 body, seen={seen_after_close}"
    );

    let health = reqwest::get(format!("http://{addr}/healthz"))
        .await
        .expect("cancel 后 healthz 仍应可访问");
    assert_eq!(health.status().as_u16(), 200);
    task.abort();
}

#[tokio::test]
async fn slow_client_reading_stream_does_not_deadlock_backpressure_path() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let chunks: Vec<Bytes> = (0..64).map(|_| Bytes::from(vec![b'a'; 1024])).collect();
    let expected_len: usize = chunks.iter().map(Bytes::len).sum();
    let mock = MockUpstream::spawn(MockBehavior::Sse {
        chunks,
        delay: Duration::ZERO,
    })
    .await;
    let config = test_config(4 * 1024 * 1024, Some(mock.endpoint()));
    let (addr, task) = spawn_listener(config).await;

    let client = reqwest::Client::new();
    let response = client
        .post(format!("http://{addr}/v1/chat/completions"))
        .header("content-type", "application/json")
        .body("{}")
        .send()
        .await
        .expect("stream 请求应成功");
    assert_eq!(response.status().as_u16(), 200);

    let mut stream = response.bytes_stream();
    let mut total = 0usize;
    while let Some(chunk) = tokio::time::timeout(Duration::from_secs(1), stream.next())
        .await
        .expect("慢读时每个 chunk 不应卡死")
    {
        let chunk = chunk.expect("stream chunk 应成功");
        total += chunk.len();
        tokio::time::sleep(Duration::from_millis(5)).await;
    }

    assert_eq!(total, expected_len);
    task.abort();
}

#[tokio::test]
async fn streaming_sse_chunks_are_passed_through_incrementally() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::Sse {
        chunks: vec![
            Bytes::from_static(b"data: one\n\n"),
            Bytes::from_static(b"data: two\n\n"),
            Bytes::from_static(b"data: [DONE]\n\n"),
        ],
        delay: Duration::from_millis(50),
    })
    .await;
    let config = test_config(4 * 1024 * 1024, Some(mock.endpoint()));
    let (addr, task) = spawn_listener(config).await;

    let response = reqwest::Client::new()
        .post(format!("http://{addr}/v1/messages"))
        .header("content-type", "application/json")
        .body("{}")
        .send()
        .await
        .expect("SSE 请求应成功");
    assert_eq!(response.status().as_u16(), 200);

    let mut stream = response.bytes_stream();
    let first = tokio::time::timeout(Duration::from_millis(300), stream.next())
        .await
        .expect("首个 SSE chunk 应增量到达")
        .expect("应有首个 chunk")
        .expect("首个 chunk 应成功");
    assert_eq!(first, Bytes::from_static(b"data: one\n\n"));

    let mut rest = Vec::new();
    while let Some(chunk) = stream.next().await {
        rest.extend_from_slice(&chunk.expect("剩余 SSE chunk 应成功"));
    }
    assert!(rest.ends_with(b"data: [DONE]\n\n"));
    task.abort();
}

#[tokio::test]
async fn request_id_is_propagated_to_upstream_and_response() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::EchoBody).await;
    let config = test_config(4 * 1024 * 1024, Some(mock.endpoint()));

    let response = build_router(config)
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/chat/completions")
                .header("content-type", "application/json")
                .header(REQUEST_ID_HEADER, "client-request-99")
                .body(Body::from(Bytes::from_static(br#"{"messages":[]}"#)))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers().get(REQUEST_ID_HEADER).unwrap(),
        "client-request-99"
    );
    assert_eq!(
        mock.last_request_id().await.as_deref(),
        Some("client-request-99")
    );
}

#[tokio::test]
async fn mock_upstream_json_error_and_slow_modes_flow_through_listener() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let json_mock = MockUpstream::spawn(MockBehavior::Json {
        status: StatusCode::CREATED,
        body: Bytes::from_static(br#"{"ok":true}"#),
    })
    .await;
    let json_response = build_router(test_config(4 * 1024 * 1024, Some(json_mock.endpoint())))
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");
    assert_eq!(json_response.status(), StatusCode::CREATED);

    let error_mock = MockUpstream::spawn(MockBehavior::Error5xx).await;
    let error_response = build_router(test_config(4 * 1024 * 1024, Some(error_mock.endpoint())))
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");
    assert_eq!(error_response.status(), StatusCode::BAD_GATEWAY);

    let slow_mock = MockUpstream::spawn(MockBehavior::SlowJson {
        delay: Duration::from_millis(30),
        body: Bytes::from_static(br#"{"slow":true}"#),
    })
    .await;
    let started = std::time::Instant::now();
    let slow_response = build_router(test_config(4 * 1024 * 1024, Some(slow_mock.endpoint())))
        .expect("build_router")
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/chat/completions")
                .body(Body::from(Bytes::from_static(b"{}")))
                .expect("request 构建应成功"),
        )
        .await
        .expect("listener 应响应");
    assert_eq!(slow_response.status(), StatusCode::OK);
    assert!(started.elapsed() >= Duration::from_millis(30));
}

#[tokio::test]
async fn request_body_idle_timeout_fails_closed_and_listener_stays_healthy() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let mock = MockUpstream::spawn(MockBehavior::CountOnly {
        per_frame_delay: Duration::ZERO,
    })
    .await;
    let config = test_config_with_resource_limits_and_timeouts(
        2 * 1024 * 1024,
        Some(mock.endpoint()),
        0,
        0,
        1,
        TestTimeouts {
            request_body_idle_timeout_ms: 80,
            ..TestTimeouts::default()
        },
    );
    let (addr, task) = spawn_listener(config).await;

    let declared_len = 4096usize;
    let mut stream = TcpStream::connect(addr).await.expect("tcp connect 应成功");
    let headers = format!(
        "POST /v1/messages HTTP/1.1\r\nHost: {addr}\r\nContent-Type: application/json\r\nContent-Length: {declared_len}\r\n\r\n"
    );
    stream
        .write_all(headers.as_bytes())
        .await
        .expect("headers 写入应成功");
    stream
        .write_all(b"{")
        .await
        .expect("首个 body byte 写入应成功");
    tokio::time::sleep(Duration::from_millis(180)).await;

    let mut response = vec![0; 512];
    let read = tokio::time::timeout(Duration::from_secs(1), stream.read(&mut response))
        .await
        .expect("request body idle 后连接应关闭或返回错误")
        .expect("读取 response 不应产生 IO 错误");
    let response_text = String::from_utf8_lossy(&response[..read]);
    assert!(
        read == 0 || response_text.starts_with("HTTP/1.1 502"),
        "body idle 应 fail closed, read={read}, response={response_text}"
    );
    assert!(
        mock.bytes_seen() < declared_len,
        "body idle 后不应继续向上游读取完整 body"
    );

    let health = reqwest::get(format!("http://{addr}/healthz"))
        .await
        .expect("body idle 后 healthz 仍应可访问");
    assert_eq!(health.status().as_u16(), 200);
    task.abort();
}

#[tokio::test]
async fn server_header_read_timeout_closes_slow_header_connection() {
    let _drain_guard = DrainModeReset::set(false); // 持 drain 测试互斥锁
    let config = test_config_with_resource_limits_and_timeouts(
        4 * 1024 * 1024,
        None,
        0,
        0,
        1,
        TestTimeouts {
            server_header_read_timeout_ms: 80,
            ..TestTimeouts::default()
        },
    );
    let listener = TcpListener::bind("127.0.0.1:0")
        .await
        .expect("listener bind 应成功");
    let addr = listener.local_addr().expect("listener addr 应存在");
    let app = build_router(config.clone()).expect("build_router");
    let (shutdown_tx, shutdown_rx) = oneshot::channel::<()>();
    let task = tokio::spawn(async move {
        let _ = server_runtime::serve_with_shutdown(
            listener,
            app,
            ServerTimeouts::from_config(&config),
            async move {
                let _ = shutdown_rx.await;
            },
        )
        .await;
    });

    let mut stream = TcpStream::connect(addr).await.expect("tcp connect 应成功");
    stream
        .write_all(b"GET /healthz HTTP/1.1\r\nHost: ")
        .await
        .expect("partial header 写入应成功");
    tokio::time::sleep(Duration::from_millis(180)).await;

    let mut buf = [0u8; 16];
    let read = tokio::time::timeout(Duration::from_secs(1), stream.read(&mut buf))
        .await
        .expect("header read timeout 后连接应关闭")
        .expect("读取连接关闭不应 IO error");
    assert_eq!(read, 0, "header read timeout 后 server 应关闭连接");

    let _ = shutdown_tx.send(());
    let _ = tokio::time::timeout(Duration::from_secs(1), task).await;
}
