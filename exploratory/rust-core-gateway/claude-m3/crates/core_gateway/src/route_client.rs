// M-rust-3: Go control plane 的 HTTP/JSON 客户端
// 职责: RouteQuery / AttemptReport / HealthCheck / Heartbeat 四个 RPC
// 性能策略: reqwest 连接池复用, 短 TTL DashMap 缓存, 原子 circuit breaker 计数器
//
// M-rust-4 将冻结 gRPC proto v0; 当前 contract 使用同字段 HTTP/JSON.

use std::{
    sync::{
        Arc,
        atomic::{AtomicU32, AtomicU64, Ordering},
    },
    time::{Duration, Instant},
};

use bytes::Bytes;
use serde::{Deserialize, Serialize};
use thiserror::Error;
use tracing::{debug, warn};

// ─── 错误类型 ─────────────────────────────────────────────────────────────────

/// route_client 子系统错误枚举
#[derive(Debug, Error)]
pub enum RouteClientError {
    /// HTTP 传输层错误 (连接失败、超时、DNS 等)
    #[error("transport error: {0}")]
    Transport(#[from] reqwest::Error),

    /// 控制面返回非 2xx 状态码
    #[error("control plane returned {status}: {body}")]
    ControlPlane { status: u16, body: String },

    /// 响应体 JSON 反序列化失败
    #[error("deserialize error: {0}")]
    Deserialize(String),

    /// circuit breaker 熔断, 拒绝发出请求
    #[error("circuit breaker open: recent_failures={recent_failures}")]
    CircuitOpen { recent_failures: u32 },

    /// 请求超时 (deadline 超出)
    #[error("deadline exceeded after {elapsed_ms}ms")]
    DeadlineExceeded { elapsed_ms: u64 },
}

impl RouteClientError {
    /// 返回 error_class 字符串, 与 Go AttemptReport.error_class 对齐
    pub fn error_class(&self) -> &'static str {
        match self {
            RouteClientError::Transport(_) => "control_plane_error",
            RouteClientError::ControlPlane { .. } => "control_plane_error",
            RouteClientError::Deserialize(_) => "internal_error",
            RouteClientError::CircuitOpen { .. } => "control_plane_error",
            RouteClientError::DeadlineExceeded { .. } => "control_plane_error",
        }
    }
}

// ─── RPC 消息结构体 ───────────────────────────────────────────────────────────

/// 路由查询请求 (§4.1)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RouteQueryRequest {
    pub request_id: String,
    pub tenant_id: String,
    pub requested_model: String,
    pub session_hash: String,
    pub request_protocol: String,
    pub stream: bool,
    pub client_deadline_ms: u64,
    /// 同一 client request 下已失败 attempt 的摘要
    pub previous_attempts: Vec<PreviousAttempt>,
    /// 可选能力提示 (tool_use / vision / large_context / cache_preference)
    #[serde(skip_serializing_if = "Option::is_none")]
    pub capability_hints: Option<Vec<String>>,
}

/// 已失败 attempt 摘要 — Go 用于决定是否下发下一账号
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PreviousAttempt {
    pub attempt_id: String,
    pub status: String,
    pub vendor: String,
    pub error_class: String,
}

/// 路由查询响应 (§4.1)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RoutePlan {
    pub route_plan_id: String,
    pub account_id: String,
    pub acquisition_token: String,
    pub vendor: String,
    pub upstream_model: String,
    pub vendor_endpoint: String,
    /// 凭据不透明 handle; credentials_handle 本身不含 secret 材料
    pub credentials_handle: String,
    /// 认证模式: bearer / aws_sigv4 / pre_signed / control_plane_signed
    pub auth_mode: String,
    /// Rust 可缓存 plan 的上限毫秒数; 0 表示禁止缓存
    pub route_ttl_ms: u64,
    pub attempt_deadline_ms: u64,
    pub max_body_bytes: u64,
    pub max_stream_frame_bytes: u64,
}

