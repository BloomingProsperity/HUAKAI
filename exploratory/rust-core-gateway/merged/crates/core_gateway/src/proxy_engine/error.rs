use http::StatusCode;

use crate::error::GatewayError;

#[derive(Debug, thiserror::Error)]
pub enum ProxyError {
    #[error("bad upstream uri: {0}")]
    BadUpstreamUri(String),
    #[error("bad upstream request: {0}")]
    BadUpstreamRequest(String),
    #[error("bad route plan: {0}")]
    BadRoutePlan(String),
    #[error("upstream error: {0}")]
    Upstream(String),
    #[error("upstream timeout")]
    Timeout,
    #[error("attempt state error: {0}")]
    AttemptState(#[from] GatewayError),
    /// W12-A D-4 Slice 3 AC-4-pre: attempt spool 接近 watermark 或最近写失败时,
    /// forward_planned 在转发上游前拒绝, 返回 503 让 client 重试; 请求未进上游 = 未产生计费事件。
    #[error("attempt report spool backpressure")]
    AttemptReportBackpressure,
}

impl ProxyError {
    pub fn status_code(&self) -> StatusCode {
        match self {
            Self::Timeout => StatusCode::GATEWAY_TIMEOUT,
            Self::AttemptReportBackpressure => StatusCode::SERVICE_UNAVAILABLE,
            Self::BadUpstreamUri(_)
            | Self::BadUpstreamRequest(_)
            | Self::BadRoutePlan(_)
            | Self::Upstream(_)
            | Self::AttemptState(_) => StatusCode::BAD_GATEWAY,
        }
    }

    pub fn code(&self) -> &'static str {
        match self {
            Self::Timeout => "upstream_timeout",
            Self::BadUpstreamUri(_) => "bad_upstream_uri",
            Self::BadUpstreamRequest(_) => "bad_upstream_request",
            Self::BadRoutePlan(_) | Self::AttemptState(_) => "bad_route_plan",
            Self::Upstream(_) => "upstream_error",
            Self::AttemptReportBackpressure => "attempt_report_spool_unavailable",
        }
    }
}
