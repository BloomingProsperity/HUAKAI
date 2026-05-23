use http::{
    HeaderMap, HeaderName, HeaderValue, Uri,
    header::{
        CONNECTION, CONTENT_LENGTH, CONTENT_TYPE, HOST, PROXY_AUTHENTICATE, PROXY_AUTHORIZATION,
        TE, TRAILER, TRANSFER_ENCODING, UPGRADE,
    },
    uri::PathAndQuery,
};

use crate::{
    account_planner::PlannedAttempt,
    request_id::{REQUEST_ID_HEADER, RequestId},
};

use super::{
    ANTHROPIC_BETA, ANTHROPIC_VERSION, GEMINI_API_CLIENT, ProxyError, auth::apply_plan_auth,
    default_content_type,
};

pub(super) fn normalize_upstream_headers(
    source: &HeaderMap,
    target: &mut HeaderMap,
    request_id: &RequestId,
    planned: Option<&PlannedAttempt>,
) -> Result<(), ProxyError> {
    for (name, value) in source {
        if should_forward_request_header(name) {
            target.insert(name, value.clone());
        }
    }

    if !target.contains_key(CONTENT_TYPE) {
        target.insert(CONTENT_TYPE, default_content_type());
    }
    set_request_id(target, request_id);

    if let Some(planned) = planned {
        apply_plan_auth(planned, target)?;
    }

    target.remove(HOST);
    Ok(())
}

// W11-D D-6: 客户端 `openai-organization` / `openai-project` 是账户/账单选择器,
// 不能透传到 vendor 上游 — 否则客户端可以伪造别人 organization/project 让 vendor 计费/路由
// 落到错的账户。HUAKAI 路由计划 (route_plan) 自身决定走哪个 organization/project,
// 由控制面在 plan 阶段注入 (W11+ 后续 contract 扩展)。本层默认 strip。
// mutation: 把 OPENAI_ORGANIZATION / OPENAI_PROJECT 加回 whitelist →
// strip_client_openai_organization_header + strip_client_openai_project_header 测试红。
fn should_forward_request_header(name: &HeaderName) -> bool {
    matches!(
        name.as_str(),
        "accept"
            | "content-length"
            | "content-type"
            | "user-agent"
            | ANTHROPIC_VERSION
            | ANTHROPIC_BETA
            | GEMINI_API_CLIENT
    )
}

pub(super) fn remove_hop_by_hop_response_headers(headers: &mut HeaderMap) {
    // KEEP_ALIVE 在 http crate 中未导出为常量; 使用字面量名称
    static KEEP_ALIVE: HeaderName = HeaderName::from_static("keep-alive");
    headers.remove(CONNECTION);
    headers.remove(&KEEP_ALIVE);
    headers.remove(PROXY_AUTHENTICATE);
    headers.remove(PROXY_AUTHORIZATION);
    headers.remove(TE);
    headers.remove(TRAILER);
    headers.remove(TRANSFER_ENCODING);
    headers.remove(UPGRADE);
    headers.remove(CONTENT_LENGTH);
}

pub(super) fn set_request_id(headers: &mut HeaderMap, request_id: &RequestId) {
    headers.insert(
        REQUEST_ID_HEADER,
        HeaderValue::from_str(request_id.as_str()).expect("request_id 已经过可见 ASCII 校验"),
    );
}

pub(super) fn build_upstream_uri(
    base: &Uri,
    request_path: Option<&PathAndQuery>,
) -> Result<Uri, ProxyError> {
    let scheme = base
        .scheme_str()
        .ok_or_else(|| ProxyError::BadUpstreamUri("upstream uri missing scheme".to_owned()))?;
    let authority = base
        .authority()
        .ok_or_else(|| ProxyError::BadUpstreamUri("upstream uri missing authority".to_owned()))?;
    let target_path = request_path.map(PathAndQuery::as_str).unwrap_or("/");
    let base_path = base.path().trim_end_matches('/');
    // Owner item 2 fix 2026-05-24: 路径前缀重叠检测防 `/v1` + `/v1/messages` -> `/v1/v1/messages`。
    // 当 vendor_endpoint 带版本 path 且客户端 URL 也带同版本 path 时, 旧逻辑直接 concat 双前缀。
    // 检测条件: target_path == base_path 或 target_path 以 "base_path/" 开头, 则单独用 target_path。
    let target_overlaps_base = !base_path.is_empty()
        && base_path != "/"
        && (target_path == base_path
            || (target_path.len() > base_path.len()
                && target_path.starts_with(base_path)
                && target_path.as_bytes().get(base_path.len()) == Some(&b'/')));
    let path_and_query = if base_path.is_empty() || base_path == "/" {
        target_path.to_owned()
    } else if target_path == "/" {
        base_path.to_owned()
    } else if target_overlaps_base {
        target_path.to_owned()
    } else {
        format!("{base_path}{target_path}")
    };

    Uri::builder()
        .scheme(scheme)
        .authority(authority.as_str())
        .path_and_query(path_and_query)
        .build()
        .map_err(|err| ProxyError::BadUpstreamUri(err.to_string()))
}

#[cfg(test)]
mod tests {
    use http::HeaderValue;

    use super::*;
    use crate::request_id::RequestId;

