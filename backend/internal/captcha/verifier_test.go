package captcha

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestTurnstileVerifierParsesSuccessBody(t *testing.T) {
	for _, tc := range []struct {
		name    string
		success bool
		wantErr error
	}{
		{name: "valid", success: true},
		{name: "invalid", success: false, wantErr: ErrVerificationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits int64
			client := roundTripClient(func(r *http.Request) (*http.Response, error) {
				atomic.AddInt64(&hits, 1)
				assertTurnstileForm(t, r, "secret", "token-123", "198.51.100.7")
				body, err := json.Marshal(map[string]bool{"success": tc.success})
				if err != nil {
					t.Fatalf("marshal response: %v", err)
				}
				return jsonResponse(r, http.StatusOK, string(body)), nil
			})

			verifier := NewTurnstileVerifier(TurnstileConfig{
				Settings:      enabledTurnstileSettings(),
				Secret:        "secret",
				Client:        client,
				SiteVerifyURL: "https://captcha.example.test/siteverify",
			})

			err := verifier.Verify(context.Background(), "token-123", "198.51.100.7")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Verify error = %v want %v", err, tc.wantErr)
			}
			if got := atomic.LoadInt64(&hits); got != 1 {
				t.Fatalf("siteverify hits = %d want 1", got)
			}
		})
	}
}

func TestTurnstileVerifierShortCircuitsWhenRuntimeDisabled(t *testing.T) {
	var hits int64
	client := roundTripClient(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt64(&hits, 1)
		return jsonResponse(r, http.StatusOK, `{"success":true}`), nil
	})
	settings := &captchaSettings{values: map[platformsettings.SettingKey]string{
		platformsettings.KeyCaptchaEnabled:  "false",
		platformsettings.KeyCaptchaProvider: "turnstile",
	}}
	verifier := NewTurnstileVerifier(TurnstileConfig{
		Settings:      settings,
		Secret:        "secret",
		Client:        client,
		SiteVerifyURL: "https://captcha.example.test/siteverify",
	})

	err := verifier.Verify(
		context.Background(),
		"token-123",
		"198.51.100.7",
	)
	if err != nil {
		t.Fatalf("Verify with runtime disabled returned %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 0 {
		t.Fatalf("disabled captcha must not call siteverify, hits=%d", got)
	}
}

func TestTurnstileVerifierRequiresTokenWithoutOutboundCall(t *testing.T) {
	var hits int64
	client := roundTripClient(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt64(&hits, 1)
		return jsonResponse(r, http.StatusOK, `{"success":true}`), nil
	})
	verifier := NewTurnstileVerifier(TurnstileConfig{
		Settings:      enabledTurnstileSettings(),
		Secret:        "secret",
		Client:        client,
		SiteVerifyURL: "https://captcha.example.test/siteverify",
	})

	err := verifier.Verify(context.Background(), " ", "198.51.100.7")
	if !errors.Is(err, ErrTokenRequired) {
		t.Fatalf("Verify blank token error = %v want %v", err, ErrTokenRequired)
	}
	if got := atomic.LoadInt64(&hits); got != 0 {
		t.Fatalf("blank token must not call siteverify, hits=%d", got)
	}
}

func TestNewVerifierNoSecretIsNoopEvenWhenRuntimeEnabled(t *testing.T) {
	var hits int64
	client := roundTripClient(func(r *http.Request) (*http.Response, error) {
		atomic.AddInt64(&hits, 1)
		return jsonResponse(r, http.StatusOK, `{"success":true}`), nil
	})
	settings := enabledTurnstileSettings()

	verifier := NewVerifier(settings, "", client)
	if err := verifier.Verify(
		context.Background(),
		"",
		"198.51.100.7",
	); err != nil {
		t.Fatalf("noop verifier returned %v", err)
	}
	if got := atomic.LoadInt64(&hits); got != 0 {
		t.Fatalf("missing secret must be fail-open noop, hits=%d", got)
	}
	if got := atomic.LoadInt64(&settings.calls); got != 0 {
		t.Fatalf("missing secret must not read runtime settings, calls=%d", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func roundTripClient(fn roundTripFunc) *http.Client {
	return &http.Client{Transport: fn}
}

func jsonResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}
}

func assertTurnstileForm(
	t *testing.T,
	r *http.Request,
	secret string,
	token string,
	remoteIP string,
) {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Errorf("method = %s want POST", r.Method)
	}
	if err := r.ParseForm(); err != nil {
		t.Errorf("parse form: %v", err)
		return
	}
	want := url.Values{
		"secret":   []string{secret},
		"response": []string{token},
		"remoteip": []string{remoteIP},
	}
	for key, values := range want {
		if got := r.Form.Get(key); got != values[0] {
			t.Errorf("form %s = %q want %q", key, got, values[0])
		}
	}
}

func enabledTurnstileSettings() *captchaSettings {
	return &captchaSettings{values: map[platformsettings.SettingKey]string{
		platformsettings.KeyCaptchaEnabled:  "true",
		platformsettings.KeyCaptchaProvider: "turnstile",
	}}
}

type captchaSettings struct {
	values map[platformsettings.SettingKey]string
	calls  int64
	err    error
}

func (s *captchaSettings) Get(
	_ context.Context,
	key platformsettings.SettingKey,
) (platformsettings.StoredSetting, error) {
	atomic.AddInt64(&s.calls, 1)
	if s.err != nil {
		return platformsettings.StoredSetting{}, s.err
	}
	if value, ok := s.values[key]; ok {
		return platformsettings.StoredSetting{Key: key, Value: value}, nil
	}
	value, _ := platformsettings.DefaultValue(key)
	return platformsettings.StoredSetting{Key: key, Value: value}, nil
}
