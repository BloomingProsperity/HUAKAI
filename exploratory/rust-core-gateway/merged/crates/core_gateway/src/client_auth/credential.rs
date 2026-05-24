//! W11-A D-1b Phase 1 credential extraction (synthesis §6 step 5)。
//!
//! 从 HTTP headers 派生 `ClientCredential` (Bearer / x-api-key), 产 canonical proto
//! value + SHA-256 fingerprint。raw secret 永不进 log / Debug / span field (A4)。
//!
//! 决策 (synthesis §3):
//! - D-2 表达: flat string canonical `"bearer:<token>"` / `"x-api-key:<key>"` (vs nested oneof)。
//! - D-4 kind 范围: Bearer + ApiKey 双族 (OpenAI Bearer + Anthropic x-api-key)。
//! - D-12 both-present: fail-closed → `Err(ClientCredentialError::Ambiguous)` (不静默 prefer)。
//! - D-6 redaction: SHA-256 前 8 hex prefix + 手写 Debug impl 覆盖默认 derive。

use std::fmt;

use http::{HeaderMap, header::AUTHORIZATION};
use sha2::{Digest, Sha256};
use thiserror::Error;

/// 客户端凭据 kind — 与 proto canonical prefix 同源 (D-2)。
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ClientCredentialKind {
    /// `Authorization: Bearer <token>` (OpenAI 系列 + Anthropic Bearer 兼容)。
    Bearer,
    /// `x-api-key: <key>` (Anthropic 推荐 + 其他 vendor)。
    XApiKey,
}

impl ClientCredentialKind {
    /// proto canonical prefix — Go control plane 用 `^bearer:|^x-api-key:` 区分 kind。
    pub fn as_proto_prefix(self) -> &'static str {
        match self {
            Self::Bearer => "bearer",
            Self::XApiKey => "x-api-key",
        }
    }
}

/// 已解析的客户端凭据 — kind + raw secret (永不入 log/Debug)。
///
/// **关键不变量** (A4 acceptance gate, mutation-tested in this module + observability_test):
/// - `Debug` impl 手写覆盖, 渲染为 `kind=<bearer|x-api-key> secret=[CLIENT_CREDENTIAL_REDACTED]`,
///   永远不包含 raw secret 字节。
/// - `fingerprint()` 返回 SHA-256 前 4 字节 (8 hex chars) prefix 形式, 用于审计相关性 +
///   防 length oracle。
///
/// Mutation: 把 `impl Debug for ClientCredential` 改回 `#[derive(Debug)]` →
/// `debug_impl_never_leaks_raw_credential` 测试红 → A4 守门生效。
#[derive(Clone)]
pub struct ClientCredential {
    kind: ClientCredentialKind,
    /// raw secret — 仅可见于本模块 + `as_route_proto_value()` 透传 control plane。
    /// 不暴露 getter 防 callers 误 log。
    secret: String,
}

