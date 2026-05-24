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

/// W11-D2: 运行时模式 — 控制生产/非生产的安全行为差异。
/// 生产模式下 mock upstream 必须 fail-fast 拒绝（防止账务/计费旁路）。
/// 默认值: Production (启动时若 HUAKAI_RUNTIME_MODE 未设, 视为最严格的生产模式)。
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RuntimeMode {
    /// 生产: 拒绝任何危险开关 (如 mock upstream)。
    Production,
    /// 开发: 允许 mock upstream 等本地开发便利, 启动时显著告警。
    Development,
    /// 测试: 与 Development 类似, 用于 CI / 单元测试。
    Test,
}

impl RuntimeMode {
    fn parse(value: &str) -> Result<Self, GatewayError> {
        match value.to_ascii_lowercase().as_str() {
            "production" | "prod" => Ok(Self::Production),
            "development" | "dev" => Ok(Self::Development),
            "test" => Ok(Self::Test),
            other => Err(GatewayError::Config(format!(
                "invalid HUAKAI_RUNTIME_MODE {other:?}; expected one of: production, development, test"
            ))),
        }
    }

    pub fn is_production(self) -> bool {
        matches!(self, Self::Production)
    }
}

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
    /// W11-D2: 仅 runtime_mode = Development/Test 下可设, Production 模式下 startup fail-fast
    pub mock_upstream_endpoint: Option<Uri>,
    /// W11-D2: 运行时模式 (生产/开发/测试), 默认 Production
    /// 生产模式下严禁 mock_upstream_endpoint 设置 (validate() 强制)
    pub runtime_mode: RuntimeMode,
    /// control plane 单次 RPC deadline, 默认 200ms
    pub control_plane_timeout_ms: u64,
    /// route query 失败后的额外重试次数, 默认 1
    pub control_plane_retry_attempts: usize,
    /// 兼容旧配置: 路由计划有意不做本地缓存, 因其携带 per-attempt 租约凭据
    pub route_cache_ttl_ms: u64,
    /// circuit breaker 连续失败阈值
    pub control_plane_circuit_breaker_failures: u32,
    /// circuit breaker 打开后的冷却时间
    pub control_plane_circuit_breaker_cooldown_ms: u64,
    /// 进程级 in-flight 请求上限; 0 表示关闭卸载, 仅保留观测计数
    pub max_in_flight_requests: usize,
    /// 进程级已接受连接上限; 0 表示关闭连接级限制
    pub max_connections: usize,
    /// 过载 503 的 Retry-After 秒数
    pub overload_retry_after_secs: u64,
    /// 上游响应 body 两帧之间的 idle 超时; 0 表示关闭
    pub upstream_body_idle_timeout_ms: u64,
    /// 下游客户端写入 idle 超时; 0 表示关闭
    pub downstream_write_idle_timeout_ms: u64,
    /// 入站请求 body 两帧之间的 idle 超时; 0 表示关闭
    pub request_body_idle_timeout_ms: u64,
    /// HTTP/1 header 读取超时; 0 表示关闭
    pub server_header_read_timeout_ms: u64,

    // ── W12-A D-4 attempt durable spool (生产必启, 第三方 P1 finding 2026-05-24) ──
    /// attempt durable spool 是否启用 (生产模式 validate 强制 = true)
    pub spool_enabled: bool,
    /// spool 持久化目录 (生产模式 enabled=true 时 validate 强制非空 + 可创建)
    pub spool_dir: PathBuf,
    /// spool 总配额 (字节); pending+reserved 越线则 reserve() 返 503
    pub spool_max_bytes: u64,
    /// reserve() 触发 backpressure 的高水位字节 (默认 = max_bytes * 8/10)
    pub spool_high_watermark_bytes: u64,
    /// 单条 record 编码后最大字节数 (oversized record 直接 reject)
    pub spool_max_record_bytes: u64,
    /// replay worker 周期 (毫秒); 控制 spool ack 后的延迟
    pub spool_replay_interval_ms: u64,
    /// fsync 开关 (生产 = true 保账务; test 可关以加速)
    pub spool_fsync_on_write: bool,
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
    /// W11-D2: 运行时模式 (production/development/test), 默认 production
    #[serde(default = "default_runtime_mode")]
    runtime_mode: String,
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
    #[serde(default)]
    max_in_flight_requests: usize,
    #[serde(default)]
    max_connections: usize,
    #[serde(default = "default_overload_retry_after_secs")]
    overload_retry_after_secs: u64,
    #[serde(default = "default_upstream_body_idle_timeout_ms")]
    upstream_body_idle_timeout_ms: u64,
    #[serde(default = "default_downstream_write_idle_timeout_ms")]
    downstream_write_idle_timeout_ms: u64,
    #[serde(default = "default_request_body_idle_timeout_ms")]
    request_body_idle_timeout_ms: u64,
    #[serde(default = "default_server_header_read_timeout_ms")]
    server_header_read_timeout_ms: u64,

    // ── W12-A D-4 spool 持久化 (第三方 P1 finding 2026-05-24) ──
    #[serde(default = "default_spool_enabled")]
    spool_enabled: bool,
    #[serde(default)]
    spool_dir: String,
    #[serde(default = "default_spool_max_bytes")]
    spool_max_bytes: u64,
    /// 0 = 自动 = max_bytes * 8/10; 非 0 直接生效
    #[serde(default)]
    spool_high_watermark_bytes: u64,
    #[serde(default = "default_spool_max_record_bytes")]
    spool_max_record_bytes: u64,
    #[serde(default = "default_spool_replay_interval_ms")]
    spool_replay_interval_ms: u64,
    #[serde(default = "default_spool_fsync_on_write")]
    spool_fsync_on_write: bool,
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
        // W11-D2: 生产模式严禁 mock upstream — 防止账务/计费旁路 + attempt 上报丢失。
        // mutation marker: 删除本块 → production_mode_rejects_mock_upstream 测试变绿 (应红)。
        if self.runtime_mode.is_production() && self.mock_upstream_endpoint.is_some() {
            return Err(GatewayError::Config(
                "HUAKAI_MOCK_UPSTREAM_ENDPOINT must not be set when HUAKAI_RUNTIME_MODE=production \
                 (W11-D2 fail-fast: production startup with mock upstream would bypass account planning, \
                 attempt reporting, and billing). Use HUAKAI_RUNTIME_MODE=development or test to enable \
                 mock upstream for local development/testing only."
                    .to_owned(),
            ));
        }

        // W12-A D-4 第三方 P1 finding 2026-05-24: 生产必须启 durable spool;
        // dev/test 仍允许 disabled (旧 in-memory drop 路径) 不破坏现有测试。
        // mutation: 删除本块 → production_mode_without_spool_dir_fails_fast 测试变绿应红;
        //           production 启动不带 HUAKAI_SPOOL_DIR 时 attempt durable code 默认 disabled
        //           = 所有 D-4 替补/replay/backpressure 形同虚设 → 账务静默丢失。
        if self.runtime_mode.is_production() && !self.spool_enabled {
            return Err(GatewayError::Config(
                "HUAKAI_SPOOL_ENABLED must be true when HUAKAI_RUNTIME_MODE=production \
                 (W12-A D-4: production 必须 durable spool, 否则 attempt report drop 时账务静默丢失)。\
                 同时必须设 HUAKAI_SPOOL_DIR=<absolute path 可写目录>。"
                    .to_owned(),
            ));
        }
        if self.spool_enabled && self.spool_dir.as_os_str().is_empty() {
            return Err(GatewayError::Config(
                "HUAKAI_SPOOL_DIR must be set when HUAKAI_SPOOL_ENABLED=true \
                 (W12-A D-4: 不指定 dir 则 AttemptSpool::open 拒绝)。"
                    .to_owned(),
            ));
        }
        // Codex round 2 P2 fix 2026-05-24: production + 相对路径 -> 不同 CWD 重启会去到不同目录,
        // 上次 spool 不会被 replay -> 账务静默丢失。production 强制绝对路径。
        if self.runtime_mode.is_production()
            && self.spool_enabled
            && !self.spool_dir.is_absolute()
        {
            return Err(GatewayError::Config(format!(
                "HUAKAI_SPOOL_DIR must be an absolute path in production (got {:?}) — \
                 相对路径在不同 CWD 重启时会指向不同目录, 上次 spool 不被 replay -> 账务丢失。",
                self.spool_dir
            )));
        }
        if self.spool_enabled {
            // 启动期主动创建 + 写探针, 把 "目录不可写" 从运行时 503 / replay 卡死提前到 fail-fast。
            std::fs::create_dir_all(&self.spool_dir).map_err(|err| {
                GatewayError::Config(format!(
                    "HUAKAI_SPOOL_DIR {:?} not creatable: {err}",
                    self.spool_dir
                ))
            })?;
            // 简易写权限探针: 在 spool_dir 下写 + 删一个临时文件; 失败表示权限不足。
            let probe = self
                .spool_dir
                .join(format!(".huakai-spool-startup-probe-{}", std::process::id()));
            std::fs::write(&probe, b"").map_err(|err| {
                GatewayError::Config(format!(
                    "HUAKAI_SPOOL_DIR {:?} not writable: {err}",
                    self.spool_dir
                ))
            })?;
            let _ = std::fs::remove_file(&probe);
        }
        if self.spool_enabled && self.spool_max_bytes == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_SPOOL_MAX_BYTES must be > 0 when spool enabled".to_owned(),
            ));
        }
        if self.spool_enabled && self.spool_max_record_bytes == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_SPOOL_MAX_RECORD_BYTES must be > 0 when spool enabled".to_owned(),
            ));
        }
        // Codex round 1 P2 fix 2026-05-24: replay_interval=0 会让 tokio::time::interval panic,
        // 把 replay worker 整个炸掉 (生产 = 静默丢失 D-4 保护)。validate 阶段提前拒绝。
        if self.spool_enabled && self.spool_replay_interval_ms == 0 {
            return Err(GatewayError::Config(
                "HUAKAI_SPOOL_REPLAY_INTERVAL_MS must be > 0 when spool enabled \
                 (tokio interval 0 会 panic 让 replay worker 整个炸掉)"
                    .to_owned(),
            ));
        }
        // Codex round 2 P2 fix 2026-05-24: watermark 必须满足 max_record < watermark <= max_bytes,
        // 否则:
        // - watermark > max_bytes -> 绕过 quota 限制 (pending+reserved 越限仍 reserve 成功)
        // - watermark <= max_record -> 空 spool 上每个 reserve 都立刻 backpressure (Brick request)
        // 仅当 explicit watermark != 0 时才校验 (0 = auto = max_bytes * 8/10 已天然合规)。
        if self.spool_enabled && self.spool_high_watermark_bytes != 0 {
            if self.spool_high_watermark_bytes > self.spool_max_bytes {
                return Err(GatewayError::Config(format!(
                    "HUAKAI_SPOOL_HIGH_WATERMARK_BYTES ({}) > HUAKAI_SPOOL_MAX_BYTES ({}) — \
                     watermark 越限会绕过 quota 限制, 让 spool 无限制增长。",
                    self.spool_high_watermark_bytes, self.spool_max_bytes
                )));
            }
            if self.spool_high_watermark_bytes <= self.spool_max_record_bytes {
                return Err(GatewayError::Config(format!(
                    "HUAKAI_SPOOL_HIGH_WATERMARK_BYTES ({}) <= HUAKAI_SPOOL_MAX_RECORD_BYTES ({}) — \
                     空 spool 上每个 reserve 都会立刻越过 watermark = 永远 backpressure, \
                     所有 forward_planned 请求都被砍 503。watermark 必须严格大于 max_record_bytes。",
                    self.spool_high_watermark_bytes, self.spool_max_record_bytes
                )));
            }
        }
        // Codex round 2 P2 fix 2026-05-24: production 禁止关 fsync — power-loss / crash 在
        // response 已 commit 但内核未 flush rename 之间会丢 attempt 记录, 推翻 D-4 durable 保证。
        // dev/test 允许关 fsync 加速 IO。
        if self.runtime_mode.is_production()
            && self.spool_enabled
            && !self.spool_fsync_on_write
        {
            return Err(GatewayError::Config(
                "HUAKAI_SPOOL_FSYNC_ON_WRITE=false 在 production 模式不允许 \
                 (crash 在 commit response 后但 fsync 之前会丢账务记录, 推翻 D-4 durable 保证)。\
                 dev/test 模式可关。"
                    .to_owned(),
            ));
        }
        Ok(())
    }

    /// 构造 attempt durable spool 选项 — 由 lib.rs::GatewayState::new 调用接到 AttemptReporter。
    ///
    /// 高水位 0 表示 "自动 = max_bytes * 8/10" 与 AttemptSpoolOptions::default 同源,
    /// 部署只需指定 HUAKAI_SPOOL_MAX_BYTES 即可获得合理 watermark。
    pub fn attempt_spool_options(&self) -> crate::attempt_reporter::AttemptSpoolOptions {
        let high_watermark_bytes = if self.spool_high_watermark_bytes == 0 {
            self.spool_max_bytes.saturating_mul(8) / 10
        } else {
            self.spool_high_watermark_bytes
        };
        crate::attempt_reporter::AttemptSpoolOptions {
            enabled: self.spool_enabled,
            dir: self.spool_dir.clone(),
            max_bytes: self.spool_max_bytes,
            high_watermark_bytes,
            max_record_bytes: self.spool_max_record_bytes,
            replay_interval: std::time::Duration::from_millis(self.spool_replay_interval_ms),
            replay_batch_size: 128,
            fsync_on_write: self.spool_fsync_on_write,
        }
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

        // W11-D2: 解析运行时模式 (默认 production, 严格防误启用)
        let runtime_mode = RuntimeMode::parse(&raw.runtime_mode)?;

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
            runtime_mode,
            control_plane_timeout_ms: raw.control_plane_timeout_ms,
            control_plane_retry_attempts: raw.control_plane_retry_attempts,
            route_cache_ttl_ms: raw.route_cache_ttl_ms,
            control_plane_circuit_breaker_failures: raw.control_plane_circuit_breaker_failures,
            control_plane_circuit_breaker_cooldown_ms: raw
                .control_plane_circuit_breaker_cooldown_ms,
            max_in_flight_requests: raw.max_in_flight_requests,
            max_connections: raw.max_connections,
            overload_retry_after_secs: raw.overload_retry_after_secs,
            upstream_body_idle_timeout_ms: raw.upstream_body_idle_timeout_ms,
            downstream_write_idle_timeout_ms: raw.downstream_write_idle_timeout_ms,
            request_body_idle_timeout_ms: raw.request_body_idle_timeout_ms,
            server_header_read_timeout_ms: raw.server_header_read_timeout_ms,
            spool_enabled: raw.spool_enabled,
            spool_dir: PathBuf::from(raw.spool_dir),
            spool_max_bytes: raw.spool_max_bytes,
            spool_high_watermark_bytes: raw.spool_high_watermark_bytes,
            spool_max_record_bytes: raw.spool_max_record_bytes,
            spool_replay_interval_ms: raw.spool_replay_interval_ms,
            spool_fsync_on_write: raw.spool_fsync_on_write,
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

/// W11-D2: 默认运行时模式 = production (最严格, 拒绝 mock upstream 等危险开关)。
/// 若 HUAKAI_RUNTIME_MODE 未显式设置, 视为生产, 防止误启用本地开发便利到生产环境。
fn default_runtime_mode() -> String {
    "production".to_owned()
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

fn default_overload_retry_after_secs() -> u64 {
    1
}

fn default_upstream_body_idle_timeout_ms() -> u64 {
    300_000
}

fn default_downstream_write_idle_timeout_ms() -> u64 {
    60_000
}

fn default_request_body_idle_timeout_ms() -> u64 {
    30_000
}

fn default_server_header_read_timeout_ms() -> u64 {
    30_000
}

// W12-A D-4 spool 默认值 (与 AttemptSpoolOptions::default 同源, 第三方 P1 finding 2026-05-24)。
// production 默认 enabled=true 强制运维显式提供 HUAKAI_SPOOL_DIR;
// dev/test 默认 enabled=false 保留旧 in-memory drop 路径不破坏已有测试。
fn default_spool_enabled() -> bool {
    false
}

fn default_spool_max_bytes() -> u64 {
    1024 * 1024 * 1024 // 1 GiB
}

fn default_spool_max_record_bytes() -> u64 {
    64 * 1024 // 64 KiB
}

fn default_spool_replay_interval_ms() -> u64 {
    250
}

fn default_spool_fsync_on_write() -> bool {
    true
}

#[cfg(test)]
mod tests {
    use super::*;

    /// W12-A D-4 第三方 P1 finding 2026-05-24 后: production validate 要求 spool 启用 + dir,
    /// 所以 valid_env() 默认带上 HUAKAI_SPOOL_ENABLED=true + 一个 tmp dir, 让大多数 config
    /// 测试仍 round-trip 解析成功; 新增的 production_mode_without_spool_dir_fails_fast 测试
    /// 显式删除这两个键以验证守门。
    fn test_spool_dir() -> String {
        let mut path = std::env::temp_dir();
        path.push("huakai-config-test-spool");
        path.to_string_lossy().into_owned()
    }

    fn valid_env() -> Vec<(String, String)> {
        let spool_dir = test_spool_dir();
        vec![
            ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
            (
                "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
                "http://127.0.0.1:48080".to_owned(),
            ),
            ("HUAKAI_LOG_LEVEL".to_owned(), "debug".to_owned()),
            ("HUAKAI_JSON_LOGS".to_owned(), "true".to_owned()),
            ("HUAKAI_WORKER_THREADS".to_owned(), "2".to_owned()),
            ("HUAKAI_MAX_IN_FLIGHT_REQUESTS".to_owned(), "0".to_owned()),
            ("HUAKAI_MAX_CONNECTIONS".to_owned(), "0".to_owned()),
            ("HUAKAI_OVERLOAD_RETRY_AFTER_SECS".to_owned(), "1".to_owned()),
            (
                "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS".to_owned(),
                "300000".to_owned(),
            ),
            (
                "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS".to_owned(),
                "60000".to_owned(),
            ),
            (
                "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS".to_owned(),
                "30000".to_owned(),
            ),
            (
                "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS".to_owned(),
                "30000".to_owned(),
            ),
            ("HUAKAI_SPOOL_ENABLED".to_owned(), "true".to_owned()),
            ("HUAKAI_SPOOL_DIR".to_owned(), spool_dir),
            // Codex round 2 P2 fix 2026-05-24: production 模式禁止 fsync=false。
            // valid_env 默认 production, 故必须 fsync=true (probe 文件 fsync 极快, 不影响测试)。
            // dev/test 路径可显式覆盖为 false (见 development_mode_allows_spool_fsync_off)。
            ("HUAKAI_SPOOL_FSYNC_ON_WRITE".to_owned(), "true".to_owned()),
        ]
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
        assert_eq!(cfg.max_in_flight_requests, 0);
        assert_eq!(cfg.max_connections, 0);
        assert_eq!(cfg.overload_retry_after_secs, 1);
        assert_eq!(cfg.upstream_body_idle_timeout_ms, 300_000);
        assert_eq!(cfg.downstream_write_idle_timeout_ms, 60_000);
        assert_eq!(cfg.request_body_idle_timeout_ms, 30_000);
        assert_eq!(cfg.server_header_read_timeout_ms, 30_000);
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
    fn config_accepts_zero_resource_limits_as_disabled() {
        let cfg = StartupConfig::from_env_iter(valid_env()).expect("valid config");

        assert_eq!(cfg.max_in_flight_requests, 0);
        assert_eq!(cfg.max_connections, 0);
        assert!(cfg.validate().is_ok(), "资源上限 0 表示关闭卸载, 应被接受");
    }

    #[test]
    fn config_parses_resource_limit_overrides() {
        let mut env = valid_env();
        env.retain(|(k, _)| {
            !matches!(
                k.as_str(),
                "HUAKAI_MAX_IN_FLIGHT_REQUESTS"
                    | "HUAKAI_MAX_CONNECTIONS"
                    | "HUAKAI_OVERLOAD_RETRY_AFTER_SECS"
            )
        });
        env.push(("HUAKAI_MAX_IN_FLIGHT_REQUESTS".to_owned(), "17".to_owned()));
        env.push(("HUAKAI_MAX_CONNECTIONS".to_owned(), "29".to_owned()));
        env.push((
            "HUAKAI_OVERLOAD_RETRY_AFTER_SECS".to_owned(),
            "3".to_owned(),
        ));

        let cfg = StartupConfig::from_env_iter(env).expect("resource limit config 应解析成功");

        assert_eq!(cfg.max_in_flight_requests, 17);
        assert_eq!(cfg.max_connections, 29);
        assert_eq!(cfg.overload_retry_after_secs, 3);
    }

    #[test]
    fn config_parses_timeout_overrides() {
        let mut env = valid_env();
        env.retain(|(k, _)| {
            !matches!(
                k.as_str(),
                "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS"
                    | "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS"
                    | "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS"
                    | "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS"
            )
        });
        env.push((
            "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS".to_owned(),
            "301".to_owned(),
        ));
        env.push((
            "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS".to_owned(),
            "302".to_owned(),
        ));
        env.push((
            "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS".to_owned(),
            "303".to_owned(),
        ));
        env.push((
            "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS".to_owned(),
            "304".to_owned(),
        ));

        let cfg = StartupConfig::from_env_iter(env).expect("timeout config 应解析成功");

        assert_eq!(cfg.upstream_body_idle_timeout_ms, 301);
        assert_eq!(cfg.downstream_write_idle_timeout_ms, 302);
        assert_eq!(cfg.request_body_idle_timeout_ms, 303);
        assert_eq!(cfg.server_header_read_timeout_ms, 304);
    }

    #[test]
    fn config_accepts_zero_timeouts_as_disabled() {
        let mut env = valid_env();
        env.retain(|(k, _)| {
            !matches!(
                k.as_str(),
                "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS"
                    | "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS"
                    | "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS"
                    | "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS"
            )
        });
        env.push((
            "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS".to_owned(),
            "0".to_owned(),
        ));
        env.push((
            "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS".to_owned(),
            "0".to_owned(),
        ));
        env.push((
            "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS".to_owned(),
            "0".to_owned(),
        ));
        env.push((
            "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS".to_owned(),
            "0".to_owned(),
        ));

        let cfg = StartupConfig::from_env_iter(env).expect("0 timeout 应表示关闭并被接受");

        assert_eq!(cfg.upstream_body_idle_timeout_ms, 0);
        assert_eq!(cfg.downstream_write_idle_timeout_ms, 0);
        assert_eq!(cfg.request_body_idle_timeout_ms, 0);
        assert_eq!(cfg.server_header_read_timeout_ms, 0);
        assert!(cfg.validate().is_ok());
    }

    #[test]
    fn config_uses_timeout_defaults_when_env_omits_timeout_keys() {
        let mut env = valid_env();
        env.retain(|(k, _)| {
            !matches!(
                k.as_str(),
                "HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS"
                    | "HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS"
                    | "HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS"
                    | "HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS"
            )
        });

        let cfg = StartupConfig::from_env_iter(env).expect("timeout 默认值应解析成功");

        assert_eq!(cfg.upstream_body_idle_timeout_ms, 300_000);
        assert_eq!(cfg.downstream_write_idle_timeout_ms, 60_000);
        assert_eq!(cfg.request_body_idle_timeout_ms, 30_000);
        assert_eq!(cfg.server_header_read_timeout_ms, 30_000);
    }

    #[test]
    fn config_with_mock_upstream_endpoint_parses_correctly() {
        // W11-D2: mock upstream 仅 development/test 模式可用; production 模式见
        // production_mode_rejects_mock_upstream 反向测试
        let mut env = valid_env();
        env.push((
            "HUAKAI_MOCK_UPSTREAM_ENDPOINT".to_owned(),
            "http://127.0.0.1:48100".to_owned(),
        ));
        env.push(("HUAKAI_RUNTIME_MODE".to_owned(), "development".to_owned()));
        let cfg = StartupConfig::from_env_iter(env)
            .expect("带 mock_upstream_endpoint 的 config 应解析成功 (dev mode)");
        assert_eq!(
            cfg.mock_upstream_endpoint.unwrap().to_string(),
            "http://127.0.0.1:48100/"
        );
        assert_eq!(cfg.runtime_mode, RuntimeMode::Development);
    }

    // ============== W11-D2: runtime_mode 守门测试组 ==============

    #[test]
    fn production_mode_rejects_mock_upstream() {
        // W11-D2 主判别性测试: 生产模式 + mock 上游 = fail-fast 拒绝启动。
        // mutation marker: 若删除 validate() 中的 production+mock 守门, 本测试变绿 (应红)。
        let mut env = valid_env();
        env.push((
            "HUAKAI_MOCK_UPSTREAM_ENDPOINT".to_owned(),
            "http://127.0.0.1:48100".to_owned(),
        ));
        // 不显式设 HUAKAI_RUNTIME_MODE → 默认 production (见 default_runtime_mode_is_production)
        let result = StartupConfig::from_env_iter(env);
        assert!(
            result.is_err(),
            "生产模式 + mock 上游 应 fail-fast (W11-D2)"
        );
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("HUAKAI_MOCK_UPSTREAM_ENDPOINT") && err_msg.contains("production"),
            "错误消息应明确提及 mock var 名 + production 模式, got: {err_msg}"
        );
    }

    #[test]
    fn default_runtime_mode_is_production() {
        // W11-D2 关键不变量: HUAKAI_RUNTIME_MODE 未设时默认为 production (最严格,
        // 不是宽松模式)。本测试与 production_mode_rejects_mock_upstream 共同证明
        // "未设环境变量 + 误设 mock upstream → fail-fast" 是默认安全姿态。
        let cfg = StartupConfig::from_env_iter(valid_env()).expect("默认 env 应解析成功");
        assert_eq!(cfg.runtime_mode, RuntimeMode::Production);
    }

    /// W12-A D-4 第三方 P1 finding 2026-05-24: 生产模式启动若未显式开 durable spool
    /// (HUAKAI_SPOOL_ENABLED + HUAKAI_SPOOL_DIR), 必须 fail-fast — 否则 attempt drop
    /// 后账务静默丢失, D-4 的所有 replay/backpressure/persist 代码形同虚设。
    ///
    /// mutation: 删除 validate() 里 production+spool 守门 → 本测试变绿应红;
    /// production 启动后 attempt_reporter 的 has_durable_spool() 仍 false → P1 finding 复现。
    #[test]
    fn production_mode_without_spool_dir_fails_fast() {
        // valid_env 默认含 HUAKAI_SPOOL_ENABLED=true + dir; 反向去掉验证守门
        let env_no_spool: Vec<(String, String)> = valid_env()
            .into_iter()
            .filter(|(k, _)| k != "HUAKAI_SPOOL_ENABLED" && k != "HUAKAI_SPOOL_DIR")
            .collect();
        let result = StartupConfig::from_env_iter(env_no_spool);
        assert!(
            result.is_err(),
            "生产模式 + spool 未启用 应 fail-fast (W12-A D-4 第三方 P1 finding)"
        );
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("HUAKAI_SPOOL_ENABLED") || err_msg.contains("HUAKAI_SPOOL_DIR"),
            "错误消息应明确提及 spool var, 实际: {err_msg}"
        );
    }

    /// 派生: 生产 + spool_enabled=true 但 dir 为空也必须 fail-fast。
    /// mutation: 删 "spool_dir.is_empty() -> Err" 分支 -> 测试红。
    #[test]
    fn production_mode_with_spool_enabled_but_empty_dir_fails_fast() {
        let env_empty_dir: Vec<(String, String)> = valid_env()
            .into_iter()
            .filter(|(k, _)| k != "HUAKAI_SPOOL_DIR")
            .collect();
        // HUAKAI_SPOOL_ENABLED 仍是 true (来自 valid_env), HUAKAI_SPOOL_DIR 缺失
        let result = StartupConfig::from_env_iter(env_empty_dir);
        assert!(result.is_err(), "spool enabled + 空 dir 应 fail-fast");
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("HUAKAI_SPOOL_DIR"),
            "错误消息应提及 spool dir, 实际: {err_msg}"
        );
    }

    /// 派生: dev/test 模式可以 spool disabled (兼容旧 in-memory drop 路径, 不破坏现有测试)。
    /// mutation: 改 validate() 让 spool 也对 non-production 强制 -> 此测试红。
    #[test]
    fn development_mode_allows_spool_disabled() {
        let mut env: Vec<(String, String)> = valid_env()
            .into_iter()
            .filter(|(k, _)| k != "HUAKAI_SPOOL_ENABLED" && k != "HUAKAI_SPOOL_DIR")
            .collect();
        env.push(("HUAKAI_RUNTIME_MODE".to_owned(), "development".to_owned()));
        let cfg = StartupConfig::from_env_iter(env)
            .expect("dev 模式 + spool disabled 应允许 (兼容旧路径)");
        assert!(!cfg.spool_enabled);
        assert!(cfg.spool_dir.as_os_str().is_empty());
    }

    /// Codex round 2 P2 fix 2026-05-24: production 必须用绝对路径作 spool_dir,
    /// 否则重启时 CWD 漂移指向不同目录, 旧 spool 不被 replay -> 账务静默丢失。
    /// mutation: 删 is_absolute() 守门 -> 测试红 (相对路径会被接受)。
    #[test]
    fn production_mode_rejects_relative_spool_dir() {
        let mut env: Vec<(String, String)> = valid_env()
            .into_iter()
            .filter(|(k, _)| k != "HUAKAI_SPOOL_DIR")
            .collect();
        env.push(("HUAKAI_SPOOL_DIR".to_owned(), "relative/spool".to_owned()));
        let result = StartupConfig::from_env_iter(env);
        assert!(result.is_err(), "production + 相对路径 spool_dir 应 fail-fast");
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("absolute"),
            "错误消息应提及 absolute, 实际: {err_msg}"
        );
    }

    /// Codex round 2 P2 fix 2026-05-24: spool watermark 超过 max_bytes 会绕过 quota,
    /// 让 pending 无限制增长。validate 必须拒绝。
    /// mutation: 删 watermark > max_bytes 守门 -> 测试红。
    #[test]
    fn spool_high_watermark_exceeding_max_bytes_fails_fast() {
        let mut env = valid_env();
        env.retain(|(k, _)| {
            k != "HUAKAI_SPOOL_HIGH_WATERMARK_BYTES" && k != "HUAKAI_SPOOL_MAX_BYTES"
        });
        env.push(("HUAKAI_SPOOL_MAX_BYTES".to_owned(), "1000000".to_owned()));
        env.push((
            "HUAKAI_SPOOL_HIGH_WATERMARK_BYTES".to_owned(),
            "2000000".to_owned(),
        ));
        let result = StartupConfig::from_env_iter(env);
        assert!(
            result.is_err(),
            "spool watermark > max_bytes 应 fail-fast (绕过 quota)"
        );
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("HIGH_WATERMARK_BYTES") && err_msg.contains("MAX_BYTES"),
            "错误消息应提及二者关系, 实际: {err_msg}"
        );
    }

    /// Codex round 2 P2 fix 2026-05-24: spool watermark <= max_record_bytes 会让每个空 spool
    /// reserve 立刻 backpressure (Brick 所有请求)。validate 必须拒绝。
    /// mutation: 删 watermark <= max_record 守门 -> 测试红。
    #[test]
    fn spool_high_watermark_at_or_below_max_record_fails_fast() {
        let mut env = valid_env();
        env.retain(|(k, _)| {
            k != "HUAKAI_SPOOL_HIGH_WATERMARK_BYTES" && k != "HUAKAI_SPOOL_MAX_RECORD_BYTES"
        });
        env.push((
            "HUAKAI_SPOOL_MAX_RECORD_BYTES".to_owned(),
            "65536".to_owned(),
        ));
        env.push((
            "HUAKAI_SPOOL_HIGH_WATERMARK_BYTES".to_owned(),
            "65536".to_owned(), // 等于 max_record -> 空 spool 也会立刻越线
        ));
        let result = StartupConfig::from_env_iter(env);
        assert!(
            result.is_err(),
            "spool watermark <= max_record_bytes 应 fail-fast (会 Brick 所有请求)"
        );
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("HIGH_WATERMARK_BYTES") && err_msg.contains("MAX_RECORD_BYTES"),
            "错误消息应提及二者关系, 实际: {err_msg}"
        );
    }

    /// Codex round 2 P2 fix 2026-05-24: production 禁止 fsync=false (crash 丢 durable record)。
    /// mutation: 删 production + fsync=false 守门 -> 测试红。
    #[test]
    fn production_mode_rejects_spool_fsync_off() {
        let mut env = valid_env();
        env.retain(|(k, _)| k != "HUAKAI_SPOOL_FSYNC_ON_WRITE");
        env.push((
            "HUAKAI_SPOOL_FSYNC_ON_WRITE".to_owned(),
            "false".to_owned(),
        ));
        // runtime_mode 默认 production (来自 default_runtime_mode)
        let result = StartupConfig::from_env_iter(env);
        assert!(result.is_err(), "production + fsync=false 应 fail-fast");
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("FSYNC") && err_msg.contains("production"),
            "错误消息应提及 fsync + production, 实际: {err_msg}"
        );
    }

    /// 派生: dev/test 模式允许 fsync=false 加速 IO。
    /// mutation: 改 fsync 检查作用于所有模式 -> 测试红。
    #[test]
    fn development_mode_allows_spool_fsync_off() {
        let mut env = valid_env();
        env.retain(|(k, _)| k != "HUAKAI_SPOOL_FSYNC_ON_WRITE");
        env.push((
            "HUAKAI_SPOOL_FSYNC_ON_WRITE".to_owned(),
            "false".to_owned(),
        ));
        env.push(("HUAKAI_RUNTIME_MODE".to_owned(), "development".to_owned()));
        let cfg = StartupConfig::from_env_iter(env)
            .expect("dev 模式 + fsync=false 应允许 (加速测试 IO)");
        assert!(!cfg.spool_fsync_on_write);
    }

    /// Codex round 1 P2 fix 2026-05-24: spool_enabled + replay_interval=0
    /// 会让 tokio::time::interval panic 整个 replay worker, validate 必须提前拒绝。
    /// mutation: 删 validate() 里的 replay_interval=0 守门 -> 测试红。
    #[test]
    fn spool_enabled_with_zero_replay_interval_fails_fast() {
        let mut env = valid_env();
        env.push((
            "HUAKAI_SPOOL_REPLAY_INTERVAL_MS".to_owned(),
            "0".to_owned(),
        ));
        let result = StartupConfig::from_env_iter(env);
        assert!(
            result.is_err(),
            "spool enabled + replay_interval=0 应 fail-fast (避免 tokio interval panic)"
        );
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("HUAKAI_SPOOL_REPLAY_INTERVAL_MS"),
            "错误消息应提及 replay interval 字段, 实际: {err_msg}"
        );
    }

    /// 派生: attempt_spool_options() 在 high_watermark=0 时自动 = max_bytes * 8/10
    /// (与 AttemptSpoolOptions::default 同源)。
    /// mutation: 改成 (max_bytes) 即 100% 或 (max_bytes / 2) 即 50% -> 算式断言红。
    #[test]
    fn attempt_spool_options_auto_high_watermark_is_80_percent_of_max_bytes() {
        let mut env = valid_env();
        env.retain(|(k, _)| {
            k != "HUAKAI_SPOOL_HIGH_WATERMARK_BYTES" && k != "HUAKAI_SPOOL_MAX_BYTES"
        });
        env.push((
            "HUAKAI_SPOOL_MAX_BYTES".to_owned(),
            (1_000u64 * 1024 * 1024).to_string(), // 1000 MiB
        ));
        // 不显式设 high watermark -> auto
        let cfg = StartupConfig::from_env_iter(env).expect("valid spool config");
        let opts = cfg.attempt_spool_options();
        assert_eq!(opts.max_bytes, 1_000 * 1024 * 1024);
        assert_eq!(
            opts.high_watermark_bytes,
            (1_000u64 * 1024 * 1024) * 8 / 10,
            "high_watermark_bytes=0 时应等于 max_bytes * 8/10"
        );
    }

    #[test]
    fn runtime_mode_rejects_invalid_value() {
        // 未知值应 fail-fast 而非静默回退到某个模式 (防止 typo 误启用宽松配置)
        let mut env = valid_env();
        env.push(("HUAKAI_RUNTIME_MODE".to_owned(), "staging".to_owned()));
        let result = StartupConfig::from_env_iter(env);
        assert!(result.is_err(), "未知 runtime_mode 应 fail-fast");
        let err_msg = result.unwrap_err().to_string();
        assert!(
            err_msg.contains("HUAKAI_RUNTIME_MODE"),
            "错误消息应提及 runtime_mode 字段: {err_msg}"
        );
    }

    #[test]
    fn runtime_mode_accepts_short_aliases() {
        // 接受 prod/dev 简写 (case-insensitive)
        let mut env = valid_env();
        env.push(("HUAKAI_RUNTIME_MODE".to_owned(), "PROD".to_owned()));
        let cfg = StartupConfig::from_env_iter(env).expect("PROD 别名应解析为 Production");
        assert_eq!(cfg.runtime_mode, RuntimeMode::Production);

        let mut env = valid_env();
        env.push(("HUAKAI_RUNTIME_MODE".to_owned(), "dev".to_owned()));
        env.push((
            "HUAKAI_MOCK_UPSTREAM_ENDPOINT".to_owned(),
            "http://127.0.0.1:48100".to_owned(),
        ));
        let cfg = StartupConfig::from_env_iter(env)
            .expect("dev 别名 + mock 上游应解析成功");
        assert_eq!(cfg.runtime_mode, RuntimeMode::Development);
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

        let cfg = StartupConfig::from_env_iter(env).expect("::1 endpoint 应允许 HTTP baseline");
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
