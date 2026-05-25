// 网关启动期类型化配置 — 来自环境变量, 缺失必填字段立即 fail-fast
// 标识符保持英文, 注释一律中文

use std::{net::SocketAddr, thread};

use http::Uri;
use serde::Deserialize;
use tracing_subscriber::filter::LevelFilter;

use crate::error::GatewayError;

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
}

/// envy 解析用的原始字符串结构体 (不对外暴露)
#[derive(Debug, Deserialize)]
struct RawStartupConfig {
    listen_addr: String,
    control_plane_endpoint: String,
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
        Ok(())
    }

    fn from_raw(raw: RawStartupConfig) -> Result<Self, GatewayError> {
        let listen_addr = raw
            .listen_addr
            .parse::<SocketAddr>()
            .map_err(|err| GatewayError::Config(format!("invalid HUAKAI_LISTEN_ADDR: {err}")))?;

        let control_plane_endpoint =
            parse_endpoint("HUAKAI_CONTROL_PLANE_ENDPOINT", &raw.control_plane_endpoint)?;

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

        let mock_upstream_endpoint = raw
            .mock_upstream_endpoint
            .as_deref()
            .map(|s| parse_endpoint("HUAKAI_MOCK_UPSTREAM_ENDPOINT", s))
            .transpose()?;

        Ok(Self {
            listen_addr,
            control_plane_endpoint,
            log_level,
            otlp_endpoint,
            json_logs: raw.json_logs,
            worker_threads: raw.worker_threads,
            max_body_bytes: raw.max_body_bytes,
            mock_upstream_endpoint,
        })
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

fn default_json_logs() -> bool {
    true
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
    fn config_validate_passes_for_valid_config() {
        let cfg = StartupConfig::from_env_iter(valid_env()).expect("valid config");
        assert!(cfg.validate().is_ok());
    }
}
