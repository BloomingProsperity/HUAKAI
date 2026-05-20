// 网关启动期类型化配置 — 来自环境变量, 缺失必填字段立即 fail-fast
// 标识符保持英文, 注释一律中文

use std::{net::SocketAddr, path::PathBuf, thread};

use http::Uri;
use serde::Deserialize;
use tracing_subscriber::filter::LevelFilter;

use crate::{
    error::GatewayError,
    route_client::{
        DEFAULT_UDS_SOCKET_PATH, RouteClientTransportConfig, TransportBaseline,
        TransportBaselineKind,
    },
};

/// 环境变量前缀
pub const ENV_PREFIX: &str = "HUAKAI_";
/// 默认请求体上限: 4 MiB
pub const DEFAULT_MAX_BODY_BYTES: usize = 4 * 1024 * 1024;

/// 网关顶层配置结构体 (强类型, 启动后不可变)
/// 所有字段均通过环境变量注入, 前缀 `HUAKAI_`
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StartupConfig {
    /// 监听地址 (如 "0.0.0.0:8080")
    pub listen_addr: SocketAddr,
    /// Go control plane 端点 (如 "http://127.0.0.1:9090")
    pub control_plane_endpoint: Uri,
    /// Rust->Go route RPC transport baseline, 默认 UDS
    pub transport_baseline: TransportBaseline,
    /// UDS baseline socket path
    pub uds_socket_path: PathBuf,
    /// mTLS server name/SNI override; 未配置时使用 endpoint host
    pub mtls_domain_name: Option<String>,
    /// mTLS client cert chain path; 仅 mTLS baseline 必填
    pub mtls_cert_chain_path: Option<PathBuf>,
    /// mTLS client private key path; 仅 mTLS baseline 必填
    pub mtls_key_path: Option<PathBuf>,
    /// mTLS CA cert path; 仅 mTLS baseline 必填
    pub mtls_ca_cert_path: Option<PathBuf>,
    /// 日志级别
    pub log_level: LevelFilter,
    /// OTLP 导出端点 (如 "http://127.0.0.1:4317"), 可选
    /// 若缺失则跳过 OTLP export, 不报错
    pub otlp_endpoint: Option<Uri>,
    /// JSON 格式日志开关 (默认 true)
    pub json_logs: bool,
    /// Tokio worker 线程数 (默认 CPU 核心数)
    pub worker_threads: usize,
    /// 单请求 body 上限, 默认 4 MiB
    pub max_body_bytes: usize,
    /// M-rust-2 测试用 mock upstream; 未配置时 listener 本地 echo
    pub mock_upstream_endpoint: Option<Uri>,
    /// control plane 单次 RPC deadline, 默认 200ms
    pub control_plane_timeout_ms: u64,
    /// route query 失败后的额外重试次数, 默认 1
    pub control_plane_retry_attempts: usize,
    /// route plan 本地短 TTL cache 的 TTL 上限/开关配置, 传入规划器和 route client
    pub route_cache_ttl_ms: u64,
    /// circuit breaker 连续失败阈值
    pub control_plane_circuit_breaker_failures: u32,
    /// circuit breaker 打开后的冷却时间
    pub control_plane_circuit_breaker_cooldown_ms: u64,
}

/// envy 解析用的原始字符串结构体 (不对外暴露)
#[derive(Debug, Deserialize)]
struct RawStartupConfig {
    listen_addr: String,
    control_plane_endpoint: String,
    #[serde(default = "default_transport_baseline")]
    transport_baseline: String,
    #[serde(default = "default_uds_socket_path")]
    uds_socket_path: String,
    #[serde(default)]
    mtls_domain_name: Option<String>,
    #[serde(default)]
    mtls_cert_chain_path: Option<String>,
    #[serde(default)]
    mtls_key_path: Option<String>,
    #[serde(default)]
    mtls_ca_cert_path: Option<String>,
    log_level: String,
    /// OTLP 端点可选
    #[serde(default)]
    otlp_endpoint: Option<String>,
    #[serde(default = "default_json_logs")]
    json_logs: bool,
    #[serde(default = "default_worker_threads")]
    worker_threads: usize,
    #[serde(default = "default_max_body_bytes")]
    max_body_bytes: usize,
    #[serde(default)]
    mock_upstream_endpoint: Option<String>,
    #[serde(default = "default_control_plane_timeout_ms")]
    control_plane_timeout_ms: u64,
    #[serde(default = "default_control_plane_retry_attempts")]
    control_plane_retry_attempts: usize,
    #[serde(default)]
    route_cache_ttl_ms: u64,
    #[serde(default = "default_control_plane_circuit_breaker_failures")]
    control_plane_circuit_breaker_failures: u32,
    #[serde(default = "default_control_plane_circuit_breaker_cooldown_ms")]
    control_plane_circuit_breaker_cooldown_ms: u64,
}

