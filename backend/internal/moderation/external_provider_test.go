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
	// 变异:去掉 429 之后的 per-key 冻结,会让第三个请求在冻结窗口内
	// 复用 key-a,于是 authorization 序列断言变红。
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
	// 变异:在 HTTP 请求之后才执行上限校验,或只检查解码后的
	// raw 字节而忽略 data-URL 长度,都会让 calls 自增并使本用例失败。
	calls := 0
	provider := NewExternalModerator(ExternalModeratorDeps{
		HTTPClient: &http.Client{Transport: moderationRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return moderationHTTPResponse(http.StatusOK, `{"results":[{"flagged":false,"categories":{},"category_scores":{}}]}`), nil
		})},
	})
	oversized := "data:image/png;base64," + strings.Repeat("A", ExternalImageDataURLMaxBytes+1)
	res, err := provider.ScreenExternal(context.Background(), ScreenRequest{
		Body:      []byte("image fixture"),
		ImageURLs: []string{oversized},
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

func TestExternalImageRejectsUnsafeRemoteSchemeBeforeHTTP(t *testing.T) {
	calls := 0
	provider := NewExternalModerator(ExternalModeratorDeps{
		HTTPClient: &http.Client{Transport: moderationRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return moderationHTTPResponse(http.StatusOK, `{}`), nil
		})},
	})
	res, err := provider.ScreenExternal(context.Background(), ScreenRequest{
		ImageURLs: []string{"http://127.0.0.1/private.png"},
	}, ExternalModerationConfig{
		Enabled: true, BaseURL: "https://moderation.example.test/v1/moderations",
		APIKeys: []string{"image-key"}, ImageEnabled: true,
	})
	if err != nil {
		t.Fatalf("不安全图片地址返回 transport error: %v", err)
	}
	if !res.Blocked || res.ReasonCode != "external_image_invalid" {
		t.Fatalf("result=%+v want blocked external_image_invalid", res)
	}
	if calls != 0 {
		t.Fatalf("不安全图片地址仍触发外部 HTTP: calls=%d", calls)
	}
}

func TestExternalThresholdRequiresReturnedCategoryScore(t *testing.T) {
	// 变异:把缺失的分数当作 Go 的零值读取,会让阈值 0
	// 拦截一个 provider 根本没有打分的类别。
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
	// 变异:把默认的 SSRF 防护客户端换成 http.DefaultClient,
	// 会尝试拨号 loopback,而不是返回 ErrOAuthEndpointBlocked。
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
