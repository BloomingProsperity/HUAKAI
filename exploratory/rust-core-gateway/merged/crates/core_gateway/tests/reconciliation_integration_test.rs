// W11-A D-1b Phase 2A.4b (2026-05-24): integration tests for dual-write
// reconciliation between Manual First (Phase 1 兜底) and Go-derived (Phase 2)
// tenant_id.
//
// Layer: account_planner.plan() ⇄ mock_control_plane.RouteServiceServer.
// 我们绕过 listener.rs 的 HTTP 表层 — listener 的 401 路径 (TenantIdMismatch arm)
// 已由 tests/listener_test.rs 既有的 auth_error_response 守门 cover (即将由
// 2A.4b 另一批扩到 tenant_id_mismatch e2e 时再补); 本文件专注 planner ↔ control
// plane 的对账契约。
//
// 5 个 scenario 一一对应 reconcile_source_label 的 5 个 source 值:
//   TC-2A4B-1 both_match       — Manual t1 + Go t1            → ok
//   TC-2A4B-2 both_mismatch FC — Manual t1 + Go t2 / FailClosed → Err TenantIdMismatch
//   TC-2A4B-3 both_mismatch LO — Manual t1 + Go t2 / LogOnly  → ok (信 Go)
//   TC-2A4B-4 manual_only      — Manual t1 + Go ""             → ok (Phase 2A 桥接)
//   TC-2A4B-5 go_only + none   — Manual None + Go t1 OR 二者空 → ok
//
// 每个 test 用独立 source label 维度避免 counter race (FC vs LO 用不同 kind,
// 同 account_planner mod tests 模式).
//
// MUTATION 守门: 若 reconcile_identity 不在 plan() 调用链上 (如被注释掉),
// TC-2A4B-2 不会 Err → 红.

use std::time::Duration;

use http::{HeaderMap, HeaderValue};

use core_gateway::{
    account_planner::{AccountPlanner, BodyRouteSignal, GatewayProtocol, PlanningError},
    client_auth::{ClientCredential, ClientCredentialKind, RouteIdentity},
    config::ReconcilePolicy,
    metrics,
    mock_control_plane::{MockControlPlane, mock_route_plan_with_derived_tenant},
    request_id::RequestId,
    route_client::{RouteClient, RouteClientOptions},
};

fn route_client(endpoint: &str) -> RouteClient {
    RouteClient::new(
        endpoint
            .parse()
            .expect("mock control plane endpoint should parse"),
        RouteClientOptions {
            rpc_timeout: Duration::from_millis(500),
            retry_attempts: 0,
            retry_backoff: Duration::from_millis(5),
            circuit_breaker_failure_threshold: 2,
            circuit_breaker_cooldown: Duration::from_millis(250),
        },
    )
    .expect("RouteClient should build")
}

fn anonymous_headers() -> HeaderMap {
    HeaderMap::new()
}

fn body_signal_default() -> BodyRouteSignal {
    BodyRouteSignal::default()
}

/// Construct a RouteIdentity carrying a real ClientCredential of the requested
/// kind plus an optional Manual First tenant marker. We construct via
/// from_headers so the credential matches production code paths exactly.
fn identity_with_kind(
    kind: ClientCredentialKind,
    secret_marker: &str,
    manual_tenant: Option<&str>,
) -> RouteIdentity {
    let mut headers = HeaderMap::new();
    match kind {
        ClientCredentialKind::Bearer => {
            headers.insert(
                "authorization",
                HeaderValue::from_str(&format!("Bearer hk_test_2A4B_{}", secret_marker))
                    .expect("HeaderValue should construct"),
            );
        }
        ClientCredentialKind::XApiKey => {
            headers.insert(
                "x-api-key",
                HeaderValue::from_str(&format!("hk_test_2A4B_{}", secret_marker))
                    .expect("HeaderValue should construct"),
            );
        }
    }
    let cred = ClientCredential::from_headers(&headers)
        .expect("from_headers parse should succeed for valid canonical input")
        .expect("from_headers should return Some when header set");
    RouteIdentity {
        client_credential: Some(cred),
        manual_first_tenant_id: manual_tenant.map(str::to_owned),
    }
}

/// anonymous identity — no credential, no Manual First tenant.
fn anonymous_identity() -> RouteIdentity {
    RouteIdentity {
        client_credential: None,
        manual_first_tenant_id: None,
    }
}

/// Snapshot a counter cell before exercising the scenario; tests assert the
/// delta is exactly 1 for the cell the scenario should hit.
fn reconcile_counter(kind: &str, source: &str) -> u64 {
    let _ = metrics::registry();
    metrics::client_credential_tenant_reconcile_total()
        .with_label_values(&[kind, source])
        .get()
}

// =============================================================================
// TC-2A4B-1 both_match (bearer kind) — Manual t1 + Go t1 → Ok + counter
// =============================================================================

