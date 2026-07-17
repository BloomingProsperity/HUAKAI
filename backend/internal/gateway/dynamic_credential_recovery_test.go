package gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

type recoverySequenceDoer struct {
	mu        sync.Mutex
	calls     int
	auth      []string
	firstBody string
}

func (d *recoverySequenceDoer) Do(req *http.Request) (*http.Response, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	d.auth = append(d.auth, req.Header.Get("Authorization"))
	if d.calls == 1 {
		return &http.Response{StatusCode: http.StatusUnauthorized, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(d.firstBody))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
}

type recoveryStub struct {
	calls int
	err   error
}

func (r *recoveryStub) ShouldRecoverDynamicCredential(_ provider.AccountInfo, status int, body []byte) bool {
	return status == http.StatusUnauthorized && strings.Contains(string(body), "task_")
}

func (r *recoveryStub) RecoverDynamicCredential(context.Context, provider.AccountInfo, provider.Credential) (provider.Credential, bool, error) {
	r.calls++
	if r.err != nil {
		return provider.Credential{}, true, r.err
	}
	return provider.Credential{Type: provider.CredentialTypeUpstreamPassthrough, Value: "assertion-new", RuntimeRef: "new-ref"}, true, nil
}

func TestDispatcherRecoversSpecificTaskFailureOnceOnSameAccount(t *testing.T) {
	doer := &recoverySequenceDoer{firstBody: `{"error":{"code":"task_expired"}}`}
	recoverer := &recoveryStub{}
	dispatcher := newDispatcherForTest(&stubAdapter{platform: "openai"}, doer)
	dispatcher.DynamicCredentialRecoverer = recoverer
	result, err := dispatcher.Dispatch(t.Context(), DispatchInput{
		ProtocolFamily: "openai_codex", UpstreamModelID: "codex-model", InboundBody: []byte(`{"input":"x"}`),
		Account:    provider.AccountInfo{AccountID: 7, TenantID: 8, Platform: "openai", AccountType: "codex_agent_identity", AccountCredentialID: 9, CredentialVersion: 1},
		Credential: provider.Credential{Type: provider.CredentialTypeUpstreamPassthrough, Value: "assertion-old", RuntimeRef: "old-ref"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if result.StatusCode != http.StatusOK || doer.calls != 2 || recoverer.calls != 1 {
		t.Fatalf("status=%d do_calls=%d recover_calls=%d", result.StatusCode, doer.calls, recoverer.calls)
	}
	if len(doer.auth) != 2 || doer.auth[0] != "Bearer assertion-old" || doer.auth[1] != "Bearer assertion-new" {
		t.Fatalf("authorization=%v", doer.auth)
	}
}

func TestDispatcherDoesNotRecoverGenericUnauthorizedAndPreservesBody(t *testing.T) {
	doer := &recoverySequenceDoer{firstBody: `{"error":{"code":"invalid_access_token"}}`}
	recoverer := &recoveryStub{}
	dispatcher := newDispatcherForTest(&stubAdapter{platform: "openai"}, doer)
	dispatcher.DynamicCredentialRecoverer = recoverer
	result, err := dispatcher.Dispatch(t.Context(), DispatchInput{
		ProtocolFamily: "openai_codex", UpstreamModelID: "codex-model",
		Account:    provider.AccountInfo{AccountID: 7, Platform: "openai", AccountType: "codex_agent_identity"},
		Credential: provider.Credential{Type: provider.CredentialTypeUpstreamPassthrough, Value: "assertion-old", RuntimeRef: "old-ref"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	body, err := io.ReadAll(result.UpstreamReader)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != doer.firstBody || doer.calls != 1 || recoverer.calls != 0 {
		t.Fatalf("body=%q do_calls=%d recover_calls=%d", body, doer.calls, recoverer.calls)
	}
}

func TestDispatcherFallsBackToOriginalUnauthorizedWhenRecoveryFails(t *testing.T) {
	doer := &recoverySequenceDoer{firstBody: `{"code":"invalid_task_id"}`}
	recoverer := &recoveryStub{err: errors.New("registration unavailable")}
	dispatcher := newDispatcherForTest(&stubAdapter{platform: "openai"}, doer)
	dispatcher.DynamicCredentialRecoverer = recoverer
	result, err := dispatcher.Dispatch(t.Context(), DispatchInput{
		ProtocolFamily: "openai_codex", UpstreamModelID: "codex-model",
		Account:    provider.AccountInfo{AccountID: 7, Platform: "openai", AccountType: "codex_agent_identity"},
		Credential: provider.Credential{Type: provider.CredentialTypeUpstreamPassthrough, Value: "assertion-old", RuntimeRef: "old-ref"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if result.StatusCode != http.StatusUnauthorized || doer.calls != 1 || recoverer.calls != 1 {
		t.Fatalf("status=%d do_calls=%d recover_calls=%d", result.StatusCode, doer.calls, recoverer.calls)
	}
}