impl StartupConfig {
    /// 从进程环境变量加载配置, 缺失必填项会在启动前 fail-fast
    pub fn from_env() -> Result<Self, GatewayError> {
        let raw: RawStartupConfig = envy::prefixed(ENV_PREFIX)
            .from_env()
            .map_err(|err| GatewayError::Config(err.to_string()))?;

        Self::from_raw(raw)
    }

    /// 从迭代器加载配置 (用于单元测试, 不依赖真实环境变量)
    pub fn from_env_iter<I>(vars: I) -> Result<Self, GatewayError>
    where
        I: IntoIterator<Item = (String, String)>,
    {
        let raw: RawStartupConfig = envy::prefixed(ENV_PREFIX)
            .from_iter(vars)
            .map_err(|err| GatewayError::Config(err.to_string()))?;

        Self::from_raw(raw)
    }

    /// 语义层校验 — 确保关键字段格式合法
    pub fn validate(&self) -> Result<(), GatewayError> {
        if self.worker_threads == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_WORKER_THREADS must be greater than zero".to_owned(),
            ));
        }
        if self.max_body_bytes == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_MAX_BODY_BYTES must be greater than zero".to_owned(),
            ));
        }
        if self.control_plane_timeout_ms == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_CONTROL_PLANE_TIMEOUT_MS must be greater than zero".to_owned(),
            ));
        }
        if self.control_plane_circuit_breaker_failures == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_CONTROL_PLANE_CIRCUIT_BREAKER_FAILURES must be greater than zero"
                    .to_owned(),
            ));
        }
        if matches!(self.transport_baseline, TransportBaseline::Mtls { .. }) {
            self.route_transport_config()?;
        }
        Ok(())
    }

    pub fn route_transport_config(&self) -> Result<RouteClientTransportConfig, GatewayError> {
        match &self.transport_baseline {
            TransportBaseline::Uds(path) => Ok(RouteClientTransportConfig::uds(path.clone())),
            TransportBaseline::Http => Ok(RouteClientTransportConfig::http(
                self.control_plane_endpoint.clone(),
            )),
            TransportBaseline::Mtls { cert, key, ca } => Ok(RouteClientTransportConfig::mtls(
                self.control_plane_endpoint.clone(),
                self.mtls_domain_name.clone(),
                cert.clone(),
                key.clone(),
                ca.clone(),
            )),
        }
    }

    fn from_raw(raw: RawStartupConfig) -> Result<Self, GatewayError> {
        let listen_addr = raw
            .listen_addr
            .parse::<SocketAddr>()
            .map_err(|err| GatewayError::Config(format!("invalid HUAKAI_LISTEN_ADDR: {err}")))?;

        let control_plane_endpoint =
            parse_endpoint("HUAKAI_CONTROL_PLANE_ENDPOINT", &raw.control_plane_endpoint)?;
        let transport_baseline_kind = TransportBaselineKind::parse(&raw.transport_baseline)?;
        let uds_socket_path = PathBuf::from(raw.uds_socket_path);
        let transport_baseline = match transport_baseline_kind {
            TransportBaselineKind::Uds => TransportBaseline::Uds(uds_socket_path.clone()),
            TransportBaselineKind::Http => {
                // R-SEC-002 守门: HTTP baseline 仅允许 loopback 端点
                // 非 loopback 会走明文 gRPC, RoutePlan 含 per-attempt upstream 凭据 → 拒绝
                require_loopback_endpoint(
                    "HUAKAI_TRANSPORT_BASELINE=http",
                    &control_plane_endpoint,
                )?;
                TransportBaseline::Http
            }
            TransportBaselineKind::Mtls => TransportBaseline::Mtls {
                cert: required_path(
                    "HUAKAI_MTLS_CERT_CHAIN_PATH",
                    &raw.mtls_cert_chain_path.as_ref().map(PathBuf::from),
                )?,
                key: required_path(
                    "HUAKAI_MTLS_KEY_PATH",
                    &raw.mtls_key_path.as_ref().map(PathBuf::from),
                )?,
                ca: required_path(
                    "HUAKAI_MTLS_CA_CERT_PATH",
                    &raw.mtls_ca_cert_path.as_ref().map(PathBuf::from),
                )?,
            },
        };

        let log_level = raw
            .log_level
            .parse::<LevelFilter>()
            .map_err(|err| GatewayError::Config(format!("invalid HUAKAI_LOG_LEVEL: {err}")))?;

        let otlp_endpoint = raw
            .otlp_endpoint
            .as_deref()
            .map(|s| parse_endpoint("HUAKAI_OTLP_ENDPOINT", s))
            .transpose()?;

        if raw.worker_threads == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_WORKER_THREADS must be greater than zero".to_owned(),
            ));
        }
        if raw.max_body_bytes == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_MAX_BODY_BYTES must be greater than zero".to_owned(),
            ));
        }
        if raw.control_plane_timeout_ms == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_CONTROL_PLANE_TIMEOUT_MS must be greater than zero".to_owned(),
            ));
        }
        if raw.control_plane_circuit_breaker_failures == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_CONTROL_PLANE_CIRCUIT_BREAKER_FAILURES must be greater than zero"
                    .to_owned(),
            ));
        }

        let mock_upstream_endpoint = raw
            .mock_upstream_endpoint
            .as_deref()
            .map(|s| parse_endpoint("HUAKAI_MOCK_UPSTREAM_ENDPOINT", s))
            .transpose()?;

        let config = Self {
            listen_addr,
            control_plane_endpoint,
            transport_baseline,
            uds_socket_path,
            mtls_domain_name: raw.mtls_domain_name,
            mtls_cert_chain_path: raw.mtls_cert_chain_path.map(PathBuf::from),
            mtls_key_path: raw.mtls_key_path.map(PathBuf::from),
            mtls_ca_cert_path: raw.mtls_ca_cert_path.map(PathBuf::from),
            log_level,
            otlp_endpoint,
            json_logs: raw.json_logs,
            worker_threads: raw.worker_threads,
            max_body_bytes: raw.max_body_bytes,
            mock_upstream_endpoint,
            control_plane_timeout_ms: raw.control_plane_timeout_ms,
            control_plane_retry_attempts: raw.control_plane_retry_attempts,
            route_cache_ttl_ms: raw.route_cache_ttl_ms,
            control_plane_circuit_breaker_failures: raw.control_plane_circuit_breaker_failures,
            control_plane_circuit_breaker_cooldown_ms: raw
                .control_plane_circuit_breaker_cooldown_ms,
        };
        config.validate()?;
        Ok(config)
    }
}

