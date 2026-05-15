use std::fmt;

use bytes::Bytes;
use uuid::Uuid;

use crate::{
    account_planner::PlannedAttempt,
    redaction::redact_acquisition_token,
    request_id::RequestId,
    route_proto::v1::{AttemptReportRequest, CacheMetrics, TokensUsed},
    stream_pipeline::StreamEvent,
};

use super::{
    idempotency::build_idempotency_key,
    metrics::{AttemptCacheMetrics, AttemptTokenMetrics},
    now_unix_ms_i64, redact_error,
};

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

#[derive(Clone, Eq, PartialEq)]
pub struct AttemptReportContext {
    pub request_id: String,
    pub route_plan_id: String,
    pub attempt_id: String,
    pub acquisition_token: Bytes,
    pub idempotency_key: String,
    pub started_at_ms: i64,
    pub bytes_in: u64,
}

impl fmt::Debug for AttemptReportContext {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("AttemptReportContext")
            .field("request_id", &self.request_id)
            .field("route_plan_id", &self.route_plan_id)
            .field("attempt_id", &self.attempt_id)
            .field(
                "acquisition_token",
                &redact_acquisition_token(self.acquisition_token.as_ref()),
            )
            .field("idempotency_key", &self.idempotency_key)
            .field("started_at_ms", &self.started_at_ms)
            .field("bytes_in", &self.bytes_in)
            .finish()
    }
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

#[derive(Clone, Eq, PartialEq)]
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

impl fmt::Debug for AttemptReport {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("AttemptReport")
            .field("request_id", &self.request_id)
            .field("route_plan_id", &self.route_plan_id)
            .field("attempt_id", &self.attempt_id)
            .field(
                "acquisition_token",
                &redact_acquisition_token(self.acquisition_token.as_ref()),
            )
            .field("status", &self.status)
            .field("http_status", &self.http_status)
            .field("started_at", &self.started_at)
            .field("ended_at", &self.ended_at)
            .field("latency_ms", &self.latency_ms)
            .field("tokens_used", &self.tokens_used)
            .field("cache_metrics", &self.cache_metrics)
            .field("bytes_in", &self.bytes_in)
            .field("bytes_out", &self.bytes_out)
            .field("frames_in", &self.frames_in)
            .field("frames_out", &self.frames_out)
            .field("vendor_request_id", &self.vendor_request_id)
            .field("retryable", &self.retryable)
            .field("error_class", &self.error_class)
            .field("error_message_redacted", &self.error_message_redacted)
            .field("idempotency_key", &self.idempotency_key)
            .finish()
    }
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

#[cfg(test)]
mod tests {
    use bytes::Bytes;

    use super::super::metrics::{AttemptCacheMetrics, AttemptTokenMetrics};
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
    fn attempt_report_context_debug_redacts_token_but_keeps_ids() {
        let context = AttemptReportContext {
            request_id: "request-redact-1".to_owned(),
            route_plan_id: "route-plan-redact-1".to_owned(),
            attempt_id: "attempt-redact-1".to_owned(),
            acquisition_token: Bytes::from_static(b"lease-token-mock-1"),
            idempotency_key: "idem-redact-1".to_owned(),
            started_at_ms: 1,
            bytes_in: 12,
        };

        let debug = format!("{:?}", context);

        assert!(!debug.contains("lease-token-mock-1"));
        assert!(debug.contains("[ACQUISITION_TOKEN_REDACTED]"));
        assert!(debug.contains("request-redact-1"));
        assert!(debug.contains("route-plan-redact-1"));
        assert!(debug.contains("attempt-redact-1"));
    }

    #[test]
    fn attempt_report_debug_redacts_token_but_keeps_report_fields() {
        let report = AttemptReport {
            request_id: "request-redact-1".to_owned(),
            route_plan_id: "route-plan-redact-1".to_owned(),
            attempt_id: "attempt-redact-1".to_owned(),
            acquisition_token: Bytes::from_static(b"lease-token-mock-1"),
            status: AttemptStatus::Success,
            http_status: 200,
            started_at: 1,
            ended_at: 2,
            latency_ms: 1,
            tokens_used: AttemptTokenMetrics {
                input_tokens: 10,
                output_tokens: 20,
                total_tokens: 30,
                source: "test".to_owned(),
            },
            cache_metrics: AttemptCacheMetrics {
                cache_read_tokens: 0,
                cache_write_tokens: 0,
                cache_hit: false,
                source: "test".to_owned(),
            },
            bytes_in: 12,
            bytes_out: 34,
            frames_in: 1,
            frames_out: 1,
            vendor_request_id: "vendor-redact-1".to_owned(),
            retryable: false,
            error_class: String::new(),
            error_message_redacted: String::new(),
            idempotency_key: "idem-redact-1".to_owned(),
        };

        let debug = format!("{:?}", report);

        assert!(!debug.contains("lease-token-mock-1"));
        assert!(debug.contains("[ACQUISITION_TOKEN_REDACTED]"));
        assert!(debug.contains("request-redact-1"));
        assert!(debug.contains("route-plan-redact-1"));
        assert!(debug.contains("Success"));
        assert!(debug.contains("vendor-redact-1"));
    }
}
