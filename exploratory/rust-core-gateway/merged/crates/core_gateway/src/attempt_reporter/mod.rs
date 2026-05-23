// M-rust-8 attempt reporter
// 职责: 非阻塞收集 attempt 终态, 异步上报 mock Go control plane, 失败时在内存队列内重试。

use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use tokio::{sync::mpsc, time};
use tracing::{debug, warn};

use crate::{error::GatewayError, redaction::redact_untrusted_text, route_client::RouteClient};

mod idempotency;
mod metrics;
mod spool;
mod types;

pub use metrics::{AttemptCacheMetrics, AttemptTokenMetrics};
pub use spool::{
    AttemptSpool, AttemptSpoolBackpressure, AttemptSpoolOptions, AttemptSpoolReservation,
    SpoolError, SpoolPersistOutcome,
};
pub use types::{
    AttemptReport, AttemptReportContext, AttemptReportStats, AttemptStatus, ReportEnqueueResult,
    TerminalReportResult,
};

pub const DEFAULT_ATTEMPT_REPORT_QUEUE_CAPACITY: usize = 1024;
const DEFAULT_RETRY_ATTEMPTS: usize = 3;
const DEFAULT_RETRY_BACKOFF: Duration = Duration::from_millis(10);
const REDACTED_ERROR_LIMIT: usize = 256;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AttemptReporterOptions {
    pub queue_capacity: usize,
    pub retry_attempts: usize,
    pub retry_backoff: Duration,
    /// W12-A D-4 Slice 2: durable spool 配置。
    /// `spool.enabled=false` (默认) -> baseline 旧 in-memory drop 路径,
    /// 已有测试与生产部署不破坏; Slice 3 加 production validate fail-fast。
    pub spool: AttemptSpoolOptions,
}

impl Default for AttemptReporterOptions {
    fn default() -> Self {
        Self {
            queue_capacity: DEFAULT_ATTEMPT_REPORT_QUEUE_CAPACITY,
            retry_attempts: DEFAULT_RETRY_ATTEMPTS,
            retry_backoff: DEFAULT_RETRY_BACKOFF,
            spool: AttemptSpoolOptions::default(),
        }
    }
}

#[derive(Clone)]
pub struct AttemptReporter {
    inner: Arc<AttemptReporterInner>,
}

/// W12-A D-4 Slice 2 (Codex P1 fix 2026-05-24): worker 必须知道 report 是否真落 spool,
/// 才能在 CP 失败 + spool=Some + persist 失败 (in-memory only) 时正确递增 failed_reports
/// (否则 spool 路径下报告 neither durable nor counted = 真静默丢账)。
#[derive(Debug)]
struct WorkerJob {
    report: AttemptReport,
    /// true = spool.persist 成功, 失败保留 pending 由 replay 兜底; false = in-memory only。
    persisted_to_spool: bool,
}

struct AttemptReporterInner {
    sender: mpsc::Sender<WorkerJob>,
    queue_depth: AtomicUsize,
    enqueued_reports: AtomicU64,
    acked_reports: AtomicU64,
    retry_reports: AtomicU64,
    failed_reports: AtomicU64,
    dropped_full_reports: AtomicU64,
    dropped_closed_reports: AtomicU64,
    /// W12-A D-4 Slice 2: 可选 durable spool。None=baseline, Some=durable-first + replay。
    spool: Option<AttemptSpool>,
    /// W12-A D-4 Slice 2 计数器: spool persist 成功 + queue notification 失败 (replay 兜底)。
    spooled_reports: AtomicU64,
    /// replay worker 成功 ack 计数 (区别 live worker acked_reports)。
    replayed_reports: AtomicU64,
    /// worker send_with_retry 耗尽后 spool 文件保留: 实际上未丢, 但延迟到下个 replay tick。
    spool_delivery_failed: AtomicU64,
    /// spool persist 物理失败 (磁盘 / 编码) 计数。
    spool_write_failed_reports: AtomicU64,
    /// reserve() 返 Backpressure 计数 (Slice 3 forward_planned 用此值 -> 503 metric)。
    spool_backpressure_reports: AtomicU64,
    /// replay worker 读 pending 时解码失败 (corrupt) 计数。
    spool_corrupt_reports: AtomicU64,
    /// W12-A D-4 Slice 3 AC-4-post: post-commit (响应头已送出) 后 terminal report 不可投递的次数。
    /// HTTP 状态不可改 (response 已 commit), 这是账务真损失的 loud counter。
    spool_drop_billable_reports: AtomicU64,
}