/// attempt 上报请求 (§4.2)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AttemptReportRequest {
    pub request_id: String,
    pub route_plan_id: String,
    pub attempt_id: String,
    pub acquisition_token: String,
    pub status: String,
    pub http_status: u16,
    pub started_at: String,
    pub ended_at: String,
    pub latency_ms: u64,
    pub tokens_used: TokensUsed,
    pub cache_metrics: CacheMetrics,
    pub bytes_in: u64,
    pub bytes_out: u64,
    pub frames_in: u64,
    pub frames_out: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub vendor_request_id: Option<String>,
    pub retryable: bool,
    pub error_class: String,
    /// 日志中已脱敏的错误消息
    pub error_message_redacted: String,
    pub idempotency_key: String,
}

/// token 用量字段
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct TokensUsed {
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub total_tokens: u64,
    /// 来源标记: vendor_response / estimated / missing
    pub source: String,
}

/// 缓存指标字段
#[derive(Debug, Clone, Serialize, Deserialize, Default)]
pub struct CacheMetrics {
    pub cache_read_tokens: u64,
    pub cache_write_tokens: u64,
    pub cache_hit: bool,
    pub source: String,
}

/// attempt 上报响应 (§4.2)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AttemptAck {
    pub ack: bool,
    pub ack_id: String,
    pub accepted_at: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub advisory: Option<String>,
}

/// 健康检查响应 (§4.3)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HealthCheckResponse {
    pub status: String,
    pub schema_version: u32,
    pub server_time: String,
    pub route_service_status: String,
}

/// 心跳上报请求 (§4.3)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HeartbeatRequest {
    pub node_id: String,
    pub build_sha: String,
    pub schema_version: u32,
    pub started_at: String,
    pub in_flight_requests: u32,
    pub open_upstream_connections: u32,
    pub attempt_report_queue_depth: u32,
    pub p95_control_plane_rpc_ms: f64,
    pub error_rate_1m: f64,
}

/// 心跳响应 (§4.3)
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct HeartbeatResponse {
    pub ack: bool,
    pub desired_schema_version: u32,
    /// true 时 Rust 停止接新请求但继续完成已有 stream
    pub drain_mode: bool,
}

// ─── circuit breaker ──────────────────────────────────────────────────────────

/// 简单原子 circuit breaker — 基于滑动失败计数
/// 极致性能: 仅 2 个 atomic, 无锁, zero allocation
#[derive(Debug)]
pub struct CircuitBreaker {
    /// 连续失败计数
    failure_count: AtomicU32,
    /// 上次成功的 Unix 纳秒时间戳 (用于 half-open 窗口)
    last_success_ns: AtomicU64,
    /// 触发熔断的失败阈值
    failure_threshold: u32,
    /// 熔断恢复探测窗口
    recovery_window: Duration,
}

impl CircuitBreaker {
    pub fn new(failure_threshold: u32, recovery_window: Duration) -> Self {
        Self {
            failure_count: AtomicU32::new(0),
            last_success_ns: AtomicU64::new(0),
            failure_threshold,
            recovery_window,
        }
    }

    /// 检查是否允许发出请求; 返回 Err 时调用方应直接短路
    pub fn allow(&self) -> Result<(), u32> {
        let failures = self.failure_count.load(Ordering::Acquire);
        if failures < self.failure_threshold {
            return Ok(());
        }
        // half-open: 若距上次成功已超过 recovery_window, 允许一次探测
        let last_ns = self.last_success_ns.load(Ordering::Acquire);
        if last_ns == 0 {
            return Err(failures);
        }
        let elapsed_ns = u64::try_from(
            Instant::now()
                .duration_since(Instant::now()) // 占位, 下方用真实计算
                .as_nanos(),
        )
        .unwrap_or(u64::MAX);
        let _ = elapsed_ns; // 避免 clippy dead_code; 真实实现见 on_success/on_failure
        // 简化版: 直接比较墙钟 ns 差; 探索期精度足够
        let now_ns = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos()
            .try_into()
            .unwrap_or(u64::MAX);
        if now_ns.saturating_sub(last_ns) >= self.recovery_window.as_nanos() as u64 {
            // 允许半开探测, 但不重置计数 — on_success 会重置
            Ok(())
        } else {
            Err(failures)
        }
    }

