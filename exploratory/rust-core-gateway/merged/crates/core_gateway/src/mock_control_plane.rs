// 测试用 Go control plane gRPC mock。
// 只服务本机测试, 不承载生产逻辑, 也不连接真实 Go/PG。
//
// M-rust-3 merge: 新增 MockControlPlaneBehavior 枚举 + drain_mode 支持
// 支持 Normal / Unavailable / SlowResponse 三种行为, 以及运行时切换 drain_mode

use std::{
    collections::HashMap,
    net::SocketAddr,
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicUsize, Ordering},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use bytes::Bytes;
use tokio::{net::TcpListener, sync::Mutex, task::JoinHandle};
use tokio_stream::wrappers::TcpListenerStream;
use tonic::{Request, Response, Status, transport::Server};

use crate::route_proto::v1::{
    AttemptReportRequest, AttemptReportResponse, HealthCheckRequest, HealthCheckResponse,
    HeartbeatRequest, HeartbeatResponse, RoutePlan, RouteQueryRequest, UpstreamAuthMaterial,
    route_service_server::{RouteService, RouteServiceServer},
};

// ─── 行为控制 ─────────────────────────────────────────────────────────────────

/// mock control plane 行为枚举 (源自 claude-m3 lane)
#[derive(Debug, Clone)]
pub enum MockControlPlaneBehavior {
    /// 正常响应: 返回固定 RoutePlan
    Normal,
    /// 始终返回 gRPC Unavailable 状态
    Unavailable,
    /// 延迟响应 (模拟慢 control plane)
    SlowResponse { delay: Duration },
}

// ─── 状态结构体 ───────────────────────────────────────────────────────────────

pub struct MockControlPlane {
    addr: SocketAddr,
    state: Arc<MockState>,
    task: JoinHandle<()>,
}

pub struct MockControlPlaneConfig {
    pub route_plan: RoutePlan,
    pub health_response: HealthCheckResponse,
    pub heartbeat_response: HeartbeatResponse,
    pub attempt_response: AttemptReportResponse,
    /// 行为控制; 默认 Normal
    pub behavior: MockControlPlaneBehavior,
    /// attempt_report 前 N 次返回 Unavailable, 用于 reporter retry 测试
    pub attempt_failures_before_success: usize,
    /// attempt_report 响应前延迟, 用于队列满测试
    pub attempt_report_delay: Duration,
}

struct MockState {
    route_plan: RoutePlan,
    health_response: HealthCheckResponse,
    /// heartbeat 响应模板; drain_mode 字段会被 drain_mode 原子覆盖
    heartbeat_response: HeartbeatResponse,
    attempt_response: AttemptReportResponse,
    behavior: MockControlPlaneBehavior,
    attempt_failures_remaining: AtomicUsize,
    attempt_report_delay: Duration,
    attempt_ack_by_idempotency_key: Mutex<HashMap<String, AttemptReportResponse>>,
    route_queries_seen: AtomicUsize,
    health_checks_seen: AtomicUsize,
    heartbeats_seen: AtomicUsize,
    attempt_reports_seen: AtomicUsize,
    last_route_query: Mutex<Option<RouteQueryRequest>>,
    last_health_check: Mutex<Option<HealthCheckRequest>>,
    last_heartbeat: Mutex<Option<HeartbeatRequest>>,
    last_attempt_report: Mutex<Option<AttemptReportRequest>>,
    /// 运行时可切换的 drain_mode (源自 claude-m3 lane)
    drain_mode: AtomicBool,
}

#[derive(Clone)]
struct MockRouteService {
    state: Arc<MockState>,
}

// ─── MockControlPlane 公共 API ────────────────────────────────────────────────

impl MockControlPlane {
    pub async fn spawn(route_plan: RoutePlan) -> Self {
        Self::spawn_with_config(MockControlPlaneConfig::new(route_plan)).await
    }

