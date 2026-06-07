package invitevalidatehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func TestValidateInvitationCodeReturnsSafeValidResponse(t *testing.T) {
	store := &stubInviteStatusStore{status: userauth.InviteCodeStatusValid}
	handler := NewHandler(Deps{
		Store:    store,
		Settings: stubInviteSettings{value: "true"},
	})

	rec := serveValidateInvite(t, handler, `{"tenant_id":7,"invite_code":"hki_ok"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeValidateInviteBody(t, rec)
	if body["valid"] != true || body["reason"] != string(userauth.InviteCodeStatusValid) {
		t.Fatalf("body=%v want valid=true reason=valid", body)
	}
	if len(body) != 2 {
		t.Fatalf("response leaked fields beyond valid/reason: %v", body)
	}
	if store.calls != 1 || store.tenantID != 7 || store.rawCode != "hki_ok" {
		t.Fatalf("store call mismatch: calls=%d tenant=%d code=%q", store.calls, store.tenantID, store.rawCode)
	}
}

func TestValidateInvitationCodeHonorsInvitationRequiredToggle(t *testing.T) {
	store := &stubInviteStatusStore{status: userauth.InviteCodeStatusValid}
	handler := NewHandler(Deps{
		Store:    store,
		Settings: stubInviteSettings{value: "false"},
	})

	rec := serveValidateInvite(t, handler, `{"tenant_id":7,"invite_code":"hki_bypassed"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := decodeValidateInviteBody(t, rec)
	if body["valid"] != true || body["reason"] != string(userauth.InviteCodeStatusDisabled) {
		t.Fatalf("body=%v want valid=true reason=disabled when invitation_required=false", body)
	}
	if store.calls != 0 {
		t.Fatalf("store called %d times; disabled invitation gate must not query invite metadata", store.calls)
	}
}

func TestValidateInvitationCodeMapsInactiveStatusesWithoutMetadata(t *testing.T) {
	cases := []struct {
		name   string
		status userauth.InviteCodeStatus
	}{
		{name: "not_found", status: userauth.InviteCodeStatusNotFound},
		{name: "used_or_exhausted", status: userauth.InviteCodeStatusUsedOrExhausted},
		{name: "expired", status: userauth.InviteCodeStatusExpired},
		{name: "disabled", status: userauth.InviteCodeStatusDisabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewHandler(Deps{
				Store:    &stubInviteStatusStore{status: tc.status},
				Settings: stubInviteSettings{value: "true"},
			})

			rec := serveValidateInvite(t, handler, `{"tenant_id":7,"invite_code":"hki_nope"}`)

			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			body := decodeValidateInviteBody(t, rec)
			if body["valid"] != false || body["reason"] != string(tc.status) {
				t.Fatalf("body=%v want valid=false reason=%s", body, tc.status)
			}
			if len(body) != 2 {
				t.Fatalf("response leaked fields beyond valid/reason: %v", body)
			}
		})
	}
}

func serveValidateInvite(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/validate-invitation-code", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeValidateInviteBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return body
}

type stubInviteStatusStore struct {
	status   userauth.InviteCodeStatus
	calls    int
	tenantID int64
	rawCode  string
	err      error
}

func (s *stubInviteStatusStore) InviteCodeStatus(_ context.Context, tenantID int64, rawCode string) (userauth.InviteCodeStatus, error) {
	s.calls++
	s.tenantID = tenantID
	s.rawCode = rawCode
	return s.status, s.err
}

type stubInviteSettings struct {
	value string
	err   error
}

func (s stubInviteSettings) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	if s.err != nil {
		return platformsettings.StoredSetting{}, s.err
	}
	return platformsettings.StoredSetting{Key: key, Value: s.value}, nil
}
