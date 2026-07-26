package mediatask

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestHTTPProviderUsesInjectedClientAndMapsSubmitPoll(t *testing.T) {
	// 变异：把注入的服务端 client 换成 http.DefaultClient 或写死路径；
	// 这个注入 transport 的 provider 就必然失败。
	var submitSeen bool
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body any
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/tasks":
			submitSeen = true
			var req SubmitReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode submit: %v", err)
			}
			if req.TaskID != 55 || req.RequestID != "req-55" || req.TaskType != "image_generation" {
				t.Fatalf("submit req=%+v", req)
			}
			body = map[string]string{"provider_task_id": "up-55"}
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/up-55":
			body = PollResult{
				Status:      StatusSucceeded,
				Progress:    100,
				ActualCents: 77,
				Result:      json.RawMessage(`{"url":"https://cdn.example/55.png"}`),
			}
		default:
			t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(raw))),
			Request:    r,
		}, nil
	})}

	provider := NewHTTPProvider("http://provider.test", client)
	providerTaskID, err := provider.Submit(context.Background(), SubmitReq{
		TaskID:      55,
		RequestID:   "req-55",
		TaskType:    "image_generation",
		InputParams: json.RawMessage(`{"prompt":"a clean test"}`),
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !submitSeen || providerTaskID != "up-55" {
		t.Fatalf("submitSeen=%v providerTaskID=%q", submitSeen, providerTaskID)
	}

	poll, err := provider.Poll(context.Background(), "up-55")
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if poll.Status != StatusSucceeded || poll.ActualCents != 77 || !strings.Contains(string(poll.Result), "cdn.example") {
		t.Fatalf("poll=%+v", poll)
	}
}

func TestHTTPProviderSubmitSendsTaskDerivedIdempotencyHeader(t *testing.T) {
	// 变异:删除 provider.go Submit 里设置 Idempotency-Key 头的那段;上游重复提交
	// 时无法去重,本断言会因 header 为空(或值不等于任务派生键)而 RED。
	// 判别性:期望值 "mediatask-55" 由 TaskID 派生,与请求体里 RequestID="req-55"
	// 不同——若改成误用 RequestID 头也会被抓到。
	var gotIdemHeader string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/tasks" {
			t.Fatalf("unexpected provider request %s %s", r.Method, r.URL.Path)
		}
		gotIdemHeader = r.Header.Get("Idempotency-Key")
		raw, err := json.Marshal(map[string]string{"provider_task_id": "up-55"})
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(string(raw))),
			Request:    r,
		}, nil
	})}

	provider := NewHTTPProvider("http://provider.test", client)
	// 故意不在请求体里预填 IdempotencyKey,验证 provider 会就地从任务身份派生。
	if _, err := provider.Submit(context.Background(), SubmitReq{
		TaskID: 55, RequestID: "req-55", TaskType: "image_generation",
		InputParams: json.RawMessage(`{"prompt":"a clean test"}`),
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	want := DeriveIdempotencyKey(55, "req-55")
	if want != "mediatask-55" {
		t.Fatalf("派生键自检失败 want mediatask-55 got %q", want)
	}
	if gotIdemHeader != want {
		t.Fatalf("Idempotency-Key 头=%q want %q(重复上游提交不会被去重)", gotIdemHeader, want)
	}
}

func TestHTTPProviderSubmitSeparatesUnknownOutcomeFromDefiniteRejection(t *testing.T) {
	tests := []struct {
		name          string
		status        int
		body          string
		transportErr  error
		wantClass     string
		wantRetryable bool
	}{
		{
			name: "网络结果未知", transportErr: errors.New("连接写出后被重置"),
			wantClass: "provider_submit_outcome_unknown",
		},
		{
			name: "服务端错误结果未知", status: http.StatusServiceUnavailable, body: `{"error":"busy"}`,
			wantClass: "provider_submit_outcome_unknown",
		},
		{
			name: "成功响应缺任务号", status: http.StatusOK, body: `{}`,
			wantClass: "provider_submit_response_invalid",
		},
		{
			name: "明确参数拒绝", status: http.StatusBadRequest, body: `{"error":"bad request"}`,
			wantClass: "provider_submit_rejected",
		},
		{
			name: "明确限流可重试", status: http.StatusTooManyRequests, body: `{"error":"rate limited"}`,
			wantClass: "upstream_rate_limited", wantRetryable: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if test.transportErr != nil {
					return nil, test.transportErr
				}
				return &http.Response{
					StatusCode: test.status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(test.body)),
					Request:    r,
				}, nil
			})}
			provider := NewHTTPProvider("http://provider.test", client)
			_, err := provider.Submit(context.Background(), SubmitReq{
				TaskID: 56, RequestID: "req-56", TaskType: "image_generation",
			})
			class, retryable, recognized := providerErrorDetails(err)
			if !recognized || class != test.wantClass || retryable != test.wantRetryable {
				t.Fatalf("class=%q retryable=%v recognized=%v err=%v",
					class, retryable, recognized, err)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestNoopProviderNeverReportsTerminalSuccess(t *testing.T) {
	provider := NewNoopProvider()
	id, err := provider.Submit(context.Background(), SubmitReq{TaskID: 1, RequestID: "req-1", TaskType: "image_generation"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if id == "" {
		t.Fatal("noop provider returned empty provider task id")
	}
	poll, err := provider.Poll(context.Background(), id)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if poll.Status != StatusInProgress {
		t.Fatalf("noop poll status=%q want in_progress", poll.Status)
	}
}

func TestHTTPProviderRegistryAcceptsMidjourneyAlias(t *testing.T) {
	// 变异：让 registry 写死为 provider 名 "http"；以 Provider=midjourney 翻译的
	// MJ 任务就无法复用异步 HTTP relay。
	registry := NewHTTPProviderRegistry(StaticConfigSource{Config: Config{
		Enabled: true, ProviderBaseURL: "http://provider.example",
		DefaultEstimatedCents: map[string]int64{
			"mj_imagine": 123,
		},
	}}, http.DefaultClient)

	if _, ok, err := registry.Provider(context.Background(), "midjourney"); err != nil || !ok {
		t.Fatalf("Provider(midjourney) ok=%v err=%v want available", ok, err)
	}
}

func TestHTTPProviderRegistryAcceptsSunoAlias(t *testing.T) {
	// 变异：让 registry 写死为 provider 名 "http" 和 "midjourney"；以
	// Provider=suno 翻译的 Suno 任务就无法复用异步 HTTP relay。
	registry := NewHTTPProviderRegistry(StaticConfigSource{Config: Config{
		Enabled: true, ProviderBaseURL: "http://provider.example",
		DefaultEstimatedCents: map[string]int64{
			"suno_generate": 123,
		},
	}}, http.DefaultClient)

	if _, ok, err := registry.Provider(context.Background(), "suno"); err != nil || !ok {
		t.Fatalf("Provider(suno) ok=%v err=%v want available", ok, err)
	}
}
func TestHTTPProviderRegistryAcceptsVideoAliases(t *testing.T) {
	// 变异：把 video 类 provider 排除在 HTTP registry 之外；/video/submit
	// 能到达 mediatask.Service 但在任务创建前就失败。
	registry := NewHTTPProviderRegistry(StaticConfigSource{Config: Config{
		Enabled: true, ProviderBaseURL: "http://provider.example",
		DefaultEstimatedCents: map[string]int64{
			"video_generate": 1000,
		},
	}}, http.DefaultClient)

	for _, provider := range []string{"video", "kling", "jimeng", "vidu", "sora", "hailuo"} {
		if _, ok, err := registry.Provider(context.Background(), provider); err != nil || !ok {
			t.Fatalf("Provider(%s) ok=%v err=%v want available", provider, ok, err)
		}
	}
}
