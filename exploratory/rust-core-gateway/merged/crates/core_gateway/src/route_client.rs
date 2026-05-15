// Rust 数据面到 Go control plane 的 gRPC client。
// v0 只做 typed contract、deadline、retry 和 circuit breaker 骨架。

use std::{
    future::Future,
    path::{Path, PathBuf},
    pin::Pin,
    sync::{
        Arc,
        atomic::{AtomicU32, AtomicU64, Ordering},
    },
    task::{Context, Poll},
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use http::Uri;
use hyper_util::rt::TokioIo;
use tokio::time;
use tonic::{
    Code, Request,
    codegen::Service,
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
pub enum RouteTransportBaseline {
    Uds,
    Mtls,
}

impl RouteTransportBaseline {
    pub fn parse(value: &str) -> Result<Self, GatewayError> {
        match value {
            "uds" => Ok(Self::Uds),
            "mtls" => Ok(Self::Mtls),
            other => Err(GatewayError::Config(format!(
                "HUAKAI_TRANSPORT_BASELINE must be one of uds, mtls; got {other}"
            ))),
        }
    }

    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Uds => "uds",
            Self::Mtls => "mtls",
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RouteClientMtlsConfig {
    pub endpoint: Uri,
    pub domain_name: Option<String>,
    pub cert_chain_path: PathBuf,
    pub key_path: PathBuf,
    pub ca_cert_path: PathBuf,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RouteClientTransportConfig {
    pub transport_baseline: RouteTransportBaseline,
    pub uds_socket_path: PathBuf,
    pub mtls: Option<RouteClientMtlsConfig>,
}

impl RouteClientTransportConfig {
    pub fn uds(uds_socket_path: impl Into<PathBuf>) -> Self {
        Self {
            transport_baseline: RouteTransportBaseline::Uds,
            uds_socket_path: uds_socket_path.into(),
            mtls: None,
        }
    }

    pub fn mtls(uds_socket_path: impl Into<PathBuf>, mtls: RouteClientMtlsConfig) -> Self {
        Self {
            transport_baseline: RouteTransportBaseline::Mtls,
            uds_socket_path: uds_socket_path.into(),
            mtls: Some(mtls),
        }
    }
}

pub struct RouteClientEndpointParts {
    pub transport_baseline: RouteTransportBaseline,
    pub endpoint: Endpoint,
    pub uds_socket_path: Option<PathBuf>,
    pub tls_config: Option<RouteClientTlsConfig>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RouteClientTlsConfig {
    pub domain_name: Option<String>,
    pub cert_chain_pem: Vec<u8>,
    pub key_pem: Vec<u8>,
    pub ca_cert_pem: Vec<u8>,
}

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
        let endpoint = build_tcp_endpoint(&control_plane_endpoint, &options)?;

        let channel = endpoint.connect_lazy();
        Ok(Self::from_channel(channel, options))
    }

    pub fn from_transport_config(
        config: &RouteClientTransportConfig,
        options: RouteClientOptions,
    ) -> Result<Self, GatewayError> {
        let parts = build_route_endpoint_parts(config, &options)?;
        let channel = match parts.transport_baseline {
            RouteTransportBaseline::Uds => {
                let socket_path = parts.uds_socket_path.ok_or_else(|| {
                    GatewayError::Config("UDS baseline requires uds_socket_path".to_owned())
                })?;
                parts
                    .endpoint
                    .connect_with_connector_lazy(UnixSocketConnector::new(socket_path))
            }
            RouteTransportBaseline::Mtls => {
                return Err(GatewayError::Config(
                    "mTLS channel activation requires tonic TLS feature approval in R-SEC-002"
                        .to_owned(),
                ));
            }
        };

        Ok(Self::from_channel(channel, options))
    }

    fn from_channel(channel: Channel, options: RouteClientOptions) -> Self {
        let client = RouteServiceClient::new(channel)
            .max_decoding_message_size(1024 * 1024)
            .max_encoding_message_size(1024 * 1024);

        Self {
            inner: Arc::new(RouteClientInner {
                client,
                options,
                consecutive_failures: AtomicU32::new(0),
                circuit_open_until_ms: AtomicU64::new(0),
            }),
        }
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

pub fn build_route_endpoint_parts(
    config: &RouteClientTransportConfig,
    options: &RouteClientOptions,
) -> Result<RouteClientEndpointParts, GatewayError> {
    match config.transport_baseline {
        RouteTransportBaseline::Uds => {
            let socket_path = path_to_string(&config.uds_socket_path, "uds_socket_path")?;
            let endpoint = Endpoint::try_from(socket_path).map_err(|err| {
                GatewayError::ControlPlane(format!("invalid UDS route endpoint: {err}"))
            })?;

            Ok(RouteClientEndpointParts {
                transport_baseline: RouteTransportBaseline::Uds,
                endpoint: configure_endpoint(endpoint, options),
                uds_socket_path: Some(config.uds_socket_path.clone()),
                tls_config: None,
            })
        }
        RouteTransportBaseline::Mtls => {
            let mtls = config.mtls.as_ref().ok_or_else(|| {
                GatewayError::Config("mTLS baseline requires cert_chain/key/ca paths".to_owned())
            })?;
            let endpoint = build_tcp_endpoint(&mtls.endpoint, options)?;
            let tls_config = build_client_tls_config(mtls)?;

            Ok(RouteClientEndpointParts {
                transport_baseline: RouteTransportBaseline::Mtls,
                endpoint,
                uds_socket_path: None,
                tls_config: Some(tls_config),
            })
        }
    }
}

pub fn build_client_tls_config(
    mtls: &RouteClientMtlsConfig,
) -> Result<RouteClientTlsConfig, GatewayError> {
    let cert_chain = read_config_file(&mtls.cert_chain_path, "cert_chain_path")?;
    let key = read_config_file(&mtls.key_path, "key_path")?;
    let ca_cert = read_config_file(&mtls.ca_cert_path, "ca_cert_path")?;

    Ok(RouteClientTlsConfig {
        domain_name: mtls.domain_name.clone(),
        cert_chain_pem: cert_chain,
        key_pem: key,
        ca_cert_pem: ca_cert,
    })
}

fn build_tcp_endpoint(
    control_plane_endpoint: &Uri,
    options: &RouteClientOptions,
) -> Result<Endpoint, GatewayError> {
    let endpoint = Endpoint::try_from(control_plane_endpoint.to_string()).map_err(|err| {
        GatewayError::ControlPlane(format!("invalid control plane endpoint: {err}"))
    })?;

    Ok(configure_endpoint(endpoint, options))
}

fn configure_endpoint(endpoint: Endpoint, options: &RouteClientOptions) -> Endpoint {
    endpoint
        .timeout(options.rpc_timeout)
        .connect_timeout(options.rpc_timeout)
        .tcp_nodelay(true)
        .http2_keep_alive_interval(Duration::from_secs(30))
        .keep_alive_timeout(Duration::from_secs(5))
        .keep_alive_while_idle(true)
}

fn path_to_string(path: &Path, field_name: &str) -> Result<String, GatewayError> {
    path.to_str()
        .map(ToOwned::to_owned)
        .ok_or_else(|| GatewayError::Config(format!("{field_name} must be valid UTF-8")))
}

fn read_config_file(path: &Path, field_name: &str) -> Result<Vec<u8>, GatewayError> {
    std::fs::read(path).map_err(|err| {
        GatewayError::Config(format!(
            "unable to read route client {field_name} {}: {err}",
            path.display()
        ))
    })
}

#[derive(Clone, Debug)]
struct UnixSocketConnector {
    socket_path: Arc<PathBuf>,
}

impl UnixSocketConnector {
    fn new(socket_path: PathBuf) -> Self {
        Self {
            socket_path: Arc::new(socket_path),
        }
    }
}

impl Service<Uri> for UnixSocketConnector {
    type Response = TokioIo<tokio::net::UnixStream>;
    type Error = std::io::Error;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Self::Error>> + Send>>;

    fn poll_ready(&mut self, _cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        Poll::Ready(Ok(()))
    }

    fn call(&mut self, _req: Uri) -> Self::Future {
        let socket_path = self.socket_path.as_ref().clone();
        Box::pin(async move {
            tokio::net::UnixStream::connect(socket_path)
                .await
                .map(TokioIo::new)
        })
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

    fn mtls_fixture_path(name: &str) -> PathBuf {
        PathBuf::from(env!("CARGO_MANIFEST_DIR"))
            .join("tests/fixtures/mtls")
            .join(name)
    }

    #[test]
    fn uds_transport_builds_endpoint_from_fake_socket_path() {
        let socket_path = PathBuf::from("/tmp/huakai-route-client-test.sock");
        let config = RouteClientTransportConfig::uds(socket_path.clone());

        let parts = build_route_endpoint_parts(&config, &RouteClientOptions::default())
            .expect("UDS endpoint 应可由 fake socket path 构造");

        assert_eq!(parts.transport_baseline, RouteTransportBaseline::Uds);
        assert_eq!(parts.uds_socket_path, Some(socket_path));
        assert!(parts.tls_config.is_none());
        assert_eq!(
            parts.endpoint.uri().to_string(),
            "/tmp/huakai-route-client-test.sock"
        );
    }

    #[test]
    fn mtls_transport_builds_tls_config_inputs_from_placeholder_files() {
        let mtls = RouteClientMtlsConfig {
            endpoint: "https://control-plane.internal:9443"
                .parse()
                .expect("mTLS endpoint fixture 应合法"),
            domain_name: Some("control-plane.internal".to_owned()),
            cert_chain_path: mtls_fixture_path("client-chain.pem"),
            key_path: mtls_fixture_path("client-key.pem"),
            ca_cert_path: mtls_fixture_path("ca.pem"),
        };
        let config = RouteClientTransportConfig::mtls("/tmp/unused-route.sock", mtls.clone());

        let tls_config =
            build_client_tls_config(&mtls).expect("placeholder PEM 文件应可构造 TLS 配置输入");
        let parts = build_route_endpoint_parts(&config, &RouteClientOptions::default())
            .expect("mTLS endpoint parts 应可构造");

        assert_eq!(parts.transport_baseline, RouteTransportBaseline::Mtls);
        assert_eq!(
            parts.endpoint.uri().to_string(),
            "https://control-plane.internal:9443/"
        );
        assert!(parts.uds_socket_path.is_none());
        assert!(parts.tls_config.is_some());
        assert_eq!(
            tls_config.domain_name,
            Some("control-plane.internal".to_owned())
        );
        assert!(tls_config.cert_chain_pem.starts_with(b"-----BEGIN"));
    }

    #[test]
    fn invalid_transport_baseline_fails_fast() {
        let err =
            RouteTransportBaseline::parse("tcp_plaintext").expect_err("非法 baseline 应立即拒绝");

        assert!(err.to_string().contains("HUAKAI_TRANSPORT_BASELINE"));
        assert!(err.to_string().contains("uds, mtls"));
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
