package copilot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOAuthBootstrapSlowDownExtendsPollingInterval(t *testing.T) {
	// Regression killed: RFC 8628 slow_down must increase the next poll interval.
	// Mutation self-check: replacing slow_down handling with a fixed sleep changes
	// the second observed delay from 7s to 2s, so this test turns red.
	var tokenPolls int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/login/device/code":
			if r.Method != http.MethodPost {
				t.Fatalf("device-code method=%s, want POST", r.Method)
			}
			assertJSONBody(t, r.Body, map[string]string{
				"client_id": "test-client",
				"scope":     "read:user",
			})
			return copilotJSONResponse(http.StatusOK, `{
				"device_code":"device-123",
				"user_code":"ABCD-EFGH",
				"verification_uri":"https://github.test/login/device",
				"interval":2,
				"expires_in":900
			}`), nil
		case "/login/oauth/access_token":
			tokenPolls++
			assertJSONBody(t, r.Body, map[string]string{
				"client_id":   "test-client",
				"device_code": "device-123",
				"grant_type":  deviceCodeGrantType,
			})
			switch tokenPolls {
			case 1:
				return copilotJSONResponse(http.StatusOK, `{"error":"authorization_pending"}`), nil
			case 2:
				return copilotJSONResponse(http.StatusOK, `{"error":"slow_down"}`), nil
			case 3:
				return copilotJSONResponse(http.StatusOK, `{"access_token":"github-oauth-token","token_type":"bearer"}`), nil
			default:
				t.Fatalf("unexpected token poll %d", tokenPolls)
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		return nil, nil
	})}

	var sleeps []time.Duration
	boot := OAuthBootstrap{
		ClientID:       "test-client",
		DeviceCodeURL:  "https://github.test/login/device/code",
		AccessTokenURL: "https://github.test/login/oauth/access_token",
		HTTPClient:     client,
		MaxPolls:       4,
		Sleep: func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return ctx.Err()
		},
	}

	result, err := boot.Authorize(context.Background())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if result.AccessToken != "github-oauth-token" || result.UserCode != "ABCD-EFGH" || result.VerificationURL != "https://github.test/login/device" {
		t.Fatalf("unexpected bootstrap result: %+v", result)
	}
	wantSleeps := []time.Duration{2 * time.Second, 7 * time.Second}
	if len(sleeps) != len(wantSleeps) {
		t.Fatalf("sleeps=%v, want %v", sleeps, wantSleeps)
	}
	for i, want := range wantSleeps {
		if sleeps[i] != want {
			t.Fatalf("sleep[%d]=%s, want %s; all=%v", i, sleeps[i], want, sleeps)
		}
	}
}

func TestOAuthBootstrapRejectsMissingVerificationURL(t *testing.T) {
	// Regression killed: a device-code response without a verification URL must
	// not enter the polling loop. Mutation self-check: dropping this validation
	// makes the test observe an unexpected access-token request.
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/login/oauth/access_token" {
			t.Fatal("must not poll when device-code response is incomplete")
		}
		return copilotJSONResponse(http.StatusOK, `{"device_code":"device-123","user_code":"ABCD","interval":1}`), nil
	})}

	_, err := (OAuthBootstrap{
		ClientID:       "test-client",
		DeviceCodeURL:  "https://github.test/login/device/code",
		AccessTokenURL: "https://github.test/login/oauth/access_token",
		HTTPClient:     client,
		Sleep:          func(context.Context, time.Duration) error { return nil },
	}).Authorize(context.Background())
	if err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("Authorize err=%v, want verification validation error", err)
	}
}

func assertJSONBody(t *testing.T, body io.Reader, want map[string]string) {
	t.Helper()
	var got map[string]string
	if err := json.NewDecoder(body).Decode(&got); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("json body %s=%q, want %q; all=%v", key, got[key], value, got)
		}
	}
}
