// 请求 ID 生成与透传
// 优先使用 UUIDv7 (单调递增, 含毫秒时间戳, 便于日志排序与 trace 关联)

use uuid::Uuid;

/// 生成新的请求 ID (UUIDv7)
/// 单调递增, 含毫秒时间戳, 全局唯一
#[inline]
pub fn new_request_id() -> Uuid {
    Uuid::now_v7()
}

/// 将请求 ID 格式化为超链接友好的短字符串 (hyphenated UUID)
#[inline]
pub fn format_request_id(id: &Uuid) -> String {
    id.hyphenated().to_string()
}

/// 从请求头或上游透传字段解析请求 ID
/// 若解析失败则生成新 ID, 确保每个请求都有有效 ID
pub fn parse_or_generate(raw: Option<&str>) -> Uuid {
    raw.and_then(|s| Uuid::parse_str(s).ok())
        .unwrap_or_else(new_request_id)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashSet;

    #[test]
    fn request_ids_are_unique() {
        // 生成 1000 个 ID, 验证全部唯一
        let ids: HashSet<_> = (0..1000).map(|_| new_request_id()).collect();
        assert_eq!(ids.len(), 1000, "request ID 应全部唯一");
    }

    #[test]
    fn request_id_is_v7() {
        let id = new_request_id();
        // UUIDv7 的 version nibble 为 7
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
        assert_eq!(original, parsed);
    }

    #[test]
    fn ids_are_monotonically_increasing() {
        // UUIDv7 含毫秒时间戳, 同 ms 内后生成的不小于先生成的
        let a = new_request_id();
        let b = new_request_id();
        // 字节序比较等价于时间顺序
        assert!(b.as_bytes() >= a.as_bytes(), "UUIDv7 应单调不减");
    }
}