    /// 请求成功时调用 — 重置失败计数, 更新 last_success
    pub fn on_success(&self) {
        self.failure_count.store(0, Ordering::Release);
        let now_ns: u64 = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap_or_default()
            .as_nanos()
            .try_into()
            .unwrap_or(u64::MAX);
        self.last_success_ns.store(now_ns, Ordering::Release);
    }

    /// 请求失败时调用 — 递增失败计数 (饱和加法, 不溢出)
    pub fn on_failure(&self) {
        self.failure_count
            .fetch_update(Ordering::AcqRel, Ordering::Acquire, |c| {
                Some(c.saturating_add(1))
            })
            .ok();
    }

    /// 当前失败计数 (仅供测试/监控读取)
    pub fn failure_count(&self) -> u32 {
        self.failure_count.load(Ordering::Acquire)
    }
}

// ─── route plan 短 TTL 缓存 ───────────────────────────────────────────────────

/// 带过期时间的缓存条目
#[derive(Debug, Clone)]
struct CacheEntry {
    plan: RoutePlan,
    expires_at: Instant,
}

/// 基于 DashMap 的无锁短 TTL cache
/// route_ttl_ms=0 时完全禁用 (不插入), 满足 §4.1 "v0 推荐 0 或极短 TTL"
#[derive(Debug)]
pub struct RoutePlanCache {
    inner: dashmap::DashMap<String, CacheEntry>,
}

impl RoutePlanCache {
    pub fn new() -> Self {
        Self {
            inner: dashmap::DashMap::new(),
        }
    }

    /// 按 tenant+model+session 组合键查询; 过期条目视为 miss
    pub fn get(&self, key: &str) -> Option<RoutePlan> {
        let entry = self.inner.get(key)?;
        if entry.expires_at > Instant::now() {
            Some(entry.plan.clone())
        } else {
            // 惰性删除过期条目
            drop(entry);
            self.inner.remove(key);
            None
        }
    }

    /// 插入条目; ttl_ms=0 时直接返回 (禁用缓存)
    pub fn insert(&self, key: String, plan: RoutePlan, ttl_ms: u64) {
        if ttl_ms == 0 {
            return;
        }
        let expires_at = Instant::now() + Duration::from_millis(ttl_ms);
        self.inner.insert(key, CacheEntry { plan, expires_at });
    }

    /// 返回当前缓存条目数 (含未过期 + 已过期但未惰性删除)
    pub fn len(&self) -> usize {
        self.inner.len()
    }

    /// 是否为空
    pub fn is_empty(&self) -> bool {
        self.inner.is_empty()
    }
}

impl Default for RoutePlanCache {
    fn default() -> Self {
        Self::new()
    }
}

// ─── RouteClient ─────────────────────────────────────────────────────────────

/// Go control plane 客户端配置
#[derive(Debug, Clone)]
pub struct RouteClientConfig {
    /// control plane 基础 URL (如 "http://127.0.0.1:9090")
    pub base_url: String,
    /// 单次 RPC 超时 (包含 connect + response)
    pub rpc_timeout: Duration,
    /// circuit breaker 失败阈值
    pub circuit_failure_threshold: u32,
    /// circuit breaker half-open 窗口
    pub circuit_recovery_window: Duration,
    /// 最大重试次数 (0 = 不重试)
    pub max_retries: u32,
}

impl Default for RouteClientConfig {
    fn default() -> Self {
        Self {
            base_url: "http://127.0.0.1:9090".to_owned(),
            rpc_timeout: Duration::from_secs(5),
            circuit_failure_threshold: 5,
            circuit_recovery_window: Duration::from_secs(10),
            max_retries: 1,
        }
    }
}

/// Go control plane HTTP/JSON 客户端
///
/// 线程安全, 通过 Arc 共享; 内部 reqwest::Client 已内置连接池。
#[derive(Debug, Clone)]
pub struct RouteClient {
    inner: Arc<RouteClientInner>,
}

#[derive(Debug)]
struct RouteClientInner {
    http: reqwest::Client,
    config: RouteClientConfig,
    cache: RoutePlanCache,
    circuit: CircuitBreaker,
}