#[derive(Clone)]
pub struct AttemptTerminalReporter {
    reporter: AttemptReporter,
    context: AttemptReportContext,
    reported: Arc<AtomicBool>,
}

impl AttemptTerminalReporter {
    pub fn new(reporter: AttemptReporter, context: AttemptReportContext) -> Self {
        Self {
            reporter,
            context,
            reported: Arc::new(AtomicBool::new(false)),
        }
    }

    pub fn report(
        &self,
        status: AttemptStatus,
        http_status: Option<u16>,
        stats: AttemptReportStats,
        error_class: Option<&str>,
        error_message_redacted: Option<&str>,
    ) -> TerminalReportResult {
        if self
            .reported
            .compare_exchange(false, true, Ordering::AcqRel, Ordering::Acquire)
            .is_err()
        {
            return TerminalReportResult::AlreadyReported;
        }

        let report = self.context.terminal_report(
            status,
            http_status,
            stats,
            error_class,
            error_message_redacted,
        );
        TerminalReportResult::Submitted(self.reporter.report(report))
    }

    pub fn report_already_submitted(&self) -> bool {
        self.reported.load(Ordering::Acquire)
    }

    pub fn context(&self) -> &AttemptReportContext {
        &self.context
    }

    /// W12-A D-4 Slice 3 AC-4-post: 响应头已送出后的 terminal report。
    /// HTTP 状态码不可改 (response 已 commit) — 仅 metric + loud structured log。
    /// 用于: streaming body 完成时 relay 的 terminal report + PinnedDrop 的 ClientCancel report。
    ///
    /// Degraded 结果触发 `spool_drop_billable++` + tracing::error! 含 request_id + idempotency_key
    /// + route_plan_id + tokens evidence, 让审计 / 计费侧能定位丢失账。
    pub fn report_post_commit(
        &self,
        status: AttemptStatus,
        http_status: Option<u16>,
        stats: AttemptReportStats,
        error_class: Option<&str>,
        error_message_redacted: Option<&str>,
    ) -> TerminalReportResult {
        // Codex P2-2 fix 2026-05-24: 在 stats 被 move 给 self.report 前抓住 tokens/bytes,
        // 让 post-commit loud log 含计费侧对账证据 (仅 ID 不够定位丢账具体金额)。
        let tokens_total = stats
            .tokens_used
            .as_ref()
            .map(|t| t.total_tokens)
            .unwrap_or(0);
        let bytes_in_stat = stats.bytes_in;
        let bytes_out = stats.bytes_out;

        let result = self.report(status, http_status, stats, error_class, error_message_redacted);
        if let TerminalReportResult::Submitted(enqueue) = result
            && enqueue.is_degraded()
        {
            self.reporter.increment_spool_drop_billable();
            tracing::error!(
                request_id = %self.context.request_id,
                idempotency_key = %self.context.idempotency_key,
                route_plan_id = %self.context.route_plan_id,
                attempt_id = %self.context.attempt_id,
                tokens_total,
                bytes_in = bytes_in_stat,
                bytes_out,
                result = ?enqueue,
                "W12-A D-4 AC-4-post: BILLABLE attempt report 不可恢复丢失 (HTTP 响应已提交不可改)"
            );
        }
        result
    }

