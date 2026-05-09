// M-rust-3: mock Go control plane — axum HTTP/JSON server
// 职责: 在单元/集成测试中替代真实 Go control plane
// 只供测试使用; 不连接真实 PG 或真实账号池
//
// 支持的 endpoint:
//   POST /v1/internal/route      → RoutePlan
//   POST /v1/internal/attempt    → AttemptAck
//   GET  /v1/internal/health     → HealthCheckResponse
//   POST /v1/internal/heartbeat  → HeartbeatResponse

use std::{
    net::SocketAddr,
    sync::{
        Arc,
        atomic::{AtomicU32, AtomicUsize, Ordering},
    },
    time::Duration,
};

use axum::{
    Json, Router,
    extract::State,
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::{get, post},
};
use serde_json::json;
use tokio::{net::TcpListener, sync::Mutex, task::JoinHandle};
use tracing::debug;

use crate::route_client::{
    AttemptAck, AttemptReportRequest, HealthCheckResponse, HeartbeatRequest, HeartbeatResponse,
    RoutePlan, RouteQueryRequest,
};

// ─── 行为控制 ─────────────────────────────────────────────────────────────────

/// mock control plane 行为枚举
#[derive(Debug, Clone)]
pub enum MockControlPlaneBehavior {
    /// 正常响应: 返回固定 RoutePlan
    Normal,
    /// 始终返回 503 (control plane 不可用)
    Unavailable,
    /// 前 N 次 route query 失败, 之后恢复 (用于 circuit breaker 测试)
    FailFirstN { n: u32 },
    /// 延迟响应 (模拟慢 control plane)
    SlowResponse { delay: Duration },
}

// ─── 共享状态 ─────────────────────────────────────────────────────────────────

#[derive(Debug)]
pub struct MockControlPlaneState {
    behavior: MockControlPlaneBehavior,
    schema_version: u32,
    route_requests_seen: AtomicUsize,
    attempt_reports_seen: AtomicUsize,
    heartbeats_seen: AtomicUsize,
    fail_count: AtomicU32,
    /// 最后收到的 attempt report (供测试验证)
    last_attempt: Mutex<Option<AttemptReportRequest>>,
    /// 最后收到的 heartbeat (供测试验证)
    last_heartbeat: Mutex<Option<HeartbeatRequest>>,
    /// 是否在心跳响应中设置 drain_mode
    drain_mode: std::sync::atomic::AtomicBool,
}

// ─── MockControlPlane 句柄 ────────────────────────────────────────────────────

/// mock control plane 句柄 — 用于测试断言与行为注入
pub struct MockControlPlane {
    addr: SocketAddr,
    state: Arc<MockControlPlaneState>,
    task: JoinHandle<()>,
}

impl std::fmt::Debug for MockControlPlane {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("MockControlPlane")
            .field("addr", &self.addr)
            .finish_non_exhaustive()
    }
}

impl MockControlPlane {
    /// 启动 mock control plane; 监听随机端口
    pub async fn spawn(behavior: MockControlPlaneBehavior) -> Self {
        let state = Arc::new(MockControlPlaneState {
            behavior,
            schema_version: 1,
            route_requests_seen: AtomicUsize::new(0),
            attempt_reports_seen: AtomicUsize::new(0),
            heartbeats_seen: AtomicUsize::new(0),
            fail_count: AtomicU32::new(0),
            last_attempt: Mutex::new(None),
            last_heartbeat: Mutex::new(None),
            drain_mode: std::sync::atomic::AtomicBool::new(false),
        });

        let app = Router::new()
            .route("/v1/internal/route", post(handle_route_query))
            .route("/v1/internal/attempt", post(handle_attempt_report))
            .route("/v1/internal/health", get(handle_health_check))
            .route("/v1/internal/heartbeat", post(handle_heartbeat))
            .with_state(state.clone());

        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("mock control plane bind 应成功");
        let addr = listener
            .local_addr()
            .expect("mock control plane addr 应存在");
        let task = tokio::spawn(async move {
            let _ = axum::serve(listener, app).await;
        });

        Self { addr, state, task }
    }

    /// 返回 base URL 字符串 (如 "http://127.0.0.1:xxxxx")
    pub fn base_url(&self) -> String {
        format!("http://{}", self.addr)
    }

