package accountintake

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/claudecookie"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type cookieSourceExchangerStub struct {
	result claudecookie.Result
}

func (s cookieSourceExchangerStub) Exchange(context.Context, claudecookie.Input) (claudecookie.Result, error) {
	return s.result, nil
}

func TestCookie计划在暂存前拒绝交换模式错配(t *testing.T) {
	tests := []struct {
		name       string
		setupToken bool
		returned   string
	}{
		{"OAuth 请求不得返回 Setup", false, credentialstore.AuthModeClaudeSetupToken},
		{"Setup 请求不得返回 OAuth", true, credentialstore.AuthModeClaudeAIOAuth},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewCookieService(&Service{}, &StagedStore{}, cookieSourceExchangerStub{
				result: claudecookie.Result{
					ImportContent:  `{"access_token":"secret"}`,
					OrganizationID: "org",
					AuthMode:       test.returned,
				},
			})
			_, err := service.Plan(context.Background(), CookiePlanInput{
				TenantID: 7, SessionKey: "session", SetupToken: test.setupToken,
				ActorID: "admin", ActorRole: admin.RolePlatformAdmin,
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err=%v，期望 ErrInvalidInput", err)
			}
		})
	}
}