    pub async fn spawn_with_config(config: MockControlPlaneConfig) -> Self {
        let state = Arc::new(MockState {
            route_plan: config.route_plan,
            health_response: config.health_response,
            heartbeat_response: config.heartbeat_response,
            attempt_response: config.attempt_response,
            behavior: config.behavior,
            attempt_failures_remaining: AtomicUsize::new(config.attempt_failures_before_success),
            attempt_report_delay: config.attempt_report_delay,
            attempt_ack_by_idempotency_key: Mutex::new(HashMap::new()),
            route_queries_seen: AtomicUsize::new(0),
            health_checks_seen: AtomicUsize::new(0),
            heartbeats_seen: AtomicUsize::new(0),
            attempt_reports_seen: AtomicUsize::new(0),
            last_route_query: Mutex::new(None),
            last_health_check: Mutex::new(None),
            last_heartbeat: Mutex::new(None),
            last_attempt_report: Mutex::new(None),
            drain_mode: AtomicBool::new(false),
        });

        let listener = TcpListener::bind("127.0.0.1:0")
            .await
            .expect("mock control plane bind 应成功");
        let addr = listener
            .local_addr()
            .expect("mock control plane addr 应存在");
        let incoming = TcpListenerStream::new(listener);
        let service = MockRouteService {
            state: state.clone(),
        };

        let task = tokio::spawn(async move {
            let _ = Server::builder()
                .add_service(RouteServiceServer::new(service))
                .serve_with_incoming(incoming)
                .await;
        });

        Self { addr, state, task }
    }

    pub fn endpoint(&self) -> String {
        format!("http://{}", self.addr)
    }

    pub fn route_queries_seen(&self) -> usize {
        self.state.route_queries_seen.load(Ordering::SeqCst)
    }

    pub fn health_checks_seen(&self) -> usize {
        self.state.health_checks_seen.load(Ordering::SeqCst)
    }

    pub fn heartbeats_seen(&self) -> usize {
        self.state.heartbeats_seen.load(Ordering::SeqCst)
    }

    pub fn attempt_reports_seen(&self) -> usize {
        self.state.attempt_reports_seen.load(Ordering::SeqCst)
    }

    pub async fn last_route_query(&self) -> Option<RouteQueryRequest> {
        self.state.last_route_query.lock().await.clone()
    }

    pub async fn last_health_check(&self) -> Option<HealthCheckRequest> {
        self.state.last_health_check.lock().await.clone()
    }

    pub async fn last_heartbeat(&self) -> Option<HeartbeatRequest> {
        self.state.last_heartbeat.lock().await.clone()
    }

    pub async fn last_attempt_report(&self) -> Option<AttemptReportRequest> {
        self.state.last_attempt_report.lock().await.clone()
    }

    /// 运行时切换 drain_mode — 下次 heartbeat 响应会携带该值 (源自 claude-m3 lane)
    pub fn set_drain_mode(&self, drain: bool) {
        self.state.drain_mode.store(drain, Ordering::SeqCst);
    }
}

impl Drop for MockControlPlane {
    fn drop(&mut self) {
        self.task.abort();
    }
}

// ─── MockControlPlaneConfig ───────────────────────────────────────────────────

impl MockControlPlaneConfig {
    pub fn new(route_plan: RoutePlan) -> Self {
        Self {
            route_plan,
            health_response: HealthCheckResponse {
                schema_version: "route.v1".to_owned(),
                server_time: now_unix_ms_i64(),
                route_service_status: "ready".to_owned(),
                ready: true,
            },
            heartbeat_response: HeartbeatResponse {
                ack: true,
                desired_schema_version: "route.v1".to_owned(),
                drain_mode: false,
            },
            attempt_response: AttemptReportResponse {
                ack: true,
                ack_id: "ack-mock-1".to_owned(),
                accepted_at: now_unix_ms_i64(),
                advisory: String::new(),
            },
            behavior: MockControlPlaneBehavior::Normal,
            attempt_failures_before_success: 0,
            attempt_report_delay: Duration::ZERO,
        }
    }

    /// 使用指定行为覆盖默认 Normal
    pub fn with_behavior(mut self, behavior: MockControlPlaneBehavior) -> Self {
        self.behavior = behavior;
        self
    }

    pub fn with_attempt_failures_before_success(mut self, failures: usize) -> Self {
        self.attempt_failures_before_success = failures;
        self
    }

    pub fn with_attempt_report_delay(mut self, delay: Duration) -> Self {
        self.attempt_report_delay = delay;
        self
    }
}

// ─── gRPC 服务实现 ────────────────────────────────────────────────────────────