// Defect caught: if reconcile_identity is removed from plan(), the counter
// inc never happens; assert_eq goes red.
// Mutation check: comment out `reconcile_identity(&attempt.route_plan, ...)`
// call in plan(); this test goes red because counter delta is 0.
#[tokio::test]
async fn reconciliation_both_match_increments_counter_and_returns_ok() {
    let plan_template = mock_route_plan_with_derived_tenant("http://mock-upstream", "tenant-2a4b1");
    let control_plane = MockControlPlane::spawn(plan_template).await;
    let planner = AccountPlanner::new(
        route_client(&control_plane.endpoint()),
        ReconcilePolicy::FailClosed,
    );
    let identity = identity_with_kind(
        ClientCredentialKind::Bearer,
        "BOTH_MATCH_TOKEN_001",
        Some("tenant-2a4b1"),
    );
    let before = reconcile_counter("bearer", "both_match");

    let request_id = RequestId::from_candidate(Some("req-2a4b1"));
    let result = planner
        .plan(
            &anonymous_headers(),
            GatewayProtocol::AnthropicMessages,
            &request_id,
            &body_signal_default(),
            &identity,
        )
        .await;

    assert!(
        result.is_ok(),
        "both_match should pass through, got {:?}",
        result.err()
    );
    let after = reconcile_counter("bearer", "both_match");
    assert_eq!(
        after,
        before + 1,
        "both_match counter must increment by 1 (mutation: reconcile_identity removed from plan() → 0 delta)"
    );
}

// =============================================================================
// TC-2A4B-2 both_mismatch FailClosed (bearer kind) — Manual t1 + Go t2 →
// Err TenantIdMismatch + counter both_mismatch
// =============================================================================

// Defect caught: if FailClosed path returns Ok instead of Err, money path
// could downgrade silently; assert match arm + counter go red.
#[tokio::test]
async fn reconciliation_both_mismatch_failclosed_returns_err_and_increments_counter() {
    let plan_template = mock_route_plan_with_derived_tenant(
        "http://mock-upstream",
        "tenant-2a4b2-go-derived",
    );
    let control_plane = MockControlPlane::spawn(plan_template).await;
    let planner = AccountPlanner::new(
        route_client(&control_plane.endpoint()),
        ReconcilePolicy::FailClosed,
    );
    // Manual First says different tenant — fail-closed required.
    let identity = identity_with_kind(
        ClientCredentialKind::Bearer,
        "MISMATCH_FC_TOKEN_002",
        Some("tenant-2a4b2-manual-first"),
    );
    let before = reconcile_counter("bearer", "both_mismatch");

    let request_id = RequestId::from_candidate(Some("req-2a4b2"));
    let result = planner
        .plan(
            &anonymous_headers(),
            GatewayProtocol::AnthropicMessages,
            &request_id,
            &body_signal_default(),
            &identity,
        )
        .await;

    match result {
        Err(PlanningError::TenantIdMismatch {
            kind,
            manual_first,
            go_derived,
        }) => {
            assert_eq!(kind, "bearer");
            assert_eq!(manual_first, "tenant-2a4b2-manual-first");
            assert_eq!(go_derived, "tenant-2a4b2-go-derived");
        }
        other => panic!(
            "FailClosed should return TenantIdMismatch, got {:?}",
            other
        ),
    }
    let after = reconcile_counter("bearer", "both_mismatch");
    assert_eq!(after, before + 1, "both_mismatch counter must increment by 1");
}

// =============================================================================
// TC-2A4B-3 both_mismatch LogOnly (x-api-key kind) — Manual t1 + Go t2 →
// Ok (warn + 信 Go) + counter both_mismatch
// =============================================================================

// Defect caught: if LogOnly path also returns Err (or counter doesn't inc),
// the staging rollout flag does not work; test goes red on Err arm or counter.
#[tokio::test]
async fn reconciliation_both_mismatch_logonly_passes_with_counter_increment() {
    let plan_template = mock_route_plan_with_derived_tenant(
        "http://mock-upstream",
        "tenant-2a4b3-go-derived",
    );
    let control_plane = MockControlPlane::spawn(plan_template).await;
    let planner = AccountPlanner::new(
        route_client(&control_plane.endpoint()),
        ReconcilePolicy::LogOnly,
    );
    // x-api-key kind isolates this test's counter cell from TC-2A4B-2
    // (which uses bearer); parallel cargo test execution still gets clean
    // (kind, source) labels per test.
    let identity = identity_with_kind(
        ClientCredentialKind::XApiKey,
        "MISMATCH_LO_TOKEN_003",
        Some("tenant-2a4b3-manual-first"),
    );
    let before = reconcile_counter("x-api-key", "both_mismatch");

    let request_id = RequestId::from_candidate(Some("req-2a4b3"));
    let result = planner
        .plan(
            &anonymous_headers(),
            GatewayProtocol::AnthropicMessages,
            &request_id,
            &body_signal_default(),
            &identity,
        )
        .await;

    assert!(
        result.is_ok(),
        "LogOnly should pass through, got {:?}",
        result.err()
    );
    let after = reconcile_counter("x-api-key", "both_mismatch");
    assert_eq!(
        after,
        before + 1,
        "both_mismatch counter must still increment under LogOnly"
    );
}

