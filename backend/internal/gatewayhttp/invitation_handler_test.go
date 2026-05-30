package gatewayhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/community/invitation"
	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestInvitationCreateHandlerUsesSessionAndDefaults(t *testing.T) {
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	stub := &invitationServiceStub{
		out: invitation.GenerateInvitationOutput{
			Code: "ABC12345", InviterUserID: 42, ExpiresAt: now.AddDate(0, 0, 30), MaxUsage: 1,
		},
	}
	handler := auth.SessionMiddleware(invitationValidatorStub{}, nil)(NewInvitationCreateHandler(InvitationDeps{Service: stub}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/invitations", strings.NewReader(`{"client_idempotency_key":"retry-1"}`))
	req.Header.Set("Authorization", "Bearer session-token")
	handler.ServeHTTP(rec, req)
	assertInvitationStatus(t, rec, http.StatusCreated)

	if stub.called != 1 {
		t.Fatalf("service called %d times", stub.called)
	}
	if stub.in.TenantID != 7 || stub.in.InviterUserID != 42 || stub.in.MaxUsage != 1 || stub.in.ExpiresInDays != 30 {
		t.Fatalf("unexpected input: %+v", stub.in)
	}
	if stub.in.ClientIdempotencyKey == nil || *stub.in.ClientIdempotencyKey != "retry-1" {
		t.Fatalf("client_idempotency_key=%v want retry-1", stub.in.ClientIdempotencyKey)
	}
	var body invitationCreateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "ABC12345" || body.InviterUserID != 42 || body.MaxUsage != 1 {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestInvitationCreateHandlerRejectsMissingSession(t *testing.T) {
	stub := &invitationServiceStub{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/invitations", strings.NewReader(`{}`))
	NewInvitationCreateHandler(InvitationDeps{Service: stub}).ServeHTTP(rec, req)
	assertInvitationStatus(t, rec, http.StatusUnauthorized)
	if !strings.Contains(rec.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestInvitationCreateHandlerMapsQuotaExceeded(t *testing.T) {
	stub := &invitationServiceStub{err: invitation.ErrQuotaExceeded}
	req := httptest.NewRequest(http.MethodPost, "/v1/invitations", strings.NewReader(`{"max_usage":2,"expires_in_days":7}`))
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: 7, UserID: 42}))
	rec := httptest.NewRecorder()
	NewInvitationCreateHandler(InvitationDeps{Service: stub}).ServeHTTP(rec, req)
	assertInvitationStatus(t, rec, http.StatusTooManyRequests)
	if !strings.Contains(rec.Body.String(), `"code":"quota_exceeded"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

func TestInvitationCreateHandlerRejectsInvalidBody(t *testing.T) {
	stub := &invitationServiceStub{}
	req := httptest.NewRequest(http.MethodPost, "/v1/invitations", strings.NewReader(`{"max_usage":101}`))
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: 7, UserID: 42}))
	rec := httptest.NewRecorder()
	NewInvitationCreateHandler(InvitationDeps{Service: stub}).ServeHTTP(rec, req)
	assertInvitationStatus(t, rec, http.StatusBadRequest)
	if stub.called != 0 {
		t.Fatalf("service should not be called")
	}
	if !strings.Contains(rec.Body.String(), `"code":"invalid_request"`) {
		t.Fatalf("body=%s", rec.Body.String())
	}
}

type invitationServiceStub struct {
	out    invitation.GenerateInvitationOutput
	err    error
	in     invitation.GenerateInvitationParams
	called int
}

func (s *invitationServiceStub) Generate(_ context.Context, in invitation.GenerateInvitationParams) (invitation.GenerateInvitationOutput, error) {
	s.called++
	s.in = in
	if s.err != nil {
		return invitation.GenerateInvitationOutput{}, s.err
	}
	return s.out, nil
}

type invitationValidatorStub struct{}

func (invitationValidatorStub) Validate(context.Context, string, string, string) (usersession.ValidatedSession, error) {
	return usersession.ValidatedSession{TenantID: 7, UserID: 42, FamilyID: "fam", TokenID: "tok", Generation: 1}, nil
}

func assertInvitationStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status=%d want %d body=%s", rec.Code, want, rec.Body.String())
	}
}
