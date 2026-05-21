package gatewayhttp

import (
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
)

func TestSignalFromClassification_Suppresses401AuthHealthSignal(t *testing.T) {
	t.Parallel()

	classification, err := gateway.Classify(http.StatusUnauthorized, nil, []byte("invalid_grant"), "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := signalFromClassification(http.StatusUnauthorized, classification); got != "" {
		t.Fatalf("signalFromClassification(401 auth)=%q want empty signal", got)
	}
}

func TestSignalFromClassification_StillEmits403Forbidden(t *testing.T) {
	t.Parallel()

	classification, err := gateway.Classify(http.StatusForbidden, nil, nil, "openai")
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if got := signalFromClassification(http.StatusForbidden, classification); got != channelhealth.SignalForbidden {
		t.Fatalf("signalFromClassification(403)=%q want %q", got, channelhealth.SignalForbidden)
	}
}
