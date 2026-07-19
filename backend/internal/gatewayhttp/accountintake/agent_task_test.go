package accountintake

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestPrepareExecutionCandidateRegistersOnlyAgentMode(t *testing.T) {
	registrar := &agentTaskRegistrarStub{payload: []byte(`{"task_id":"new-task"}`)}
	service := (&Service{}).WithAgentTaskRegistrar(registrar)
	agent, err := service.prepareExecutionCandidate(context.Background(), credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexAgent,
		Payload: []byte(`{"task_id":""}`),
	})
	if err != nil || string(agent.Payload) != `{"task_id":"new-task"}` || registrar.calls != 1 {
		t.Fatalf("candidate=%s calls=%d err=%v", agent.Payload, registrar.calls, err)
	}
	normal, err := service.prepareExecutionCandidate(context.Background(), credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth,
		Payload: []byte(`{"access_token":"token"}`),
	})
	if err != nil || string(normal.Payload) != `{"access_token":"token"}` || registrar.calls != 1 {
		t.Fatalf("normal=%s calls=%d err=%v", normal.Payload, registrar.calls, err)
	}
}

func TestPrepareExecutionCandidateFailsClosedWithoutRegistrar(t *testing.T) {
	_, err := (&Service{}).prepareExecutionCandidate(context.Background(), credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexAgent,
		Payload: []byte(`{}`),
	})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("err=%v", err)
	}
}

type agentTaskRegistrarStub struct {
	payload []byte
	err     error
	calls   int
}

func (s *agentTaskRegistrarStub) EnsureTask(context.Context, []byte) ([]byte, error) {
	s.calls++
	return append([]byte(nil), s.payload...), s.err
}
