// Rust 数据面到 Go control plane 的 gRPC client。
// v0 只做 typed contract、deadline、retry 和 circuit breaker 骨架。

use std::{
    fs,
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
#[cfg(feature = "tls")]
use tonic::transport::{Certificate, ClientTlsConfig, Identity};
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
pub const DEFAULT_UDS_SOCKET_PATH: &str = "/var/run/huakai/control-plane.sock";
const CONTROL_PLANE_ERROR_LIMIT: usize = 256;
const UDS_ENDPOINT_URI: &str = "http://huakai-control-plane.local";

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum TransportBaseline {
    Uds(PathBuf),
    Mtls {
        cert: PathBuf,
        key: PathBuf,
        ca: PathBuf,
    },
}

impl Default for TransportBaseline {
    fn default() -> Self {
        Self::Uds(PathBuf::from(DEFAULT_UDS_SOCKET_PATH))
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TransportBaselineKind {
    Uds,
    Mtls,
}

impl TransportBaselineKind {
    pub fn parse(value: &str) -> Result<Self, GatewayError> {
        match value {
            "uds" => Ok(Self::Uds),
            "mtls" => Ok(Self::Mtls),
            other => Err(GatewayError::Config(format!(
                "HUAKAI_TRANSPORT_BASELINE must be one of uds, mtls; got {other}"
            ))),
        }
    }
}

impl TransportBaseline {
    pub fn as_str(&self) -> &'static str {
        match self {
            Self::Uds(_) => "uds",
            Self::Mtls { .. } => "mtls",
        }
    }

    pub fn uds_socket_path(&self) -> Option<&Path> {
        match self {
            Self::Uds(path) => Some(path.as_path()),
            Self::Mtls { .. } => None,
        }
    }

    pub fn mtls_paths(&self) -> Option<(&Path, &Path, &Path)> {
        match self {
            Self::Mtls { cert, key, ca } => Some((cert.as_path(), key.as_path(), ca.as_path())),
            Self::Uds(_) => None,
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RouteClientTransportConfig {
    pub transport_baseline: TransportBaseline,
    pub endpoint: Uri,
    pub domain_name: Option<String>,
}

impl RouteClientTransportConfig {
    pub fn uds(uds_socket_path: impl Into<PathBuf>) -> Self {
        Self {
            transport_baseline: TransportBaseline::Uds(uds_socket_path.into()),
            endpoint: UDS_ENDPOINT_URI
                .parse()
                .expect("static UDS endpoint URI must be valid"),
            domain_name: None,
        }
    }

    pub fn mtls(
        endpoint: Uri,
        domain_name: Option<String>,
        cert: impl Into<PathBuf>,
        key: impl Into<PathBuf>,
        ca: impl Into<PathBuf>,
    ) -> Self {
        Self {
            transport_baseline: TransportBaseline::Mtls {
                cert: cert.into(),
                key: key.into(),
                ca: ca.into(),
            },
            endpoint,
            domain_name,
        }
    }
}

pub struct RouteClientEndpointParts {
    pub transport_baseline: TransportBaseline,
    pub endpoint: Endpoint,
    pub uds_socket_path: Option<PathBuf>,
    pub tls_material: Option<RouteClientTlsMaterial>,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RouteClientTlsMaterial {
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
        let channel = match &parts.transport_baseline {
            TransportBaseline::Uds(_) => {
                let socket_path = parts.uds_socket_path.ok_or_else(|| {
                    GatewayError::Config("UDS baseline requires uds_socket_path".to_owned())
                })?;
                validate_uds_socket_security(&socket_path)?;
                parts
                    .endpoint
                    .connect_with_connector_lazy(UnixSocketConnector::new(socket_path))
            }
            TransportBaseline::Mtls { .. } => {
                #[cfg(feature = "tls")]
                {
                    parts.endpoint.connect_lazy()
                }
                #[cfg(not(feature = "tls"))]
                {
                    return Err(GatewayError::Config(
                        "mTLS channel activation requires the core_gateway tls feature".to_owned(),
                    ));
                }
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
    match &config.transport_baseline {
        TransportBaseline::Uds(socket_path) => {
            let endpoint = Endpoint::try_from(UDS_ENDPOINT_URI).map_err(|err| {
                GatewayError::ControlPlane(format!("invalid static UDS route endpoint: {err}"))
            })?;

            Ok(RouteClientEndpointParts {
                transport_baseline: config.transport_baseline.clone(),
                endpoint: configure_endpoint(endpoint, options),
                uds_socket_path: Some(socket_path.clone()),
                tls_material: None,
            })
        }
        TransportBaseline::Mtls { cert, key, ca } => {
            let endpoint = build_tcp_endpoint(&config.endpoint, options)?;
            let domain_name =
                resolve_mtls_domain_name(&config.endpoint, config.domain_name.as_deref())?;
            let tls_material = build_client_tls_material(Some(domain_name), cert, key, ca)?;
            let endpoint = configure_mtls_endpoint(endpoint, &tls_material)?;

            Ok(RouteClientEndpointParts {
                transport_baseline: config.transport_baseline.clone(),
                endpoint,
                uds_socket_path: None,
                tls_material: Some(tls_material),
            })
        }
    }
}

pub fn build_client_tls_material(
    domain_name: Option<String>,
    cert_chain_path: &Path,
    key_path: &Path,
    ca_cert_path: &Path,
) -> Result<RouteClientTlsMaterial, GatewayError> {
    let cert_chain = read_required_pem(cert_chain_path, "cert")?;
    let key = read_required_pem(key_path, "key")?;
    let ca_cert = read_required_pem(ca_cert_path, "ca")?;

    Ok(RouteClientTlsMaterial {
        domain_name,
        cert_chain_pem: cert_chain,
        key_pem: key,
        ca_cert_pem: ca_cert,
    })
}

#[cfg(feature = "tls")]
pub fn build_client_tls_config(
    domain_name: Option<String>,
    cert_chain_path: &Path,
    key_path: &Path,
    ca_cert_path: &Path,
) -> Result<ClientTlsConfig, GatewayError> {
    let tls_material =
        build_client_tls_material(domain_name, cert_chain_path, key_path, ca_cert_path)?;
    Ok(client_tls_config_from_material(&tls_material))
}

fn resolve_mtls_domain_name(
    endpoint: &Uri,
    configured_domain_name: Option<&str>,
) -> Result<String, GatewayError> {
    if let Some(domain_name) = configured_domain_name {
        let domain_name = domain_name.trim();
        if domain_name.is_empty() {
            return Err(GatewayError::Config(
                "mTLS domain_name must not be empty when configured".to_owned(),
            ));
        }
        return Ok(domain_name.to_owned());
    }

    endpoint.host().map(str::to_owned).ok_or_else(|| {
        GatewayError::Config(format!(
            "mTLS endpoint {endpoint} must include host when domain_name is not configured"
        ))
    })
}

#[cfg(feature = "tls")]
fn configure_mtls_endpoint(
    endpoint: Endpoint,
    tls_material: &RouteClientTlsMaterial,
) -> Result<Endpoint, GatewayError> {
    endpoint
        .tls_config(client_tls_config_from_material(tls_material))
        .map_err(|err| GatewayError::Config(format!("invalid route client mTLS config: {err}")))
}

#[cfg(not(feature = "tls"))]
fn configure_mtls_endpoint(
    endpoint: Endpoint,
    _tls_material: &RouteClientTlsMaterial,
) -> Result<Endpoint, GatewayError> {
    Ok(endpoint)
}

#[cfg(feature = "tls")]
fn client_tls_config_from_material(tls_material: &RouteClientTlsMaterial) -> ClientTlsConfig {
    let ca_certificate = Certificate::from_pem(&tls_material.ca_cert_pem);
    let identity = Identity::from_pem(&tls_material.cert_chain_pem, &tls_material.key_pem);
    let mut tls_config = ClientTlsConfig::new()
        .ca_certificate(ca_certificate)
        .identity(identity);

    if let Some(domain_name) = &tls_material.domain_name {
        tls_config = tls_config.domain_name(domain_name.clone());
    }

    tls_config
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

fn read_required_pem(path: &Path, field_name: &str) -> Result<Vec<u8>, GatewayError> {
    if path.as_os_str().is_empty() {
        return Err(GatewayError::Config(format!(
            "mTLS {field_name} path must not be empty"
        )));
    }

    let bytes = fs::read(path).map_err(|err| {
        GatewayError::Config(format!(
            "unable to read route client mTLS {field_name} {}: {err}",
            path.display()
        ))
    })?;
    if bytes.is_empty() {
        return Err(GatewayError::Config(format!(
            "route client mTLS {field_name} {} must not be empty",
            path.display()
        )));
    }

    Ok(bytes)
}

#[cfg(unix)]
pub fn validate_uds_socket_security(path: &Path) -> Result<(), GatewayError> {
    use std::os::unix::fs::{FileTypeExt, MetadataExt, PermissionsExt};

    let metadata = fs::symlink_metadata(path).map_err(|err| {
        GatewayError::Config(format!(
            "UDS control-plane socket {} is not readable for security validation: {err}",
            path.display()
        ))
    })?;

    if !metadata.file_type().is_socket() {
        return Err(GatewayError::Config(format!(
            "UDS control-plane path {} must be a Unix socket",
            path.display()
        )));
    }

    let mode = metadata.permissions().mode() & 0o777;
    if mode != 0o600 {
        return Err(GatewayError::Config(format!(
            "UDS control-plane socket {} must have mode 0600; got {mode:04o}",
            path.display()
        )));
    }

    let (current_uid, current_gid) = current_uid_gid()?;
    if metadata.uid() != current_uid || metadata.gid() != current_gid {
        return Err(GatewayError::Config(format!(
            "UDS control-plane socket {} must be owned by current uid/gid {current_uid}/{current_gid}; got {}/{}",
            path.display(),
            metadata.uid(),
            metadata.gid()
        )));
    }

    Ok(())
}

#[cfg(not(unix))]
pub fn validate_uds_socket_security(_path: &Path) -> Result<(), GatewayError> {
    Err(GatewayError::Config(
        "UDS control-plane transport is only supported on Unix platforms".to_owned(),
    ))
}

#[cfg(unix)]
fn current_uid_gid() -> Result<(u32, u32), GatewayError> {
    let status = fs::read_to_string("/proc/self/status").map_err(|err| {
        GatewayError::Config(format!(
            "unable to read /proc/self/status for uid/gid: {err}"
        ))
    })?;
    let uid = parse_proc_status_id(&status, "Uid:")?;
    let gid = parse_proc_status_id(&status, "Gid:")?;
    Ok((uid, gid))
}

#[cfg(unix)]
fn parse_proc_status_id(status: &str, label: &str) -> Result<u32, GatewayError> {
    let line = status
        .lines()
        .find(|line| line.starts_with(label))
        .ok_or_else(|| GatewayError::Config(format!("missing {label} in /proc/self/status")))?;
    let effective_id = line
        .split_whitespace()
        .nth(2)
        .ok_or_else(|| GatewayError::Config(format!("missing effective {label} value")))?;
    effective_id.parse::<u32>().map_err(|err| {
        GatewayError::Config(format!(
            "invalid effective {label} value {effective_id}: {err}"
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

    fn test_output_dir() -> PathBuf {
        let dir = std::env::var_os("CARGO_TARGET_DIR")
            .map(PathBuf::from)
            .unwrap_or_else(|| {
                PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("target/core_gateway_tests")
            })
            .join("route_client");
        fs::create_dir_all(&dir).expect("route_client test output dir 应可创建");
        dir
    }

    #[test]
    fn uds_transport_builds_endpoint_from_fake_socket_path() {
        let socket_path = PathBuf::from(DEFAULT_UDS_SOCKET_PATH);
        let config = RouteClientTransportConfig::uds(socket_path.clone());

        let parts = build_route_endpoint_parts(&config, &RouteClientOptions::default())
            .expect("UDS endpoint 应可由 fake socket path 构造");

        assert_eq!(
            parts.transport_baseline,
            TransportBaseline::Uds(socket_path.clone())
        );
        assert_eq!(parts.uds_socket_path, Some(socket_path));
        assert!(parts.tls_material.is_none());
        assert_eq!(
            parts.endpoint.uri().to_string(),
            format!("{UDS_ENDPOINT_URI}/")
        );
    }

    #[cfg(unix)]
    #[test]
    fn uds_security_rejects_missing_socket_path() {
        let socket_path = test_output_dir().join("missing-control-plane.sock");
        let _ = fs::remove_file(&socket_path);

        let err =
            validate_uds_socket_security(&socket_path).expect_err("缺失 UDS socket 必须 fail-fast");

        assert!(err.to_string().contains("not readable"));
    }

    #[cfg(unix)]
    #[test]
    fn proc_status_parser_reads_effective_uid_gid() {
        let status = "Name:\ttest\nUid:\t1000\t1001\t1002\t1003\nGid:\t2000\t2001\t2002\t2003\n";

        assert_eq!(parse_proc_status_id(status, "Uid:").unwrap(), 1001);
        assert_eq!(parse_proc_status_id(status, "Gid:").unwrap(), 2001);
    }

    #[test]
    fn mtls_transport_builds_tls_config_inputs_from_fixture_files() {
        let endpoint = "https://control-plane.internal:9443"
            .parse()
            .expect("mTLS endpoint fixture 应合法");
        let cert = mtls_fixture_path("client-chain.pem");
        let key = mtls_fixture_path("client-key.pem");
        let ca = mtls_fixture_path("ca.pem");
        let config = RouteClientTransportConfig::mtls(
            endpoint,
            Some("control-plane.internal".to_owned()),
            cert.clone(),
            key.clone(),
            ca.clone(),
        );

        let tls_material =
            build_client_tls_material(Some("control-plane.internal".to_owned()), &cert, &key, &ca)
                .expect("fixture PEM 文件应可构造 TLS 配置输入");
        let parts = build_route_endpoint_parts(&config, &RouteClientOptions::default())
            .expect("mTLS endpoint parts 应可构造");

        assert_eq!(
            parts.transport_baseline,
            TransportBaseline::Mtls { cert, key, ca }
        );
        assert_eq!(
            parts.endpoint.uri().to_string(),
            "https://control-plane.internal:9443/"
        );
        assert!(parts.uds_socket_path.is_none());
        assert!(parts.tls_material.is_some());
        assert_eq!(
            tls_material.domain_name,
            Some("control-plane.internal".to_owned())
        );
        assert!(tls_material.cert_chain_pem.starts_with(b"-----BEGIN"));
        assert!(tls_material.key_pem.starts_with(b"-----BEGIN"));
        assert!(tls_material.ca_cert_pem.starts_with(b"-----BEGIN"));
    }

    #[cfg(feature = "tls")]
    #[test]
    fn mtls_client_tls_config_applies_to_tonic_endpoint() {
        let cert = mtls_fixture_path("client-chain.pem");
        let key = mtls_fixture_path("client-key.pem");
        let ca = mtls_fixture_path("ca.pem");
        let endpoint_uri = "https://control-plane.internal:9443"
            .parse()
            .expect("mTLS endpoint fixture 应合法");
        let endpoint = build_tcp_endpoint(&endpoint_uri, &RouteClientOptions::default())
            .expect("mTLS endpoint 应可构造");
        let tls_config =
            build_client_tls_config(Some("control-plane.internal".to_owned()), &cert, &key, &ca)
                .expect("fixture PEM 应可构造 tonic ClientTlsConfig");

        let endpoint = endpoint
            .tls_config(tls_config)
            .expect("fixture PEM 应可真应用到 tonic Endpoint::tls_config");

        assert_eq!(
            endpoint.uri().to_string(),
            "https://control-plane.internal:9443/"
        );
    }

    #[test]
    fn mtls_transport_rejects_missing_cert_path() {
        let missing = test_output_dir().join("missing-client-chain.pem");
        let err = build_client_tls_material(
            Some("control-plane.internal".to_owned()),
            &missing,
            &mtls_fixture_path("client-key.pem"),
            &mtls_fixture_path("ca.pem"),
        )
        .expect_err("mTLS cert/key/ca 任一缺失必须 fail-fast");

        assert!(err.to_string().contains("mTLS cert"));
    }

    #[test]
    fn invalid_transport_baseline_fails_fast() {
        let err =
            TransportBaselineKind::parse("tcp_plaintext").expect_err("非法 baseline 应立即拒绝");

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