    /// W12-A D-4 Slice 3: 响应头未送出 (synthetic_mock / planning_error / bad_vendor / payload_too_large 路径),
    /// 失败 warn 即可 — caller 已返 4xx/5xx HTTP, 不构成 silent loss。
    pub fn report_pre_commit(
        &self,
        status: AttemptStatus,
        http_status: Option<u16>,
        stats: AttemptReportStats,
        error_class: Option<&str>,
        error_message_redacted: Option<&str>,
    ) -> TerminalReportResult {
        let result = self.report(status, http_status, stats, error_class, error_message_redacted);
        if let TerminalReportResult::Submitted(enqueue) = result
            && enqueue.is_degraded()
        {
            tracing::warn!(
                request_id = %self.context.request_id,
                idempotency_key = %self.context.idempotency_key,
                result = ?enqueue,
                "W12-A D-4 pre-commit attempt report degraded (caller 已返 4xx/5xx, 非 silent loss)"
            );
        }
        result
    }
}

impl AttemptReporter {
    pub fn spawn(route_client: RouteClient) -> Self {
        Self::spawn_with_options(route_client, AttemptReporterOptions::default())
    }

    pub fn spawn_with_options(route_client: RouteClient, options: AttemptReporterOptions) -> Self {
        let capacity = options.queue_capacity.max(1);
        let (sender, receiver) = mpsc::channel(capacity);

        // W12-A D-4 Slice 2: 启动期 open spool (enabled=false -> None, baseline 旧路径)。
        // open 失败 (例如 dir 不可写) 当前打 warn 仍降级到 baseline; Slice 3 production validate fail-fast。
        let spool = match AttemptSpool::open(options.spool.clone()) {
            Ok(spool) => spool,
            Err(err) => {
                warn!(
                    error = %err,
                    "W12-A D-4: spool open 失败, 降级到 baseline 内存 drop 路径 (Slice 3 production validate 应 fail-fast)"
                );
                None
            }
        };

        let inner = Arc::new(AttemptReporterInner {
            sender,
            queue_depth: AtomicUsize::new(0),
            enqueued_reports: AtomicU64::new(0),
            acked_reports: AtomicU64::new(0),
            retry_reports: AtomicU64::new(0),
            failed_reports: AtomicU64::new(0),
            dropped_full_reports: AtomicU64::new(0),
            dropped_closed_reports: AtomicU64::new(0),
            spool: spool.clone(),
            spooled_reports: AtomicU64::new(0),
            replayed_reports: AtomicU64::new(0),
            spool_delivery_failed: AtomicU64::new(0),
            spool_write_failed_reports: AtomicU64::new(0),
            spool_backpressure_reports: AtomicU64::new(0),
            spool_corrupt_reports: AtomicU64::new(0),
            spool_drop_billable_reports: AtomicU64::new(0),
        });

        tokio::spawn(worker_loop(
            route_client.clone(),
            receiver,
            inner.clone(),
            options.clone(),
        ));

        // W12-A D-4 Slice 2: spool enabled -> spawn 周期性 replay worker。启动期一次性扫
        // pending/ + 之后按 replay_interval 间隔 drain。这是 AC-2 崩溃恢复的真正路径。
        if let Some(spool_for_replay) = spool {
            tokio::spawn(replay_worker_loop(
                route_client,
                inner.clone(),
                spool_for_replay,
                options,
            ));
        }

        Self { inner }
    }

    /// W12-A D-4 Slice 2: durable-first report 入口。
    ///
    /// spool enabled 路径:
    /// 1. reserve() 字节预算; Err -> SpoolBackpressure (Slice 3 forward_planned 转 503)
    /// 2. persist() 落盘; Err -> SpoolWriteFailed + 仍走 try_send 兜底通知 live worker
    /// 3. try_send 通知 live worker; Full 不再算丢失 -> Spooled (replay 兜底)
    ///
    /// spool disabled 路径 (baseline): 与 Slice 1 之前完全一致, 不破坏现有调用方。
    pub fn report(&self, report: AttemptReport) -> ReportEnqueueResult {
        if let Some(spool) = &self.inner.spool {
            return self.report_with_spool(spool, report);
        }
        self.report_baseline(report)
    }

