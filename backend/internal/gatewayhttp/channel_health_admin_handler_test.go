package gatewayhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
)

type channelHealthControllerStub struct {
	key      channelhealth.ChannelKey
	actorID  string
	reason   string
	called   string
	response channelhealth.Record
	err      error
}

func (s *channelHealthControllerStub) ManualPause(_ context.Context, key channelhealth.ChannelKey, actorID, reason string) (channelhealth.Record, error) {
	s.called, s.key, s.actorID, s.reason = "pause", key, actorID, reason
	if s.err != nil {
		return channelhealth.Record{}, s.err
	}
	if s.response.State == "" {
		s.response = channelhealth.Record{Key: key, State: channelhealth.StateManualPaused, ReasonClass: channelhealth.SignalManualOverride}
	}
	return s.response, nil
}

func (s *channelHealthControllerStub) ManualResume(_ context.Context, key channelhealth.ChannelKey, actorID, reason string) (channelhealth.Record, error) {
	s.called, s.key, s.actorID, s.reason = "resume", key, actorID, reason
	return channelhealth.Record{Key: key, State: channelhealth.StateRamping, RampStagePct: 1, ReasonClass: channelhealth.SignalManualOverride}, s.err
}

func (s *channelHealthControllerStub) ForceActive(_ context.Context, key channelhealth.ChannelKey, actorID, reason string) (channelhealth.Record, error) {
	s.called, s.key, s.actorID, s.reason = "force-active", key, actorID, reason
	return channelhealth.Record{Key: key, State: channelhealth.StateActive, ReasonClass: channelhealth.SignalManualOverride}, s.err
}

func TestChannelHealthAdminPause(t *testing.T) {
	ctrl := &channelHealthControllerStub{}
	rec := invokeChannelHealthAdmin(t, ctrl, adminPoolAdmin(), http.MethodPost,
		"/v1/admin/pool-accounts/101/channel-health/pause",
		`{"tenant_id":7,"vendor":"openai","account_credential_id":9001,"credential_version":1,"reason":"ops pause"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ctrl.called != "pause" || ctrl.key.ProviderAccountID != 101 || ctrl.key.TenantID != 7 ||
		ctrl.key.AccountCredentialID != 9001 || ctrl.reason != "ops pause" || ctrl.actorID != "11" {
		t.Fatalf("controller call mismatch: %+v", ctrl)
	}
	if !strings.Contains(rec.Body.String(), `"state":"manual_paused"`) {
		t.Fatalf("response body=%s", rec.Body.String())
	}
}

func TestChannelHealthAdminRejectsUnauthorizedAndMissingReason(t *testing.T) {
	ctrl := &channelHealthControllerStub{}
	rec := invokeChannelHealthAdmin(t, ctrl, adminPoolAuthStub{err: admin.ErrAdminUnauthorized}, http.MethodPost,
		"/v1/admin/pool-accounts/101/channel-health/force-active",
		`{"tenant_id":7,"vendor":"openai","account_credential_id":9001,"credential_version":1,"reason":"break glass"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d body=%s", rec.Code, rec.Body.String())
	}
	if ctrl.called != "" {
		t.Fatalf("unauthorized touched controller: %+v", ctrl)
	}
	rec = invokeChannelHealthAdmin(t, ctrl, adminPoolAdmin(), http.MethodPost,
		"/v1/admin/pool-accounts/101/channel-health/force-active",
		`{"tenant_id":7,"vendor":"openai","account_credential_id":9001,"credential_version":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing reason status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func invokeChannelHealthAdmin(t *testing.T, ctrl ChannelHealthController, auth ChannelHealthAdminAuth, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/admin/pool-accounts", func(r chi.Router) {
		MountChannelHealthAdminRoutes(r, ChannelHealthAdminDeps{Auth: auth, Controller: ctrl})
	})
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
