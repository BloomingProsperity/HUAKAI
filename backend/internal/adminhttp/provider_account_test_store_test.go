package adminhttp

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/accountprobe"
	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
)

func TestProviderAccountTestAuditPayloadIsSafeAndTruthful(t *testing.T) {
	secretMarker := "raw-upstream-secret-marker"
	payload, err := providerAccountTestAuditPayload(providerAccountTestRecordInput{
		Identity: admin.AdminIdentity{Role: admin.RoleTenantOperator},
		TenantID: 7, AccountID: 99, RequestID: "req-probe",
		Result: accountprobe.Result{
			Attempted: true, Model: "model-a", ProtocolFamily: "openai_chat",
			StatusCode: 401, ErrorClass: "credential_rejected", LatencyMS: 22,
			HealthSignal: channelhealth.SignalAuthChallenge,
		},
		TestError: errors.New("upstream body contains " + secretMarker),
	})
	if err != nil {
		t.Fatalf("providerAccountTestAuditPayload: %v", err)
	}
	text := string(payload)
	for _, required := range []string{
		`"operation":"provider_account_model_probe"`,
		`"controlled_probe":true`,
		`"billed_to_user":false`,
		`"attempted":true`,
		`"result":"unavailable"`,
		`"error_class":"credential_rejected"`,
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("payload=%s 缺少 %s", text, required)
		}
	}
	if strings.Contains(text, secretMarker) || strings.Contains(text, "upstream body") {
		t.Fatalf("日志载荷泄露原始错误: %s", text)
	}
}

func TestProviderAccountProbeLatencyClampsToDatabaseRange(t *testing.T) {
	if got := providerAccountProbeLatency(-1); got != 0 {
		t.Fatalf("负延迟=%d want 0", got)
	}
	if got := providerAccountProbeLatency(math.MaxInt32 + 1); got != math.MaxInt32 {
		t.Fatalf("超界延迟=%d want %d", got, math.MaxInt32)
	}
}
