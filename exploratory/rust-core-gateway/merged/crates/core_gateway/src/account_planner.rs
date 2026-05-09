// M-rust-5 account planner
// 职责: 将 listener 请求映射为 route query, 缓存短 TTL plan, 并维护 attempt 状态机。

use std::{
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use bytes::Bytes;
use dashmap::DashMap;
use http::{HeaderMap, Uri, header::ACCEPT};
use thiserror::Error;
use tracing::debug;
use uuid::Uuid;

use crate::{
    error::GatewayError,
    request_id::RequestId,
    route_client::RouteClient,
    route_proto::v1::{RoutePlan, RouteQueryRequest},
};

const DEFAULT_CLIENT_DEADLINE_MS: u64 = 30_000;
const TENANT_ID_HEADER: &str = "x-tenant-id";
const REQUESTED_MODEL_HEADER: &str = "x-huakai-model";
const SESSION_HASH_HEADER: &str = "x-huakai-session-hash";
const STREAM_HEADER: &str = "x-huakai-stream";

#[derive(Clone)]
pub struct AccountPlanner {
    inner: Arc<AccountPlannerInner>,
}

struct AccountPlannerInner {
    route_client: RouteClient,
    cache: DashMap<String, CachedPlannedRoute>,
    cache_ttl: Duration,
}

#[derive(Clone)]
struct CachedPlannedRoute {
    plan: RoutePlan,
    expires_at_ms: u64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum GatewayProtocol {
    AnthropicMessages,
    OpenAiChatCompletions,
}

impl GatewayProtocol {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::AnthropicMessages => "anthropic_messages",
            Self::OpenAiChatCompletions => "openai_chat_completions",
        }
    }
}

