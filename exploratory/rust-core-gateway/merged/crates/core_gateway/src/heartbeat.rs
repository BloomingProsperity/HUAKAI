// M-rust-9 heartbeat — 定时发送心跳并处理 drain_mode 切换
// 每 5 秒向 control plane 发送 HeartbeatRequest, 读取 ack 中的 drain_mode bool。
// drain_mode=true 时拒绝新入连接(503), 已进行中的流继续完成。

use std::{
    sync::atomic::{AtomicBool, Ordering},
    time::Duration,
};

use tokio::{task::JoinHandle, time};
use tracing::{debug, info, warn};

use crate::{route_client::RouteClient, route_proto::v1::HeartbeatRequest};

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

// ─── HeartbeatWorker ─────────────────────────────────────────────────────────

/// 心跳 worker — 通过 tokio::spawn 运行, 返回 JoinHandle 供调用方 abort
pub struct HeartbeatWorker {
    task: JoinHandle<()>,
}

impl HeartbeatWorker {
    /// 启动心跳任务。route_client 为 Clone 且 Send+Sync, 直接共享。
    pub fn spawn(route_client: RouteClient) -> Self {
        let task = tokio::spawn(heartbeat_loop(route_client));
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

async fn heartbeat_loop(route_client: RouteClient) {
    let mut interval = time::interval(HEARTBEAT_INTERVAL);
    // 第一个 tick 立即触发 (tokio::interval 默认行为)
    loop {
        interval.tick().await;
        send_heartbeat_once(&route_client).await;
    }
}

async fn send_heartbeat_once(route_client: &RouteClient) {
    let request = HeartbeatRequest {
        node_id: "rust-core-gateway".to_owned(),
        build_sha: env!("CARGO_PKG_VERSION").to_owned(),
        schema_version: crate::route_client::ROUTE_SCHEMA_VERSION.to_owned(),
        started_at: 0,
        in_flight_requests: 0,
        open_upstream_connections: 0,
        attempt_report_queue_depth: 0,
        p95_control_plane_rpc_ms: 0.0,
        error_rate_1m: 0.0,
    };

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
}