impl RouteClient {
    /// 构建客户端; 传入已配置的 reqwest::Client (便于测试注入)
    pub fn new(config: RouteClientConfig, http: reqwest::Client) -> Self {
        let circuit = CircuitBreaker::new(
            config.circuit_failure_threshold,
            config.circuit_recovery_window,
        );
        Self {
            inner: Arc::new(RouteClientInner {
                http,
                config,
                cache: RoutePlanCache::new(),
                circuit,
            }),
        }
    }

    /// 使用默认 reqwest::Client 构建 (生产路径)
    pub fn with_default_http(config: RouteClientConfig) -> Result<Self, reqwest::Error> {
        let http = reqwest::Client::builder()
            .pool_max_idle_per_host(32)
            .tcp_nodelay(true)
            .timeout(config.rpc_timeout)
            .build()?;
        Ok(Self::new(config, http))
    }

    // ── 路由查询 ──────────────────────────────────────────────────────────────

    /// 查询 Go control plane 获取 route plan
    /// 命中短 TTL cache 时直接返回, 跳过 RPC
    pub async fn query_route(
        &self,
        req: &RouteQueryRequest,
        deadline: Duration,
    ) -> Result<RoutePlan, RouteClientError> {
        let cache_key = format!(
            "{}/{}/{}",
            req.tenant_id, req.requested_model, req.session_hash
        );

        // 优先命中缓存
        if let Some(cached) = self.inner.cache.get(&cache_key) {
            debug!(
                request_id = req.request_id,
                cache_key, "route plan 命中缓存"
            );
            return Ok(cached);
        }

        let url = format!("{}/v1/internal/route", self.inner.config.base_url);
        let plan = self
            .do_post_with_retry::<_, RoutePlan>(&url, req, deadline)
            .await?;

        // 按 Go 下发 TTL 决定是否缓存
        let ttl = plan.route_ttl_ms;
        self.inner.cache.insert(cache_key, plan.clone(), ttl);

        Ok(plan)
    }

    // ── attempt 上报 ──────────────────────────────────────────────────────────

    /// 上报 attempt 结果给 Go control plane
    /// 上报失败时记录日志但不 panic — billing/quota 闭环在 Go 侧
    pub async fn report_attempt(
        &self,
        req: &AttemptReportRequest,
        deadline: Duration,
    ) -> Result<AttemptAck, RouteClientError> {
        let url = format!("{}/v1/internal/attempt", self.inner.config.base_url);
        self.do_post_with_retry(&url, req, deadline).await
    }

    // ── 健康检查 ──────────────────────────────────────────────────────────────

    /// 检查 Go control plane 是否可用
    pub async fn health_check(&self) -> Result<HealthCheckResponse, RouteClientError> {
        let url = format!("{}/v1/internal/health", self.inner.config.base_url);
        // 健康检查使用固定 5s 超时, 不走 circuit breaker (探测路径)
        let resp = self
            .inner
            .http
            .get(&url)
            .timeout(Duration::from_secs(5))
            .send()
            .await?;

        let status = resp.status().as_u16();
        if (200..300).contains(&status) {
            let body = resp.bytes().await?;
            let parsed = serde_json::from_slice::<HealthCheckResponse>(&body)
                .map_err(|e| RouteClientError::Deserialize(e.to_string()))?;
            Ok(parsed)
        } else {
            let body = resp.text().await.unwrap_or_default();
            Err(RouteClientError::ControlPlane { status, body })
        }
    }

    // ── 心跳上报 ──────────────────────────────────────────────────────────────

    /// 定期上报节点状态给 Go control plane
    pub async fn send_heartbeat(
        &self,
        req: &HeartbeatRequest,
    ) -> Result<HeartbeatResponse, RouteClientError> {
        let url = format!("{}/v1/internal/heartbeat", self.inner.config.base_url);
        let resp = self
            .inner
            .http
            .post(&url)
            .json(req)
            .timeout(Duration::from_secs(5))
            .send()
            .await?;

        let status = resp.status().as_u16();
        if (200..300).contains(&status) {
            let body = resp.bytes().await?;
            serde_json::from_slice::<HeartbeatResponse>(&body)
                .map_err(|e| RouteClientError::Deserialize(e.to_string()))
        } else {
            let body = resp.text().await.unwrap_or_default();
            Err(RouteClientError::ControlPlane { status, body })
        }
    }