/// 解析并校验 URI 端点 (必须含 scheme 和 authority)
fn parse_endpoint(name: &str, value: &str) -> Result<Uri, GatewayError> {
    let uri = value
        .parse::<Uri>()
        .map_err(|err| GatewayError::Config(format!("invalid {name}: {err}")))?;

    if uri.scheme().is_none() || uri.authority().is_none() {
        return Err(GatewayError::Config(format!(
            "{name} must include scheme and authority"
        )));
    }

    Ok(uri)
}

fn required_path(name: &str, value: &Option<PathBuf>) -> Result<PathBuf, GatewayError> {
    value
        .clone()
        .ok_or_else(|| GatewayError::Config(format!("{name} is required when mTLS is enabled")))
}

/// R-SEC-002 守门: HTTP baseline 走明文 gRPC, 仅允许 loopback (本地测试 / 同机调试)。
/// 非 loopback 拒绝, 强制走 UDS 或 mTLS。
fn require_loopback_endpoint(context: &str, endpoint: &Uri) -> Result<(), GatewayError> {
    let host = endpoint.host().ok_or_else(|| {
        GatewayError::Config(format!(
            "{context} requires HUAKAI_CONTROL_PLANE_ENDPOINT with a host"
        ))
    })?;
    // 去掉 IPv6 字面量的方括号: "[::1]" → "::1"
    let stripped = host.trim_start_matches('[').trim_end_matches(']');
    let is_loopback = match stripped.parse::<std::net::IpAddr>() {
        Ok(ip) => ip.is_loopback(),
        Err(_) => matches!(stripped, "localhost"),
    };
    if !is_loopback {
        return Err(GatewayError::Config(format!(
            "{context} only permits loopback HUAKAI_CONTROL_PLANE_ENDPOINT \
             (127.0.0.1 / ::1 / localhost); got {host}. R-SEC-002 禁止明文 gRPC 走非本机网络。"
        )));
    }
    Ok(())
}