impl ClientCredential {
    /// 从 HTTP headers 解析。返回:
    /// - `Ok(None)` — 缺凭据 (caller 按 `require_credential` 决定 401 或 anonymous, A1)。
    /// - `Ok(Some(_))` — 解析成功。
    /// - `Err(Ambiguous)` — 两 header 同时存在 (D-12 fail-closed)。
    /// - `Err(EmptyToken)` — header 在但值空 trimmed = ""。
    /// - `Err(InvalidEncoding)` — header value 非 ASCII (HTTP spec 违例)。
    /// - `Err(MalformedAuthorization)` — Authorization 不以 `Bearer ` (大小写不敏感) 开头。
    pub fn from_headers(headers: &HeaderMap) -> Result<Option<Self>, ClientCredentialError> {
        // Codex round 2 HIGH finding fix 2026-05-24: 重复同名 Authorization 或 x-api-key
        // 头是 audit/tenant 歧义同类问题 — `headers.get(...)` 默认只返回第一行, 静默丢弃
        // 其它 → 攻击者可拼两个 header 让审计与上游看到的凭据不一致。HTTP/1.1 允许
        // 同名头 (RFC 7230 §3.2.2 list-based fields, comma 合并); 但 Authorization /
        // x-api-key 都是单值凭据头, 重复一律 fail-closed。
        //
        // mutation: 删本两段 → from_headers_duplicate_authorization_header_fails_closed
        // / from_headers_duplicate_x_api_key_header_fails_closed 红 (HeaderMap::append 多
        // 行后被静默 collapse 成第一行)。
        let auth_count = headers.get_all(AUTHORIZATION).iter().count();
        let xapi_count = headers.get_all("x-api-key").iter().count();
        if auth_count > 1 || xapi_count > 1 {
            return Err(ClientCredentialError::Ambiguous);
        }

        let auth = headers.get(AUTHORIZATION);
        let xapi = headers.get("x-api-key");

        match (auth, xapi) {
            (None, None) => Ok(None),
            (Some(_), Some(_)) => Err(ClientCredentialError::Ambiguous),
            (Some(value), None) => {
                let s = value
                    .to_str()
                    .map_err(|_| ClientCredentialError::InvalidEncoding)?;
                let token = strip_bearer_prefix(s)
                    .ok_or(ClientCredentialError::MalformedAuthorization)?;
                let token = token.trim();
                if token.is_empty() {
                    return Err(ClientCredentialError::EmptyToken);
                }
                Ok(Some(Self {
                    kind: ClientCredentialKind::Bearer,
                    secret: token.to_owned(),
                }))
            }
            (None, Some(value)) => {
                let s = value
                    .to_str()
                    .map_err(|_| ClientCredentialError::InvalidEncoding)?;
                let trimmed = s.trim();
                if trimmed.is_empty() {
                    return Err(ClientCredentialError::EmptyToken);
                }
                Ok(Some(Self {
                    kind: ClientCredentialKind::XApiKey,
                    secret: trimmed.to_owned(),
                }))
            }
        }
    }

    /// 凭据 kind。
    pub fn kind(&self) -> ClientCredentialKind {
        self.kind
    }

    /// proto canonical value — 写入 `RouteQueryRequest.client_credential`。
    /// 形如 `"bearer:<token>"` / `"x-api-key:<key>"` (D-2)。
    ///
    /// **safety**: 此 value 含 raw secret, 仅可走 gRPC body 透传 control plane (经 UDS/mTLS
    /// 加密通道), 不可写入 log / metric label / cache key。
    pub fn as_route_proto_value(&self) -> String {
        format!("{}:{}", self.kind.as_proto_prefix(), self.secret)
    }

    /// SHA-256 前 4 字节 (8 hex chars) prefix — 用于审计相关性 + 防 raw leak (D-6)。
    /// 相同 credential 多次请求生成相同 fingerprint, 便于运维关联同源流量。
    pub fn fingerprint(&self) -> ClientCredentialFingerprint {
        let canonical = self.as_route_proto_value();
        let mut hasher = Sha256::new();
        hasher.update(canonical.as_bytes());
        let digest = hasher.finalize();
        // 前 4 字节 = 8 hex chars (32 bit prefix), 防 length oracle + 足够碰撞抗性供审计。
        let hex_prefix: String = digest.iter().take(4).fold(String::with_capacity(8), |mut acc, b| {
            use std::fmt::Write;
            let _ = write!(acc, "{:02x}", b);
            acc
        });
        ClientCredentialFingerprint {
            kind: self.kind,
            sha256_first_8_hex: hex_prefix,
        }
    }

    /// 全量 SHA-256 hex (64 chars) — 仅供 Manual First resolver 内部哈希匹配, **不入 log**。
    pub(super) fn full_sha256_hex(&self) -> String {
        let canonical = self.as_route_proto_value();
        let mut hasher = Sha256::new();
        hasher.update(canonical.as_bytes());
        hasher.finalize().iter().fold(String::with_capacity(64), |mut acc, b| {
            use std::fmt::Write;
            let _ = write!(acc, "{:02x}", b);
            acc
        })
    }
}

