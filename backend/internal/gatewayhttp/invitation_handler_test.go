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

func TestInvitationSummaryHandlerUsesSessionScope(t *testing.T) {
	stub := &invitationServiceStub{summary: invitation.ReferralSummary{
		QualifiedCount:     2,
		RewardedCount:      1,
		RewardsEarnedCents: 73,
	}}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/invitations", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: 7, UserID: 42}))
	rec := httptest.NewRecorder()

	NewInvitationSummaryHandler(InvitationDeps{Service: stub}).ServeHTTP(rec, req)
	assertInvitationStatus(t, rec, http.StatusOK)

	if stub.summaryTenantID != 7 || stub.summaryReferrerUserID != 42 {
		t.Fatalf("summary scope=(tenant=%d, referrer=%d) want (7,42)", stub.summaryTenantID, stub.summaryReferrerUserID)
	}
	var body invitationSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.QualifiedCount != 2 || body.RewardedCount != 1 || body.RewardsEarnedCents != 73 {
		t.Fatalf("summary body=%+v want qualified=2 rewarded=1 rewards_cents=73", body)
	}
}

type invitationServiceStub struct {
	out                   invitation.GenerateInvitationOutput
	summary               invitation.ReferralSummary
	err                   error
	in                    invitation.GenerateInvitationParams
	called                int
	summaryTenantID       int64
	summaryReferrerUserID int64
	selfOut               invitation.GenerateInvitationOutput
	selfErr               error
	selfTenantID          int64
	selfInviterUserID     int64
}

func (s *invitationServiceStub) GetOrCreateSelfReferralCode(_ context.Context, tenantID, inviterUserID int64, _ time.Time) (invitation.GenerateInvitationOutput, error) {
	s.selfTenantID = tenantID
	s.selfInviterUserID = inviterUserID
	if s.selfErr != nil {
		return invitation.GenerateInvitationOutput{}, s.selfErr
	}
	return s.selfOut, nil
}

func (s *invitationServiceStub) Generate(_ context.Context, in invitation.GenerateInvitationParams) (invitation.GenerateInvitationOutput, error) {
	s.called++
	s.in = in
	if s.err != nil {
		return invitation.GenerateInvitationOutput{}, s.err
	}
	return s.out, nil
}

func (s *invitationServiceStub) ReferralSummary(_ context.Context, tenantID, referrerUserID int64) (invitation.ReferralSummary, error) {
	s.summaryTenantID = tenantID
	s.summaryReferrerUserID = referrerUserID
	if s.err != nil {
		return invitation.ReferralSummary{}, s.err
	}
	return s.summary, nil
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

// TestMyReferralCodeHandlerExemptFromQuota 是针对报告者所述 bug 的 handler 级
// 鉴别守卫:即使 campaign 路径已达配额上限,GET /v1/me/invitation-code 也必须
// 返回 200 并带上用户自己的码。桩对象的 campaign Generate 接成了
// ErrQuotaExceeded,而 self 路径返回一个码;handler 必须走 self 路径。变异:
// 把 handler 指向 Generate(受配额限制的路径)→ 桩返回 ErrQuotaExceeded
// → 429 → 变红。
func TestMyReferralCodeHandlerExemptFromQuota(t *testing.T) {
	stub := &invitationServiceStub{
		err:     invitation.ErrQuotaExceeded,
		selfOut: invitation.GenerateInvitationOutput{Code: "SELF1234", InviterUserID: 42},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/invitation-code", nil)
	req = req.WithContext(auth.ContextWithSession(req.Context(), auth.SessionIdentity{TenantID: 7, UserID: 42}))
	rec := httptest.NewRecorder()
	NewMyReferralCodeHandler(InvitationDeps{Service: stub}).ServeHTTP(rec, req)
	assertInvitationStatus(t, rec, http.StatusOK)
	if stub.selfTenantID != 7 || stub.selfInviterUserID != 42 {
		t.Fatalf("self scope=(tenant=%d, inviter=%d) want (7,42)", stub.selfTenantID, stub.selfInviterUserID)
	}
	var body myReferralCodeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "SELF1234" || body.InviterUserID != 42 {
		t.Fatalf("body=%+v want code=SELF1234 inviter=42", body)
	}
}

func TestMyReferralCodeHandlerRejectsMissingSession(t *testing.T) {
	stub := &invitationServiceStub{selfOut: invitation.GenerateInvitationOutput{Code: "X"}}
	req := httptest.NewRequest(http.MethodGet, "/v1/me/invitation-code", nil)
	rec := httptest.NewRecorder()
	NewMyReferralCodeHandler(InvitationDeps{Service: stub}).ServeHTTP(rec, req)
	assertInvitationStatus(t, rec, http.StatusUnauthorized)
}