fn default_json_logs() -> bool {
    true
}

fn default_transport_baseline() -> String {
    "uds".to_owned()
}

fn default_uds_socket_path() -> String {
    DEFAULT_UDS_SOCKET_PATH.to_owned()
}

fn default_worker_threads() -> usize {
    // 不使用 Tokio 默认线程数; 即使 ENV 未覆盖也显式写入 builder
    thread::available_parallelism()
        .map(usize::from)
        .unwrap_or(1)
        .max(1)
}

fn default_max_body_bytes() -> usize {
    DEFAULT_MAX_BODY_BYTES
}

fn default_control_plane_timeout_ms() -> u64 {
    200
}

fn default_control_plane_retry_attempts() -> usize {
    1
}

fn default_control_plane_circuit_breaker_failures() -> u32 {
    5
}

fn default_control_plane_circuit_breaker_cooldown_ms() -> u64 {
    1_000
}

#[cfg(test)]
mod tests {
    use super::*;

    fn valid_env() -> Vec<(String, String)> {
        [
            ("HUAKAI_LISTEN_ADDR", "127.0.0.1:0"),
            ("HUAKAI_CONTROL_PLANE_ENDPOINT", "http://127.0.0.1:48080"),
            ("HUAKAI_LOG_LEVEL", "debug"),
            ("HUAKAI_JSON_LOGS", "true"),
            ("HUAKAI_WORKER_THREADS", "2"),
        ]
        .into_iter()
        .map(|(k, v)| (k.to_owned(), v.to_owned()))
        .collect()
    }

    #[test]
    fn config_parse_succeeds_with_valid_env() {
        let cfg = StartupConfig::from_env_iter(valid_env()).expect("config 解析应成功");
        assert_eq!(cfg.listen_addr.to_string(), "127.0.0.1:0");
        assert_eq!(
            cfg.control_plane_endpoint.to_string(),
            "http://127.0.0.1:48080/"
        );
        assert_eq!(cfg.worker_threads, 2);
        assert_eq!(cfg.max_body_bytes, DEFAULT_MAX_BODY_BYTES);
        assert!(cfg.json_logs);
        assert!(cfg.otlp_endpoint.is_none());
        assert!(cfg.mock_upstream_endpoint.is_none());
        assert_eq!(
            cfg.transport_baseline,
            TransportBaseline::Uds(PathBuf::from(DEFAULT_UDS_SOCKET_PATH))
        );
        assert_eq!(cfg.uds_socket_path, PathBuf::from(DEFAULT_UDS_SOCKET_PATH));
    }

    #[test]
    fn config_with_otlp_endpoint_parses_correctly() {
        let mut env = valid_env();
        env.push((
            "HUAKAI_OTLP_ENDPOINT".to_owned(),
            "http://127.0.0.1:4317".to_owned(),
        ));
        let cfg = StartupConfig::from_env_iter(env).expect("带 otlp_endpoint 的 config 应解析成功");
        assert!(cfg.otlp_endpoint.is_some());
        assert_eq!(
            cfg.otlp_endpoint.unwrap().to_string(),
            "http://127.0.0.1:4317/"
        );
    }