/// A4 acceptance gate 关键守门: raw credential 永不入 Debug 渲染。
/// mutation: 改回 `#[derive(Debug)]` → `debug_impl_never_leaks_raw_credential` 红。
impl fmt::Debug for ClientCredential {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "ClientCredential {{ kind: {:?}, secret: [CLIENT_CREDENTIAL_REDACTED], fingerprint: {} }}",
            self.kind,
            self.fingerprint()
        )
    }
}

/// 凭据 fingerprint — 仅 kind label + SHA-256 first 8 hex chars。
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ClientCredentialFingerprint {
    pub kind: ClientCredentialKind,
    /// 4 bytes = 8 hex chars。
    pub sha256_first_8_hex: String,
}

impl fmt::Display for ClientCredentialFingerprint {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "kind={} sha256={}",
            self.kind.as_proto_prefix(),
            self.sha256_first_8_hex
        )
    }
}

/// 凭据解析错误 — 全部由 listener 转 401 JSON envelope (synthesis D-9)。
#[derive(Debug, Error, Eq, PartialEq)]
pub enum ClientCredentialError {
    /// D-12: Authorization + x-api-key 同时存在 → fail-closed 防 audit 路径歧义。
    #[error("ambiguous client credential: both Authorization and x-api-key headers present")]
    Ambiguous,
    /// header value 不可转 UTF-8 (HTTP spec 违例 / 攻击载荷)。
    #[error("client credential header has invalid (non-ASCII) encoding")]
    InvalidEncoding,
    /// header 在但值 trimmed 后为空。
    #[error("client credential header present but empty after trim")]
    EmptyToken,
    /// Authorization 不以 `Bearer ` (case-insensitive) 开头。
    #[error("Authorization header malformed: expected 'Bearer <token>'")]
    MalformedAuthorization,
}

impl ClientCredentialError {
    /// 401 error code 对应字段值 (synthesis D-9 JSON envelope `error.type`)。
    pub fn error_code(&self) -> &'static str {
        match self {
            Self::Ambiguous => "ambiguous_client_credential",
            Self::InvalidEncoding => "invalid_credential_encoding",
            Self::EmptyToken => "empty_client_credential",
            Self::MalformedAuthorization => "malformed_authorization_header",
        }
    }
}

