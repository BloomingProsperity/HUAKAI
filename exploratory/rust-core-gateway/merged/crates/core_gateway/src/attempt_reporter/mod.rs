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
mod types;

pub use metrics::{AttemptCacheMetrics, AttemptTokenMetrics};
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
}

impl Default for AttemptReporterOptions {
    fn default() -> Self {
        Self {
            queue_capacity: DEFAULT_ATTEMPT_REPORT_QUEUE_CAPACITY,
            retry_attempts: DEFAULT_RETRY_ATTEMPTS,
            retry_backoff: DEFAULT_RETRY_BACKOFF,
        }
    }
}

#[derive(Clone)]
pub struct AttemptReporter {
    inner: Arc<AttemptReporterInner>,
}

struct AttemptReporterInner {
    sender: mpsc::Sender<AttemptReport>,
    queue_depth: AtomicUsize,
    enqueued_reports: AtomicU64,
    acked_reports: AtomicU64,
    retry_reports: AtomicU64,
    failed_reports: AtomicU64,
    dropped_full_reports: AtomicU64,
    dropped_closed_reports: AtomicU64,
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
}

impl AttemptReporter {
    pub fn spawn(route_client: RouteClient) -> Self {
        Self::spawn_with_options(route_client, AttemptReporterOptions::default())
    }

    pub fn spawn_with_options(route_client: RouteClient, options: AttemptReporterOptions) -> Self {
        let capacity = options.queue_capacity.max(1);
        let (sender, receiver) = mpsc::channel(capacity);
        let inner = Arc::new(AttemptReporterInner {
            sender,
            queue_depth: AtomicUsize::new(0),
            enqueued_reports: AtomicU64::new(0),
            acked_reports: AtomicU64::new(0),
            retry_reports: AtomicU64::new(0),
            failed_reports: AtomicU64::new(0),
            dropped_full_reports: AtomicU64::new(0),
            dropped_closed_reports: AtomicU64::new(0),
        });

        tokio::spawn(worker_loop(route_client, receiver, inner.clone(), options));

        Self { inner }
    }

    pub fn report(&self, report: AttemptReport) -> ReportEnqueueResult {
        match self.inner.sender.try_send(report) {
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

    pub fn terminal_reporter(&self, context: AttemptReportContext) -> AttemptTerminalReporter {
        AttemptTerminalReporter::new(self.clone(), context)
    }

    pub fn queue_depth(&self) -> usize {
        self.inner.queue_depth.load(Ordering::Relaxed)
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
}

async fn worker_loop(
    route_client: RouteClient,
    mut receiver: mpsc::Receiver<AttemptReport>,
    inner: Arc<AttemptReporterInner>,
    options: AttemptReporterOptions,
) {
    while let Some(report) = receiver.recv().await {
        inner.queue_depth.fetch_sub(1, Ordering::Relaxed);
        send_with_retry(&route_client, &inner, &options, report).await;
    }
}

async fn send_with_retry(
    route_client: &RouteClient,
    inner: &AttemptReporterInner,
    options: &AttemptReporterOptions,
    report: AttemptReport,
) {
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
                return;
            }
            Ok(ack) => {
                let advisory = redact_untrusted_text(&ack.advisory, REDACTED_ERROR_LIMIT);
                let message = format!("attempt report not acked: {advisory}");
                if attempt < options.retry_attempts {
                    retry_delay(inner, options.retry_backoff, attempt).await;
                    attempt = attempt.saturating_add(1);
                    continue;
                }

                inner.failed_reports.fetch_add(1, Ordering::Relaxed);
                warn!(status, idempotency_key, message, "attempt report failed");
                return;
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
                inner.failed_reports.fetch_add(1, Ordering::Relaxed);
                warn!(
                    error = %err,
                    status,
                    idempotency_key,
                    "attempt report failed"
                );
                return;
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