    /// Baseline 路径 (spool disabled): try_send 老语义, Full -> DroppedFull, Closed -> DroppedClosed。
    fn report_baseline(&self, report: AttemptReport) -> ReportEnqueueResult {
        let job = WorkerJob {
            report,
            persisted_to_spool: false,
        };
        match self.inner.sender.try_send(job) {
            Ok(()) => {
                self.inner.queue_depth.fetch_add(1, Ordering::Relaxed);
                self.inner.enqueued_reports.fetch_add(1, Ordering::Relaxed);
                ReportEnqueueResult::Enqueued
            }
            Err(mpsc::error::TrySendError::Full(_)) => {
                self.inner
                    .dropped_full_reports
                    .fetch_add(1, Ordering::Relaxed);
                ReportEnqueueResult::DroppedFull
            }
            Err(mpsc::error::TrySendError::Closed(_)) => {
                self.inner
                    .dropped_closed_reports
                    .fetch_add(1, Ordering::Relaxed);
                ReportEnqueueResult::DroppedClosed
            }
        }
    }

    /// Durable-first 路径 (spool enabled): 先 persist 再 notify worker。
    ///
    /// Codex P2-1 deferred Slice 3: spool.persist 当前 sync IO 在 Tokio worker 跑,
    /// production multi_thread runtime 暂可承受; Slice 3 将用 tokio::task::block_in_place
    /// 或专门 spool-writer 线程 decouple。本 slice fixture fsync_on_write=false 测试已规避抖动。
    fn report_with_spool(
        &self,
        spool: &AttemptSpool,
        report: AttemptReport,
    ) -> ReportEnqueueResult {
        let reservation = match spool.reserve() {
            Ok(r) => r,
            Err(_) => {
                self.inner
                    .spool_backpressure_reports
                    .fetch_add(1, Ordering::Relaxed);
                return ReportEnqueueResult::SpoolBackpressure;
            }
        };

        // persist 即使失败也尝试 try_send (in-memory worker 仍可投递, 不要直接丢)。
        // persisted_to_spool 透传给 worker_loop, 让其在 CP 失败时正确分别 spool-covered vs in-memory-only。
        let persisted_to_spool = match spool.persist(&report, reservation) {
            Ok(_) => true,
            Err(err) => {
                warn!(
                    error = %err,
                    idempotency_key = %report.idempotency_key,
                    "W12-A D-4: spool persist 失败, 回落 try_send 兜底 (Codex P1 fix: 透传 persisted=false 给 worker)"
                );
                self.inner
                    .spool_write_failed_reports
                    .fetch_add(1, Ordering::Relaxed);
                false
            }
        };

        let job = WorkerJob {
            report,
            persisted_to_spool,
        };

        // 通知 live worker; Full 不算丢失 (persist OK 时 replay 兜底; in-memory-only 时 worker fail-> failed_reports++)。
        match self.inner.sender.try_send(job) {
            Ok(()) => {
                self.inner.queue_depth.fetch_add(1, Ordering::Relaxed);
                self.inner.enqueued_reports.fetch_add(1, Ordering::Relaxed);
                if !persisted_to_spool {
                    ReportEnqueueResult::SpoolWriteFailed
                } else {
                    ReportEnqueueResult::Enqueued
                }
            }
            Err(mpsc::error::TrySendError::Full(_)) | Err(mpsc::error::TrySendError::Closed(_)) => {
                if persisted_to_spool {
                    self.inner
                        .spooled_reports
                        .fetch_add(1, Ordering::Relaxed);
                    ReportEnqueueResult::Spooled
                } else {
                    // in-memory 通道也满: 既无 spool 兜底也无 worker 处理 -> 真丢, ++failed_reports。
                    self.inner.failed_reports.fetch_add(1, Ordering::Relaxed);
                    ReportEnqueueResult::SpoolWriteFailed
                }
            }
        }
    }

