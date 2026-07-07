package codexreqctl

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestAddUnsupportedRequestControlLossesRecordsEachUnsupportedControl(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	maxTokens := 12
	temperature := 0.2
	topP := 0.9
	env.RequestControls.MaxTokens = &maxTokens
	env.RequestControls.Temperature = &temperature
	env.RequestControls.TopP = &topP
	env.RequestControls.Stop = []string{"END"}

	AddUnsupportedRequestControlLosses(env, "openai_codex")

	assertLoss(t, env.CapabilityGraph.ProtocolLoss, "codex_max_output_tokens_stripped", "max_output_tokens")
	assertLoss(t, env.CapabilityGraph.ProtocolLoss, "codex_temperature_stripped", "temperature")
	assertLoss(t, env.CapabilityGraph.ProtocolLoss, "codex_top_p_stripped", "top_p")
	assertLoss(t, env.CapabilityGraph.ProtocolLoss, "codex_stop_stripped", "stop")
}

func TestAddUnsupportedRequestControlLossesRecordsStopSequences(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.RequestControls.StopSequences = []string{"DONE"}

	AddUnsupportedRequestControlLosses(env, "openai_codex")

	if len(env.CapabilityGraph.ProtocolLoss) != 1 {
		t.Fatalf("loss count=%d want 1; losses=%+v", len(env.CapabilityGraph.ProtocolLoss), env.CapabilityGraph.ProtocolLoss)
	}
	assertLoss(t, env.CapabilityGraph.ProtocolLoss, "codex_stop_stripped", "stop")
}

func TestAddUnsupportedRequestControlLossesSkipsOtherFamilies(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	maxTokens := 12
	env.RequestControls.MaxTokens = &maxTokens

	AddUnsupportedRequestControlLosses(env, "openai_responses")
	AddUnsupportedRequestControlLosses(nil, "openai_codex")

	if len(env.CapabilityGraph.ProtocolLoss) != 0 {
		t.Fatalf("non-codex family should not record losses: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

func assertLoss(t *testing.T, losses []proto.ProtocolLossEntry, code, field string) {
	t.Helper()
	for _, loss := range losses {
		if loss.Code != code {
			continue
		}
		if loss.Severity != proto.ProtocolLossWarning {
			t.Fatalf("%s severity=%q want %q", code, loss.Severity, proto.ProtocolLossWarning)
		}
		if loss.Direction != string(proto.DirectionCanonicalToUpstream) {
			t.Fatalf("%s direction=%q want %q", code, loss.Direction, proto.DirectionCanonicalToUpstream)
		}
		if loss.Verdict != proto.VerdictLossy {
			t.Fatalf("%s verdict=%q want %q", code, loss.Verdict, proto.VerdictLossy)
		}
		if loss.Field != field {
			t.Fatalf("%s field=%q want %q", code, loss.Field, field)
		}
		if loss.Vendor != "openai_codex" {
			t.Fatalf("%s vendor=%q want openai_codex", code, loss.Vendor)
		}
		return
	}
	t.Fatalf("loss code %q missing; losses=%+v", code, losses)
}
