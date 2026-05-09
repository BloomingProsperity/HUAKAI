// M-rust-8 attempt reporter
// 职责: 非阻塞收集 attempt 终态, 异步上报 mock Go control plane, 失败时在内存队列内重试。

use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use bytes::Bytes;
use tokio::{sync::mpsc, time};
use tracing::{debug, warn};
use uuid::Uuid;

use crate::{
    account_planner::PlannedAttempt,
    error::GatewayError,
    request_id::RequestId,
    route_client::RouteClient,
    route_proto::v1::{AttemptReportRequest, CacheMetrics, TokensUsed},
    stream_pipeline::{CacheDelta, StreamEvent, UsageDelta},
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

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ReportEnqueueResult {
    Enqueued,
    DroppedFull,
    DroppedClosed,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TerminalReportResult {
    Submitted(ReportEnqueueResult),
    AlreadyReported,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AttemptStatus {
    Success,
    ClientCancel,
    Timeout,
    ProtocolError,
    Upstream4xx,
    Upstream5xx,
    NetworkError,
    ControlPlaneError,
    InternalError,
}

impl AttemptStatus {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Success => "success",
            Self::ClientCancel => "client_cancel",
            Self::Timeout => "timeout",
            Self::ProtocolError => "protocol_error",
            Self::Upstream4xx => "upstream_4xx",
            Self::Upstream5xx => "upstream_5xx",
            Self::NetworkError => "network_error",
            Self::ControlPlaneError => "control_plane_error",
            Self::InternalError => "internal_error",
        }
    }

    pub fn retryable(self) -> bool {
        matches!(
            self,
            Self::Timeout | Self::Upstream5xx | Self::NetworkError | Self::ControlPlaneError
        )
    }

    pub fn error_class(self) -> &'static str {
        match self {
            Self::Success => "",
            Self::ClientCancel => "client_cancel",
            Self::Timeout => "timeout",
            Self::ProtocolError => "protocol_error",
            Self::Upstream4xx | Self::Upstream5xx => "upstream_error",
            Self::NetworkError => "network_error",
            Self::ControlPlaneError => "control_plane_error",
            Self::InternalError => "internal_error",
        }
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct AttemptTokenMetrics {
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub total_tokens: u64,
    pub source: String,
}

impl AttemptTokenMetrics {
    fn missing() -> Self {
        Self {
            source: "missing".to_owned(),
            ..Self::default()
        }
    }

    fn add_usage_delta(&mut self, delta: &UsageDelta) {
        self.input_tokens = self.input_tokens.saturating_add(delta.input_tokens);
        self.output_tokens = self.output_tokens.saturating_add(delta.output_tokens);
        self.total_tokens = self.total_tokens.saturating_add(delta.total_tokens);
        if self.source.is_empty() || self.source == "missing" {
            self.source = "stream_pipeline".to_owned();
        }
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct AttemptCacheMetrics {
    pub cache_read_tokens: u64,
    pub cache_write_tokens: u64,
    pub cache_hit: bool,
    pub source: String,
}

impl AttemptCacheMetrics {
    fn missing() -> Self {
        Self {
            source: "missing".to_owned(),
            ..Self::default()
        }
    }

    fn add_cache_delta(&mut self, delta: &CacheDelta) {
        self.cache_read_tokens = self
            .cache_read_tokens
            .saturating_add(delta.cache_read_input_tokens);
        self.cache_write_tokens = self
            .cache_write_tokens
            .saturating_add(delta.cache_creation_input_tokens);
        self.cache_hit |= delta.cache_read_input_tokens > 0;
        if self.source.is_empty() || self.source == "missing" {
            self.source = "stream_pipeline".to_owned();
        }
    }
}

#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct AttemptReportStats {
    pub bytes_in: u64,
    pub bytes_out: u64,
    pub frames_in: u64,
    pub frames_out: u64,
    pub tokens_used: Option<AttemptTokenMetrics>,
    pub cache_metrics: Option<AttemptCacheMetrics>,
    pub vendor_request_id: Option<String>,
}

impl AttemptReportStats {
    pub fn record_body_chunk(&mut self, len: usize) {
        self.frames_in = self.frames_in.saturating_add(1);
        self.frames_out = self.frames_out.saturating_add(1);
        self.bytes_out = self.bytes_out.saturating_add(len as u64);
    }

    pub fn record_stream_event(&mut self, event: &StreamEvent) {
        match event {
            StreamEvent::Data(_) => {}
            StreamEvent::Usage(delta) => self
                .tokens_used
                .get_or_insert_with(AttemptTokenMetrics::missing)
                .add_usage_delta(delta),
            StreamEvent::CacheMetric(delta) => self
                .cache_metrics
                .get_or_insert_with(AttemptCacheMetrics::missing)
                .add_cache_delta(delta),
            StreamEvent::Done | StreamEvent::ProtocolError(_) | StreamEvent::UpstreamError(_) => {}
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AttemptReportContext {
    pub request_id: String,
    pub route_plan_id: String,
    pub attempt_id: String,
    pub acquisition_token: Bytes,
    pub idempotency_key: String,
    pub started_at_ms: i64,
    pub bytes_in: u64,
}

impl AttemptReportContext {
    pub fn from_planned(
        request_id: &RequestId,
        planned: &PlannedAttempt,
        bytes_in: u64,
        started_at_ms: i64,
    ) -> Self {
        let attempt_id = planned.attempt.attempt_id().to_owned();
        let idempotency_key =
            build_idempotency_key(request_id.as_str(), &attempt_id, &planned.acquisition_token);

        Self {
            request_id: request_id.as_str().to_owned(),
            route_plan_id: planned.route_plan.route_plan_id.clone(),
            attempt_id,
            acquisition_token: planned.acquisition_token.clone(),
            idempotency_key,
            started_at_ms,
            bytes_in,
        }
    }

    pub fn synthetic_control_plane_error(request_id: &RequestId) -> Self {
        let attempt_id = format!("attempt-{}", Uuid::now_v7());
        let acquisition_token = Bytes::new();
        let idempotency_key =
            build_idempotency_key(request_id.as_str(), &attempt_id, &acquisition_token);

        Self {
            request_id: request_id.as_str().to_owned(),
            route_plan_id: String::new(),
            attempt_id,
            acquisition_token,
            idempotency_key,
            started_at_ms: now_unix_ms_i64(),
            bytes_in: 0,
        }
    }

    pub fn terminal_report(
        &self,
        status: AttemptStatus,
        http_status: Option<u16>,
        stats: AttemptReportStats,
        error_class: Option<&str>,
        error_message_redacted: Option<&str>,
    ) -> AttemptReport {
        let ended_at_ms = now_unix_ms_i64();
        let latency_ms = ended_at_ms
            .saturating_sub(self.started_at_ms)
            .max(0)
            .try_into()
            .unwrap_or(u64::MAX);

        AttemptReport {
            request_id: self.request_id.clone(),
            route_plan_id: self.route_plan_id.clone(),
            attempt_id: self.attempt_id.clone(),
            acquisition_token: self.acquisition_token.clone(),
            status,
            http_status: http_status.map(i32::from).unwrap_or_default(),
            started_at: self.started_at_ms,
            ended_at: ended_at_ms,
            latency_ms,
            tokens_used: stats
                .tokens_used
                .unwrap_or_else(AttemptTokenMetrics::missing),
            cache_metrics: stats
                .cache_metrics
                .unwrap_or_else(AttemptCacheMetrics::missing),
            bytes_in: self.bytes_in.saturating_add(stats.bytes_in),
            bytes_out: stats.bytes_out,
            frames_in: stats.frames_in,
            frames_out: stats.frames_out,
            vendor_request_id: stats.vendor_request_id.unwrap_or_default(),
            retryable: status.retryable(),
            error_class: error_class
                .unwrap_or_else(|| status.error_class())
                .to_owned(),
            error_message_redacted: redact_error(error_message_redacted.unwrap_or_default()),
            idempotency_key: self.idempotency_key.clone(),
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AttemptReport {
    pub request_id: String,
    pub route_plan_id: String,
    pub attempt_id: String,
    pub acquisition_token: Bytes,
    pub status: AttemptStatus,
    pub http_status: i32,
    pub started_at: i64,
    pub ended_at: i64,
    pub latency_ms: u64,
    pub tokens_used: AttemptTokenMetrics,
    pub cache_metrics: AttemptCacheMetrics,
    pub bytes_in: u64,
    pub bytes_out: u64,
    pub frames_in: u64,
    pub frames_out: u64,
    pub vendor_request_id: String,
    pub retryable: bool,
    pub error_class: String,
    pub error_message_redacted: String,
    pub idempotency_key: String,
}

impl AttemptReport {
    pub fn into_proto(self) -> AttemptReportRequest {
        AttemptReportRequest {
            request_id: self.request_id,
            route_plan_id: self.route_plan_id,
            attempt_id: self.attempt_id,
            acquisition_token: self.acquisition_token,
            status: self.status.as_str().to_owned(),
            http_status: self.http_status,
            started_at: self.started_at,
            ended_at: self.ended_at,
            latency_ms: self.latency_ms,
            tokens_used: Some(TokensUsed {
                input_tokens: self.tokens_used.input_tokens,
                output_tokens: self.tokens_used.output_tokens,
                total_tokens: self.tokens_used.total_tokens,
                source: self.tokens_used.source,
            }),
            cache_metrics: Some(CacheMetrics {
                cache_read_tokens: self.cache_metrics.cache_read_tokens,
                cache_write_tokens: self.cache_metrics.cache_write_tokens,
                cache_hit: self.cache_metrics.cache_hit,
                source: self.cache_metrics.source,
            }),
            bytes_in: self.bytes_in,
            bytes_out: self.bytes_out,
            frames_in: self.frames_in,
            frames_out: self.frames_out,
            vendor_request_id: self.vendor_request_id,
            retryable: self.retryable,
            error_class: self.error_class,
            error_message_redacted: self.error_message_redacted,
            idempotency_key: self.idempotency_key,
        }
    }
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
                let message = format!("attempt report not acked: {}", ack.advisory);
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

fn build_idempotency_key(request_id: &str, attempt_id: &str, acquisition_token: &Bytes) -> String {
    let attempt_uuid = attempt_id
        .strip_prefix("attempt-")
        .unwrap_or(attempt_id)
        .to_owned();
    let fingerprint = stable_fingerprint(request_id, attempt_id, acquisition_token);
    format!("idem-v7-{attempt_uuid}-{fingerprint:016x}")
}

fn stable_fingerprint(request_id: &str, attempt_id: &str, acquisition_token: &Bytes) -> u64 {
    // FNV-1a 足够做幂等 key 的短指纹; 不用于安全边界。
    let mut hash = 0xcbf2_9ce4_8422_2325u64;
    for byte in request_id
        .as_bytes()
        .iter()
        .chain(attempt_id.as_bytes())
        .chain(acquisition_token.iter())
    {
        hash ^= u64::from(*byte);
        hash = hash.wrapping_mul(0x0000_0100_0000_01b3);
    }
    hash
}

fn redact_error(message: &str) -> String {
    let mut redacted = String::with_capacity(message.len().min(REDACTED_ERROR_LIMIT));
    for ch in message.chars().take(REDACTED_ERROR_LIMIT) {
        if ch.is_control() {
            redacted.push(' ');
        } else {
            redacted.push(ch);
        }
    }
    redacted
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
    fn status_strings_match_attempt_report_contract() {
        assert_eq!(AttemptStatus::Success.as_str(), "success");
        assert_eq!(AttemptStatus::ClientCancel.as_str(), "client_cancel");
        assert_eq!(AttemptStatus::Timeout.as_str(), "timeout");
        assert_eq!(AttemptStatus::ProtocolError.as_str(), "protocol_error");
        assert_eq!(AttemptStatus::Upstream4xx.as_str(), "upstream_4xx");
        assert_eq!(AttemptStatus::Upstream5xx.as_str(), "upstream_5xx");
        assert_eq!(AttemptStatus::NetworkError.as_str(), "network_error");
        assert_eq!(
            AttemptStatus::ControlPlaneError.as_str(),
            "control_plane_error"
        );
        assert_eq!(AttemptStatus::InternalError.as_str(), "internal_error");
    }

    #[test]
    fn idempotency_key_uses_attempt_uuid_and_token_fingerprint() {
        let attempt_id = format!("attempt-{}", Uuid::now_v7());
        let key = build_idempotency_key("request-1", &attempt_id, &Bytes::from_static(b"token-1"));
        assert!(key.contains(attempt_id.trim_start_matches("attempt-")));
        assert_eq!(
            key,
            build_idempotency_key("request-1", &attempt_id, &Bytes::from_static(b"token-1"))
        );
        assert_ne!(
            key,
            build_idempotency_key("request-1", &attempt_id, &Bytes::from_static(b"token-2"))
        );
    }
}