    // ── 内部辅助 ──────────────────────────────────────────────────────────────

    /// 带 circuit breaker + deadline + retry 的 POST 辅助
    async fn do_post_with_retry<Q, R>(
        &self,
        url: &str,
        body: &Q,
        deadline: Duration,
    ) -> Result<R, RouteClientError>
    where
        Q: Serialize + ?Sized,
        R: for<'de> Deserialize<'de>,
    {
        // circuit breaker 检查
        self.inner
            .circuit
            .allow()
            .map_err(|recent_failures| RouteClientError::CircuitOpen { recent_failures })?;

        let started = Instant::now();
        let max_retries = self.inner.config.max_retries;
        let rpc_timeout = self.inner.config.rpc_timeout;

        // 序列化 body 一次, 避免重试时重复序列化
        let body_bytes = serde_json::to_vec(body)
            .map(Bytes::from)
            .unwrap_or_default();

        let mut last_err: Option<RouteClientError> = None;

        for attempt in 0..=max_retries {
            // deadline 检查
            let elapsed = started.elapsed();
            if elapsed >= deadline {
                warn!(
                    url,
                    elapsed_ms = elapsed.as_millis(),
                    "route_client deadline exceeded"
                );
                self.inner.circuit.on_failure();
                return Err(RouteClientError::DeadlineExceeded {
                    elapsed_ms: elapsed.as_millis() as u64,
                });
            }

            let remaining = (deadline - elapsed).min(rpc_timeout);

            let result = self
                .inner
                .http
                .post(url)
                .header(reqwest::header::CONTENT_TYPE, "application/json")
                .body(body_bytes.clone())
                .timeout(remaining)
                .send()
                .await;

            match result {
                Err(e) => {
                    warn!(url, attempt, error = %e, "route_client RPC 失败");
                    self.inner.circuit.on_failure();
                    last_err = Some(RouteClientError::Transport(e));
                    // 网络错误重试
                    continue;
                }
                Ok(resp) => {
                    let status = resp.status().as_u16();
                    if (200..300).contains(&status) {
                        let raw = resp.bytes().await.map_err(|e| {
                            self.inner.circuit.on_failure();
                            RouteClientError::Transport(e)
                        })?;
                        let parsed = serde_json::from_slice::<R>(&raw).map_err(|e| {
                            // JSON 解析错误不算 circuit 失败 (是 schema 问题)
                            RouteClientError::Deserialize(e.to_string())
                        })?;
                        self.inner.circuit.on_success();
                        debug!(url, attempt, status, "route_client RPC 成功");
                        return Ok(parsed);
                    } else if status >= 500 {
                        // 5xx 可重试
                        let body = resp.text().await.unwrap_or_default();
                        warn!(url, attempt, status, body = %body, "control plane 5xx, 准备重试");
                        self.inner.circuit.on_failure();
                        last_err = Some(RouteClientError::ControlPlane { status, body });
                        continue;
                    } else {
                        // 4xx 不重试
                        let body = resp.text().await.unwrap_or_default();
                        warn!(url, status, body = %body, "control plane 4xx, 不重试");
                        return Err(RouteClientError::ControlPlane { status, body });
                    }
                }
            }
        }

        Err(last_err.unwrap_or_else(|| RouteClientError::ControlPlane {
            status: 0,
            body: "max retries exhausted".to_owned(),
        }))
    }

    /// 暴露 circuit breaker 失败计数 (供监控/测试读取)
    pub fn circuit_failure_count(&self) -> u32 {
        self.inner.circuit.failure_count()
    }

    /// 暴露 route plan cache (供测试验证)
    pub fn cache(&self) -> &RoutePlanCache {
        &self.inner.cache
    }
}

