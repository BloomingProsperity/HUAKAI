use std::fmt;

use crate::{
    redaction::{
        redact_acquisition_token, redact_credential_handle, redact_upstream_auth_material,
    },
    route_proto::v1::{AttemptReportRequest, RoutePlan, UpstreamAuthMaterial},
};

impl fmt::Debug for UpstreamAuthMaterial {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("UpstreamAuthMaterial")
            .field("material_kind", &self.material_kind)
            .field(
                "material",
                &redact_upstream_auth_material(self.material.as_ref()),
            )
            .field("header_name", &self.header_name)
            .field("expires_at_unix_ms", &self.expires_at_unix_ms)
            .finish()
    }
}

impl fmt::Debug for RoutePlan {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("RoutePlan")
            .field("route_plan_id", &self.route_plan_id)
            .field("account_id", &self.account_id)
            .field(
                "acquisition_token",
                &redact_acquisition_token(self.acquisition_token.as_ref()),
            )
            .field("vendor", &self.vendor)
            .field("upstream_model", &self.upstream_model)
            .field("vendor_endpoint", &self.vendor_endpoint)
            .field(
                "credentials_handle",
                &redact_credential_handle(&self.credentials_handle),
            )
            .field("auth_mode", &self.auth_mode)
            .field("route_ttl_ms", &self.route_ttl_ms)
            .field("attempt_deadline_ms", &self.attempt_deadline_ms)
            .field("max_body_bytes", &self.max_body_bytes)
            .field("max_stream_frame_bytes", &self.max_stream_frame_bytes)
            .field("upstream_auth", &self.upstream_auth)
            .finish()
    }
}

impl fmt::Debug for AttemptReportRequest {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("AttemptReportRequest")
            .field("request_id", &self.request_id)
            .field("route_plan_id", &self.route_plan_id)
            .field("attempt_id", &self.attempt_id)
            .field(
                "acquisition_token",
                &redact_acquisition_token(self.acquisition_token.as_ref()),
            )
            .field("status", &self.status)
            .field("http_status", &self.http_status)
            .field("started_at", &self.started_at)
            .field("ended_at", &self.ended_at)
            .field("latency_ms", &self.latency_ms)
            .field("tokens_used", &self.tokens_used)
            .field("cache_metrics", &self.cache_metrics)
            .field("bytes_in", &self.bytes_in)
            .field("bytes_out", &self.bytes_out)
            .field("frames_in", &self.frames_in)
            .field("frames_out", &self.frames_out)
            .field("vendor_request_id", &self.vendor_request_id)
            .field("retryable", &self.retryable)
            .field("error_class", &self.error_class)
            .field("error_message_redacted", &self.error_message_redacted)
            .field("idempotency_key", &self.idempotency_key)
            .finish()
    }
}

#[cfg(test)]
mod tests {
    use bytes::Bytes;

    use crate::route_proto::v1::{CacheMetrics, TokensUsed};

    use super::*;

    fn secret_route_plan() -> RoutePlan {
        RoutePlan {
            route_plan_id: "route-plan-redact-1".to_owned(),
            account_id: "account-redact-1".to_owned(),
            acquisition_token: Bytes::from_static(b"lease-token-mock-1"),
            vendor: "anthropic".to_owned(),
            upstream_model: "claude-mock".to_owned(),
            vendor_endpoint: "https://api.anthropic.com".to_owned(),
            credentials_handle: "credential-handle-mock-1".to_owned(),
            auth_mode: "bearer".to_owned(),
            route_ttl_ms: 1000,
            attempt_deadline_ms: 30_000,
            max_body_bytes: 4 * 1024 * 1024,
            max_stream_frame_bytes: 64 * 1024,
            upstream_auth: Some(UpstreamAuthMaterial {
                material_kind: "bearer_token".to_owned(),
                material: Bytes::from_static(b"upstream-secret-mock-1"),
                header_name: "authorization".to_owned(),
                expires_at_unix_ms: 0,
            }),
        }
    }

    #[test]
    fn route_plan_debug_redacts_secret_fields_but_keeps_routing_fields() {
        let debug = format!("{:?}", secret_route_plan());

        assert!(!debug.contains("lease-token-mock-1"));
        assert!(!debug.contains("upstream-secret-mock-1"));
        assert!(!debug.contains("credential-handle-mock-1"));
        assert!(debug.contains("[ACQUISITION_TOKEN_REDACTED]"));
        assert!(debug.contains("[UPSTREAM_AUTH_MATERIAL_REDACTED]"));
        assert!(debug.contains("[CREDENTIAL_HANDLE_REDACTED]"));
        assert!(debug.contains("route-plan-redact-1"));
        assert!(debug.contains("account-redact-1"));
        assert!(debug.contains("anthropic"));
    }

    #[test]
    fn attempt_report_request_debug_redacts_token_but_keeps_report_fields() {
        let request = AttemptReportRequest {
            request_id: "request-redact-1".to_owned(),
            route_plan_id: "route-plan-redact-1".to_owned(),
            attempt_id: "attempt-redact-1".to_owned(),
            acquisition_token: Bytes::from_static(b"lease-token-mock-1"),
            status: "success".to_owned(),
            http_status: 200,
            started_at: 1,
            ended_at: 2,
            latency_ms: 1,
            tokens_used: Some(TokensUsed {
                input_tokens: 10,
                output_tokens: 20,
                total_tokens: 30,
                source: "test".to_owned(),
            }),
            cache_metrics: Some(CacheMetrics {
                cache_read_tokens: 0,
                cache_write_tokens: 0,
                cache_hit: false,
                source: "test".to_owned(),
            }),
            bytes_in: 12,
            bytes_out: 34,
            frames_in: 1,
            frames_out: 1,
            vendor_request_id: "vendor-redact-1".to_owned(),
            retryable: false,
            error_class: String::new(),
            error_message_redacted: String::new(),
            idempotency_key: "idem-redact-1".to_owned(),
        };

        let debug = format!("{:?}", request);

        assert!(!debug.contains("lease-token-mock-1"));
        assert!(debug.contains("[ACQUISITION_TOKEN_REDACTED]"));
        assert!(debug.contains("request-redact-1"));
        assert!(debug.contains("route-plan-redact-1"));
        assert!(debug.contains("success"));
    }
}