    #[test]
    fn config_rejects_zero_worker_threads() {
        let mut env = valid_env();
        // 覆盖 worker_threads 为 0
        env.retain(|(k, _)| k != "HUAKAI_WORKER_THREADS");
        env.push(("HUAKAI_WORKER_THREADS".to_owned(), "0".to_owned()));
        let result = StartupConfig::from_env_iter(env);
        assert!(result.is_err(), "worker_threads=0 应解析失败");
    }

    #[test]
    fn config_rejects_zero_max_body_bytes() {
        let mut env = valid_env();
        env.push(("HUAKAI_MAX_BODY_BYTES".to_owned(), "0".to_owned()));
        let result = StartupConfig::from_env_iter(env);
        assert!(result.is_err(), "max_body_bytes=0 应解析失败");
    }

    #[test]
    fn config_with_mock_upstream_endpoint_parses_correctly() {
        let mut env = valid_env();
        env.push((
            "HUAKAI_MOCK_UPSTREAM_ENDPOINT".to_owned(),
            "http://127.0.0.1:48100".to_owned(),
        ));
        let cfg = StartupConfig::from_env_iter(env)
            .expect("带 mock_upstream_endpoint 的 config 应解析成功");
        assert_eq!(
            cfg.mock_upstream_endpoint.unwrap().to_string(),
            "http://127.0.0.1:48100/"
        );
    }

    #[test]
    fn config_rejects_invalid_listen_addr() {
        let mut env = valid_env();
        env.retain(|(k, _)| k != "HUAKAI_LISTEN_ADDR");
        env.push(("HUAKAI_LISTEN_ADDR".to_owned(), "not_an_addr".to_owned()));
        let result = StartupConfig::from_env_iter(env);
        assert!(result.is_err(), "非法 listen_addr 应解析失败");
    }

    #[test]
    fn config_rejects_invalid_transport_baseline() {
        let mut env = valid_env();
        env.push((
            "HUAKAI_TRANSPORT_BASELINE".to_owned(),
            "tcp_plaintext".to_owned(),
        ));
        let result = StartupConfig::from_env_iter(env);
        assert!(result.is_err(), "非法 transport_baseline 应 fail-fast");
    }

