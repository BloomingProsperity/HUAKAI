use http::{HeaderMap, HeaderValue, header::AUTHORIZATION};

use crate::account_planner::{AuthMode, PlannedAttempt};

use super::ProxyError;

pub(super) fn apply_plan_auth(
    planned: &PlannedAttempt,
    headers: &mut HeaderMap,
) -> Result<(), ProxyError> {
    if planned.auth_mode != AuthMode::Bearer {
        return Err(ProxyError::BadRoutePlan("unsupported auth mode".to_owned()));
    }
    if !vendor_supports_bearer(&planned.route_plan.vendor) {
        return Err(ProxyError::BadRoutePlan(format!(
            "unsupported bearer vendor {:?}",
            planned.route_plan.vendor
        )));
    }

    let upstream_auth = planned
        .route_plan
        .upstream_auth
        .as_ref()
        .ok_or_else(|| ProxyError::BadRoutePlan("missing upstream_auth".to_owned()))?;
    if upstream_auth.material_kind != "bearer_token" {
        return Err(ProxyError::BadRoutePlan(format!(
            "unsupported upstream_auth.material_kind {:?}",
            upstream_auth.material_kind
        )));
    }

    let token = std::str::from_utf8(upstream_auth.material.as_ref())
        .map_err(|err| ProxyError::BadRoutePlan(format!("bearer token is not utf8: {err}")))?
        .trim();
    if token.is_empty() {
        return Err(ProxyError::BadRoutePlan("bearer token is empty".to_owned()));
    }
    if token.as_bytes() == planned.acquisition_token.as_ref().trim_ascii() {
        return Err(ProxyError::BadRoutePlan(
            "bearer token must differ from acquisition_token".to_owned(),
        ));
    }

    let value = HeaderValue::from_str(&format!("Bearer {token}"))
        .map_err(|err| ProxyError::BadRoutePlan(format!("bad bearer token: {err}")))?;
    headers.insert(AUTHORIZATION, value);
    Ok(())
}

fn vendor_supports_bearer(vendor: &str) -> bool {
    matches!(
        vendor.to_ascii_lowercase().as_str(),
        "anthropic" | "openai" | "codex" | "gemini"
    )
}

#[cfg(test)]
mod tests {
    use bytes::Bytes;
    use http::{HeaderMap, Uri, header::AUTHORIZATION};

    use crate::{
        account_planner::{AttemptLifecycle, AuthMode, PlannedAttempt},
        route_proto::v1::{RoutePlan, UpstreamAuthMaterial},
    };

    use super::*;

    fn planned_attempt_for_auth(material: Bytes, acquisition_token: Bytes) -> PlannedAttempt {
        let vendor_endpoint = "https://api.anthropic.com"
            .parse::<Uri>()
            .expect("测试 vendor endpoint 应合法");
        let route_plan = RoutePlan {
            route_plan_id: "route-plan-auth-test".to_owned(),
            account_id: "account-auth-test".to_owned(),
            acquisition_token: acquisition_token.clone(),
            vendor: "anthropic".to_owned(),
            upstream_model: "claude-test".to_owned(),
            vendor_endpoint: vendor_endpoint.to_string(),
            credentials_handle: "credential-auth-test".to_owned(),
            auth_mode: "bearer".to_owned(),
            route_ttl_ms: 0,
            attempt_deadline_ms: 30_000,
            max_body_bytes: 1024 * 1024,
            max_stream_frame_bytes: 64 * 1024,
            upstream_auth: Some(UpstreamAuthMaterial {
                material_kind: "bearer_token".to_owned(),
                material,
                header_name: String::new(),
                expires_at_unix_ms: 0,
            }),
        };

        PlannedAttempt {
            route_plan,
            account_id: "account-auth-test".to_owned(),
            acquisition_token,
            vendor_endpoint,
            auth_mode: AuthMode::Bearer,
            attempt: AttemptLifecycle::new("attempt-auth-test".to_owned()),
        }
    }

    #[test]
    fn apply_plan_auth_rejects_trimmed_acquisition_token_match() {
        let planned = planned_attempt_for_auth(
            Bytes::from_static(b"lease-token"),
            Bytes::from_static(b" lease-token\n"),
        );
        let mut headers = HeaderMap::new();

        let err = apply_plan_auth(&planned, &mut headers)
            .expect_err("注入边界必须拒绝 trim 后等于 acquisition_token 的 bearer");

        assert!(matches!(err, ProxyError::BadRoutePlan(_)));
        assert!(!headers.contains_key(AUTHORIZATION));
    }

    #[test]
    fn apply_plan_auth_allows_non_utf8_acquisition_token() {
        let planned = planned_attempt_for_auth(
            Bytes::from_static(b"upstream-secret-mock-1"),
            Bytes::from(vec![0xff, 0xfe, 0x00, 0x01, 0x02, 0x03]),
        );
        let mut headers = HeaderMap::new();

        apply_plan_auth(&planned, &mut headers).expect("非 UTF-8 acquisition_token 不应被拒绝");

        assert_eq!(
            headers.get(AUTHORIZATION).unwrap(),
            "Bearer upstream-secret-mock-1"
        );
    }
}
