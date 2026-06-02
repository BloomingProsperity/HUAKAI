package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

func TestS2_011_MountRoutesKeepsRefreshOutsideSessionMiddleware(t *testing.T) {
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	sessionSvc := usersession.NewService(usersession.NewMemoryStore())
	sessionSvc.Now = func() time.Time { return now }
	sessionSvc.SigningKey = []byte("0123456789abcdef0123456789abcdef")
	sessionSvc.SessionTTL = time.Minute
	sessionSvc.RefreshTTL = time.Hour

	issued, err := sessionSvc.Create(context.Background(), usersession.CreateInput{
		TenantID: 9, UserID: 9001, IP: "198.51.100.10", UserAgent: "Chrome/1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := sessionSvc.Validate(context.Background(), issued.SessionToken, "198.51.100.10", "Chrome/1"); !errors.Is(err, usersession.ErrTokenExpired) {
		t.Fatalf("fixture session token = %v, want ErrTokenExpired", err)
	}

	r := chi.NewRouter()
	mountRoutes(r, &deps{
		cfg:          &Config{BillingPolicyVersion: "test", RequestClass: "standard"},
		userSessions: sessionSvc,
	}, zap.NewNop())

	refreshRec := serveGatewayJSON(t, r, http.MethodPost, "/v1/sessions/refresh", map[string]any{
		"refresh_token": issued.RefreshToken,
	}, issued.SessionToken)
	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh status=%d want 200 body=%s", refreshRec.Code, refreshRec.Body.String())
	}

	listRec := serveGatewayJSON(t, r, http.MethodPost, "/v1/sessions/list", map[string]any{}, issued.SessionToken)
	if listRec.Code != http.StatusUnauthorized {
		t.Fatalf("list status=%d want 401 body=%s", listRec.Code, listRec.Body.String())
	}
}

func serveGatewayJSON(t *testing.T, h http.Handler, method, target string, body any, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
