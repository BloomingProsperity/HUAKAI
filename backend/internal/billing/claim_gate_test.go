package billing

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestPR4ReReserveAbortedClaimSQLResetsRouteAndAcquisition(t *testing.T) {
	sqlText := readBillingClaimsSQL(t)
	for _, want := range []string{
		"pooling_group_id = $4",
		"provider_account_id = NULL",
		"acquisition_token = NULL",
		"tenant_id = $5",
	} {
		if !strings.Contains(sqlText, want) {
			t.Fatalf("ReReserveAbortedClaim SQL missing %q", want)
		}
	}
}

func TestComputeIdempotencyFingerprintIgnoresPoolingGroupID(t *testing.T) {
	req := ReserveRequest{
		TenantID:              101,
		APIKeyID:              202,
		UserID:                303,
		LogicalRequestID:      "same-logical-request",
		EndpointFamily:        "chat",
		NormalizedPayloadHash: "same-payload",
		RequestedModel:        "gpt-4.1-mini",
		PoolingGroupID:        10,
		BillingPolicyVersion:  "1.0",
		RequestClass:          "standard",
		PredictedCost:         decimal.RequireFromString("0.01000000"),
	}
	first := ComputeIdempotencyFingerprint(req)
	req.PoolingGroupID = 20
	second := ComputeIdempotencyFingerprint(req)
	if first != second {
		t.Fatalf("fingerprint changed across pool groups: first=%s second=%s", first, second)
	}
}

func readBillingClaimsSQL(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "sql", "queries", "billing_claims.sql"))
	if err != nil {
		t.Fatalf("read billing_claims.sql: %v", err)
	}
	return string(raw)
}