    #[test]
    fn config_builds_http_transport_baseline_from_endpoint() {
        let mut env = valid_env();
        env.push(("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()));

        let cfg = StartupConfig::from_env_iter(env).expect("HTTP baseline config 应解析成功");

        assert_eq!(cfg.transport_baseline, TransportBaseline::Http);
        let route_config = cfg
            .route_transport_config()
            .expect("HTTP baseline 应能转 route transport config");
        assert_eq!(route_config.endpoint.to_string(), "http://127.0.0.1:48080/");
        assert_eq!(route_config.transport_baseline, TransportBaseline::Http);
    }

    #[test]
    fn config_rejects_http_baseline_when_endpoint_is_non_loopback() {
        // R-SEC-002: HTTP baseline 走明文 gRPC, 非 loopback 端点应被拒
        let mut env = valid_env();
        env.retain(|(k, _)| k != "HUAKAI_CONTROL_PLANE_ENDPOINT");
        env.push((
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            "http://control-plane.internal:9090".to_owned(),
        ));
        env.push(("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()));

        let result = StartupConfig::from_env_iter(env);
        assert!(
            result.is_err(),
            "HTTP baseline 配非 loopback 端点应 fail-fast (R-SEC-002)"
        );
    }

    #[test]
    fn config_accepts_http_baseline_with_localhost_alias() {
        // localhost 作为 loopback 别名应被接受
        let mut env = valid_env();
        env.retain(|(k, _)| k != "HUAKAI_CONTROL_PLANE_ENDPOINT");
        env.push((
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            "http://localhost:48080".to_owned(),
        ));
        env.push(("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()));

        let cfg =
            StartupConfig::from_env_iter(env).expect("localhost endpoint 应允许 HTTP baseline");
        assert_eq!(cfg.transport_baseline, TransportBaseline::Http);
    }

    #[test]
    fn config_accepts_http_baseline_with_ipv6_loopback() {
        // [::1] IPv6 loopback 字面量应被接受
        let mut env = valid_env();
        env.retain(|(k, _)| k != "HUAKAI_CONTROL_PLANE_ENDPOINT");
        env.push((
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            "http://[::1]:48080".to_owned(),
        ));
        env.push(("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()));

        let cfg =
            StartupConfig::from_env_iter(env).expect("::1 endpoint 应允许 HTTP baseline");
        assert_eq!(cfg.transport_baseline, TransportBaseline::Http);
    }

    #[test]
    fn config_rejects_mtls_without_cert_paths() {
        let mut env = valid_env();
        env.push(("HUAKAI_TRANSPORT_BASELINE".to_owned(), "mtls".to_owned()));
        let result = StartupConfig::from_env_iter(env);
        assert!(result.is_err(), "mTLS 缺少证书路径应 fail-fast");
    }

    #[test]
    fn config_rejects_mtls_when_any_cert_path_is_missing() {
        for missing_key in [
            "HUAKAI_MTLS_CERT_CHAIN_PATH",
            "HUAKAI_MTLS_KEY_PATH",
            "HUAKAI_MTLS_CA_CERT_PATH",
        ] {
            let mut env = valid_env();
            env.push(("HUAKAI_TRANSPORT_BASELINE".to_owned(), "mtls".to_owned()));
            env.push((
                "HUAKAI_MTLS_CERT_CHAIN_PATH".to_owned(),
                "/etc/huakai/client.pem".to_owned(),
            ));
            env.push((
                "HUAKAI_MTLS_KEY_PATH".to_owned(),
                "/etc/huakai/client.key".to_owned(),
            ));
            env.push((
                "HUAKAI_MTLS_CA_CERT_PATH".to_owned(),
                "/etc/huakai/ca.pem".to_owned(),
            ));
            env.retain(|(key, _)| key != missing_key);

            let result = StartupConfig::from_env_iter(env);
            assert!(
                result.is_err(),
                "{missing_key} 缺失时 mTLS 配置必须 fail-fast"
            );
        }
    }

    #[test]
    fn config_builds_mtls_transport_baseline_with_all_paths() {
        let mut env = valid_env();
        env.retain(|(key, _)| key != "HUAKAI_CONTROL_PLANE_ENDPOINT");
        env.push(("HUAKAI_TRANSPORT_BASELINE".to_owned(), "mtls".to_owned()));
        env.push((
            "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
            "https://control-plane.internal:9443".to_owned(),
        ));
        env.push((
            "HUAKAI_MTLS_DOMAIN_NAME".to_owned(),
            "control-plane.internal".to_owned(),
        ));
        env.push((
            "HUAKAI_MTLS_CERT_CHAIN_PATH".to_owned(),
            "/etc/huakai/client.pem".to_owned(),
        ));
        env.push((
            "HUAKAI_MTLS_KEY_PATH".to_owned(),
            "/etc/huakai/client.key".to_owned(),
        ));
        env.push((
            "HUAKAI_MTLS_CA_CERT_PATH".to_owned(),
            "/etc/huakai/ca.pem".to_owned(),
        ));

        let cfg = StartupConfig::from_env_iter(env).expect("完整 mTLS config 应解析成功");

        assert_eq!(
            cfg.transport_baseline,
            TransportBaseline::Mtls {
                cert: PathBuf::from("/etc/huakai/client.pem"),
                key: PathBuf::from("/etc/huakai/client.key"),
                ca: PathBuf::from("/etc/huakai/ca.pem"),
            }
        );
        let route_config = cfg
            .route_transport_config()
            .expect("完整 mTLS config 应能转 route transport config");
        assert_eq!(
            route_config.endpoint.to_string(),
            "https://control-plane.internal:9443/"
        );
        assert_eq!(
            route_config.domain_name,
            Some("control-plane.internal".to_owned())
        );
    }

    #[test]
    fn config_validate_passes_for_valid_config() {
        let cfg = StartupConfig::from_env_iter(valid_env()).expect("valid config");
        assert!(cfg.validate().is_ok());
    }
}
