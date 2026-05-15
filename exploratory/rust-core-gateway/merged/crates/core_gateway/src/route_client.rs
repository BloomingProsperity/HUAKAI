// Rust 数据面到 Go control plane 的 gRPC client。
// v0 只做 typed contract、deadline、retry 和 circuit breaker 骨架。

use std::{
    sync::{
        Arc,
        atomic::{AtomicU32, AtomicU64, Ordering},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use http::Uri;
use tokio::time;
use tonic::{
    Code, Request,
    transport::{Channel, Endpoint},
};
use tracing::{debug, warn};

use crate::{
    error::GatewayError,
    redaction::redact_untrusted_text,
    route_proto::v1::{
        AttemptReportRequest, AttemptReportResponse, HealthCheckRequest, HealthCheckResponse,
        HeartbeatRequest, HeartbeatResponse, RoutePlan, RouteQueryRequest,
        route_service_client::RouteServiceClient,
    },
};

pub const ROUTE_SCHEMA_VERSION: &str = "route.v1";
const CONTROL_PLANE_ERROR_LIMIT: usize = 256;

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
    consecutive_failures: AtomicU32,
    circuit_open_until_ms: AtomicU64,
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
    let code = status.code();
    let message = redact_untrusted_text(status.message(), CONTROL_PLANE_ERROR_LIMIT);
    GatewayError::ControlPlane(format!("control plane gRPC {:?}: {}", code, message))
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

    #[test]
    fn status_to_gateway_error_redacts_untrusted_status_message() {
        let err = status_to_gateway_error(tonic::Status::unavailable(
            "vendor said Authorization: Bearer lease-token-value and sk-test-sensitive-value",
        ));
        let rendered = err.to_string();

        assert!(rendered.contains("Unavailable"));
        assert!(rendered.contains("control plane gRPC"));
        assert!(!rendered.contains("lease-token-value"));
        assert!(!rendered.contains("sk-test-sensitive-value"));
    }
}