    pub fn terminal_reporter(&self, context: AttemptReportContext) -> AttemptTerminalReporter {
        AttemptTerminalReporter::new(self.clone(), context)
    }

    pub fn queue_depth(&self) -> usize {
        self.inner.queue_depth.load(Ordering::Relaxed)
    }

    /// W12-C D-7 测试辅助: 直接 override gauge 让 heartbeat 测试可断言非零 queue_depth。
    /// 不实际入队 (worker recv 会无视该值, 因为 fixture channel 仍空)。仅 test 可见。
    /// mutation: 把 heartbeat 里 queue_depth 永远塞常量 → 此测试断言 ==5 红。
    #[cfg(test)]
    pub(crate) fn set_queue_depth_for_test(&self, n: usize) {
        self.inner.queue_depth.store(n, Ordering::Relaxed);
    }

    pub fn enqueued_count(&self) -> u64 {
        self.inner.enqueued_reports.load(Ordering::Relaxed)
    }

    pub fn acked_count(&self) -> u64 {
        self.inner.acked_reports.load(Ordering::Relaxed)
    }

    pub fn retry_count(&self) -> u64 {
        self.inner.retry_reports.load(Ordering::Relaxed)
    }

    pub fn failed_count(&self) -> u64 {
        self.inner.failed_reports.load(Ordering::Relaxed)
    }

    pub fn dropped_full_count(&self) -> u64 {
        self.inner.dropped_full_reports.load(Ordering::Relaxed)
    }

    pub fn dropped_closed_count(&self) -> u64 {
        self.inner.dropped_closed_reports.load(Ordering::Relaxed)
    }

    // W12-A D-4 Slice 2: spool 相关计数访问 (heartbeat / Slice 3 metrics / 测试断言)。

    pub fn spooled_count(&self) -> u64 {
        self.inner.spooled_reports.load(Ordering::Relaxed)
    }

    pub fn replayed_count(&self) -> u64 {
        self.inner.replayed_reports.load(Ordering::Relaxed)
    }

    pub fn spool_delivery_failed_count(&self) -> u64 {
        self.inner.spool_delivery_failed.load(Ordering::Relaxed)
    }

    pub fn spool_write_failed_count(&self) -> u64 {
        self.inner.spool_write_failed_reports.load(Ordering::Relaxed)
    }

    pub fn spool_backpressure_count(&self) -> u64 {
        self.inner.spool_backpressure_reports.load(Ordering::Relaxed)
    }

    pub fn spool_corrupt_count(&self) -> u64 {
        self.inner.spool_corrupt_reports.load(Ordering::Relaxed)
    }

    /// W12-A D-4 Slice 3 AC-4-post 必检: 响应头已送出后报告 不可投递 = 真账务损失计数。
    pub fn spool_drop_billable_count(&self) -> u64 {
        self.inner.spool_drop_billable_reports.load(Ordering::Relaxed)
    }

    pub fn spool_pending_count(&self) -> usize {
        self.inner
            .spool
            .as_ref()
            .map(|s| s.pending_count())
            .unwrap_or(0)
    }

    pub fn spool_pending_bytes(&self) -> u64 {
        self.inner
            .spool
            .as_ref()
            .map(|s| s.pending_bytes())
            .unwrap_or(0)
    }

    /// W12-A D-4 Slice 3 AC-4-pre: forward_planned 转发前调, 检查 spool 是否仍可接受新报告。
    /// spool=None (baseline): 永远 Ok (维持旧行为, 不引 503)。
    /// spool=Some: 尝试 reserve, 立即 Drop 释放预算 = 纯 capacity probe;
    /// Err 时 ++spool_backpressure_reports 计数 (AC-4-pre 测试断言用)。
    pub fn would_accept(&self) -> Result<(), AttemptSpoolBackpressure> {
        match &self.inner.spool {
            Some(spool) => match spool.reserve() {
                Ok(_reservation) => Ok(()), // Drop 自动还预算
                Err(err) => {
                    self.inner
                        .spool_backpressure_reports
                        .fetch_add(1, Ordering::Relaxed);
                    Err(err)
                }
            },
            None => Ok(()),
        }
    }

