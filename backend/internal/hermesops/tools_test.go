package hermesops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialworker"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
)

const secretSentinel = "sk-SECRET-LEAK-SENTINEL-9f3a"

// jsonContains reports whether the sentinel appears anywhere in the JSON
// encoding of v. Used to prove a tool result / persisted row never carries an
// injected secret.
func jsonContains(t *testing.T, v any, needle string) bool {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return strings.Contains(string(raw), needle)
}

func req(tenant int64) ToolRequest {
	return ToolRequest{TenantID: tenant, ActorUserID: 42, Role: RoleTenantOperator, Args: map[string]any{}}
}

// --- credential_diagnose ----------------------------------------------------

type fakeCredTestStore struct{}

func (fakeCredTestStore) LoadForProviderAccountTest(context.Context, int64, int64) (credentialstore.CredentialRecord, error) {
	return credentialstore.CredentialRecord{}, nil
}

func TestCredentialDiagnoseShapeAndPrivacy(t *testing.T) {
	// Regression: the tool must surface the dry-run ok flag + error_class and the
	// renew status's diagnostic fields, and must NOT leak the secret a faked
	// renew row carries. Mutation: dropping the error_class field, or projecting
	// the whole renew row (which would carry the sentinel), fails this.
	deps := CredentialDiagnoseDeps{
		DryRun: func(_ context.Context, _ credentialworker.ProviderAccountCredentialTestStore, _ *credentialworker.ModeAdapterRegistry, tenantID, accountID int64, _ time.Time) (credentialworker.ProviderAccountCredentialTestResult, error) {
			return credentialworker.ProviderAccountCredentialTestResult{OK: false, ErrorClass: "invalid_grant", Message: "credential authorization failed; operator re-authentication is required"}, nil
		},
		TestStore: fakeCredTestStore{},
		RenewStatus: func(_ context.Context, _ credentialstore.ListRenewStatusParams) ([]credentialstore.RenewStatusMetadata, error) {
			// AccountName carries the injected secret sentinel — it is a non-
			// diagnostic field the projection MUST drop.
			fc := "invalid_grant"
			return []credentialstore.RenewStatusMetadata{{
				CredentialID: 9, AccountID: 5, AccountName: secretSentinel,
				Vendor: "anthropic", AuthMode: "api_key", State: "failing",
				FailureClass: &fc, FailureCount: 3,
			}}, nil
		},
	}
	spec := CredentialDiagnoseSpec(deps)
	r := req(7)
	r.Args["account_id"] = float64(5)
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ErrorClass != "invalid_grant" {
		t.Fatalf("error_class=%q want invalid_grant", res.ErrorClass)
	}
	if res.Summary["credential_ok"] != false {
		t.Fatalf("credential_ok=%v want false", res.Summary["credential_ok"])
	}
	if jsonContains(t, res.Summary, secretSentinel) {
		t.Fatalf("credential_diagnose result leaked the account-name secret sentinel: %v", res.Summary)
	}
}

func TestCredentialDiagnoseFailsClosedOnNilDep(t *testing.T) {
	// Regression: a nil dry-run/store dependency must yield ErrDependencyUnwired,
	// never a panic.
	spec := CredentialDiagnoseSpec(CredentialDiagnoseDeps{})
	r := req(7)
	r.Args["account_id"] = float64(5)
	if _, err := spec.Run(context.Background(), r); !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("err=%v want ErrDependencyUnwired", err)
	}
}

func TestCredentialDiagnoseRejectsMissingAccountID(t *testing.T) {
	// Regression: a missing/zero account_id must be ErrInvalidArgs (400), not a
	// read against account 0.
	spec := CredentialDiagnoseSpec(CredentialDiagnoseDeps{
		DryRun: func(_ context.Context, _ credentialworker.ProviderAccountCredentialTestStore, _ *credentialworker.ModeAdapterRegistry, _, _ int64, _ time.Time) (credentialworker.ProviderAccountCredentialTestResult, error) {
			return credentialworker.ProviderAccountCredentialTestResult{OK: true}, nil
		},
		TestStore: fakeCredTestStore{},
	})
	if _, err := spec.Run(context.Background(), req(7)); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("err=%v want ErrInvalidArgs", err)
	}
}

// --- account_health_diagnose ------------------------------------------------