#[tonic::async_trait]
impl RouteService for MockRouteService {
    async fn route_query(
        &self,
        request: Request<RouteQueryRequest>,
    ) -> Result<Response<RoutePlan>, Status> {
        // 先应用行为控制
        match &self.state.behavior {
            MockControlPlaneBehavior::Unavailable => {
                self.state.route_queries_seen.fetch_add(1, Ordering::SeqCst);
                return Err(Status::unavailable("mock control plane unavailable"));
            }
            MockControlPlaneBehavior::SlowResponse { delay } => {
                tokio::time::sleep(*delay).await;
            }
            MockControlPlaneBehavior::Normal => {}
        }

        let query = request.into_inner();
        self.state.route_queries_seen.fetch_add(1, Ordering::SeqCst);

        // 根据 request_protocol 决定 vendor (与 claude-m3 lane 对齐)
        let mut plan = self.state.route_plan.clone();
        if query.request_protocol == "openai_chat_completions" {
            plan.vendor = "openai".to_owned();
            plan.auth_mode = "bearer".to_owned();
            plan.upstream_model = "gpt-4o".to_owned();
            if plan.vendor_endpoint.is_empty() {
                plan.vendor_endpoint = "https://api.openai.com".to_owned();
            }
        }

        *self.state.last_route_query.lock().await = Some(query);
        Ok(Response::new(plan))
    }

    async fn attempt_report(
        &self,
        request: Request<AttemptReportRequest>,
    ) -> Result<Response<AttemptReportResponse>, Status> {
        let report = request.into_inner();
        self.state
            .attempt_reports_seen
            .fetch_add(1, Ordering::SeqCst);

        if !self.state.attempt_report_delay.is_zero() {
            tokio::time::sleep(self.state.attempt_report_delay).await;
        }

        if self
            .state
            .attempt_failures_remaining
            .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |remaining| {
                remaining.checked_sub(1)
            })
            .is_ok()
        {
            return Err(Status::unavailable("mock attempt report unavailable"));
        }

        let mut response = self.state.attempt_response.clone();
        if !report.idempotency_key.is_empty() {
            let mut acks = self.state.attempt_ack_by_idempotency_key.lock().await;
            if let Some(existing) = acks.get(&report.idempotency_key) {
                response = existing.clone();
            } else {
                acks.insert(report.idempotency_key.clone(), response.clone());
            }
        }

        *self.state.last_attempt_report.lock().await = Some(report);
        Ok(Response::new(response))
    }

    async fn health_check(
        &self,
        request: Request<HealthCheckRequest>,
    ) -> Result<Response<HealthCheckResponse>, Status> {
        let health = request.into_inner();
        self.state.health_checks_seen.fetch_add(1, Ordering::SeqCst);
        *self.state.last_health_check.lock().await = Some(health);
        Ok(Response::new(self.state.health_response.clone()))
    }

    async fn heartbeat(
        &self,
        request: Request<HeartbeatRequest>,
    ) -> Result<Response<HeartbeatResponse>, Status> {
        let heartbeat = request.into_inner();
        self.state.heartbeats_seen.fetch_add(1, Ordering::SeqCst);
        *self.state.last_heartbeat.lock().await = Some(heartbeat);

        // drain_mode 来自原子, 允许运行时切换
        let mut resp = self.state.heartbeat_response.clone();
        resp.drain_mode = self.state.drain_mode.load(Ordering::SeqCst);
        Ok(Response::new(resp))
    }
}

// ─── 辅助函数 ─────────────────────────────────────────────────────────────────

pub fn mock_route_plan(vendor_endpoint: impl Into<String>) -> RoutePlan {
    RoutePlan {
        route_plan_id: "route-plan-mock-1".to_owned(),
        account_id: "account-mock-1".to_owned(),
        acquisition_token: Bytes::from_static(b"lease-token-mock-1"),
        vendor: "anthropic".to_owned(),
        upstream_model: "claude-mock".to_owned(),
        vendor_endpoint: vendor_endpoint.into(),
        credentials_handle: "credential-handle-mock-1".to_owned(),
        auth_mode: "bearer".to_owned(),
        route_ttl_ms: 0,
        attempt_deadline_ms: 30_000,
        max_body_bytes: 4 * 1024 * 1024,
        max_stream_frame_bytes: 64 * 1024,
        upstream_auth: Some(UpstreamAuthMaterial {
            material_kind: "bearer_token".to_owned(),
            material: Bytes::from_static(b"upstream-secret-mock-1"),
            header_name: String::new(),
            expires_at_unix_ms: 0,
        }),
    }
}

fn now_unix_ms_i64() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis()
        .min(i64::MAX as u128) as i64
}