    /// W12-A D-4 Slice 3: 内部 helper 给 AttemptTerminalReporter::report_post_commit 调,
    /// 累 spool_drop_billable counter (post-commit 不可恢复账务损失计数)。
    pub(crate) fn increment_spool_drop_billable(&self) {
        self.inner
            .spool_drop_billable_reports
            .fetch_add(1, Ordering::Relaxed);
    }
}

async fn worker_loop(
    route_client: RouteClient,
    mut receiver: mpsc::Receiver<WorkerJob>,
    inner: Arc<AttemptReporterInner>,
    options: AttemptReporterOptions,
) {
    while let Some(job) = receiver.recv().await {
        inner.queue_depth.fetch_sub(1, Ordering::Relaxed);
        let WorkerJob {
            report,
            persisted_to_spool,
        } = job;
        let key = report.idempotency_key.clone();
        let outcome = send_with_retry(&route_client, &inner, &options, report).await;

        // W12-A D-4 Slice 2 (Codex P1 fix): 必须根据 persisted_to_spool 区分账务责任:
        // - persisted_to_spool=true + 失败 -> spool 兜底, 不 ++failed_reports
        // - persisted_to_spool=false + 失败 -> in-memory only, 真丢, 必须 ++failed_reports
        match (outcome, persisted_to_spool) {
            (SendOutcome::Acked, true) => {
                if let Some(spool) = &inner.spool
                    && let Err(err) = spool.ack(&key)
                {
                    warn!(
                        error = %err,
                        idempotency_key = %key,
                        "W12-A D-4: live worker ack 后 spool 删 pending 失败, replay 会重发 (控制面去重)"
                    );
                }
                // Codex P2-2 fix: notify_health_recovered 不在此清 latch (CP ack ≠ 磁盘健康),
                // 改由 spool.persist 成功路径清 + replay drain_pending health probe 清。
            }
            (SendOutcome::Acked, false) => {
                // baseline (spool=None) 或 spool persist 失败的 in-memory 路径成功 -> 已 ack, 无后续。
            }
            (SendOutcome::DeliveryFailed, true) => {
                inner
                    .spool_delivery_failed
                    .fetch_add(1, Ordering::Relaxed);
                debug!(
                    idempotency_key = %key,
                    "W12-A D-4: live worker 投递失败, spool 保留 pending 等 replay (Codex P1: 已 persisted, 不 ++failed)"
                );
            }
            (SendOutcome::DeliveryFailed, false) => {
                // Codex round 1 P1 + round 2 P2 fix: 区分 baseline vs spool-in-memory-fallback
                // - baseline (spool=None): send_with_retry 已 ++failed_reports, 本分支不再 ++ (round 2 P2 double-count fix)
                // - spool=Some + persisted=false (in-memory-only fallback): send_with_retry 没 ++ (因 spool.is_some()),
                //   本分支必须 ++failed_reports (round 1 P1: 真丢账)
                if inner.spool.is_some() {
                    inner.failed_reports.fetch_add(1, Ordering::Relaxed);
                    warn!(
                        idempotency_key = %key,
                        "W12-A D-4: in-memory-only fallback worker 失败, failed_reports++ (账务真损失)"
                    );
                }
            }
        }
    }
}

