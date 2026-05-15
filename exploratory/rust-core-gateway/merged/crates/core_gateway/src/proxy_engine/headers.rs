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
    ANTHROPIC_BETA, ANTHROPIC_VERSION, GEMINI_API_CLIENT, OPENAI_ORGANIZATION, OPENAI_PROJECT,
    ProxyError, auth::apply_plan_auth, default_content_type,
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

fn should_forward_request_header(name: &HeaderName) -> bool {
    matches!(
        name.as_str(),
        "accept"
            | "content-length"
            | "content-type"
            | "user-agent"
            | ANTHROPIC_VERSION
            | ANTHROPIC_BETA
            | OPENAI_ORGANIZATION
            | OPENAI_PROJECT
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
    let path_and_query = if base_path.is_empty() || base_path == "/" {
        target_path.to_owned()
    } else if target_path == "/" {
        base_path.to_owned()
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