    /// 收到的 route query 次数
    pub fn route_requests_seen(&self) -> usize {
        self.state.route_requests_seen.load(Ordering::SeqCst)
    }

    /// 收到的 attempt report 次数
    pub fn attempt_reports_seen(&self) -> usize {
        self.state.attempt_reports_seen.load(Ordering::SeqCst)
    }

    /// 收到的 heartbeat 次数
    pub fn heartbeats_seen(&self) -> usize {
        self.state.heartbeats_seen.load(Ordering::SeqCst)
    }

    /// 最后一次收到的 attempt report (异步锁读取)
    pub async fn last_attempt(&self) -> Option<AttemptReportRequest> {
        self.state.last_attempt.lock().await.clone()
    }

    /// 最后一次收到的 heartbeat
    pub async fn last_heartbeat(&self) -> Option<HeartbeatRequest> {
        self.state.last_heartbeat.lock().await.clone()
    }

    /// 设置 drain_mode: 下次 heartbeat 响应中携带 drain_mode=true
    pub fn set_drain_mode(&self, drain: bool) {
        self.state
            .drain_mode
            .store(drain, std::sync::atomic::Ordering::SeqCst);
    }

    /// 等待直到 route_requests_seen >= min (超时后返回当前值)
    pub async fn wait_for_route_requests(&self, min: usize, timeout: Duration) -> usize {
        let started = std::time::Instant::now();
        loop {
            let current = self.route_requests_seen();
            if current >= min || started.elapsed() >= timeout {
                return current;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    }
}

impl Drop for MockControlPlane {
    fn drop(&mut self) {
        self.task.abort();
    }
}

// ─── axum handler ─────────────────────────────────────────────────────────────

type MockState = Arc<MockControlPlaneState>;

/// POST /v1/internal/route
async fn handle_route_query(
    State(state): State<MockState>,
    Json(req): Json<RouteQueryRequest>,
) -> Response {
    state.route_requests_seen.fetch_add(1, Ordering::SeqCst);
    debug!(request_id = %req.request_id, "mock control plane: route query 收到");

    match &state.behavior {
        MockControlPlaneBehavior::Unavailable => {
            return (
                StatusCode::SERVICE_UNAVAILABLE,
                Json(json!({"error": "control plane unavailable"})),
            )
                .into_response();
        }
        MockControlPlaneBehavior::FailFirstN { n } => {
            let count = state.fail_count.fetch_add(1, Ordering::SeqCst);
            if count < *n {
                return (
                    StatusCode::SERVICE_UNAVAILABLE,
                    Json(json!({"error": "simulated failure", "attempt": count})),
                )
                    .into_response();
            }
        }
        MockControlPlaneBehavior::SlowResponse { delay } => {
            tokio::time::sleep(*delay).await;
        }
        MockControlPlaneBehavior::Normal => {}
    }

    let plan = mock_route_plan(&req);
    Json(plan).into_response()
}

/// POST /v1/internal/attempt
async fn handle_attempt_report(
    State(state): State<MockState>,
    Json(req): Json<AttemptReportRequest>,
) -> Response {
    state.attempt_reports_seen.fetch_add(1, Ordering::SeqCst);
    debug!(
        request_id = %req.request_id,
        attempt_id = %req.attempt_id,
        status = %req.status,
        "mock control plane: attempt report 收到"
    );
    *state.last_attempt.lock().await = Some(req.clone());

    let ack = AttemptAck {
        ack: true,
        ack_id: format!("ack-{}", req.attempt_id),
        accepted_at: "2026-05-09T00:00:00Z".to_owned(),
        advisory: None,
    };
    Json(ack).into_response()
}

/// GET /v1/internal/health
async fn handle_health_check(State(state): State<MockState>) -> Response {
    let resp = HealthCheckResponse {
        status: "ok".to_owned(),
        schema_version: state.schema_version,
        server_time: "2026-05-09T00:00:00Z".to_owned(),
        route_service_status: "ready".to_owned(),
    };
    Json(resp).into_response()
}

/// POST /v1/internal/heartbeat
async fn handle_heartbeat(
    State(state): State<MockState>,
    Json(req): Json<HeartbeatRequest>,
) -> Response {
    state.heartbeats_seen.fetch_add(1, Ordering::SeqCst);
    debug!(node_id = %req.node_id, "mock control plane: heartbeat 收到");
    *state.last_heartbeat.lock().await = Some(req);

    let drain = state.drain_mode.load(std::sync::atomic::Ordering::SeqCst);
    let resp = HeartbeatResponse {
        ack: true,
        desired_schema_version: state.schema_version,
        drain_mode: drain,
    };
    Json(resp).into_response()
}

// ─── 辅助: 构造 mock RoutePlan ────────────────────────────────────────────────

fn mock_route_plan(req: &RouteQueryRequest) -> RoutePlan {
    // 根据 request_protocol 选择模拟 vendor
    let (vendor, upstream_model, vendor_endpoint, auth_mode) = match req.request_protocol.as_str() {
        "openai_chat_completions" => ("openai", "gpt-4o", "https://api.openai.com", "bearer"),
        "bedrock_runtime" => (
            "bedrock",
            "anthropic.claude-3-5-sonnet-20241022-v2:0",
            "https://bedrock-runtime.us-east-1.amazonaws.com",
            "aws_sigv4",
        ),
        _ => (
            "anthropic",
            "claude-3-5-sonnet-20241022",
            "https://api.anthropic.com",
            "bearer",
        ),
    };

    RoutePlan {
        route_plan_id: format!("plan-{}", req.request_id),
        account_id: format!("acct-{}", req.tenant_id),
        acquisition_token: format!("tok-{}-{}", req.tenant_id, req.request_id),
        vendor: vendor.to_owned(),
        upstream_model: upstream_model.to_owned(),
        vendor_endpoint: vendor_endpoint.to_owned(),
        credentials_handle: format!("hdl-{}", req.tenant_id),
        auth_mode: auth_mode.to_owned(),
        route_ttl_ms: 0, // v0 默认禁用缓存
        attempt_deadline_ms: 30_000,
        max_body_bytes: 4 * 1024 * 1024,
        max_stream_frame_bytes: 65_536,
    }
}

// ─── 单元测试 ─────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn mock_control_plane_spawns_and_health_check_ok() {
        let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Normal).await;
        let resp = reqwest::get(format!("{}/v1/internal/health", mock.base_url()))
            .await
            .expect("health check 应成功");
        assert_eq!(resp.status().as_u16(), 200);
        let body: HealthCheckResponse = resp.json().await.expect("应能解析 HealthCheckResponse");
        assert_eq!(body.status, "ok");
        assert_eq!(body.route_service_status, "ready");
    }

    #[tokio::test]
    async fn mock_control_plane_route_query_returns_plan() {
        let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Normal).await;
        let req = RouteQueryRequest {
            request_id: "req-test-1".to_owned(),
            tenant_id: "tenant-1".to_owned(),
            requested_model: "claude-3-5-sonnet".to_owned(),
            session_hash: "hash-abc".to_owned(),
            request_protocol: "anthropic_messages".to_owned(),
            stream: true,
            client_deadline_ms: 30_000,
            previous_attempts: vec![],
            capability_hints: None,
        };
        let resp = reqwest::Client::new()
            .post(format!("{}/v1/internal/route", mock.base_url()))
            .json(&req)
            .send()
            .await
            .expect("route query 应成功");
        assert_eq!(resp.status().as_u16(), 200);
        let plan: RoutePlan = resp.json().await.expect("应能解析 RoutePlan");
        assert_eq!(plan.vendor, "anthropic");
        assert_eq!(plan.auth_mode, "bearer");
        assert_eq!(mock.route_requests_seen(), 1);
    }

    #[tokio::test]
    async fn mock_control_plane_unavailable_returns_503() {
        let mock = MockControlPlane::spawn(MockControlPlaneBehavior::Unavailable).await;
        let req = RouteQueryRequest {
            request_id: "req-test-2".to_owned(),
            tenant_id: "t2".to_owned(),
            requested_model: "m".to_owned(),
            session_hash: "h".to_owned(),
            request_protocol: "anthropic_messages".to_owned(),
            stream: false,
            client_deadline_ms: 5_000,
            previous_attempts: vec![],
            capability_hints: None,
        };
        let resp = reqwest::Client::new()
            .post(format!("{}/v1/internal/route", mock.base_url()))
            .json(&req)
            .send()
            .await
            .expect("请求应到达 mock");
        assert_eq!(resp.status().as_u16(), 503, "Unavailable 模式应返回 503");
    }
}
