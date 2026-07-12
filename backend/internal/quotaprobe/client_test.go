package quotaprobe

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHTTPUsageFetcherRequestAndResponseShape(t *testing.T) {
	reset5h := "2026-07-12T14:00:00Z"
	reset7d := "2026-07-18T10:00:00Z"
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.String() != "https://usage.test/v1" {
			t.Fatalf("请求=%s %s", req.Method, req.URL.String())
		}
		if req.Header.Get("Authorization") != "Bearer access-101" {
			t.Fatalf("Authorization=%q", req.Header.Get("Authorization"))
		}
		if req.Header.Get("Anthropic-Beta") != oauthUsageBeta {
			t.Fatalf("Anthropic-Beta=%q", req.Header.Get("Anthropic-Beta"))
		}
		body := `{"five_hour":{"utilization":37.5,"resets_at":"` + reset5h + `"},"seven_day":{"utilization":62.25,"resets_at":"` + reset7d + `"}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	fetcher := NewHTTPUsageFetcher(&http.Client{Transport: transport}, nil)
	fetcher.endpoint = "https://usage.test/v1"

	snapshot, err := fetcher.FetchUsage(context.Background(), 101, "access-101")
	if err != nil {
		t.Fatalf("FetchUsage: %v", err)
	}
	if snapshot.FiveHour.Utilization == nil || *snapshot.FiveHour.Utilization != 37.5 ||
		snapshot.SevenDay.Utilization == nil || *snapshot.SevenDay.Utilization != 62.25 {
		t.Fatalf("利用率响应=%+v", snapshot)
	}
	want5h, _ := time.Parse(time.RFC3339, reset5h)
	want7d, _ := time.Parse(time.RFC3339, reset7d)
	if snapshot.FiveHour.ResetsAt == nil || !snapshot.FiveHour.ResetsAt.Equal(want5h) ||
		snapshot.SevenDay.ResetsAt == nil || !snapshot.SevenDay.ResetsAt.Equal(want7d) {
		t.Fatalf("重置时间响应=%+v", snapshot)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