/// W12-A D-4 Slice 2: replay worker — 启动期 + 周期性扫 pending/, 重发未 ack 报告。
///
/// 这是 AC-2 崩溃恢复的真正路径: 进程重启后 spool 启动 open 已把 pending/ counters 初始化,
/// replay_worker_loop 立即扫一次后按 replay_interval 持续 drain。
async fn replay_worker_loop(
    route_client: RouteClient,
    inner: Arc<AttemptReporterInner>,
    spool: AttemptSpool,
    options: AttemptReporterOptions,
) {
    // 启动期 immediate drain (AC-2 prerequisite)。
    drain_pending(&route_client, &inner, &spool, &options).await;

    let mut tick = time::interval(options.spool.replay_interval);
    // 首次 tick 立即触发, skip 它防止启动后两次连续 drain。
    tick.tick().await;
    loop {
        tick.tick().await;
        drain_pending(&route_client, &inner, &spool, &options).await;
    }
}

async fn drain_pending(
    route_client: &RouteClient,
    inner: &Arc<AttemptReporterInner>,
    spool: &AttemptSpool,
    options: &AttemptReporterOptions,
) {
    // Codex round 1 P2-2 + round 2 P1 fix: probe 仅用于尝试清 last_write_failed latch,
    // 不阻 replay。pending_snapshot 是 read-dir, route_client.report_attempt 是 RPC,
    // spool.ack 是 file unlink — 三者都不需 write 能力, 即使 disk full 仍能让 ack 释放空间。
    // 跳过 drain 等于卡死 backpressure 直到外部清理 / 重启 (round 2 P1 catch)。
    if spool.last_write_failed() {
        let _ = spool.probe_write_health(); // 成功清 latch, 失败继续 drain (ack delete 仍可能释放空间)
    }

    let batch = spool.pending_snapshot(options.spool.replay_batch_size.max(1));
    if batch.is_empty() {
        return;
    }

    for key in batch {
        let proto = match spool.load_pending(&key) {
            Ok(p) => p,
            Err(SpoolError::KeyNotFound(_)) => continue, // 已被 live worker ack
            Err(SpoolError::Decode(err)) => {
                inner.spool_corrupt_reports.fetch_add(1, Ordering::Relaxed);
                warn!(
                    error = %err,
                    idempotency_key = %key,
                    "W12-A D-4: replay 遇 corrupt spool 文件, 跳过 (Slice 后续可移到 quarantine 目录)"
                );
                continue;
            }
            Err(err) => {
                warn!(
                    error = %err,
                    idempotency_key = %key,
                    "W12-A D-4: replay 读 pending 失败"
                );
                continue;
            }
        };

        match route_client.report_attempt(proto).await {
            Ok(ack) if ack.ack => {
                if let Err(err) = spool.ack(&key) {
                    warn!(
                        error = %err,
                        idempotency_key = %key,
                        "W12-A D-4: replay ack 后 spool 删 pending 失败 (控制面去重承接)"
                    );
                }
                inner.replayed_reports.fetch_add(1, Ordering::Relaxed);
                // Codex P2-2 fix: 不在此清 latch (CP ack ≠ 磁盘健康),
                // probe_write_health 在 drain 开头已做或 spool.persist 成功路径清。
                debug!(
                    ack_id = %ack.ack_id,
                    idempotency_key = %key,
                    "W12-A D-4: replay worker acked"
                );
            }
            Ok(ack) => {
                inner
                    .spool_delivery_failed
                    .fetch_add(1, Ordering::Relaxed);
                let advisory = redact_untrusted_text(&ack.advisory, REDACTED_ERROR_LIMIT);
                debug!(
                    idempotency_key = %key,
                    advisory = %advisory,
                    "W12-A D-4: replay 投递未 ack, 保留 pending 等下次 tick"
                );
            }
            Err(err) => {
                inner
                    .spool_delivery_failed
                    .fetch_add(1, Ordering::Relaxed);
                debug!(
                    error = %err,
                    idempotency_key = %key,
                    "W12-A D-4: replay 投递错, 保留 pending 等下次 tick"
                );
            }
        }
    }
}