#[derive(Debug, Error)]
pub enum PlanningError {
    #[error("route query failed: {0}")]
    ControlPlane(#[from] GatewayError),
    #[error("invalid route plan: {0}")]
    InvalidRoutePlan(String),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AuthMode {
    Bearer,
}

impl AuthMode {
    pub fn parse(raw: &str) -> Result<Self, PlanningError> {
        if raw.eq_ignore_ascii_case("bearer") {
            Ok(Self::Bearer)
        } else {
            Err(PlanningError::InvalidRoutePlan(format!(
                "unsupported auth_mode {raw:?}"
            )))
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AttemptState {
    Planned,
    Forwarding,
    Reporting,
    Done,
    Failed,
}

#[derive(Clone, Debug)]
pub struct AttemptLifecycle {
    attempt_id: String,
    state: AttemptState,
}

impl AttemptLifecycle {
    pub fn new(attempt_id: String) -> Self {
        Self {
            attempt_id,
            state: AttemptState::Planned,
        }
    }

    pub fn attempt_id(&self) -> &str {
        &self.attempt_id
    }

    pub fn state(&self) -> AttemptState {
        self.state
    }

    pub fn mark_forwarding(&mut self) -> Result<(), GatewayError> {
        self.transition(AttemptState::Planned, AttemptState::Forwarding)
    }

    pub fn mark_reporting(&mut self) -> Result<(), GatewayError> {
        self.transition(AttemptState::Forwarding, AttemptState::Reporting)
    }

    pub fn mark_done(&mut self) -> Result<(), GatewayError> {
        self.transition(AttemptState::Reporting, AttemptState::Done)
    }

    pub fn mark_failed(&mut self) -> Result<(), GatewayError> {
        match self.state {
            AttemptState::Planned | AttemptState::Forwarding | AttemptState::Reporting => {
                self.state = AttemptState::Failed;
                Ok(())
            }
            AttemptState::Done | AttemptState::Failed => Err(GatewayError::Internal(format!(
                "invalid attempt transition {:?} -> Failed",
                self.state
            ))),
        }
    }

    fn transition(
        &mut self,
        expected: AttemptState,
        next: AttemptState,
    ) -> Result<(), GatewayError> {
        if self.state == expected {
            self.state = next;
            Ok(())
        } else {
            Err(GatewayError::Internal(format!(
                "invalid attempt transition {:?} -> {:?}",
                self.state, next
            )))
        }
    }
}

#[derive(Clone, Debug)]
pub struct PlannedAttempt {
    pub route_plan: RoutePlan,
    pub account_id: String,
    pub acquisition_token: Bytes,
    pub vendor_endpoint: Uri,
    pub auth_mode: AuthMode,
    pub attempt: AttemptLifecycle,
}

impl AccountPlanner {
    pub fn new(route_client: RouteClient, cache_ttl: Duration) -> Self {
        Self {
            inner: Arc::new(AccountPlannerInner {
                route_client,
                cache: DashMap::new(),
                cache_ttl,
            }),
        }
    }

    pub fn route_client(&self) -> &RouteClient {
        &self.inner.route_client
    }

    pub async fn plan(
        &self,
        headers: &HeaderMap,
        protocol: GatewayProtocol,
        request_id: &RequestId,
    ) -> Result<PlannedAttempt, PlanningError> {
        let query = build_route_query(headers, protocol, request_id);
        let plan = match self.cache_get(&query) {
            Some(plan) => plan,
            None => {
                let plan = self.inner.route_client.query_route(query.clone()).await?;
                self.cache_put(&query, &plan);
                plan
            }
        };

        planned_attempt(plan)
    }

    fn cache_get(&self, query: &RouteQueryRequest) -> Option<RoutePlan> {
        let key = route_cache_key(query)?;
        let now = now_unix_ms();
        let entry = self.inner.cache.get(&key)?;

        if entry.expires_at_ms > now {
            debug!(cache_key = %key, "account planner route plan cache hit");
            Some(entry.plan.clone())
        } else {
            drop(entry);
            self.inner.cache.remove(&key);
            None
        }
    }

    fn cache_put(&self, query: &RouteQueryRequest, plan: &RoutePlan) {
        let Some(key) = route_cache_key(query) else {
            return;
        };

        let configured_ms = duration_millis_u64(self.inner.cache_ttl);
        if configured_ms == 0 || plan.route_ttl_ms == 0 {
            return;
        }

        let ttl_ms = configured_ms.min(plan.route_ttl_ms);
        self.inner.cache.insert(
            key,
            CachedPlannedRoute {
                plan: plan.clone(),
                expires_at_ms: now_unix_ms().saturating_add(ttl_ms),
            },
        );
    }
}

pub fn build_route_query(
    headers: &HeaderMap,
    protocol: GatewayProtocol,
    request_id: &RequestId,
) -> RouteQueryRequest {
    RouteQueryRequest {
        request_id: request_id.as_str().to_owned(),
        tenant_id: header_str(headers, TENANT_ID_HEADER)
            .unwrap_or("default-tenant")
            .to_owned(),
        requested_model: header_str(headers, REQUESTED_MODEL_HEADER)
            .unwrap_or("unknown")
            .to_owned(),
        session_hash: header_str(headers, SESSION_HASH_HEADER)
            .unwrap_or(request_id.as_str())
            .to_owned(),
        request_protocol: protocol.as_str().to_owned(),
        stream: wants_stream(headers),
        client_deadline_ms: DEFAULT_CLIENT_DEADLINE_MS,
        previous_attempts: Vec::new(),
        capability_hints: Vec::new(),
    }
}

fn planned_attempt(plan: RoutePlan) -> Result<PlannedAttempt, PlanningError> {
    if plan.account_id.is_empty() {
        return Err(PlanningError::InvalidRoutePlan(
            "missing account_id".to_owned(),
        ));
    }

    if plan.acquisition_token.is_empty() {
        return Err(PlanningError::InvalidRoutePlan(
            "missing acquisition_token".to_owned(),
        ));
    }

    let vendor_endpoint = plan
        .vendor_endpoint
        .parse::<Uri>()
        .map_err(|err| PlanningError::InvalidRoutePlan(format!("bad vendor_endpoint: {err}")))?;

    if vendor_endpoint.scheme().is_none() || vendor_endpoint.authority().is_none() {
        return Err(PlanningError::InvalidRoutePlan(
            "vendor_endpoint missing scheme or authority".to_owned(),
        ));
    }

    let auth_mode = AuthMode::parse(&plan.auth_mode)?;
    let attempt_id = format!("attempt-{}", Uuid::now_v7());

    Ok(PlannedAttempt {
        account_id: plan.account_id.clone(),
        acquisition_token: plan.acquisition_token.clone(),
        vendor_endpoint,
        auth_mode,
        route_plan: plan,
        attempt: AttemptLifecycle::new(attempt_id),
    })
}

fn header_str<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers.get(name).and_then(|value| value.to_str().ok())
}

fn wants_stream(headers: &HeaderMap) -> bool {
    header_str(headers, STREAM_HEADER).is_some_and(|value| {
        value.eq_ignore_ascii_case("true") || value == "1" || value.eq_ignore_ascii_case("yes")
    }) || headers
        .get(ACCEPT)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.contains("text/event-stream"))
}

fn route_cache_key(query: &RouteQueryRequest) -> Option<String> {
    if !query.previous_attempts.is_empty() {
        return None;
    }

    let mut key = String::with_capacity(192);
    push_key_part(&mut key, &query.tenant_id);
    push_key_part(&mut key, &query.requested_model);
    push_key_part(&mut key, &query.session_hash);
    push_key_part(&mut key, &query.request_protocol);
    push_key_part(&mut key, if query.stream { "1" } else { "0" });

    let mut hints: Vec<_> = query.capability_hints.iter().collect();
    hints.sort_by(|a, b| a.name.cmp(&b.name).then(a.value.cmp(&b.value)));
    for hint in hints {
        push_key_part(&mut key, &hint.name);
        push_key_part(&mut key, &hint.value);
    }

    Some(key)
}

fn push_key_part(key: &mut String, part: &str) {
    key.push_str(&part.len().to_string());
    key.push(':');
    key.push_str(part);
    key.push('|');
}

fn duration_millis_u64(duration: Duration) -> u64 {
    duration.as_millis().min(u128::from(u64::MAX)) as u64
}

fn now_unix_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis()
        .min(u128::from(u64::MAX)) as u64
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn attempt_lifecycle_accepts_happy_path() {
        let mut attempt = AttemptLifecycle::new("attempt-test".to_owned());
        assert_eq!(attempt.state(), AttemptState::Planned);

        attempt.mark_forwarding().unwrap();
        attempt.mark_reporting().unwrap();
        attempt.mark_done().unwrap();

        assert_eq!(attempt.state(), AttemptState::Done);
    }

    #[test]
    fn attempt_lifecycle_rejects_out_of_order_transition() {
        let mut attempt = AttemptLifecycle::new("attempt-test".to_owned());
        assert!(attempt.mark_reporting().is_err());
        assert_eq!(attempt.state(), AttemptState::Planned);
    }
}
