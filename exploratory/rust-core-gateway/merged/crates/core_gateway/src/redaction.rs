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

/// P1-7 Codex round 2 fix 2026-05-24: 客户端请求中应剥除的凭据 header (request-side
/// subset, 去掉 set-cookie 这个 response-only header)。listener.rs mock 分支与未来
/// 其它 strip 路径 (W11-D2 vendor 透传策略变更) 必须从这里取, 避免与 redaction 名单
/// 漂移。
pub const SENSITIVE_REQUEST_CREDENTIAL_HEADERS: &[&str] = &[
    "authorization",
    "x-api-key",
    "cookie",
    "proxy-authorization",
    "x-auth-token",
    "x-access-token",
];
const REDACTED_SECRET_PATTERN: &str = "[REDACTED_SECRET]";

/// 判断 header 名称是否需要脱敏 (O(n), n=8 常数极小, 优于 HashSet 构造开销)
#[inline]
pub fn is_sensitive_header(name: &str) -> bool {
    SENSITIVE_HEADERS
        .iter()
        .any(|sensitive| name.eq_ignore_ascii_case(sensitive))
}

/// 对 header value 脱敏: 敏感 header 返回 `[REDACTED]`, 否则返回原值。
/// 返回借用的字符串切片: 敏感 header 返回静态占位符, 否则借用原 value。
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

/// W11-A D-1b Phase 1 A4 acceptance gate (2026-05-24): 脱敏 RouteQueryRequest.client_credential
/// (proto canonical string "bearer:<token>" / "x-api-key:<key>")。
///
/// 返回形式:
/// - `"[empty]"` — 空字段 (Manual First 静态表外或缺凭据 anonymous)。
/// - `"[CLIENT_CREDENTIAL_REDACTED kind=<bearer|x-api-key|unknown> sha256=<8hex>]"` —
///   kind label 让审计区分协议族 + SHA-256 前 4 字节 (8 hex chars) prefix 用于审计相关性
///   (同凭据多次请求生成相同 fingerprint)。raw secret 永不入此函数返回值。
///
/// **Codex round 1 LOW finding 2026-05-24 fix**: 早期实现只输出 kind, 与 redacting_debug.rs
/// 注释 "渲染为 fingerprint" 不符。本版本对完整 canonical 串 SHA-256 后取前 4 字节 hex,
/// 与 `ClientCredential::fingerprint()` 同源 → 审计能用 fingerprint 串关联 raw secret 不入 log。
///
/// mutation: 改返回值含 raw `value` 字符 → A4 测试红 (observability_test +
/// client_auth::credential::tests::debug_impl_never_leaks_raw_credential 双线守门);
/// 改回不含 sha256 → route_query_debug_contains_credential_fingerprint 红 (新增 below)。
pub fn redact_client_credential_for_debug(value: &str) -> String {
    use sha2::{Digest, Sha256};
    use std::fmt::Write;

    if value.is_empty() {
        return "[empty]".to_owned();
    }
    let kind = match value.split_once(':') {
        Some((prefix, _rest)) if prefix == "bearer" || prefix == "x-api-key" => prefix,
        _ => "unknown",
    };
    let digest = Sha256::digest(value.as_bytes());
    let mut sha256_first_8 = String::with_capacity(8);
    for b in digest.iter().take(4) {
        let _ = write!(sha256_first_8, "{:02x}", b);
    }
    format!("[CLIENT_CREDENTIAL_REDACTED kind={kind} sha256={sha256_first_8}]")
}

/// W11-A D-1b A4 fingerprint 守门测试组 (codex round 1 LOW fix 2026-05-24)。
/// 与文件末尾旧 `mod tests` 共存; 命名不冲突防 E0428 错。
#[cfg(test)]
mod redact_client_credential_tests {
    use super::*;

    /// A4 fingerprint 守门 (codex round 1 LOW fix): redact 输出必须含 sha256 prefix。
    /// mutation: 删 SHA-256 计算 / 改回 `[CLIENT_CREDENTIAL_REDACTED kind=...]` → 红。
    #[test]
    fn redact_client_credential_for_debug_includes_kind_and_fingerprint() {
        let out = redact_client_credential_for_debug("bearer:FAKE-redact-test-token");
        assert!(out.contains("kind=bearer"), "应含 kind label: {out}");
        assert!(out.contains("sha256="), "A4 fingerprint: 应含 sha256 prefix: {out}");
        // sha256 段值长 8 hex chars (4 bytes)
        let sha_segment = out
            .split("sha256=")
            .nth(1)
            .and_then(|s| s.split(']').next())
            .expect("redact 输出应含 sha256=<hex>] 段");
        assert_eq!(
            sha_segment.len(),
            8,
            "SHA-256 prefix 必须 8 hex chars (4 bytes), 实际: {sha_segment:?} ({})",
            sha_segment.len()
        );
        assert!(
            sha_segment.chars().all(|c| c.is_ascii_hexdigit()),
            "sha256 prefix 必须全为 hex digit, 实际: {sha_segment:?}"
        );
        // raw token 永不出现
        assert!(
            !out.contains("FAKE-redact-test-token"),
            "A4: raw token 不能入 redact 输出: {out}"
        );
    }

    /// A4 deterministic: 同 canonical 多次 redact 同结果 (审计相关性)。
    /// mutation: 把 SHA-256 换成 random / time-based → 红 (两次结果不等)。
    #[test]
    fn redact_client_credential_for_debug_is_deterministic() {
        let v = "bearer:FAKE-deterministic-test";
        assert_eq!(
            redact_client_credential_for_debug(v),
            redact_client_credential_for_debug(v),
            "redact 必须 deterministic 让审计能关联同凭据"
        );
    }

    /// A4: 空字符串 → `[empty]` 占位 (anonymous 通路, dev/test 默认)。
    #[test]
    fn redact_client_credential_for_debug_empty_value() {
        assert_eq!(redact_client_credential_for_debug(""), "[empty]");
    }

    /// kind=unknown 处理: 非 bearer/x-api-key 前缀 → kind=unknown + 仍含 sha256。
    /// mutation: 把 unknown 分支删 → 红 (panic on Option::unwrap)。
    #[test]
    fn redact_client_credential_for_debug_unknown_kind_still_fingerprints() {
        let out = redact_client_credential_for_debug("garbage-no-prefix");
        assert!(out.contains("kind=unknown"), "未知 kind 应标 unknown: {out}");
        assert!(out.contains("sha256="), "未知 kind 仍应有 fingerprint: {out}");
    }
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
    fn long_non_sensitive_header_name_is_not_sensitive() {
        let header_name =
            "x-huakai-long-non-sensitive-routing-diagnostic-header-name-for-observability";

        assert!(!is_sensitive_header(header_name));
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
