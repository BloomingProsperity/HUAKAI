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
    /// Baseline (spool disabled) try_send 成功; 或 spool enabled + queue notification 成功。
    Enqueued,
    /// W12-A D-4 Slice 2: spool 持久化成功但 live channel 满 (replay worker 兜底处理, 不算丢失)。
    Spooled,
    /// Baseline (spool disabled) channel 满 → 报告真丢失。
    DroppedFull,
    /// Channel 关闭 → 报告真丢失。
    DroppedClosed,
    /// W12-A D-4 Slice 2: spool reserve() Err WatermarkExceeded / LastWriteFailed;
    /// Slice 3 forward_planned 看到此结果 → 返 503 (AC-4-pre)。
    SpoolBackpressure,
    /// W12-A D-4 Slice 2: spool persist 物理失败 (IO / 编码) → 走 try_send 兜底, 详细错日志已记。
    /// Slice 3 post-commit 路径会用 `spool_drop_billable` counter 标识不可逆账务损失。
    SpoolWriteFailed,
}

impl ReportEnqueueResult {
    /// W12-A D-4 Slice 3: 是否 "降级" (账务非完整成功) — Enqueued/Spooled 是 OK, 其他都需关注。
    /// post-commit 路径用此判定是否触发 `spool_drop_billable` + loud log。
    pub fn is_degraded(self) -> bool {
        matches!(
            self,
            Self::DroppedFull
                | Self::DroppedClosed
                | Self::SpoolBackpressure
                | Self::SpoolWriteFailed
        )
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum TerminalReportResult {
    Submitted(ReportEnqueueResult),
    AlreadyReported,
}

/// W12-D D-8 P1-2 fix 2026-05-24: 4xx transient 白名单 (允许 retry)。
///
/// 408 Request Timeout (RFC 9110 §15.5.9): 服务端没在 timeout 内收齐请求, 客户端 retry。
/// 425 Too Early (RFC 8470): 0-RTT 拒绝, retry 应不带 early data。
/// 429 Too Many Requests (RFC 6585): 节流, 按 Retry-After 退避后 retry。
/// 449 Retry With (Microsoft 私有): 服务要求带额外信息 retry。
///
/// 其他 4xx (400/401/403/404/405/410/422/451 ...) 视为永久不可重试 —
/// retry 不会修复, 反而可能加重 vendor 防御性拒绝 (401/403 撞太多次封号)。
fn is_4xx_transient_status(http_status: u16) -> bool {
    matches!(http_status, 408 | 425 | 429 | 449)
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

    /// W12-D D-8 P1-2 fix 2026-05-24: 旧 retryable() 在所有 Upstream4xx 上返 false,
    /// 把 vendor 临时限流 (429 Too Many Requests) / 请求超时 (408 Request Timeout) 当成
    /// 永久错误不让控制面重试 -> 客户端被无谓拒绝, 浪费 quota; 反之没区分 401/403
    /// (auth/permission 永久) 与 429 (节流可恢复), 把它们都标 retry 会让控制面挑同一 token
    /// 反复撞同一 401/403 = vendor 端禁号风险。
    ///
    /// 新行为基于 HTTP 终态 + http_status 联合判定。无 http_status 时降级到旧语义保兼容。
    /// 4xx 中 408 (Request Timeout) / 425 (Too Early) / 429 (Too Many Requests) /
    /// 449 (Retry With) 视为 transient 可重试; 其他 4xx (400/401/403/404/422 ...)
    /// 视为永久不可重试 (重试只会同样失败 + 加重 vendor 拒绝)。
    pub fn retryable_with_http(self, http_status: u16) -> bool {
        match self {
            Self::Timeout
            | Self::Upstream5xx
            | Self::NetworkError
            | Self::ControlPlaneError => true,
            Self::Upstream4xx => is_4xx_transient_status(http_status),
            Self::Success | Self::ClientCancel | Self::ProtocolError | Self::InternalError => {
                false
            }
        }
    }

    /// 旧 API 保留: 无 http_status 时按保守语义 — Upstream4xx 全 non-retryable, 与
    /// W12-D D-8 之前行为完全一致, 避免无 http 信息的 caller 突变行为。
    pub fn retryable(self) -> bool {
        self.retryable_with_http(0)
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

    /// W12-B D-5: 非流式 2xx body 解析出 usage → 写权威 source="response_body"。
    /// 调用方负责保证只在 stream_tap=None + 2xx + JSON 路径调用。
    pub fn record_response_body_usage(&mut self, delta: &crate::stream_pipeline::UsageDelta) {
        self.tokens_used = Some(AttemptTokenMetrics::from_response_body(delta));
    }

    /// W12-B D-5: 非流式 2xx body 检查过但 usage 字段不可解析 → pending_reconciliation,
    /// 区别于 "missing" (从未尝试), 让控制面对账时知道这条 attempt 已检查过 body。
    pub fn record_response_body_usage_unparsable(&mut self) {
        self.tokens_used = Some(AttemptTokenMetrics::pending_reconciliation());
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

    /// W11-D2 B2: mock 上游分支专用合成 context。
    /// `attempt_id` 以 `attempt-mock-` 起头 + `route_plan_id = "mock-upstream-drill"`，
    /// 让控制面 / 审计后台能立即把演练流量与真实流量区分开，避免账本对账空洞。
    pub fn synthetic_mock_attempt(request_id: &RequestId) -> Self {
        let attempt_id = format!("attempt-mock-{}", Uuid::now_v7());
        let acquisition_token = Bytes::new();
        let idempotency_key =
            build_idempotency_key(request_id.as_str(), &attempt_id, &acquisition_token);

        Self {
            request_id: request_id.as_str().to_owned(),
            route_plan_id: "mock-upstream-drill".to_owned(),
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
            // W12-D D-8 P1-2 fix 2026-05-24: 用 http_status 精细化 retry 分类,
            // 让 429/408 retry 但 401/403/404 等永久错不 retry (防 vendor 封号)。
            retryable: status.retryable_with_http(http_status.unwrap_or(0)),
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
    use crate::request_id::RequestId;

    /// W11-D2 B2 + P2 codex review 2026-05-23: synthetic_mock_attempt 必须使用
    /// 显著区别于 from_planned / synthetic_control_plane_error 的标记字段,
    /// 让控制面 / 审计后台能按 attempt_id 前缀和 route_plan_id 立即识别演练流量。
    /// mutation: 改 attempt_id 前缀去掉 "mock-" / 把 route_plan_id 改回 String::new() → 此测试红。
    #[test]
    fn synthetic_mock_attempt_uses_distinctive_marker_fields() {
        let request_id = RequestId::generate();

        let context = AttemptReportContext::synthetic_mock_attempt(&request_id);

        assert!(
            context.attempt_id.starts_with("attempt-mock-"),
            "synthetic_mock_attempt 必须以 attempt-mock- 前缀让审计区分演练流量, 实际: {}",
            context.attempt_id
        );
        assert_eq!(
            context.route_plan_id, "mock-upstream-drill",
            "synthetic_mock_attempt 必须使用 mock-upstream-drill 标记 route_plan_id, 实际: {}",
            context.route_plan_id
        );
        assert_eq!(
            context.bytes_in, 0,
            "synthetic context 不携带请求 body 字节"
        );
        assert!(
            context.acquisition_token.is_empty(),
            "synthetic 演练 attempt 不携带真实凭据 token"
        );
    }

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

    /// W12-D D-8 P1-2 fix 2026-05-24: 4xx 必须按 http_status 区分 retryable vs 非
    /// retryable — 429/408 retryable; 401/403/400/404/422 non-retryable。
    ///
    /// 判别性 + mutation 设计 (9 case 全独立断言):
    /// 1) Upstream4xx + 408 -> true (Request Timeout)
    /// 2) Upstream4xx + 425 -> true (Too Early)
    /// 3) Upstream4xx + 429 -> true (Too Many Requests)
    /// 4) Upstream4xx + 449 -> true (Retry With)
    /// 5) Upstream4xx + 400 -> false (Bad Request)
    /// 6) Upstream4xx + 401 -> false (Unauthorized — 防 vendor 封号)
    /// 7) Upstream4xx + 403 -> false (Forbidden — 防 vendor 封号)
    /// 8) Upstream4xx + 404 -> false (Not Found — 端点不存在)
    /// 9) Upstream4xx + 422 -> false (Unprocessable — 客户端 bug)
    ///
    /// mutation:
    /// - is_4xx_transient_status 改成 `true` -> case 5-9 红 (全 false 但被 true 覆盖)
    /// - is_4xx_transient_status 改成 `false` -> case 1-4 红
    /// - 删 4xx 分支让它走 default false -> case 1-4 红
    /// - 把 429 从白名单删 -> case 3 红
    /// - 把 401 加进白名单 -> case 6 红
    #[test]
    fn upstream_4xx_retryable_only_for_408_425_429_449() {
        // Transient (retry 应允许)
        assert!(
            AttemptStatus::Upstream4xx.retryable_with_http(408),
            "408 Request Timeout 必须 retryable (RFC 9110 §15.5.9)"
        );
        assert!(
            AttemptStatus::Upstream4xx.retryable_with_http(425),
            "425 Too Early 必须 retryable (RFC 8470)"
        );
        assert!(
            AttemptStatus::Upstream4xx.retryable_with_http(429),
            "429 Too Many Requests 必须 retryable (RFC 6585, vendor 节流后退避)"
        );
        assert!(
            AttemptStatus::Upstream4xx.retryable_with_http(449),
            "449 Retry With 必须 retryable (MS 私有, 携带补充信息后 retry)"
        );

        // Permanent (retry 反而有害)
        for permanent_code in [400, 401, 403, 404, 405, 410, 422, 451] {
            assert!(
                !AttemptStatus::Upstream4xx.retryable_with_http(permanent_code),
                "{permanent_code} 必须 NON-retryable (永久错误, retry 浪费配额且 401/403 可能加重 vendor 拒绝)"
            );
        }
    }

    /// W12-D D-8 P1-2 fix: Upstream5xx 全部仍 retryable (不变); 其它状态规则维持。
    /// mutation: 把 Upstream5xx 移出 retryable_with_http -> case 1 红;
    /// 把 InternalError 加进 retryable -> case 4 红。
    #[test]
    fn retryable_with_http_keeps_legacy_5xx_timeout_network_control_plane_behavior() {
        assert!(
            AttemptStatus::Upstream5xx.retryable_with_http(500),
            "Upstream5xx 必须 retryable (服务端临时故障)"
        );
        assert!(
            AttemptStatus::Timeout.retryable_with_http(504),
            "Timeout 必须 retryable"
        );
        assert!(
            AttemptStatus::NetworkError.retryable_with_http(0),
            "NetworkError 必须 retryable (传输层临时)"
        );
        assert!(
            AttemptStatus::ControlPlaneError.retryable_with_http(503),
            "ControlPlaneError 必须 retryable (CP 临时不可用)"
        );

        // Non-retryable 终态
        assert!(!AttemptStatus::Success.retryable_with_http(200));
        assert!(!AttemptStatus::ClientCancel.retryable_with_http(0));
        assert!(!AttemptStatus::ProtocolError.retryable_with_http(0));
        assert!(
            !AttemptStatus::InternalError.retryable_with_http(0),
            "InternalError 不可 retry (HUAKAI 自身 bug, retry 还会撞)"
        );
    }

    /// 派生: 旧 retryable() (无 http_status 输入) 必须保兼容 — Upstream4xx 永远 false,
    /// 防止无 http 信息的 caller 突变行为。
    /// mutation: 让 retryable() 默认 true 4xx -> 此测试红。
    #[test]
    fn legacy_retryable_keeps_upstream_4xx_non_retryable_for_compat() {
        assert!(!AttemptStatus::Upstream4xx.retryable());
        // 5xx/Timeout/Network/CP 仍 retryable (与旧行为一致)
        assert!(AttemptStatus::Upstream5xx.retryable());
        assert!(AttemptStatus::Timeout.retryable());
        assert!(AttemptStatus::NetworkError.retryable());
        assert!(AttemptStatus::ControlPlaneError.retryable());
    }

    /// W12-D D-8 P1-2 集成判别性: terminal_report() 把 http_status 真传到
    /// retryable_with_http, AttemptReport.retryable 反映精细化结果。
    /// mutation: terminal_report 退回 status.retryable() (旧版) -> 429 报告
    /// retryable=false -> 测试红 (复现 P1 finding: vendor 节流被当永久错)。
    #[test]
    fn terminal_report_marks_429_retryable_and_401_non_retryable() {
        let request_id = RequestId::generate();
        let ctx = AttemptReportContext::synthetic_control_plane_error(&request_id);

        // 429 -> retryable
        let report_429 = ctx.terminal_report(
            AttemptStatus::Upstream4xx,
            Some(429),
            AttemptReportStats::default(),
            None,
            None,
        );
        assert!(
            report_429.retryable,
            "429 报告必须 retryable=true (mutation: 退回旧 retryable() -> false)"
        );
        assert_eq!(report_429.http_status, 429);

        // 401 -> non-retryable
        let report_401 = ctx.terminal_report(
            AttemptStatus::Upstream4xx,
            Some(401),
            AttemptReportStats::default(),
            None,
            None,
        );
        assert!(
            !report_401.retryable,
            "401 报告必须 retryable=false (防 vendor 封号 — 同 token 反复撞 401)"
        );
        assert_eq!(report_401.http_status, 401);
    }
}
