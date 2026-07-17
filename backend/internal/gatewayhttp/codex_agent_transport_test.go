package gatewayhttp

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func TestCodexAgentIdentityUsesCodexTransportWithoutProtocolHint(t *testing.T) {
	account := provider.AccountInfo{
		Platform:    credentialstore.VendorOpenAI,
		AccountType: credentialstore.AuthModeCodexAgentIdentity,
	}
	if got := transportProviderForDispatch(account, "openai_chat"); got != transport.ProviderOpenAICodex {
		t.Fatalf("transport=%q want %q", got, transport.ProviderOpenAICodex)
	}
}