// =============================================================================
// TC-2A4B-4 manual_only (bearer kind) — Manual t1 + Go "" → Ok + counter manual_only
// =============================================================================

// Defect caught: if reconcile_source_label collapses "manual_only" into
// "both_match" when go_derived is empty (or treats empty == empty match),
// this test would land in the wrong cell — its (bearer, manual_only)
// counter would NOT increment, going red.
#[tokio::test]
async fn reconciliation_manual_only_increments_counter_and_returns_ok() {
    // Plan emits empty derived_tenant_id — Phase 2A mock baseline.
    let plan_template = mock_route_plan_with_derived_tenant("http://mock-upstream", "");
    let control_plane = MockControlPlane::spawn(plan_template).await;
    let planner = AccountPlanner::new(
        route_client(&control_plane.endpoint()),
        ReconcilePolicy::FailClosed,
    );
    let identity = identity_with_kind(
        ClientCredentialKind::Bearer,
        "MANUAL_ONLY_TOKEN_004",
        Some("tenant-2a4b4-manual-only"),
    );
    let before = reconcile_counter("bearer", "manual_only");

    let request_id = RequestId::from_candidate(Some("req-2a4b4"));
    let result = planner
        .plan(
            &anonymous_headers(),
            GatewayProtocol::AnthropicMessages,
            &request_id,
            &body_signal_default(),
            &identity,
        )
        .await;

    assert!(
        result.is_ok(),
        "manual_only should pass through (Phase 2A 桥接, Go 暂未派), got {:?}",
        result.err()
    );
    let after = reconcile_counter("bearer", "manual_only");
    assert_eq!(after, before + 1, "manual_only counter must increment by 1");
}

// =============================================================================
// TC-2A4B-5 go_only (x-api-key kind) — Manual None + Go t1 → Ok + counter go_only
// =============================================================================

// Defect caught: if go_only path is mis-routed to both_match (treating absent
// Manual First as if it agreed with whatever Go says), the (x-api-key, go_only)
// counter does not increment; assert_eq goes red.
#[tokio::test]
async fn reconciliation_go_only_increments_counter_and_returns_ok() {
    let plan_template =
        mock_route_plan_with_derived_tenant("http://mock-upstream", "tenant-2a4b5-go-authoritative");
    let control_plane = MockControlPlane::spawn(plan_template).await;
    let planner = AccountPlanner::new(
        route_client(&control_plane.endpoint()),
        ReconcilePolicy::FailClosed,
    );
    // No Manual First tenant — Go is sole identity authority.
    let identity = identity_with_kind(
        ClientCredentialKind::XApiKey,
        "GO_ONLY_TOKEN_005",
        None,
    );
    let before = reconcile_counter("x-api-key", "go_only");

    let request_id = RequestId::from_candidate(Some("req-2a4b5"));
    let result = planner
        .plan(
            &anonymous_headers(),
            GatewayProtocol::AnthropicMessages,
            &request_id,
            &body_signal_default(),
            &identity,
        )
        .await;

    assert!(
        result.is_ok(),
        "go_only should pass through (Go authoritative), got {:?}",
        result.err()
    );
    let after = reconcile_counter("x-api-key", "go_only");
    assert_eq!(after, before + 1, "go_only counter must increment by 1");
}

// =============================================================================
// TC-2A4B-6 none (anonymous identity, Go ""): Manual None + Go "" → Ok + counter none
// =============================================================================

// Defect caught: anonymous path (dev/test require_credential=false) must still
// increment a `none/none` cell so SLO看板 can see traffic that bypassed identity
// entirely. If reconcile_source_label drops this case, counter delta = 0.
#[tokio::test]
async fn reconciliation_none_increments_counter_and_returns_ok() {
    let plan_template = mock_route_plan_with_derived_tenant("http://mock-upstream", "");
    let control_plane = MockControlPlane::spawn(plan_template).await;
    let planner = AccountPlanner::new(
        route_client(&control_plane.endpoint()),
        ReconcilePolicy::FailClosed,
    );
    let identity = anonymous_identity();
    let before = reconcile_counter("none", "none");

    let request_id = RequestId::from_candidate(Some("req-2a4b6"));
    let result = planner
        .plan(
            &anonymous_headers(),
            GatewayProtocol::AnthropicMessages,
            &request_id,
            &body_signal_default(),
            &identity,
        )
        .await;

    assert!(
        result.is_ok(),
        "anonymous + empty Go should pass through (dev/test require_credential=false), got {:?}",
        result.err()
    );
    let after = reconcile_counter("none", "none");
    assert_eq!(after, before + 1, "none counter must increment by 1");
}
