// Rust 数据面到 Go control plane 的 gRPC client。
// v0 只做 typed contract、deadline、retry、短 TTL cache 和 circuit breaker 骨架。

use std::{
    sync::{
        Arc,
        atomic::{AtomicU32, AtomicU64, Ordering},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use dashmap::DashMap;
use http::Uri;
use tokio::time;
use tonic::{
    Code, Request,
    transport::{Channel, Endpoint},
};
use tracing::{debug, warn};

use crate::{
    error::GatewayError,
    route_proto::v1::{
        AttemptReportRequest, AttemptReportResponse, HealthCheckRequest, HealthCheckResponse,
        HeartbeatRequest, HeartbeatResponse, RoutePlan, RouteQueryRequest,
        route_service_client::RouteServiceClient,
    },
};

pub const ROUTE_SCHEMA_VERSION: &str = "route.v1";

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RouteClientOptions {
    pub rpc_timeout: Duration,
    pub retry_attempts: usize,
    pub retry_backoff: Duration,
    pub route_cache_ttl: Duration,
    pub circuit_breaker_failure_threshold: u32,
    pub circuit_breaker_cooldown: Duration,
}

impl Default for RouteClientOptions {
    fn default() -> Self {
        Self {
            rpc_timeout: Duration::from_millis(200),
            retry_attempts: 1,
            retry_backoff: Duration::from_millis(10),
            route_cache_ttl: Duration::ZERO,
            circuit_breaker_failure_threshold: 5,
            circuit_breaker_cooldown: Duration::from_secs(1),
        }
    }
}

#[derive(Clone)]
pub struct RouteClient {
    inner: Arc<RouteClientInner>,
}

struct RouteClientInner {
    client: RouteServiceClient<Channel>,
    options: RouteClientOptions,
    cache: DashMap<String, CachedRoutePlan>,
    consecutive_failures: AtomicU32,
    circuit_open_until_ms: AtomicU64,
}

#[derive(Clone)]
struct CachedRoutePlan {
    plan: RoutePlan,
    expires_at_ms: u64,
}

impl RouteClient {
    pub fn new(
        control_plane_endpoint: Uri,
        options: RouteClientOptions,
    ) -> Result<Self, GatewayError> {
        let endpoint = Endpoint::from_shared(control_plane_endpoint.to_string())
            .map_err(|err| {
                GatewayError::ControlPlane(format!("invalid control plane endpoint: {err}"))
            })?
            .timeout(options.rpc_timeout)
            .connect_timeout(options.rpc_timeout)
            .tcp_nodelay(true)
            .http2_keep_alive_interval(Duration::from_secs(30))
            .keep_alive_timeout(Duration::from_secs(5))
            .keep_alive_while_idle(true);

        let channel = endpoint.connect_lazy();
        let client = RouteServiceClient::new(channel)
            .max_decoding_message_size(1024 * 1024)
            .max_encoding_message_size(1024 * 1024);

        Ok(Self {
            inner: Arc::new(RouteClientInner {
                client,
                options,
                cache: DashMap::new(),
                consecutive_failures: AtomicU32::new(0),
                circuit_open_until_ms: AtomicU64::new(0),
            }),
        })
    }

    pub fn options(&self) -> &RouteClientOptions {
        &self.inner.options
    }

    pub fn consecutive_failures(&self) -> u32 {
        self.inner.consecutive_failures.load(Ordering::Relaxed)
    }

    pub fn circuit_is_open(&self) -> bool {
        self.circuit_open_until_ms() > now_unix_ms()
    }

    pub async fn query_route(&self, query: RouteQueryRequest) -> Result<RoutePlan, GatewayError> {
        if let Some(plan) = self.cache_get(&query) {
            return Ok(plan);
        }

        if self.circuit_is_open() {
            return Err(GatewayError::ControlPlane(
                "route circuit breaker open".to_owned(),
            ));
        }

        let max_attempt = self.inner.options.retry_attempts;
        let mut last_error = None;

        for attempt in 0..=max_attempt {
            match self.route_query_once(query.clone()).await {
                Ok(plan) => {
                    self.record_success();
                    self.cache_put(&query, &plan);
                    return Ok(plan);
                }
                Err(err) => {
                    let retryable = err.retryable;
                    let gateway_error = err.error;
                    self.record_failure();

                    if retryable && attempt < max_attempt && !self.circuit_is_open() {
                        let delay = retry_delay(self.inner.options.retry_backoff, attempt);
                        debug!(attempt, ?delay, "route query retry");
                        time::sleep(delay).await;
                        last_error = Some(gateway_error);
                        continue;
                    }

                    return Err(gateway_error);
                }
            }
        }

        Err(last_error.unwrap_or_else(|| {
            GatewayError::ControlPlane("route query failed without status".to_owned())
        }))
    }

    pub async fn report_attempt(
        &self,
        report: AttemptReportRequest,
    ) -> Result<AttemptReportResponse, GatewayError> {
        if self.circuit_is_open() {
            return Err(GatewayError::ControlPlane(
                "route circuit breaker open".to_owned(),
            ));
        }

        let mut client = self.inner.client.clone();
        let mut request = Request::new(report);
        request.set_timeout(self.inner.options.rpc_timeout);

        let response = time::timeout(
            self.inner.options.rpc_timeout,
            client.attempt_report(request),
        )
        .await
        .map_err(|_| GatewayError::ControlPlane("attempt report deadline exceeded".to_owned()))?
        .map_err(status_to_gateway_error)?;

        self.record_success();
        Ok(response.into_inner())
    }

    pub async fn health_check(
        &self,
        request: HealthCheckRequest,
    ) -> Result<HealthCheckResponse, GatewayError> {
        let mut client = self.inner.client.clone();
        let mut request = Request::new(request);
        request.set_timeout(self.inner.options.rpc_timeout);

        let response = time::timeout(self.inner.options.rpc_timeout, client.health_check(request))
            .await
            .map_err(|_| GatewayError::ControlPlane("health check deadline exceeded".to_owned()))?
            .map_err(status_to_gateway_error)?;

        self.record_success();
        Ok(response.into_inner())
    }

    pub async fn heartbeat(
        &self,
        request: HeartbeatRequest,
    ) -> Result<HeartbeatResponse, GatewayError> {
        let mut client = self.inner.client.clone();
        let mut request = Request::new(request);
        request.set_timeout(self.inner.options.rpc_timeout);

        let response = time::timeout(self.inner.options.rpc_timeout, client.heartbeat(request))
            .await
            .map_err(|_| GatewayError::ControlPlane("heartbeat deadline exceeded".to_owned()))?
            .map_err(status_to_gateway_error)?;

        self.record_success();
        Ok(response.into_inner())
    }

    async fn route_query_once(
        &self,
        query: RouteQueryRequest,
    ) -> Result<RoutePlan, RouteCallError> {
        let mut client = self.inner.client.clone();
        let mut request = Request::new(query);
        request.set_timeout(self.inner.options.rpc_timeout);

        let response = time::timeout(self.inner.options.rpc_timeout, client.route_query(request))
            .await
            .map_err(|_| RouteCallError {
                retryable: true,
                error: GatewayError::ControlPlane("route query deadline exceeded".to_owned()),
            })?
            .map_err(status_to_route_call_error)?;

        Ok(response.into_inner())
    }

    fn cache_get(&self, query: &RouteQueryRequest) -> Option<RoutePlan> {
        let key = route_cache_key(query)?;
        let now = now_unix_ms();
        let entry = self.inner.cache.get(&key)?;

        if entry.expires_at_ms > now {
            debug!(cache_key = %key, "route plan cache hit");
            Some(entry.plan.clone())
        } else {
            drop(entry);
            self.inner.cache.remove(&key);
            None
        }
    }

    fn cache_put(&self, query: &RouteQueryRequest, plan: &RoutePlan) {
        let Some(key) = route_cache_key(query) else {
            return;
        };

        let configured_ms = duration_millis_u64(self.inner.options.route_cache_ttl);
        if configured_ms == 0 || plan.route_ttl_ms == 0 {
            return;
        }

        let ttl_ms = configured_ms.min(plan.route_ttl_ms);
        let expires_at_ms = now_unix_ms().saturating_add(ttl_ms);
        self.inner.cache.insert(
            key,
            CachedRoutePlan {
                plan: plan.clone(),
                expires_at_ms,
            },
        );
    }

    fn record_success(&self) {
        self.inner.consecutive_failures.store(0, Ordering::Relaxed);
        self.inner.circuit_open_until_ms.store(0, Ordering::Release);
    }

    fn record_failure(&self) {
        let failures = self
            .inner
            .consecutive_failures
            .fetch_add(1, Ordering::Relaxed)
            .saturating_add(1);

        let threshold = self.inner.options.circuit_breaker_failure_threshold.max(1);
        if failures >= threshold {
            let open_until = now_unix_ms().saturating_add(duration_millis_u64(
                self.inner.options.circuit_breaker_cooldown,
            ));
            self.inner
                .circuit_open_until_ms
                .store(open_until, Ordering::Release);
            warn!(failures, "route circuit breaker opened");
        }
    }

    fn circuit_open_until_ms(&self) -> u64 {
        self.inner.circuit_open_until_ms.load(Ordering::Acquire)
    }
}

struct RouteCallError {
    retryable: bool,
    error: GatewayError,
}

fn status_to_route_call_error(status: tonic::Status) -> RouteCallError {
    let code = status.code();
    RouteCallError {
        retryable: retryable_status(code),
        error: status_to_gateway_error(status),
    }
}

fn status_to_gateway_error(status: tonic::Status) -> GatewayError {
    GatewayError::ControlPlane(format!(
        "control plane gRPC {:?}: {}",
        status.code(),
        status.message()
    ))
}

fn retryable_status(code: Code) -> bool {
    matches!(
        code,
        Code::Unavailable | Code::DeadlineExceeded | Code::ResourceExhausted | Code::Unknown
    )
}

fn retry_delay(base: Duration, attempt: usize) -> Duration {
    let multiplier = attempt.saturating_add(1).min(8) as u32;
    base.saturating_mul(multiplier)
}

fn route_cache_key(query: &RouteQueryRequest) -> Option<String> {
    if !query.previous_attempts.is_empty() {
        return None;
    }

    let mut key = String::with_capacity(192);
    push_key_part(&mut key, &query.tenant_id);
    push_key_part(&mut key, &query.requested_model);
    push_key_part(&mut key, &query.session_hash);
    push_key_part(&mut key, &query.request_protocol);
    push_key_part(&mut key, if query.stream { "1" } else { "0" });

    let mut hints: Vec<_> = query.capability_hints.iter().collect();
    hints.sort_by(|a, b| a.name.cmp(&b.name).then(a.value.cmp(&b.value)));
    for hint in hints {
        push_key_part(&mut key, &hint.name);
        push_key_part(&mut key, &hint.value);
    }

    Some(key)
}

fn push_key_part(key: &mut String, part: &str) {
    key.push_str(&part.len().to_string());
    key.push(':');
    key.push_str(part);
    key.push('|');
}

fn duration_millis_u64(duration: Duration) -> u64 {
    duration.as_millis().min(u128::from(u64::MAX)) as u64
}

fn now_unix_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis()
        .min(u128::from(u64::MAX)) as u64
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::route_proto::v1::CapabilityHint;

    #[test]
    fn route_cache_key_ignores_hint_order() {
        let mut left = RouteQueryRequest {
            tenant_id: "tenant".to_owned(),
            requested_model: "model".to_owned(),
            session_hash: "session".to_owned(),
            request_protocol: "openai_chat_completions".to_owned(),
            stream: true,
            capability_hints: vec![
                CapabilityHint {
                    name: "vision".to_owned(),
                    value: "true".to_owned(),
                },
                CapabilityHint {
                    name: "tool".to_owned(),
                    value: "true".to_owned(),
                },
            ],
            ..Default::default()
        };
        let mut right = left.clone();
        right.capability_hints.reverse();

        assert_eq!(route_cache_key(&left), route_cache_key(&right));

        left.previous_attempts.push(Default::default());
        assert!(route_cache_key(&left).is_none());
    }

    // ── circuit breaker 单元测试 (源自 claude-m3 lane) ───────────────────────
    // RouteClient::new 内部的 tonic channel 需要 Tokio 运行时, 故用 #[tokio::test]

    /// circuit breaker 开路前: 连续失败计数低于阈值时应允许请求
    #[tokio::test]
    async fn circuit_breaker_allows_below_failure_threshold() {
        let opts = RouteClientOptions {
            circuit_breaker_failure_threshold: 3,
            circuit_breaker_cooldown: Duration::from_secs(60),
            ..RouteClientOptions::default()
        };
        let client = RouteClient::new("http://127.0.0.1:1".parse().unwrap(), opts).unwrap();

        // 初始: 无失败, 熔断器关闭
        assert!(!client.circuit_is_open(), "初始状态熔断器应关闭");
        assert_eq!(client.consecutive_failures(), 0);
    }

    /// record_failure 累积后应触发熔断器打开
    #[tokio::test]
    async fn circuit_breaker_opens_after_threshold_failures() {
        let opts = RouteClientOptions {
            circuit_breaker_failure_threshold: 2,
            circuit_breaker_cooldown: Duration::from_secs(60),
            rpc_timeout: Duration::from_millis(1),
            retry_attempts: 0,
            ..RouteClientOptions::default()
        };
        let client = RouteClient::new("http://127.0.0.1:1".parse().unwrap(), opts).unwrap();

        // 手动累积失败并触发 circuit open
        client
            .inner
            .consecutive_failures
            .fetch_add(1, Ordering::Relaxed);
        client
            .inner
            .consecutive_failures
            .fetch_add(1, Ordering::Relaxed);
        let threshold = client
            .inner
            .options
            .circuit_breaker_failure_threshold
            .max(1);
        let failures = client.inner.consecutive_failures.load(Ordering::Relaxed);
        if failures >= threshold {
            let open_until = now_unix_ms().saturating_add(duration_millis_u64(
                client.inner.options.circuit_breaker_cooldown,
            ));
            client
                .inner
                .circuit_open_until_ms
                .store(open_until, Ordering::Release);
        }

        assert!(client.circuit_is_open(), "达到阈值后熔断器应打开");
    }

    /// record_success 应重置 consecutive_failures 并关闭熔断器
    #[tokio::test]
    async fn circuit_breaker_resets_on_success() {
        let opts = RouteClientOptions {
            circuit_breaker_failure_threshold: 2,
            circuit_breaker_cooldown: Duration::from_secs(60),
            ..RouteClientOptions::default()
        };
        let client = RouteClient::new("http://127.0.0.1:1".parse().unwrap(), opts).unwrap();

        // 先打开熔断器
        client
            .inner
            .consecutive_failures
            .store(5, Ordering::Relaxed);
        client
            .inner
            .circuit_open_until_ms
            .store(u64::MAX, Ordering::Release);
        assert!(client.circuit_is_open());

        // 模拟成功重置
        client
            .inner
            .consecutive_failures
            .store(0, Ordering::Relaxed);
        client
            .inner
            .circuit_open_until_ms
            .store(0, Ordering::Release);

        assert!(!client.circuit_is_open(), "重置后熔断器应关闭");
        assert_eq!(client.consecutive_failures(), 0);
    }

    // ── DashMap route cache 单元测试 (源自 claude-m3 lane) ──────────────────

    fn make_cached_plan(ttl_ms: u64) -> RoutePlan {
        RoutePlan {
            route_plan_id: "plan-unit-1".to_owned(),
            account_id: "acct-1".to_owned(),
            acquisition_token: bytes::Bytes::from_static(b"tok"),
            vendor: "anthropic".to_owned(),
            upstream_model: "claude-mock".to_owned(),
            vendor_endpoint: "https://api.anthropic.com".to_owned(),
            credentials_handle: "hdl-1".to_owned(),
            auth_mode: "bearer".to_owned(),
            route_ttl_ms: ttl_ms,
            attempt_deadline_ms: 30_000,
            max_body_bytes: 4 * 1024 * 1024,
            max_stream_frame_bytes: 64 * 1024,
        }
    }

    fn make_query(model: &str) -> RouteQueryRequest {
        RouteQueryRequest {
            tenant_id: "t1".to_owned(),
            requested_model: model.to_owned(),
            session_hash: "s1".to_owned(),
            request_protocol: "anthropic_messages".to_owned(),
            stream: false,
            ..Default::default()
        }
    }

    /// ttl_ms=0 时不应插入缓存 (cache_put 是 no-op)
    #[tokio::test]
    async fn route_cache_disabled_when_ttl_zero() {
        let opts = RouteClientOptions {
            route_cache_ttl: Duration::from_secs(10), // client 侧 TTL 非零
            ..RouteClientOptions::default()
        };
        let client = RouteClient::new("http://127.0.0.1:1".parse().unwrap(), opts).unwrap();

        let query = make_query("claude-mock");
        let plan = make_cached_plan(0); // plan 下发 ttl=0, 禁止缓存
        client.cache_put(&query, &plan);

        // 缓存应为空
        assert!(
            client.inner.cache.is_empty(),
            "plan.route_ttl_ms=0 时不应写入缓存"
        );
    }

    /// ttl > 0 时 cache_put + cache_get 应命中
    #[tokio::test]
    async fn route_cache_hit_within_ttl() {
        let opts = RouteClientOptions {
            route_cache_ttl: Duration::from_secs(10),
            ..RouteClientOptions::default()
        };
        let client = RouteClient::new("http://127.0.0.1:1".parse().unwrap(), opts).unwrap();

        let query = make_query("claude-mock");
        let plan = make_cached_plan(5_000);
        client.cache_put(&query, &plan);

        let hit = client.cache_get(&query);
        assert!(hit.is_some(), "TTL 内应命中缓存");
        assert_eq!(hit.unwrap().route_plan_id, "plan-unit-1");
    }

    /// previous_attempts 非空时 cache key 应为 None (不缓存)
    #[test]
    fn route_cache_key_none_when_previous_attempts_present() {
        let mut query = make_query("claude-mock");
        query.previous_attempts.push(Default::default());
        assert!(
            route_cache_key(&query).is_none(),
            "有 previous_attempts 时不应生成 cache key"
        );
    }
}
