// M-rust-9 redaction — log/trace 脱敏
// 静态 HashSet 匹配敏感 header 名称, 替换值为 [REDACTED]。
// prompt body 不进 log (守门 helper)。
// credential_handle / acquisition_token 在 span 内自动脱敏。

// ─── 敏感 header 名称集合 (全小写, HTTP header 名称大小写不敏感) ────────────────

/// 需要脱敏的 header 名称 (全小写)
/// 使用 phf 风格的静态常量替代; 编译时确定, 无堆分配。
const SENSITIVE_HEADERS: &[&str] = &[
    "authorization",
    "x-api-key",
    "cookie",
    "set-cookie",
    "proxy-authorization",
    "x-auth-token",
    "x-access-token",
];

/// 判断 header 名称是否需要脱敏 (O(n), n=8 常数极小, 优于 HashSet 构造开销)
#[inline]
pub fn is_sensitive_header(name: &str) -> bool {
    let lower = name.to_ascii_lowercase();
    SENSITIVE_HEADERS.iter().any(|&s| s == lower)
}

/// 对 header value 脱敏: 敏感 header 返回 `[REDACTED]`, 否则返回原值。
/// 返回 &'static str 或借用 value 的 str — 为避免生命周期复杂性, 统一返回 String。
pub fn redact_header_value<'a>(name: &str, value: &'a str) -> &'a str {
    if is_sensitive_header(name) {
        "[REDACTED]"
    } else {
        value
    }
}

/// 脱敏 credential_handle (始终替换为占位符, 不记录真实句柄)
#[inline]
pub fn redact_credential_handle(_handle: &str) -> &'static str {
    "[CREDENTIAL_HANDLE_REDACTED]"
}

/// 脱敏 acquisition_token (始终替换为占位符)
#[inline]
pub fn redact_acquisition_token(_token: &[u8]) -> &'static str {
    "[ACQUISITION_TOKEN_REDACTED]"
}

/// prompt body 守门: 判断是否为 prompt-class content-type (不应进 log)
/// 如需 log body 摘要, 调用方应先调用此函数做守门判断。
#[inline]
pub fn is_prompt_body_content_type(content_type: &str) -> bool {
    let lower = content_type.to_ascii_lowercase();
    // application/json 通常包含 prompt; text/* 也视为敏感
    lower.starts_with("application/json") || lower.starts_with("text/")
}

// ─── 单元测试 ─────────────────────────────────────────────────────────────────

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn authorization_header_is_sensitive() {
        assert!(is_sensitive_header("Authorization"));
        assert!(is_sensitive_header("authorization"));
        assert!(is_sensitive_header("AUTHORIZATION"));
    }

    #[test]
    fn x_api_key_header_is_sensitive() {
        assert!(is_sensitive_header("X-Api-Key"));
        assert!(is_sensitive_header("x-api-key"));
    }

    #[test]
    fn cookie_header_is_sensitive() {
        assert!(is_sensitive_header("Cookie"));
        assert!(is_sensitive_header("set-cookie"));
    }

    #[test]
    fn non_sensitive_headers_pass_through() {
        assert!(!is_sensitive_header("content-type"));
        assert!(!is_sensitive_header("accept"));
        assert!(!is_sensitive_header("x-request-id"));
        assert!(!is_sensitive_header("user-agent"));
    }

    #[test]
    fn redact_header_value_replaces_sensitive() {
        let redacted = redact_header_value("Authorization", "Bearer sk-secret-token");
        assert_eq!(redacted, "[REDACTED]");
    }

    #[test]
    fn redact_header_value_passes_non_sensitive() {
        let value = redact_header_value("content-type", "application/json");
        assert_eq!(value, "application/json");
    }

    #[test]
    fn redact_credential_handle_always_redacts() {
        let r = redact_credential_handle("real-handle-abc123");
        assert_eq!(r, "[CREDENTIAL_HANDLE_REDACTED]");
    }

    #[test]
    fn redact_acquisition_token_always_redacts() {
        let r = redact_acquisition_token(b"super-secret-token");
        assert_eq!(r, "[ACQUISITION_TOKEN_REDACTED]");
    }

    #[test]
    fn prompt_body_content_type_detection() {
        assert!(is_prompt_body_content_type("application/json"));
        assert!(is_prompt_body_content_type(
            "application/json; charset=utf-8"
        ));
        assert!(is_prompt_body_content_type("text/plain"));
        assert!(!is_prompt_body_content_type("multipart/form-data"));
        assert!(!is_prompt_body_content_type("application/octet-stream"));
    }
}