func TestAccountHealthDiagnoseShape(t *testing.T) {
	// Regression: the tool must surface the account health row's state + failure
	// class/count and the channel summary's per-state counts. Mutation: dropping
	// failure_class breaks the error_class derivation asserted below.
	fc := "rate_limit_exceeded"
	deps := AccountHealthDeps{
		ProviderAccountHealth: func(_ context.Context, p admindb.GetAdminProviderAccountHealthParams) (admindb.GetAdminProviderAccountHealthRow, error) {
			if p.TenantID != 7 || p.ID != 5 {
				t.Fatalf("scope leaked: params=%+v want tenant=7 id=5", p)
			}
			return admindb.GetAdminProviderAccountHealthRow{ID: 5, TenantID: 7, HealthState: "degraded", Enabled: true, FailureClass: &fc, FailureCount: 2}, nil
		},
		ChannelSummary: func(_ context.Context, tenantID int64) (channelhealth.ChannelHealthSummary, error) {
			return channelhealth.ChannelHealthSummary{Total: 4, ByState: map[channelhealth.HealthState]int64{"active": 3, "cooldown": 1}}, nil
		},
	}
	spec := AccountHealthDiagnoseSpec(deps)
	r := req(7)
	r.Args["account_id"] = float64(5)
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["health_state"] != "degraded" || res.ErrorClass != "rate_limit_exceeded" {
		t.Fatalf("summary=%v error_class=%q want degraded/rate_limit_exceeded", res.Summary, res.ErrorClass)
	}
	cs, ok := res.Summary["channel_summary"].(map[string]any)
	if !ok || cs["total"].(int64) != 4 {
		t.Fatalf("channel_summary=%v want total=4", res.Summary["channel_summary"])
	}
}

func TestAccountHealthDiagnoseFailsClosedOnNilDep(t *testing.T) {
	spec := AccountHealthDiagnoseSpec(AccountHealthDeps{})
	r := req(7)
	r.Args["account_id"] = float64(5)
	if _, err := spec.Run(context.Background(), r); !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("err=%v want ErrDependencyUnwired", err)
	}
}

// --- request_diagnose -------------------------------------------------------

func TestRequestDiagnoseCorrelatesAndDropsCost(t *testing.T) {
	// Regression: the tool must correlate only the matching request_id and must
	// DROP actual_cost (money). Mutation: emitting actual_cost, or not filtering
	// by request_id, fails this.
	deps := ObservabilityDeps{
		ListUsage: func(_ context.Context, p dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			return []dbbilling.ListUsageRecordsRow{
				{ID: 1, ClaimID: 11, RequestID: "req-A", EndClass: "ok", TokensInput: 10},
				{ID: 2, ClaimID: 22, RequestID: "req-B", EndClass: "error"},
			}, nil
		},
		ListClaims: func(_ context.Context, p dbbilling.ListBillingClaimsParams) ([]dbbilling.ListBillingClaimsRow, error) {
			return []dbbilling.ListBillingClaimsRow{
				{ID: 11, LogicalRequestID: "req-A", Status: "settled", EndpointFamily: "chat"},
			}, nil
		},
	}
	spec := RequestDiagnoseSpec(deps)
	r := req(7)
	r.Args["request_id"] = "req-A"
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["usage_count"].(int) != 1 || res.Summary["claim_count"].(int) != 1 {
		t.Fatalf("counts=%v/%v want 1/1 (request_id filter not applied?)", res.Summary["usage_count"], res.Summary["claim_count"])
	}
	if jsonContains(t, res.Summary, "actual_cost") {
		t.Fatalf("request_diagnose leaked the money field actual_cost: %v", res.Summary)
	}
}

func TestRequestDiagnoseFailsClosedOnNilDep(t *testing.T) {
	spec := RequestDiagnoseSpec(ObservabilityDeps{})
	r := req(7)
	r.Args["request_id"] = "x"
	if _, err := spec.Run(context.Background(), r); !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("err=%v want ErrDependencyUnwired", err)
	}
}

// TestRequestDiagnoseProjectsModelRewriteFields guards the requested_model /
// upstream_model projection that lets an operator see model rewrite / fallback
// (requested != upstream) for a request_id. Mutation: drop either key from
// usageDiagnosticShape -> the field is absent from usage_records[0] -> red.
func TestRequestDiagnoseProjectsModelRewriteFields(t *testing.T) {
	upstream := "claude-opus-4-20260514"
	deps := ObservabilityDeps{
		ListUsage: func(_ context.Context, _ dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			return []dbbilling.ListUsageRecordsRow{
				{ID: 1, ClaimID: 11, RequestID: "req-A", RequestedModel: "claude-opus-4", UpstreamModel: &upstream, EndClass: "ok"},
			}, nil
		},
		ListClaims: func(_ context.Context, _ dbbilling.ListBillingClaimsParams) ([]dbbilling.ListBillingClaimsRow, error) {
			return nil, nil
		},
	}
	spec := RequestDiagnoseSpec(deps)
	r := req(7)
	r.Args["request_id"] = "req-A"
	res, err := spec.Run(context.Background(), r)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	records, ok := res.Summary["usage_records"].([]map[string]any)
	if !ok || len(records) != 1 {
		t.Fatalf("usage_records=%v want exactly one shape", res.Summary["usage_records"])
	}
	rec := records[0]
	// Discriminating: requested != upstream is a real rewrite; a dropped projection
	// leaves the key nil, which differs from both seeded values.
	if rec["requested_model"] != "claude-opus-4" {
		t.Fatalf("requested_model=%v want claude-opus-4 (projection dropped?)", rec["requested_model"])
	}
	if rec["upstream_model"] != "claude-opus-4-20260514" {
		t.Fatalf("upstream_model=%v want claude-opus-4-20260514 (projection dropped?)", rec["upstream_model"])
	}
}

