package moderation

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

func TestExternalMultiKeyFreeze(t *testing.T) {
	// Mutation: removing per-key freeze after 429 makes the third request reuse
	// key-a inside its freeze window, so the authorization sequence assertion is red.
	now := time.Date(2026, 6, 6, 1, 2, 3, 0, time.UTC)
	var authHeaders []string
	provider := NewExternalModerator(ExternalModeratorDeps{
		HTTPClient: &http.Client{Transport: moderationRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			authHeaders = append(authHeaders, r.Header.Get("Authorization"))
			switch r.Header.Get("Authorization") {
			case "Bearer key-a":
				return moderationHTTPResponse(http.StatusTooManyRequests, `{"error":"rate limited"}`), nil
			case "Bearer key-b":
				return moderationHTTPResponse(http.StatusOK, `{"results":[{"flagged":false,"categories":{},"category_scores":{}}]}`), nil
			default:
				t.Fatalf("unexpected authorization header %q", r.Header.Get("Authorization"))
				return nil, nil
			}
		})},
		Now: func() time.Time { return now },
	})
	cfg := ExternalModerationConfig{
		Enabled:    true,
		BaseURL:    "https://moderation.example.test/v1/moderations",
		APIKeys:    []string{"key-a", "key-b"},
		Model:      "omni-moderation-latest",
		RetryCount: 1,
	}

	first, err := provider.ScreenExternal(context.Background(), ScreenRequest{Body: []byte("first")}, cfg)
	if err != nil {
		t.Fatalf("first ScreenExternal returned error after retry: %v", err)
	}
	if first.Blocked {
		t.Fatalf("first result blocked=%v want false", first.Blocked)
	}
	second, err := provider.ScreenExternal(context.Background(), ScreenRequest{Body: []byte("second")}, cfg)
	if err != nil {
		t.Fatalf("second ScreenExternal returned error: %v", err)
	}
	if second.Blocked {
		t.Fatalf("second result blocked=%v want false", second.Blocked)
	}
	if got, want := strings.Join(authHeaders, ","), "Bearer key-a,Bearer key-b,Bearer key-b"; got != want {
		t.Fatalf("authorization sequence=%s want %s", got, want)
	}
}

func TestExternalImageByteCap(t *testing.T) {
	// Mutation: enforcing caps after the HTTP request, or checking only decoded
	// raw bytes and ignoring data-URL length, increments calls and fails this test.
	calls := 0
	provider := NewExternalModerator(ExternalModeratorDeps{
		HTTPClient: &http.Client{Transport: moderationRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return moderationHTTPResponse(http.StatusOK, `{"results":[{"flagged":false,"categories":{},"category_scores":{}}]}`), nil
		})},
	})
	oversized := "data:image/png;base64," + strings.Repeat("A", ExternalImageDataURLMaxBytes+1)
	res, err := provider.ScreenExternal(context.Background(), ScreenRequest{
		Body:          []byte("image fixture"),
		ImageDataURLs: []string{oversized},
	}, ExternalModerationConfig{
		Enabled:      true,
		BaseURL:      "https://moderation.example.test/v1/moderations",
		APIKeys:      []string{"image-key"},
		ImageEnabled: true,
	})
	if err != nil {
		t.Fatalf("oversized image returned transport error: %v", err)
	}
	if !res.Blocked || res.ReasonCode != "external_image_too_large" {
		t.Fatalf("result=%+v want blocked external_image_too_large", res)
	}
	if calls != 0 {
		t.Fatalf("http calls=%d want 0 before rejecting oversized image", calls)
	}
}

func TestExternalThresholdRequiresReturnedCategoryScore(t *testing.T) {
	// Mutation: reading a missing score as Go's zero value makes threshold 0
	// block a category the provider did not score.
	provider := NewExternalModerator(ExternalModeratorDeps{
		HTTPClient: &http.Client{Transport: moderationRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			body := `{"results":[{"flagged":false,"categories":{},"category_scores":{"violence":0.12}}]}`
			return moderationHTTPResponse(http.StatusOK, body), nil
		})},
	})
	res, err := provider.ScreenExternal(context.Background(), ScreenRequest{Body: []byte("ordinary")}, ExternalModerationConfig{
		Enabled:    true,
		BaseURL:    "https://moderation.example.test/v1/moderations",
		APIKeys:    []string{"threshold-key"},
		Thresholds: map[string]float64{"self-harm": 0},
	})
	if err != nil {
		t.Fatalf("ScreenExternal: %v", err)
	}
	if res.Blocked {
		t.Fatalf("result=%+v want pass when threshold category has no returned score", res)
	}
}

func TestExternalSSRF(t *testing.T) {
	// Mutation: replacing the default SSRF-protected client with http.DefaultClient
	// attempts to dial loopback instead of returning ErrOAuthEndpointBlocked.
	provider := NewExternalModerator(ExternalModeratorDeps{})
	_, err := provider.ScreenExternal(context.Background(), ScreenRequest{
		Body: []byte("ssrf fixture"),
	}, ExternalModerationConfig{
		Enabled:   true,
		BaseURL:   "http://127.0.0.1:9/v1/moderations",
		APIKeys:   []string{"ssrf-key"},
		TimeoutMS: 100,
	})
	if !errors.Is(err, auth.ErrOAuthEndpointBlocked) {
		t.Fatalf("err=%v want ErrOAuthEndpointBlocked for loopback base URL", err)
	}
}
