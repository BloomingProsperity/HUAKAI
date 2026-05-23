// M-rust-9 heartbeat — 定时发送心跳并处理 drain_mode 切换
// 每 5 秒向 control plane 发送 HeartbeatRequest, 读取 ack 中的 drain_mode bool。
// drain_mode=true 时拒绝新入连接(503), 已进行中的流继续完成。

use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

use tokio::{task::JoinHandle, time};
use tracing::{debug, info, warn};

use crate::{
    attempt_reporter::AttemptReporter, resource_limits::ResourceLimits,
    route_client::RouteClient, route_proto::v1::HeartbeatRequest,
};

/// 心跳间隔
const HEARTBEAT_INTERVAL: Duration = Duration::from_secs(5);

/// 全局 drain_mode 原子标志 — listener 在接受新连接前检查
static DRAIN_MODE: AtomicBool = AtomicBool::new(false);

// ─── 公共访问器 ───────────────────────────────────────────────────────────────

/// 读取当前 drain_mode 状态 (原子, 无锁)
#[inline]
pub fn is_drain_mode() -> bool {
    DRAIN_MODE.load(Ordering::Acquire)
}

/// 直接设置 drain_mode (测试 / 管理接口使用)
#[inline]
pub fn set_drain_mode(drain: bool) {
    DRAIN_MODE.store(drain, Ordering::Release);
}

// ─── W12-C D-7 heartbeat metrics 源 ────────────────────────────────────────

/// W12-C D-7: heartbeat 报告的真实 gauge 源, 替换原硬编码 0。
///
/// 当前覆盖: in_flight_requests + attempt_report_queue_depth + started_at_unix_ms。
/// `open_upstream_connections` 由 W12-E D-9+O-2 完成 upstream connector 生命周期接线后补;
/// `p95_control_plane_rpc_ms` / `error_rate_1m` 是 P2 直方图项, 留 roadmap。
#[derive(Clone)]
pub struct HeartbeatMetricsSource {
    pub resource_limits: Arc<ResourceLimits>,
    pub attempt_reporter: AttemptReporter,
    pub started_at_unix_ms: i64,
}

/// W12-C D-7: 构建一次性 HeartbeatRequest, 单元测试可直接验证字段。
/// mutation marker: 把 in_flight / queue_depth / started_at 任一改回 0 →
/// heartbeat_carries_real_* 测试断言红。
pub fn build_heartbeat_request(metrics: &HeartbeatMetricsSource) -> HeartbeatRequest {
    let in_flight = metrics.resource_limits.current_in_flight().max(0) as u64;
    let queue_depth = metrics.attempt_reporter.queue_depth() as u64;
    // W12-E O-2: 从 prometheus gauge 拉真实 upstream 连接数; LimitedListener / 未来
    // proxy_engine UpstreamConnectionGuard 写值, heartbeat 读值。当前 lifecycle 已盖
    // ACTIVE_CONNECTIONS (listener accept→drop), OPEN_UPSTREAM_CONNECTIONS 待
    // proxy_engine upstream body 包装后再 inc/dec (后续小切片)。
    let open_upstream_connections = crate::metrics::open_upstream_connections().get().max(0) as u64;

    HeartbeatRequest {
        node_id: "rust-core-gateway".to_owned(),
        build_sha: env!("CARGO_PKG_VERSION").to_owned(),
        schema_version: crate::route_client::ROUTE_SCHEMA_VERSION.to_owned(),
        started_at: metrics.started_at_unix_ms,
        in_flight_requests: in_flight,
        open_upstream_connections,
        attempt_report_queue_depth: queue_depth,
        // P2 roadmap: 需直方图 + 1-min rate window 才能精确, 暂保留 0.0。
        p95_control_plane_rpc_ms: 0.0,
        error_rate_1m: 0.0,
    }
}

// ─── HeartbeatWorker ─────────────────────────────────────────────────────────

/// 心跳 worker — 通过 tokio::spawn 运行, 返回 JoinHandle 供调用方 abort
pub struct HeartbeatWorker {
    task: JoinHandle<()>,
}

impl HeartbeatWorker {
    /// 启动心跳任务。route_client 为 Clone 且 Send+Sync, 直接共享。
    pub fn spawn(route_client: RouteClient, metrics: HeartbeatMetricsSource) -> Self {
        let task = tokio::spawn(heartbeat_loop(route_client, metrics));
        Self { task }
    }

    /// 停止心跳任务
    pub fn abort(&self) {
        self.task.abort();
    }
}

impl Drop for HeartbeatWorker {
    fn drop(&mut self) {
        self.task.abort();
    }
}

// ─── 心跳主循环 ───────────────────────────────────────────────────────────────

async fn heartbeat_loop(route_client: RouteClient, metrics: HeartbeatMetricsSource) {
    let mut interval = time::interval(HEARTBEAT_INTERVAL);
    // 第一个 tick 立即触发 (tokio::interval 默认行为)
    loop {
        interval.tick().await;
        send_heartbeat_once(&route_client, &metrics).await;
    }
}

async fn send_heartbeat_once(route_client: &RouteClient, metrics: &HeartbeatMetricsSource) {
    let request = build_heartbeat_request(metrics);

    match route_client.heartbeat(request).await {
        Ok(resp) => {
            let new_drain = resp.drain_mode;
            let old_drain = DRAIN_MODE.swap(new_drain, Ordering::AcqRel);

            if old_drain != new_drain {
                if new_drain {
                    info!("drain_mode 已启用 — 拒绝新连接, 等待进行中流完成");
                } else {
                    info!("drain_mode 已关闭 — 恢复接受新连接");
                }
            } else {
                debug!(drain_mode = new_drain, "heartbeat ack 收到");
            }
        }
        Err(err) => {
            warn!(error = %err, "heartbeat 发送失败, drain_mode 保持当前状态");
        }
    }
}

