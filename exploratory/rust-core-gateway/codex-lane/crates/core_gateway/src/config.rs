use std::{net::SocketAddr, thread};

use http::Uri;
use serde::Deserialize;
use tracing_subscriber::filter::LevelFilter;

use crate::error::GatewayError;

pub const ENV_PREFIX: &str = "HUAKAI_";

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct StartupConfig {
    pub listen_addr: SocketAddr,
    pub control_plane_endpoint: Uri,
    pub log_level: LevelFilter,
    pub tracing_endpoint: Uri,
    pub json_logs: bool,
    pub worker_threads: usize,
}

#[derive(Debug, Deserialize)]
struct RawStartupConfig {
    listen_addr: String,
    control_plane_endpoint: String,
    log_level: String,
    tracing_endpoint: String,
    #[serde(default = "default_json_logs")]
    json_logs: bool,
    #[serde(default = "default_worker_threads")]
    worker_threads: usize,
}

impl StartupConfig {
    // 启动配置只从 ENV 读取，缺失必填项会在启动前失败。
    pub fn from_env() -> Result<Self, GatewayError> {
        let raw: RawStartupConfig = envy::prefixed(ENV_PREFIX)
            .from_env()
            .map_err(|err| GatewayError::Config(err.to_string()))?;

        Self::from_raw(raw)
    }

    pub fn from_env_iter<I>(vars: I) -> Result<Self, GatewayError>
    where
        I: IntoIterator<Item = (String, String)>,
    {
        let raw: RawStartupConfig = envy::prefixed(ENV_PREFIX)
            .from_iter(vars)
            .map_err(|err| GatewayError::Config(err.to_string()))?;

        Self::from_raw(raw)
    }

    fn from_raw(raw: RawStartupConfig) -> Result<Self, GatewayError> {
        let listen_addr = raw
            .listen_addr
            .parse::<SocketAddr>()
            .map_err(|err| GatewayError::Config(format!("invalid HUAKAI_LISTEN_ADDR: {err}")))?;
        let control_plane_endpoint =
            parse_endpoint("HUAKAI_CONTROL_PLANE_ENDPOINT", &raw.control_plane_endpoint)?;
        let tracing_endpoint = parse_endpoint("HUAKAI_TRACING_ENDPOINT", &raw.tracing_endpoint)?;
        let log_level = raw
            .log_level
            .parse::<LevelFilter>()
            .map_err(|err| GatewayError::Config(format!("invalid HUAKAI_LOG_LEVEL: {err}")))?;

        if raw.worker_threads == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_WORKER_THREADS must be greater than zero".to_owned(),
            ));
        }

        Ok(Self {
            listen_addr,
            control_plane_endpoint,
            log_level,
            tracing_endpoint,
            json_logs: raw.json_logs,
            worker_threads: raw.worker_threads,
        })
    }
}

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
    // 不使用 Tokio 默认线程数；即使 ENV 未覆盖，也显式写入 builder。
    thread::available_parallelism()
        .map(usize::from)
        .unwrap_or(1)
        .max(1)
}