/// Bearer prefix 大小写不敏感 strip。
fn strip_bearer_prefix(value: &str) -> Option<&str> {
    if value.len() < 7 {
        return None;
    }
    let (head, tail) = value.split_at(6);
    if head.eq_ignore_ascii_case("Bearer") && tail.starts_with(' ') {
        Some(&tail[1..])
    } else {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use http::HeaderValue;

    /// 测试用显式 fake placeholder secret (防 block-secrets.sh hook + 防 fixture 误入 prod)。
    const FAKE_BEARER: &str = "FAKE-d1b-bearer-fixture-do-not-log";
    const FAKE_APIKEY: &str = "FAKE-d1b-apikey-fixture-do-not-log";

    fn headers_with(authorization: Option<&str>, xapi: Option<&str>) -> HeaderMap {
        let mut h = HeaderMap::new();
        if let Some(v) = authorization {
            h.insert(AUTHORIZATION, HeaderValue::from_str(v).unwrap());
        }
        if let Some(v) = xapi {
            h.insert("x-api-key", HeaderValue::from_str(v).unwrap());
        }
        h
    }

    #[test]
    fn from_headers_missing_returns_none_for_caller_to_decide_401() {
        let h = headers_with(None, None);
        let r = ClientCredential::from_headers(&h).unwrap();
        assert!(r.is_none(), "缺凭据 caller 自行决定 require_credential 路径");
    }

    #[test]
    fn from_headers_extracts_bearer_token_kind() {
        let auth_value = format!("Bearer {FAKE_BEARER}");
        let h = headers_with(Some(&auth_value), None);
        let cred = ClientCredential::from_headers(&h)
            .expect("解析应成功")
            .expect("应有凭据");
        assert_eq!(cred.kind(), ClientCredentialKind::Bearer);
        assert_eq!(
            cred.as_route_proto_value(),
            format!("bearer:{FAKE_BEARER}")
        );
    }

    #[test]
    fn from_headers_extracts_x_api_key_kind() {
        let h = headers_with(None, Some(FAKE_APIKEY));
        let cred = ClientCredential::from_headers(&h)
            .expect("解析应成功")
            .expect("应有凭据");
        assert_eq!(cred.kind(), ClientCredentialKind::XApiKey);
        assert_eq!(
            cred.as_route_proto_value(),
            format!("x-api-key:{FAKE_APIKEY}")
        );
    }

    /// Codex round 2 HIGH finding fix 2026-05-24: 重复 Authorization 头必 fail-closed。
    /// HTTP/1.1 RFC 7230 § 3.2.2 允许同名头多行 (comma list); `HeaderMap::get` 默认只返
    /// 第一行, 静默丢弃其他 → 攻击者拼两个 Authorization 让审计与上游看到不一致凭据。
    ///
    /// mutation: 删 `auth_count > 1` 守门 → from_headers 仅看第一行 → 此测试红
    /// (返回 Ok(Some(_)) 而非 Err(Ambiguous))。
    #[test]
    fn from_headers_duplicate_authorization_header_fails_closed() {
        let mut h = HeaderMap::new();
        h.append(
            AUTHORIZATION,
            HeaderValue::from_str(&format!("Bearer {FAKE_BEARER}")).unwrap(),
        );
        h.append(
            AUTHORIZATION,
            HeaderValue::from_str("Bearer DIFFERENT-FAKE-token-second-row").unwrap(),
        );
        let err = ClientCredential::from_headers(&h)
            .expect_err("重复 Authorization 头必 fail-closed");
        assert_eq!(err, ClientCredentialError::Ambiguous);
    }

    /// Codex round 2 HIGH finding fix 2026-05-24: 重复 x-api-key 头同样 fail-closed。
    /// mutation: 删 `xapi_count > 1` 守门 → 此测试红 (静默接受第一行)。
    #[test]
    fn from_headers_duplicate_x_api_key_header_fails_closed() {
        let mut h = HeaderMap::new();
        h.append("x-api-key", HeaderValue::from_str(FAKE_APIKEY).unwrap());
        h.append(
            "x-api-key",
            HeaderValue::from_str("DIFFERENT-FAKE-apikey-second-row").unwrap(),
        );
        let err = ClientCredential::from_headers(&h)
            .expect_err("重复 x-api-key 头必 fail-closed");
        assert_eq!(err, ClientCredentialError::Ambiguous);
    }

    /// D-12 (mutation): 删 Ambiguous arm → 此测试红, listener 改静默 prefer Bearer →
    /// audit 路径歧义。
    #[test]
    fn from_headers_both_authorization_and_x_api_key_fails_closed_ambiguous() {
        let auth_value = format!("Bearer {FAKE_BEARER}");
        let h = headers_with(Some(&auth_value), Some(FAKE_APIKEY));
        let err = ClientCredential::from_headers(&h).expect_err("both-present 必须 fail-closed");
        assert_eq!(err, ClientCredentialError::Ambiguous);
        assert_eq!(err.error_code(), "ambiguous_client_credential");
    }

    #[test]
    fn from_headers_bearer_without_prefix_is_malformed() {
        let h = headers_with(Some(FAKE_BEARER), None);
        let err = ClientCredential::from_headers(&h).expect_err("缺 Bearer 前缀必拒");
        assert_eq!(err, ClientCredentialError::MalformedAuthorization);
    }

    #[test]
    fn from_headers_bearer_case_insensitive_prefix_accepted() {
        let lower = format!("bearer {FAKE_BEARER}");
        let h = headers_with(Some(&lower), None);
        let cred = ClientCredential::from_headers(&h)
            .expect("lowercase bearer 应可接受")
            .expect("有凭据");
        assert_eq!(cred.kind(), ClientCredentialKind::Bearer);
    }

    #[test]
    fn from_headers_empty_token_after_bearer_prefix_rejected() {
        let h = headers_with(Some("Bearer    "), None);
        let err = ClientCredential::from_headers(&h).expect_err("空 token 必拒");
        assert_eq!(err, ClientCredentialError::EmptyToken);
    }

    #[test]
    fn from_headers_empty_x_api_key_rejected() {
        let h = headers_with(None, Some("   "));
        let err = ClientCredential::from_headers(&h).expect_err("空 x-api-key 必拒");
        assert_eq!(err, ClientCredentialError::EmptyToken);
    }

    /// A4 mutation: 删手写 Debug impl 改 derive(Debug) → 此测试红 → A4 守门生效。
    /// fixture secret 含 distinctive 字符串, raw 不能在 Debug 输出中现身。
    #[test]
    fn debug_impl_never_leaks_raw_credential() {
        let auth_value = format!("Bearer {FAKE_BEARER}");
        let h = headers_with(Some(&auth_value), None);
        let cred = ClientCredential::from_headers(&h).unwrap().unwrap();
        let debug = format!("{:?}", cred);
        // raw 必须不出现
        assert!(
            !debug.contains(FAKE_BEARER),
            "A4 acceptance gate: Debug 渲染必须不含 raw secret 字节 ({FAKE_BEARER}); \
             实际 debug = {debug:?}; mutation: 改 derive(Debug) → 此红"
        );
        // 必须含 redaction 占位符 + fingerprint
        assert!(
            debug.contains("[CLIENT_CREDENTIAL_REDACTED]"),
            "Debug 必须含 [CLIENT_CREDENTIAL_REDACTED] 占位符; 实际 = {debug:?}"
        );
        assert!(
            debug.contains("sha256="),
            "Debug 必须含 fingerprint sha256= 段; 实际 = {debug:?}"
        );
    }

    /// fingerprint 应稳定: 同 credential 多次调用相同结果 (审计相关性)。
    #[test]
    fn fingerprint_is_deterministic_for_same_credential() {
        let auth_value = format!("Bearer {FAKE_BEARER}");
        let h = headers_with(Some(&auth_value), None);
        let cred = ClientCredential::from_headers(&h).unwrap().unwrap();
        let fp1 = cred.fingerprint();
        let fp2 = cred.fingerprint();
        assert_eq!(fp1, fp2, "fingerprint 必须 deterministic");
        assert_eq!(fp1.sha256_first_8_hex.len(), 8, "8 hex chars = 4 bytes prefix");
    }

    /// fingerprint 应区分 Bearer 与 x-api-key 即便 secret 相同 (kind 进 canonical value)。
    #[test]
    fn fingerprint_distinguishes_bearer_vs_x_api_key_same_secret() {
        let auth_value = format!("Bearer {FAKE_BEARER}");
        let h_bearer = headers_with(Some(&auth_value), None);
        let h_xapi = headers_with(None, Some(FAKE_BEARER));
        let fp_bearer = ClientCredential::from_headers(&h_bearer)
            .unwrap()
            .unwrap()
            .fingerprint();
        let fp_xapi = ClientCredential::from_headers(&h_xapi)
            .unwrap()
            .unwrap()
            .fingerprint();
        assert_ne!(
            fp_bearer.sha256_first_8_hex, fp_xapi.sha256_first_8_hex,
            "kind 进 canonical value → fingerprint 必须区分 (mutation: 把 canonical 改成只含 secret → 红)"
        );
    }

    /// cookie / proxy-authorization / 其他 header 不被识别为 client credential
    /// (D-4 scope: 仅 Authorization + x-api-key)。
    #[test]
    fn from_headers_ignores_non_credential_headers() {
        let mut h = HeaderMap::new();
        h.insert("cookie", HeaderValue::from_static("session=foo"));
        h.insert(
            "proxy-authorization",
            HeaderValue::from_static("Bearer something"),
        );
        let r = ClientCredential::from_headers(&h).unwrap();
        assert!(
            r.is_none(),
            "D-4: 仅 Authorization + x-api-key 视为客户端凭据, 其他 header 一律忽略"
        );
    }
}
