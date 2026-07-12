package codexreqctl

import (
	"fmt"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

// AddUnsupportedRequestControlLosses 记录 codex 出站前会被剥离的请求控制项。
func AddUnsupportedRequestControlLosses(env *proto.HCSF, endpointFamily string) {
	if env == nil || endpointFamily != "openai_codex" {
		return
	}
	for _, ctl := range []struct {
		on          bool
		field, code string
	}{
		{env.RequestControls.MaxTokens != nil, "max_output_tokens", "codex_max_output_tokens_stripped"},
		{env.RequestControls.Temperature != nil, "temperature", "codex_temperature_stripped"},
		{env.RequestControls.TopP != nil, "top_p", "codex_top_p_stripped"},
		{len(env.RequestControls.Stop) > 0 || len(env.RequestControls.StopSequences) > 0, "stop", "codex_stop_stripped"},
	} {
		if !ctl.on {
			continue
		}
		loss, _ := proto.NewClientLossEntry(proto.ProtocolLossWarning, fmt.Sprintf("codex 上游不支持 %s 请求控制, 已在出站前剥离", ctl.field), ctl.code, "", "")
		loss.Direction = string(proto.DirectionCanonicalToUpstream)
		loss.Verdict = proto.VerdictLossy
		loss.Field = ctl.field
		loss.Vendor = "openai_codex"
		env.CapabilityGraph.ProtocolLoss = append(env.CapabilityGraph.ProtocolLoss, loss)
	}
}
