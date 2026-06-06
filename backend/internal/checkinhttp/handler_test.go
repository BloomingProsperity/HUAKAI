package checkinhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/checkin"
)

func TestPostCheckinRequiresSession(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/v1/me", func(r chi.Router) {
		MountRoutes(r, Deps{Service: &fakeCheckinService{}})
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/me/checkin", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rec.Code)
	}
	assertErrorCode(t, rec.Body.String(), "session_token_required")
}

func TestPostCheckinSuccessUsesSessionIdentity(t *testing.T) {
	svc := &fakeCheckinService{result: checkin.Result{
		RewardCents: 11,
		CheckinDate: time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
		NewBalance:  111,
	}}
	r := chi.NewRouter()
	r.Route("/v1/me", func(r chi.Router) {
		MountRoutes(r, Deps{Service: svc})
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/me/checkin", nil)
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 5, UserID: 7}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if svc.gotTenant != 5 || svc.gotUser != 7 {
		t.Fatalf("service got tenant=%d user=%d want 5/7", svc.gotTenant, svc.gotUser)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["checkin_date"] != "2026-06-06" || int64(body["reward_cents"].(float64)) != 11 || int64(body["new_balance"].(float64)) != 111 {
		t.Fatalf("response=%v want date/reward/new_balance", body)
	}
}

func TestPostCheckinAlreadyCheckedInStable4xx(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/v1/me", func(r chi.Router) {
		MountRoutes(r, Deps{Service: &fakeCheckinService{err: checkin.ErrAlreadyCheckedIn}})
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/me/checkin", nil)
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 5, UserID: 7}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s want 409", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec.Body.String(), "daily_checkin_already_claimed")
}

func TestPostCheckinDisabledReturnsNotFound(t *testing.T) {
	r := chi.NewRouter()
	r.Route("/v1/me", func(r chi.Router) {
		MountRoutes(r, Deps{Service: &fakeCheckinService{err: checkin.ErrDisabled}})
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/me/checkin", nil)
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 5, UserID: 7}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s want 404", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec.Body.String(), "daily_checkin_disabled")
}

func TestGetCheckinStatusPassesMonth(t *testing.T) {
	svc := &fakeCheckinService{status: checkin.Status{
		Enabled:        true,
		MinCents:       1,
		MaxCents:       20,
		Month:          "2026-06",
		CheckedInToday: true,
		Records: []checkin.Record{{
			CheckinDate: time.Date(2026, 6, 6, 0, 0, 0, 0, time.UTC),
			RewardCents: 11,
		}},
	}}
	r := chi.NewRouter()
	r.Route("/v1/me", func(r chi.Router) {
		MountRoutes(r, Deps{Service: svc})
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/me/checkin?month=2026-06", nil)
	req = req.WithContext(sessionauth.ContextWithSession(req.Context(), sessionauth.SessionIdentity{TenantID: 5, UserID: 7}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	if svc.gotMonth != "2026-06" {
		t.Fatalf("month passed to service=%q want 2026-06", svc.gotMonth)
	}
	if !strings.Contains(rec.Body.String(), `"checked_in_today":true`) {
		t.Fatalf("status response missing checked_in_today: %s", rec.Body.String())
	}
}

type fakeCheckinService struct {
	result checkin.Result
	status checkin.Status
	err    error

	gotTenant int64
	gotUser   int64
	gotMonth  string
}

func (s *fakeCheckinService) DoCheckin(_ context.Context, tenantID, userID int64) (checkin.Result, error) {
	s.gotTenant = tenantID
	s.gotUser = userID
	return s.result, s.err
}

func (s *fakeCheckinService) GetStatus(_ context.Context, tenantID, userID int64, month string) (checkin.Status, error) {
	s.gotTenant = tenantID
	s.gotUser = userID
	s.gotMonth = month
	if s.err != nil && !errors.Is(s.err, checkin.ErrAlreadyCheckedIn) {
		return checkin.Status{}, s.err
	}
	return s.status, nil
}

func assertErrorCode(t *testing.T, body, want string) {
	t.Helper()
	if !strings.Contains(body, `"code":"`+want+`"`) {
		t.Fatalf("body=%s missing error code %q", body, want)
	}
}
