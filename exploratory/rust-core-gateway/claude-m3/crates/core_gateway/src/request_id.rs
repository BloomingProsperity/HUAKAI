// 请求 ID 生成与透传
// 优先使用 UUIDv7 (单调递增, 含毫秒时间戳, 便于日志排序与 trace 关联)

use std::sync::Arc;

use http::{HeaderMap, HeaderValue};
use uuid::Uuid;

/// 请求 ID 头名称 (标准化小写)
pub const REQUEST_ID_HEADER: &str = "x-request-id";
/// 透传 request_id 的最大允许长度 (防日志污染)
const MAX_REQUEST_ID_LEN: usize = 128;

/// 强类型请求 ID — 内部持有 Arc<str>, clone 成本极低
#[derive(Clone, Debug, Eq, Hash, PartialEq)]
pub struct RequestId {
    value: Arc<str>,
}

impl RequestId {
    /// 生成新的 UUIDv7 请求 ID
    pub fn generate() -> Self {
        Self {
            value: Arc::from(Uuid::now_v7().to_string()),
        }
    }

    /// 从候选字符串构建请求 ID; 若候选无效则生成新 ID
    pub fn from_candidate(candidate: Option<&str>) -> Self {
        candidate
            .and_then(normalize_candidate)
            .map_or_else(Self::generate, |value| Self {
                value: Arc::from(value),
            })
    }

    /// 从 HeaderMap 中读取请求 ID (读取 x-request-id 头)
    pub fn from_headers(headers: &HeaderMap) -> Self {
        Self::from_header_value(headers.get(REQUEST_ID_HEADER))
    }

    /// 从单个 HeaderValue 解析请求 ID
    pub fn from_header_value(value: Option<&HeaderValue>) -> Self {
        let candidate = value.and_then(|h| h.to_str().ok());
        Self::from_candidate(candidate)
    }

    /// 返回内部字符串切片
    pub fn as_str(&self) -> &str {
        &self.value
    }
}

impl std::fmt::Display for RequestId {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

/// 生成新的 UUIDv7 请求 ID (裸 Uuid, 供简单场景使用)
#[inline]
pub fn new_request_id() -> Uuid {
    Uuid::now_v7()
}

/// 将 UUID 格式化为 hyphenated 字符串
#[inline]
pub fn format_request_id(id: &Uuid) -> String {
    id.hyphenated().to_string()
}

/// 从字符串解析 UUID; 解析失败时生成新 ID (不 panic)
pub fn parse_or_generate(raw: Option<&str>) -> Uuid {
    raw.and_then(|s| Uuid::parse_str(s).ok())
        .unwrap_or_else(new_request_id)
}

/// 校验候选字符串: 非空、长度不超过上限、仅含可见 ASCII 字符
fn normalize_candidate(candidate: &str) -> Option<&str> {
    let trimmed = candidate.trim();

    if trimmed.is_empty() || trimmed.len() > MAX_REQUEST_ID_LEN {
        return None;
    }

    // 仅允许可见 ASCII (0x21..=0x7e), 防止日志注入
    if !trimmed.bytes().all(|b| matches!(b, 0x21..=0x7e)) {
        return None;
    }

    Some(trimmed)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashSet;

    #[test]
    fn request_ids_are_globally_unique() {
        // 生成 1000 个 ID, 验证全部唯一
        let ids: HashSet<_> = (0..1000).map(|_| RequestId::generate()).collect();
        assert_eq!(ids.len(), 1000, "request ID 应全部唯一");
    }

    #[test]
    fn request_id_is_v7() {
        let id = new_request_id();
        assert_eq!(id.get_version_num(), 7, "应为 UUIDv7");
    }

    #[test]
    fn parse_or_generate_falls_back_on_invalid() {
        // 无效字符串应生成新 ID 而不是 panic
        let id = parse_or_generate(Some("not-a-uuid"));
        assert_eq!(id.get_version_num(), 7);
    }

    #[test]
    fn parse_or_generate_roundtrips_valid_uuid() {
        let original = new_request_id();
        let formatted = format_request_id(&original);
        let parsed = parse_or_generate(Some(&formatted));
        assert_eq!(original, parsed, "ID 序列化后再解析应与原值相等");
    }

    #[test]
    fn ids_are_monotonically_nondecreasing() {
        // UUIDv7 含毫秒时间戳, 字节序比较等价于时间顺序
        let a = new_request_id();
        let b = new_request_id();
        assert!(b.as_bytes() >= a.as_bytes(), "UUIDv7 应单调不减");
    }

    #[test]
    fn from_candidate_propagates_valid_id() {
        let rid = RequestId::from_candidate(Some("client-request-42"));
        assert_eq!(rid.as_str(), "client-request-42");
    }

    #[test]
    fn from_candidate_generates_on_empty() {
        let rid = RequestId::from_candidate(Some(""));
        let uuid = Uuid::parse_str(rid.as_str()).expect("fallback 应为合法 UUID");
        assert_eq!(uuid.get_version_num(), 7);
    }

    #[test]
    fn from_candidate_rejects_overlong_input() {
        let long = "a".repeat(129);
        let rid = RequestId::from_candidate(Some(&long));
        // 超长输入应触发 generate(), 结果为合法 UUID
        assert!(Uuid::parse_str(rid.as_str()).is_ok());
    }
}
