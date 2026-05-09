use std::sync::Arc;

use http::{HeaderMap, HeaderValue};
use uuid::Uuid;

pub const REQUEST_ID_HEADER: &str = "x-request-id";
const MAX_REQUEST_ID_LEN: usize = 128;

#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct RequestId {
    value: Arc<str>,
}

impl RequestId {
    pub fn generate() -> Self {
        Self {
            value: Arc::from(Uuid::now_v7().to_string()),
        }
    }

    pub fn from_candidate(candidate: Option<&str>) -> Self {
        candidate
            .and_then(normalize_candidate)
            .map_or_else(Self::generate, |value| Self {
                value: Arc::from(value),
            })
    }

    pub fn from_headers(headers: &HeaderMap) -> Self {
        Self::from_header_value(headers.get(REQUEST_ID_HEADER))
    }

    pub fn from_header_value(value: Option<&HeaderValue>) -> Self {
        let candidate = value.and_then(|header| header.to_str().ok());
        Self::from_candidate(candidate)
    }

    pub fn as_str(&self) -> &str {
        &self.value
    }
}

impl std::fmt::Display for RequestId {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter.write_str(self.as_str())
    }
}

fn normalize_candidate(candidate: &str) -> Option<&str> {
    // 允许透传客户端 request_id，但限制长度和可见字符，避免日志污染。
    let trimmed = candidate.trim();

    if trimmed.is_empty() || trimmed.len() > MAX_REQUEST_ID_LEN {
        return None;
    }

    if !trimmed.bytes().all(|byte| matches!(byte, 0x21..=0x7e)) {
        return None;
    }

    Some(trimmed)
}
