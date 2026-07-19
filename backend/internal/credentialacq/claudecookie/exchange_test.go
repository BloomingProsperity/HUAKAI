package claudecookie

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExchange完成Cookie换码且不把Cookie放进结果(t *testing.T) {
	var requests int
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if req.URL.String() != organizationsURL {
				t.Fatalf("organizations URL=%s", req.URL)
			}
			cookie, err := req.Cookie("sessionKey")
			if err != nil || cookie.Value != "cookie-secret" {
				t.Fatalf("cookie=%v err=%v", cookie, err)
			}
			return response(200, `[{"uuid":"org-1","name":"Team"}]`), nil
		case 2:
			var body map[string]string
			_ = json.NewDecoder(req.Body).Decode(&body)
			callback := redirectURI + "?code=auth-code&state=" + body["state"]
			return response(200, `{"redirect_uri":"`+callback+`"}`), nil
		case 3:
			return response(200, `{"access_token":"access-secret","refresh_token":"refresh-secret","token_type":"Bearer","scope":"user:inference","expires_in":3600,"account":{"uuid":"account-1","email_address":"owner@example.com"}}`), nil
		default:
			t.Fatalf("多余请求 %d", requests)
			return nil, nil
		}
	})}
	exchanger := New(client)
	exchanger.now = func() time.Time { return time.Date(2026, 7, 19, 1, 2, 3, 0, time.UTC) }
	got, err := exchanger.Exchange(context.Background(), Input{SessionKey: "cookie-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthMode != "claude_ai_oauth" || got.AccountID != "account-1" || got.Email != "owner@example.com" {
		t.Fatalf("result=%+v", got)
	}
	if strings.Contains(got.ImportContent, "cookie-secret") || !strings.Contains(got.ImportContent, "refresh-secret") {
		t.Fatalf("import content=%s", got.ImportContent)
	}
}

func TestExchangeSetupToken输出专用模式(t *testing.T) {
	step := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		step++
		switch step {
		case 1:
			return response(200, `[{"uuid":"org-1"}]`), nil
		case 2:
			var body map[string]string
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body["scope"] != setupScope {
				t.Fatalf("scope=%q", body["scope"])
			}
			return response(200, `{"redirect_uri":"`+redirectURI+`?code=setup-code&state=`+body["state"]+`"}`), nil
		default:
			return response(200, `{"access_token":"setup-secret","account":{"uuid":"account-1"}}`), nil
		}
	})}
	got, err := New(client).Exchange(context.Background(), Input{SessionKey: "cookie-secret", SetupToken: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.AuthMode != "claude_setup_token" || !strings.Contains(got.ImportContent, `"setup_token":"setup-secret"`) || strings.Contains(got.ImportContent, "access_token") {
		t.Fatalf("result=%+v", got)
	}
}

func TestExchange多组织要求显式选择(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(200, `[{"uuid":"org-1","name":"Personal"},{"uuid":"org-2","name":"Team","raven_type":"team"}]`), nil
	})}
	_, err := New(client).Exchange(context.Background(), Input{SessionKey: "cookie-secret"})
	if !errors.Is(err, ErrOrganizationChoiceRequired) {
		t.Fatalf("err=%v", err)
	}
	var choice *OrganizationChoiceError
	if !errors.As(err, &choice) || len(choice.Organizations) != 2 {
		t.Fatalf("choice=%+v err=%v", choice, err)
	}
}

func TestExchange拒绝状态不匹配和重定向(t *testing.T) {
	step := 0
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		step++
		if step == 1 {
			return response(200, `[{"uuid":"org-1"}]`), nil
		}
		return response(200, `{"redirect_uri":"`+redirectURI+`?code=auth-code&state=wrong"}`), nil
	})}
	_, err := New(client).Exchange(context.Background(), Input{SessionKey: "cookie-secret"})
	if !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("err=%v", err)
	}

	locked := lockedClient(&http.Client{})
	if locked.CheckRedirect == nil || !errors.Is(locked.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("Cookie 客户端必须禁用重定向")
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
