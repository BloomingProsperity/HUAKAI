package credentialworker

import (
	"context"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/codexagent"
)

type codexAgentModeAdapter struct {
	broker *codexagent.TaskBroker
}

func newCodexAgentModeAdapter() ModeRefreshAdapter {
	return codexAgentModeAdapter{
		broker: codexagent.NewTaskBroker(auth.NewSSRFProtectedOAuthClient(nil)),
	}
}

func (a codexAgentModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	if a.broker == nil {
		return ModeRefreshResult{}, ErrProviderAdapterMissing
	}
	payload, err := a.broker.RenewTask(ctx, in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	return ModeRefreshResult{Payload: payload, Outcome: "agent_task_renewed"}, nil
}
