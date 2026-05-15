// M-rust-9 redaction — log/trace 脱敏基础件
// 敏感 header 名称匹配后替换值为 [REDACTED]。
// prompt body 不进 log (守门 helper)。
// 带 secret 的 RoutePlan / UpstreamAuthMaterial / AttemptReportRequest /
// PlannedAttempt / AttemptReportContext / AttemptReport 通过手写 Debug impl
// 调用本文件的占位符 helper；本文件本身不自动拦截任意 span 字段。

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
const REDACTED_SECRET_PATTERN: &str = "[REDACTED_SECRET]";

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

/// 脱敏 upstream_auth.material (真实上游凭据按最高敏感级处理)
#[inline]
pub fn redact_upstream_auth_material(_material: &[u8]) -> &'static str {
    "[UPSTREAM_AUTH_MATERIAL_REDACTED]"
}

/// prompt body 守门: 判断是否为 prompt-class content-type (不应进 log)
/// 如需 log body 摘要, 调用方应先调用此函数做守门判断。
#[inline]
pub fn is_prompt_body_content_type(content_type: &str) -> bool {
    let lower = content_type.to_ascii_lowercase();
    // application/json 通常包含 prompt; text/* 也视为敏感
    lower.starts_with("application/json") || lower.starts_with("text/")
}

/// 外部错误/advisory 进入日志或 AttemptReport 前的统一清洗入口。
pub fn redact_untrusted_text(message: &str, limit: usize) -> String {
    let mut normalized = String::with_capacity(message.len().min(limit));
    for ch in message.chars().take(limit) {
        if ch.is_control() {
            normalized.push(' ');
        } else {
            normalized.push(ch);
        }
    }
    redact_secret_patterns(&normalized)
}

/// 擦除常见 secret 形态, 保留上下文中的错误类别和非敏感文字。
pub fn redact_secret_patterns(message: &str) -> String {
    let mut redacted = String::with_capacity(message.len());
    let mut token = String::new();
    let mut redact_next_after_bearer = false;

    for ch in message.chars() {
        if secret_token_delimiter(ch) {
            flush_secret_token(&mut token, &mut redacted, &mut redact_next_after_bearer);
            redacted.push(ch);
        } else {
            token.push(ch);
        }
    }
    flush_secret_token(&mut token, &mut redacted, &mut redact_next_after_bearer);

    redacted
}

fn flush_secret_token(
    token: &mut String,
    output: &mut String,
    redact_next_after_bearer: &mut bool,
) {
    if token.is_empty() {
        return;
    }

    let lower = token.to_ascii_lowercase();
    let should_redact = *redact_next_after_bearer || looks_like_secret_token(token, &lower);
    if should_redact {
        output.push_str(REDACTED_SECRET_PATTERN);
    } else {
        output.push_str(token);
    }

    *redact_next_after_bearer = lower == "bearer";
    token.clear();
}

fn secret_token_delimiter(ch: char) -> bool {
    ch.is_whitespace()
        || matches!(
            ch,
            '"' | '\''
                | ','
                | ';'
                | '('
                | ')'
                | '['
                | ']'
                | '{'
                | '}'
                | '<'
                | '>'
                | ':'
                | '='
                | '/'
                | '\\'
        )
}

fn looks_like_secret_token(token: &str, lower: &str) -> bool {
    lower.starts_with("sk-")
        || lower.starts_with("ya29.")
        || looks_like_jwt(token)
        || looks_like_long_token(token)
}

fn looks_like_jwt(token: &str) -> bool {
    let mut parts = token.split('.');
    let Some(first) = parts.next() else {
        return false;
    };
    let Some(second) = parts.next() else {
        return false;
    };
    let Some(third) = parts.next() else {
        return false;
    };
    if parts.next().is_some() {
        return false;
    }

    token.len() >= 24
        && first.starts_with("eyJ")
        && [first, second, third]
            .into_iter()
            .all(|part| part.len() >= 4 && part.chars().all(is_base64url_char))
}

fn looks_like_long_token(token: &str) -> bool {
    if token.len() < 40 || looks_like_uuid(token) {
        return false;
    }

    let mut has_alpha = false;
    let mut has_digit = false;
    let mut secretish_chars = 0usize;

    for ch in token.chars() {
        if ch.is_ascii_alphabetic() {
            has_alpha = true;
            secretish_chars = secretish_chars.saturating_add(1);
        } else if ch.is_ascii_digit() {
            has_digit = true;
            secretish_chars = secretish_chars.saturating_add(1);
        } else if matches!(ch, '-' | '_' | '.') {
            secretish_chars = secretish_chars.saturating_add(1);
        } else {
            return false;
        }
    }

    has_alpha && has_digit && secretish_chars >= 40
}

fn looks_like_uuid(token: &str) -> bool {
    let parts = token.split('-').collect::<Vec<_>>();
    let sizes = [8, 4, 4, 4, 12];
    parts.len() == sizes.len()
        && parts
            .iter()
            .zip(sizes)
            .all(|(part, size)| part.len() == size && part.chars().all(|ch| ch.is_ascii_hexdigit()))
}

fn is_base64url_char(ch: char) -> bool {
    ch.is_ascii_alphanumeric() || matches!(ch, '-' | '_')
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
    fn redact_upstream_auth_material_always_redacts() {
        let r = redact_upstream_auth_material(b"upstream-secret-real");
        assert_eq!(r, "[UPSTREAM_AUTH_MATERIAL_REDACTED]");
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

    #[test]
    fn untrusted_text_redacts_secret_patterns_and_controls() {
        let message = concat!(
            "grpc unavailable\n",
            "authorization: Bearer lease-token-value ",
            "api=sk-test-sensitive-value ",
            "oauth=ya29.real-access-token-value ",
            "jwt=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature000"
        );

        let redacted = redact_untrusted_text(message, 512);

        assert!(!redacted.contains("lease-token-value"));
        assert!(!redacted.contains("sk-test-sensitive-value"));
        assert!(!redacted.contains("ya29.real-access-token-value"));
        assert!(!redacted.contains("eyJhbGci"));
        assert!(!redacted.contains('\n'));
        assert!(redacted.contains(REDACTED_SECRET_PATTERN));
        assert!(redacted.contains("grpc unavailable"));
    }

    #[test]
    fn untrusted_text_redacts_long_token_but_keeps_uuid() {
        let message = concat!(
            "id=018f2a28-5f24-7c48-93cc-3fd10d197f24 ",
            "token=abcDEF1234567890abcDEF1234567890abcDEF123456"
        );

        let redacted = redact_untrusted_text(message, 512);

        assert!(redacted.contains("018f2a28-5f24-7c48-93cc-3fd10d197f24"));
        assert!(!redacted.contains("abcDEF1234567890abcDEF1234567890abcDEF123456"));
        assert!(redacted.contains(REDACTED_SECRET_PATTERN));
    }
}