    fn normalize(source: HeaderMap) -> HeaderMap {
        let mut target = HeaderMap::new();
        let request_id = RequestId::generate();
        normalize_upstream_headers(&source, &mut target, &request_id, None)
            .expect("normalize 不应错");
        target
    }

    #[test]
    fn strip_client_openai_organization_header() {
        let mut src = HeaderMap::new();
        src.insert(
            "openai-organization",
            HeaderValue::from_static("org-attacker"),
        );
        let target = normalize(src);
        assert!(
            target.get("openai-organization").is_none(),
            "openai-organization 客户端值必须被剥除 (W11-D D-6 防账单 / 路由旁路)"
        );
    }

    #[test]
    fn strip_client_openai_project_header() {
        let mut src = HeaderMap::new();
        src.insert("openai-project", HeaderValue::from_static("proj-attacker"));
        let target = normalize(src);
        assert!(
            target.get("openai-project").is_none(),
            "openai-project 客户端值必须被剥除"
        );
    }

    #[test]
    fn allow_legitimate_content_type_and_anthropic_version() {
        let mut src = HeaderMap::new();
        src.insert("content-type", HeaderValue::from_static("application/json"));
        src.insert("anthropic-version", HeaderValue::from_static("2023-06-01"));
        let target = normalize(src);
        assert_eq!(
            target.get("content-type").and_then(|v| v.to_str().ok()),
            Some("application/json")
        );
        assert_eq!(
            target.get("anthropic-version").and_then(|v| v.to_str().ok()),
            Some("2023-06-01")
        );
    }

    /// 自证 mutation 同等性: 改 strip 列表前后, 测试期望产生不同结果。
    /// (此元测试本身不在生产路径上, 但记录测试设计意图。)
    #[test]
    fn header_filter_discriminates_org_vs_content_type() {
        let mut src = HeaderMap::new();
        src.insert("openai-organization", HeaderValue::from_static("org-a"));
        src.insert("content-type", HeaderValue::from_static("application/json"));
        let target = normalize(src);
        // strip 列表正确时: content-type 在, openai-organization 不在 → 期望区别。
        assert!(target.contains_key("content-type"));
        assert!(!target.contains_key("openai-organization"));
    }

    // ─── Owner item 2 fix 2026-05-24: build_upstream_uri path-prefix overlap 检测 ───

    fn build_uri(base: &str, target: &str) -> String {
        let base_uri: Uri = base.parse().expect("base uri parse");
        let target_pq: http::uri::PathAndQuery = target.parse().expect("target path parse");
        let result = build_upstream_uri(&base_uri, Some(&target_pq)).expect("build OK");
        result.to_string()
    }

    /// Owner item 2: 旧实现 base=`https://api.anthropic.com/v1` + target=`/v1/messages` ->
    /// 拼出 `/v1/v1/messages` 错 URI -> 上游 404 / 路由错乱。
    /// 新检测: target 以 base_path/ 开头时单独用 target, 不双前缀。
    ///
    /// mutation: 删 target_overlaps_base 分支 -> 退化旧 concat -> 测试断言 /v1/messages 红 (变成 /v1/v1/messages)。
    #[test]
    fn build_upstream_uri_avoids_double_v1_prefix_when_target_overlaps_base() {
        assert_eq!(
            build_uri("https://api.anthropic.com/v1", "/v1/messages"),
            "https://api.anthropic.com/v1/messages",
            "base path /v1 + target /v1/messages 不应拼出 /v1/v1/messages"
        );
    }

    /// Owner item 2: 兼容性—无重叠路径仍正常 concat (base 带 prefix /api, target 不同前缀 /v1)。
    #[test]
    fn build_upstream_uri_concats_when_target_does_not_overlap_base() {
        assert_eq!(
            build_uri("https://api.example.com/api", "/v1/messages"),
            "https://api.example.com/api/v1/messages"
        );
    }

    /// Owner item 2: 严格性—target=base_path 时不重复 (例如 client 探针请求 base path 本身)。
    #[test]
    fn build_upstream_uri_returns_base_path_when_target_exactly_equals() {
        assert_eq!(
            build_uri("https://api.openai.com/v1", "/v1"),
            "https://api.openai.com/v1"
        );
    }

    /// Owner item 2: 边界—target 是 base_path 的 string-prefix 但非 path-prefix (例如 /v1x)
    /// 应当走 concat 不当作 overlap (那是不同 endpoint)。
    /// 反例: 旧贪心 starts_with 会把 /v1x 当 /v1 overlap, 现要求 / 边界 -> 不 overlap -> concat。
    ///
    /// mutation: 检测放宽到 starts_with 而非"以 / 紧跟"-> /v1x 被当 overlap 单独用 -> 结果不带 base path -> 红。
    #[test]
    fn build_upstream_uri_does_not_treat_v1x_as_v1_overlap() {
        // base /v1 + target /v1x -> /v1x 不是 /v1 的 path-sub, 该 concat -> /v1/v1x
        assert_eq!(
            build_uri("https://api.openai.com/v1", "/v1x"),
            "https://api.openai.com/v1/v1x"
        );
    }

    /// Owner item 2: base 无 path 时维持旧行为 (target 单独)。
    #[test]
    fn build_upstream_uri_uses_target_when_base_has_no_path() {
        assert_eq!(
            build_uri("https://api.openai.com", "/v1/messages"),
            "https://api.openai.com/v1/messages"
        );
    }
}