// --- audit_lookup -----------------------------------------------------------

func TestAuditLookupDropsPayloadAndReason(t *testing.T) {
	// Regression (PRIVACY): the audit projection must DROP the free-form Payload
	// blob and Reason string, which here carry the secret sentinel. Mutation:
	// surfacing payload/reason re-introduces the leak.
	reason := secretSentinel
	deps := ObservabilityDeps{
		ListAudit: func(_ context.Context, p dbbilling.ListAuditEventsParams) ([]dbbilling.ListAuditEventsRow, error) {
			return []dbbilling.ListAuditEventsRow{{
				ID: 1, TenantID: 7, EventClass: "billing", EventType: "settle", Severity: "info",
				Reason:  &reason,
				Payload: []byte(`{"leak":"` + secretSentinel + `"}`),
			}}, nil
		},
	}
	spec := AuditLookupSpec(deps)
	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["event_count"].(int) != 1 {
		t.Fatalf("event_count=%v want 1", res.Summary["event_count"])
	}
	if jsonContains(t, res.Summary, secretSentinel) {
		t.Fatalf("audit_lookup leaked payload/reason sentinel: %v", res.Summary)
	}
}

// --- log_analyze ------------------------------------------------------------

func TestLogAnalyzeAggregatesEnums(t *testing.T) {
	// Regression: the tool must count end_class enums (here 2 ok + 1 error) and
	// NEVER read a raw body. Mutation: miscounting (e.g. only counting the first
	// row) fails the discriminating expected map.
	deps := ObservabilityDeps{
		ListUsage: func(_ context.Context, p dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error) {
			return []dbbilling.ListUsageRecordsRow{
				{ID: 1, EndClass: "ok", SettlementSource: "live"},
				{ID: 2, EndClass: "ok", SettlementSource: "live"},
				{ID: 3, EndClass: "error", SettlementSource: "reconcile", PendingReconciliation: true},
			}, nil
		},
	}
	spec := LogAnalyzeSpec(deps)
	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["sample_size"].(int) != 3 {
		t.Fatalf("sample_size=%v want 3", res.Summary["sample_size"])
	}
	byEnd := res.Summary["by_end_class"].(map[string]any)
	if byEnd["ok"].(int) != 2 || byEnd["error"].(int) != 1 {
		t.Fatalf("by_end_class=%v want ok=2 error=1", byEnd)
	}
	if res.Summary["pending_reconcile"].(int) != 1 {
		t.Fatalf("pending_reconcile=%v want 1", res.Summary["pending_reconcile"])
	}
}

// --- dlq_inspect ------------------------------------------------------------

func TestDLQInspectDropsPayloadAndAggregates(t *testing.T) {
	// Regression (PRIVACY + shape): dlq_inspect must DROP the raw event Payload
	// (carrying the sentinel) and aggregate by status/kind. Mutation: surfacing
	// the payload re-leaks; miscounting fails the discriminating maps.
	deps := DLQInspectDeps{
		List: func(_ context.Context, f dlq.ListFilter) ([]dlq.Record, error) {
			if f.TenantID == nil || *f.TenantID != 7 {
				t.Fatalf("tenant filter not applied: %+v", f)
			}
			return []dlq.Record{
				{ID: 1, EventKind: "usage_record", Status: "pending", Payload: []byte(`{"body":"` + secretSentinel + `"}`), FailureReason: "timeout", FailureAt: time.Now(), NextRetryAt: time.Now()},
				{ID: 2, EventKind: "usage_record", Status: "failed", Payload: []byte(`{}`), FailureReason: "5xx", FailureAt: time.Now(), NextRetryAt: time.Now()},
			}, nil
		},
	}
	spec := DLQInspectSpec(deps)
	res, err := spec.Run(context.Background(), req(7))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Summary["dlq_count"].(int) != 2 {
		t.Fatalf("dlq_count=%v want 2", res.Summary["dlq_count"])
	}
	byKind := res.Summary["by_kind"].(map[string]any)
	if byKind["usage_record"].(int) != 2 {
		t.Fatalf("by_kind=%v want usage_record=2", byKind)
	}
	if jsonContains(t, res.Summary, secretSentinel) {
		t.Fatalf("dlq_inspect leaked the raw event payload sentinel: %v", res.Summary)
	}
}

func TestDLQInspectFailsClosedOnNilDep(t *testing.T) {
	spec := DLQInspectSpec(DLQInspectDeps{})
	if _, err := spec.Run(context.Background(), req(7)); !errors.Is(err, ErrDependencyUnwired) {
		t.Fatalf("err=%v want ErrDependencyUnwired", err)
	}
}

// ensure pgtype stays referenced for fixtures that need a valid timestamp.
var _ = pgtype.Timestamptz{Valid: true}