// ─── 单元测试 ─────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{config::StartupConfig, route_client::RouteClient};

    #[test]
    fn drain_mode_default_is_false() {
        // 重置确保测试隔离
        set_drain_mode(false);
        assert!(!is_drain_mode(), "初始 drain_mode 应为 false");
    }

    #[test]
    fn drain_mode_set_and_clear() {
        set_drain_mode(true);
        assert!(is_drain_mode(), "设置后 drain_mode 应为 true");
        set_drain_mode(false);
        assert!(!is_drain_mode(), "清除后 drain_mode 应为 false");
    }

    fn fixture_config() -> StartupConfig {
        StartupConfig::from_env_iter(vec![
            ("HUAKAI_LISTEN_ADDR".to_owned(), "127.0.0.1:0".to_owned()),
            (
                "HUAKAI_CONTROL_PLANE_ENDPOINT".to_owned(),
                "http://127.0.0.1:48080".to_owned(),
            ),
            ("HUAKAI_TRANSPORT_BASELINE".to_owned(), "http".to_owned()),
            ("HUAKAI_LOG_LEVEL".to_owned(), "debug".to_owned()),
            ("HUAKAI_JSON_LOGS".to_owned(), "false".to_owned()),
            ("HUAKAI_WORKER_THREADS".to_owned(), "2".to_owned()),
            ("HUAKAI_MAX_IN_FLIGHT_REQUESTS".to_owned(), "100".to_owned()),
            (
                "HUAKAI_RUNTIME_MODE".to_owned(),
                "development".to_owned(),
            ),
        ])
        .expect("heartbeat fixture config 应可解析")
    }

    fn fixture_attempt_reporter() -> AttemptReporter {
        let route_client = RouteClient::new(
            "http://127.0.0.1:48080"
                .parse()
                .expect("test endpoint 应可解析"),
            crate::route_client::RouteClientOptions {
                rpc_timeout: Duration::from_millis(50),
                retry_attempts: 0,
                retry_backoff: Duration::from_millis(5),
                circuit_breaker_failure_threshold: 1,
                circuit_breaker_cooldown: Duration::from_millis(100),
            },
        )
        .expect("test route client 应可构建");
        AttemptReporter::spawn(route_client)
    }

    /// W12-C D-7: heartbeat 必须报告真实 in-flight 计数, 不再硬编码 0。
    /// mutation: 把 build_heartbeat_request 里 in_flight 改回 0 → 此测试断言红。
    #[tokio::test]
    async fn heartbeat_carries_real_in_flight_count() {
        let config = fixture_config();
        let resource_limits = Arc::new(ResourceLimits::new(&config));
        resource_limits.set_in_flight_for_test(7);

        let metrics = HeartbeatMetricsSource {
            resource_limits,
            attempt_reporter: fixture_attempt_reporter(),
            started_at_unix_ms: 1234567890,
        };

        let request = build_heartbeat_request(&metrics);

        assert_eq!(
            request.in_flight_requests, 7,
            "heartbeat 必须报告真实 in-flight 数 (W12-C D-7)"
        );
    }

    /// W12-C D-7: heartbeat 必须报告真实 attempt_report_queue_depth, 不再硬编码 0。
    /// 用 set_queue_depth_for_test 注入非零值 5, 让此测试对 "改回 0" 的 mutation 也红。
    /// mutation: 把 build_heartbeat_request 里 queue_depth 改回硬编码 0 → 此断言红。
    #[tokio::test]
    async fn heartbeat_carries_real_attempt_report_queue_depth_when_nonzero() {
        let config = fixture_config();
        let resource_limits = Arc::new(ResourceLimits::new(&config));

        let reporter = fixture_attempt_reporter();
        // 注入非零 queue_depth 让断言不与"硬编码 0"巧合相等 (判别 codex P2 修)
        reporter.set_queue_depth_for_test(5);

        let metrics = HeartbeatMetricsSource {
            resource_limits,
            attempt_reporter: reporter,
            started_at_unix_ms: 0,
        };

        let request = build_heartbeat_request(&metrics);

        assert_eq!(
            request.attempt_report_queue_depth, 5,
            "queue_depth 必须从 AttemptReporter 拉真值 (注入 5), 实际: {}",
            request.attempt_report_queue_depth
        );
    }

    /// W12-C D-7: started_at 必须来自 process boot 时刻 (一次性 i64), 不再硬编码 0。
    /// mutation: 把 build_heartbeat_request 里 started_at 改回 0 → 此测试断言红。
    #[tokio::test]
    async fn heartbeat_carries_started_at_unix_ms() {
        let config = fixture_config();
        let resource_limits = Arc::new(ResourceLimits::new(&config));
        let metrics = HeartbeatMetricsSource {
            resource_limits,
            attempt_reporter: fixture_attempt_reporter(),
            started_at_unix_ms: 1234567890,
        };

        let request = build_heartbeat_request(&metrics);

        assert_eq!(
            request.started_at, 1234567890,
            "started_at 必须用 HeartbeatMetricsSource 注入的真值"
        );
    }
}