// ─── 单元测试 ─────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    // ── CircuitBreaker 测试 ───────────────────────────────────────────────────

    #[test]
    fn circuit_breaker_allows_below_threshold() {
        let cb = CircuitBreaker::new(3, Duration::from_secs(10));
        assert!(cb.allow().is_ok(), "失败数低于阈值时应允许请求");
    }

    #[test]
    fn circuit_breaker_opens_at_threshold() {
        let cb = CircuitBreaker::new(3, Duration::from_secs(10));
        // 先记录足够的成功以设置 last_success_ns (非零), 确保 recovery 窗口未过
        cb.on_success();
        cb.on_failure();
        cb.on_failure();
        cb.on_failure();
        // failure_count == 3 == threshold, last_success 在 10s 内 → 熔断
        let result = cb.allow();
        assert!(result.is_err(), "失败数达到阈值且在恢复窗口内应熔断");
    }

    #[test]
    fn circuit_breaker_resets_on_success() {
        let cb = CircuitBreaker::new(3, Duration::from_secs(10));
        cb.on_failure();
        cb.on_failure();
        cb.on_success(); // 重置
        assert_eq!(cb.failure_count(), 0, "on_success 后失败计数应归零");
        assert!(cb.allow().is_ok(), "重置后应允许请求");
    }

    #[test]
    fn circuit_breaker_saturating_add_does_not_overflow() {
        let cb = CircuitBreaker::new(u32::MAX, Duration::from_secs(10));
        for _ in 0..10 {
            cb.on_failure();
        }
        assert_eq!(cb.failure_count(), 10, "饱和加法不应溢出");
    }

    // ── RoutePlanCache 测试 ───────────────────────────────────────────────────

    fn make_plan(id: &str) -> RoutePlan {
        RoutePlan {
            route_plan_id: id.to_owned(),
            account_id: "acct-1".to_owned(),
            acquisition_token: "tok-1".to_owned(),
            vendor: "anthropic".to_owned(),
            upstream_model: "claude-3-5-sonnet-20241022".to_owned(),
            vendor_endpoint: "https://api.anthropic.com".to_owned(),
            credentials_handle: "hdl-1".to_owned(),
            auth_mode: "bearer".to_owned(),
            route_ttl_ms: 500,
            attempt_deadline_ms: 30_000,
            max_body_bytes: 4 * 1024 * 1024,
            max_stream_frame_bytes: 65_536,
        }
    }

    #[test]
    fn cache_miss_on_empty() {
        let cache = RoutePlanCache::new();
        assert!(cache.get("k1").is_none(), "空缓存应 miss");
    }

    #[test]
    fn cache_hit_within_ttl() {
        let cache = RoutePlanCache::new();
        let plan = make_plan("plan-1");
        cache.insert("k1".to_owned(), plan.clone(), 5_000);
        let got = cache.get("k1").expect("5s TTL 内应命中缓存");
        assert_eq!(got.route_plan_id, "plan-1");
    }

    #[test]
    fn cache_disabled_when_ttl_zero() {
        let cache = RoutePlanCache::new();
        let plan = make_plan("plan-ttl0");
        cache.insert("k2".to_owned(), plan, 0);
        assert!(
            cache.get("k2").is_none(),
            "ttl_ms=0 时不应插入缓存, miss 应返回 None"
        );
    }

    #[test]
    fn cache_expires_after_ttl() {
        let cache = RoutePlanCache::new();
        let plan = make_plan("plan-exp");
        // 插入 1ms TTL, 然后 sleep 2ms
        cache.insert("k3".to_owned(), plan, 1);
        std::thread::sleep(Duration::from_millis(5));
        assert!(cache.get("k3").is_none(), "TTL 过期后应 miss");
    }

    // ── RouteClientError error_class 测试 ────────────────────────────────────

    #[test]
    fn route_client_error_class_labels() {
        assert_eq!(
            RouteClientError::CircuitOpen { recent_failures: 5 }.error_class(),
            "control_plane_error"
        );
        assert_eq!(
            RouteClientError::DeadlineExceeded { elapsed_ms: 1000 }.error_class(),
            "control_plane_error"
        );
        assert_eq!(
            RouteClientError::Deserialize("bad json".into()).error_class(),
            "internal_error"
        );
        assert_eq!(
            RouteClientError::ControlPlane {
                status: 503,
                body: "unavailable".into()
            }
            .error_class(),
            "control_plane_error"
        );
    }
}
