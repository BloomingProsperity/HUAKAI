package setuphttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInstallRequiresConfiguredHighEntropyToken(t *testing.T) {
	body := `{"email":"boss@example.test","password":"goodpass1"}`
	tests := []struct {
		name       string
		configured string
		provided   string
		wantStatus int
		wantCode   string
	}{
		{name: "未配置", wantStatus: http.StatusServiceUnavailable, wantCode: "setup_token_not_configured"},
		{name: "配置过短", configured: "too-short", provided: "too-short", wantStatus: http.StatusServiceUnavailable, wantCode: "setup_token_not_configured"},
		{name: "令牌缺失", configured: setupTestUnitToken, wantStatus: http.StatusUnauthorized, wantCode: "invalid_setup_token"},
		{name: "令牌错误", configured: setupTestUnitToken, provided: "wrong-token-0123456789-abcdefgh", wantStatus: http.StatusUnauthorized, wantCode: "invalid_setup_token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/setup/install", strings.NewReader(body))
			req.Header.Set(SetupTokenHeader, tc.provided)
			rec := httptest.NewRecorder()
			Deps{SetupToken: tc.configured}.handleInstall(rec, req)
			if rec.Code != tc.wantStatus || !strings.Contains(rec.Body.String(), tc.wantCode) {
				t.Fatalf("status=%d body=%s want %d/%s", rec.Code, rec.Body.String(), tc.wantStatus, tc.wantCode)
			}
		})
	}
}

const setupTestUnitToken = "setup-unit-token-0123456789-abcdef"

func TestInstallAcceptsCorrectTokenBeforeStorageGate(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/setup/install", strings.NewReader(
		`{"email":"boss@example.test","password":"goodpass1"}`))
	req.Header.Set(SetupTokenHeader, setupTestUnitToken)
	rec := httptest.NewRecorder()
	Deps{SetupToken: setupTestUnitToken}.handleInstall(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "setup_unavailable") {
		t.Fatalf("正确令牌应越过令牌门并在缺存储时失败,status=%d body=%s", rec.Code, rec.Body.String())
	}
}