/// W12-A D-4 Slice 2: live worker 投递结果, 让 worker_loop 决定是否 ack spool。
#[derive(Debug, Clone, Copy, Eq, PartialEq)]
enum SendOutcome {
    Acked,
    DeliveryFailed,
}

async fn send_with_retry(
    route_client: &RouteClient,
    inner: &AttemptReporterInner,
    options: &AttemptReporterOptions,
    report: AttemptReport,
) -> SendOutcome {
    let mut attempt = 0usize;

    loop {
        let status = report.status.as_str();
        let idempotency_key = report.idempotency_key.as_str();
        match route_client
            .report_attempt(report.clone().into_proto())
            .await
        {
            Ok(ack) if ack.ack => {
                inner.acked_reports.fetch_add(1, Ordering::Relaxed);
                debug!(
                    ack_id = %ack.ack_id,
                    status,
                    idempotency_key,
                    "attempt report acked"
                );
                return SendOutcome::Acked;
            }
            Ok(ack) => {
                let advisory = redact_untrusted_text(&ack.advisory, REDACTED_ERROR_LIMIT);
                let message = format!("attempt report not acked: {advisory}");
                if attempt < options.retry_attempts {
                    retry_delay(inner, options.retry_backoff, attempt).await;
                    attempt = attempt.saturating_add(1);
                    continue;
                }

                // W12-A D-4 Slice 2: spool enabled 时 failed_reports 不再 ++, 因为 spool 兜底未丢;
                // baseline (spool=None) 时仍 ++ 标账务真损失。
                if inner.spool.is_none() {
                    inner.failed_reports.fetch_add(1, Ordering::Relaxed);
                }
                warn!(status, idempotency_key, message, "attempt report failed");
                return SendOutcome::DeliveryFailed;
            }
            Err(err) if is_transient_report_error(&err) && attempt < options.retry_attempts => {
                warn!(
                    error = %err,
                    status,
                    idempotency_key,
                    retry_attempt = attempt + 1,
                    "attempt report transient failure, retrying"
                );
                retry_delay(inner, options.retry_backoff, attempt).await;
                attempt = attempt.saturating_add(1);
            }
            Err(err) => {
                if inner.spool.is_none() {
                    inner.failed_reports.fetch_add(1, Ordering::Relaxed);
                }
                warn!(
                    error = %err,
                    status,
                    idempotency_key,
                    "attempt report failed"
                );
                return SendOutcome::DeliveryFailed;
            }
        }
    }
}

async fn retry_delay(inner: &AttemptReporterInner, base: Duration, attempt: usize) {
    inner.retry_reports.fetch_add(1, Ordering::Relaxed);
    let multiplier = attempt.saturating_add(1).min(8) as u32;
    time::sleep(base.saturating_mul(multiplier)).await;
}

fn is_transient_report_error(err: &GatewayError) -> bool {
    match err {
        GatewayError::Network(_) => true,
        GatewayError::ControlPlane(message) => {
            let message = message.to_ascii_lowercase();
            message.contains("unavailable")
                || message.contains("deadline")
                || message.contains("resourceexhausted")
                || message.contains("resource exhausted")
                || message.contains("unknown")
                || message.contains("circuit breaker open")
        }
        GatewayError::Config(_)
        | GatewayError::Upstream(_)
        | GatewayError::Stream(_)
        | GatewayError::Internal(_) => false,
    }
}

fn redact_error(message: &str) -> String {
    redact_untrusted_text(message, REDACTED_ERROR_LIMIT)
}

pub fn now_unix_ms_i64() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis()
        .min(i64::MAX as u128) as i64
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn redact_error_removes_secret_patterns() {
        let redacted = redact_error(
            "control advisory Authorization: Bearer lease-token-value sk-test-sensitive-value",
        );

        assert!(!redacted.contains("lease-token-value"));
        assert!(!redacted.contains("sk-test-sensitive-value"));
        assert!(redacted.contains("[REDACTED_SECRET]"));
        assert!(redacted.contains("control advisory"));
    }
}
