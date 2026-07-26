package mediatask

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestGeminiVideoProviderPinsAccountAndNormalizesVeoResult(t *testing.T) {
	selector := &capturingVideoSelector{accountID: 41}
	vault := provider.NewStaticVault()
	if err := vault.Set(41,
		provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		provider.AccountInfo{AccountID: 41, TenantID: 7, Platform: "gemini", AccountType: credentialstore.AuthModeAIStudioAPIKey},
	); err != nil {
		t.Fatal(err)
	}
	dispatcher := &videoDispatcherStub{responses: []*gateway.DispatchResult{
		dispatchResult(http.StatusOK, `{"name":"models/veo-3.1-generate-preview/operations/op-1"}`),
		dispatchResult(http.StatusOK, `{"done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":"https://generativelanguage.googleapis.com/v1beta/files/out-1"}}]}}}`),
	}}
	mediaProvider := NewGeminiVideoProvider(GrokVideoProviderDeps{
		Selector: selector, CredentialVault: vault, Dispatcher: dispatcher,
	})
	task := geminiBoundVideoTask()
	providerTaskID, err := mediaProvider.SubmitBound(context.Background(), task, SubmitReq{
		TaskID: task.ID, RequestID: task.RequestID, TaskType: task.TaskType,
		InputParams: jsonBody(`{"model":"video-public","prompt":"hello","aspect_ratio":"16:9","duration":8}`),
	})
	if err != nil {
		t.Fatalf("SubmitBound: %v", err)
	}
	if providerTaskID != "models/veo-3.1-generate-preview/operations/op-1" {
		t.Fatalf("provider task id=%q", providerTaskID)
	}
	poll, err := mediaProvider.PollBound(context.Background(), task, providerTaskID)
	if err != nil {
		t.Fatalf("PollBound: %v", err)
	}
	if poll.Status != StatusSucceeded || poll.ActualCents != task.EstimatedCents || poll.Progress != 100 {
		t.Fatalf("poll=%+v", poll)
	}
	var public map[string]any
	if json.Unmarshal(poll.Result, &public) != nil || public["status"] != "completed" {
		t.Fatalf("公开结果未归一: %s", poll.Result)
	}
	if len(dispatcher.inputs) != 2 {
		t.Fatalf("dispatch calls=%d", len(dispatcher.inputs))
	}
	if dispatcher.inputs[0].EndpointPath != "/v1beta/models/veo-3.1-generate-preview:predictLongRunning" ||
		dispatcher.inputs[0].HTTPMethod != http.MethodPost {
		t.Fatalf("提交出站=%+v", dispatcher.inputs[0])
	}
	var submit map[string]any
	if json.Unmarshal(dispatcher.inputs[0].InboundBody, &submit) != nil {
		t.Fatalf("提交体无效: %s", dispatcher.inputs[0].InboundBody)
	}
	parameters := submit["parameters"].(map[string]any)
	if parameters["aspectRatio"] != "16:9" || parameters["durationSeconds"].(float64) != 8 {
		t.Fatalf("Veo 参数映射错误: %+v", parameters)
	}
	if dispatcher.inputs[1].EndpointPath != "/v1beta/models/veo-3.1-generate-preview/operations/op-1" ||
		dispatcher.inputs[1].HTTPMethod != http.MethodGet {
		t.Fatalf("轮询出站=%+v", dispatcher.inputs[1])
	}
	if len(selector.requests) != 1 || selector.requests[0].PinnedAccountID != 41 {
		t.Fatalf("没有固定原账号: %+v", selector.requests)
	}
	if selector.releaseCount != 0 {
		t.Fatalf("异步任务尚未统一结算就释放槽位: %d", selector.releaseCount)
	}
}

func TestGeminiVideoProviderKeepsPendingAndProviderFailureDistinct(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Status
	}{
		{name: "仍在运行", body: `{"done":false}`, want: StatusInProgress},
		{name: "供应商终态失败", body: `{"done":true,"error":{"code":400,"status":"INVALID_ARGUMENT","message":"bad"}}`, want: StatusFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			vault := provider.NewStaticVault()
			_ = vault.Set(41, provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
				provider.AccountInfo{AccountID: 41, TenantID: 7, Platform: "gemini", AccountType: credentialstore.AuthModeAIStudioAPIKey})
			mediaProvider := NewGeminiVideoProvider(GrokVideoProviderDeps{
				Selector: &capturingVideoSelector{accountID: 41}, CredentialVault: vault,
				Dispatcher: &videoDispatcherStub{responses: []*gateway.DispatchResult{dispatchResult(http.StatusOK, test.body)}},
			})
			poll, err := mediaProvider.PollBound(context.Background(), geminiBoundVideoTask(), "models/veo/operations/op-1")
			if err != nil || poll.Status != test.want {
				t.Fatalf("poll=%+v err=%v", poll, err)
			}
		})
	}
}

func TestGeminiVideoProviderTruncatedAmbiguousSubmitResponseNeverRetries(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusConflict,
		http.StatusTooEarly,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			selector := &capturingVideoSelector{accountID: 41}
			vault := provider.NewStaticVault()
			_ = vault.Set(41,
				provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
				provider.AccountInfo{
					AccountID: 41, TenantID: 7, Platform: "gemini",
					AccountType: credentialstore.AuthModeAIStudioAPIKey,
				},
			)
			mediaProvider := NewGeminiVideoProvider(GrokVideoProviderDeps{
				Selector: selector, CredentialVault: vault,
				Dispatcher: &videoDispatcherStub{responses: []*gateway.DispatchResult{
					dispatchReadErrorResult(status),
				}},
			})
			task := geminiBoundVideoTask()
			_, err := mediaProvider.SubmitBound(context.Background(), task, SubmitReq{
				TaskID: task.ID, RequestID: task.RequestID, TaskType: task.TaskType,
				InputParams: jsonBody(`{"model":"veo","prompt":"x"}`),
			})
			class, retryable, recognized := providerErrorDetails(err)
			if !recognized || retryable || class != "provider_submit_outcome_unknown" {
				t.Fatalf("status=%d class=%q retryable=%v recognized=%v err=%v",
					status, class, retryable, recognized, err)
			}
			if selector.releaseCount != 1 {
				t.Fatalf("提交结果未知时本轮临时选择句柄释放次数=%d want 1", selector.releaseCount)
			}
		})
	}
}

func geminiBoundVideoTask() Task {
	return Task{
		ID: 18, TenantID: 7, UserID: 11, APIKeyID: 13, TaskType: "video_generate",
		Provider: geminiVideoProviderName, ProviderAccountID: 41, PoolGroupID: 29,
		ProtocolFamily: "gemini_messages", RequestedModel: "video-public",
		ProviderModelID: "veo-3.1-generate-preview", RequestID: "video_public_2",
		HoldRef: "claim:32", EstimatedCents: 40,
		BindingID: 19, BindingRPMLimit: 7, BindingTPMLimit: 700, BindingMaxParallelRequests: 3,
	}
}

// 变异刀:①去掉 DownloadBound 的凭据解析/账号绑定 → Credential 断言转红;
// ②删掉 host/scheme 允许名单 → 恶意地址用例会真发起 dispatch 转红。
func TestGeminiVideoDownloadStreamsWithBoundAccountCredential(t *testing.T) {
	admitter := &recordingAccountAdmitter{}
	vault := provider.NewStaticVault()
	if err := vault.Set(41,
		provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		provider.AccountInfo{AccountID: 41, TenantID: 7, Platform: "gemini", AccountType: credentialstore.AuthModeAIStudioAPIKey},
	); err != nil {
		t.Fatal(err)
	}
	dispatcher := &videoDispatcherStub{responses: []*gateway.DispatchResult{
		dispatchResult(http.StatusOK, "MP4-BYTES"),
	}}
	mediaProvider := NewGeminiVideoProvider(GrokVideoProviderDeps{
		Selector: &capturingVideoSelector{accountID: 41}, AccountAdmitter: admitter,
		CredentialVault: vault, Dispatcher: dispatcher,
	})
	task := geminiBoundVideoTask()
	task.Status = StatusSucceeded
	task.Result = json.RawMessage(`{"upstream_content":{"uri":"https://generativelanguage.googleapis.com/v1beta/files/out-1:download?alt=media"}}`)
	content, err := mediaProvider.DownloadBound(context.Background(), task)
	if err != nil {
		t.Fatalf("DownloadBound: %v", err)
	}
	defer func() {
		if content.Close != nil {
			_ = content.Close()
		}
	}()
	body, err := io.ReadAll(content.Body)
	if err != nil || string(body) != "MP4-BYTES" {
		t.Fatalf("产物字节没有流回: %q err=%v", body, err)
	}
	if len(dispatcher.inputs) != 1 {
		t.Fatalf("dispatch calls=%d", len(dispatcher.inputs))
	}
	sent := dispatcher.inputs[0]
	if sent.HTTPMethod != http.MethodGet || sent.EndpointPath != "/v1beta/files/out-1:download" || sent.EndpointQuery != "alt=media" {
		t.Fatalf("下载出站=%+v", sent)
	}
	if sent.Credential.Value != "secret" || sent.Account.AccountID != 41 {
		t.Fatalf("没有用生成账号的凭据代下: cred=%q account=%d", sent.Credential.Value, sent.Account.AccountID)
	}
	if admitter.calls != 1 || admitter.accountID != 41 {
		t.Fatalf("产物下载没有执行原账号 RPM 准入: %+v", admitter)
	}
}

func TestGeminiVideoDownloadRejectsNonUpstreamURI(t *testing.T) {
	vault := provider.NewStaticVault()
	if err := vault.Set(41,
		provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
		provider.AccountInfo{AccountID: 41, TenantID: 7, Platform: "gemini", AccountType: credentialstore.AuthModeAIStudioAPIKey},
	); err != nil {
		t.Fatal(err)
	}
	for name, uri := range map[string]string{
		"外部主机":     "https://evil.example.com/v1beta/files/out-1:download?alt=media",
		"明文scheme": "http://generativelanguage.googleapis.com/v1beta/files/out-1:download",
		"越界路径":     "https://generativelanguage.googleapis.com/v2/files/out-1",
	} {
		t.Run(name, func(t *testing.T) {
			dispatcher := &videoDispatcherStub{responses: []*gateway.DispatchResult{dispatchResult(http.StatusOK, "MP4-BYTES")}}
			mediaProvider := NewGeminiVideoProvider(GrokVideoProviderDeps{
				Selector: &capturingVideoSelector{accountID: 41}, CredentialVault: vault, Dispatcher: dispatcher,
			})
			task := geminiBoundVideoTask()
			task.Status = StatusSucceeded
			task.Result = json.RawMessage(`{"upstream_content":{"uri":"` + uri + `"}}`)
			if _, err := mediaProvider.DownloadBound(context.Background(), task); err == nil {
				t.Fatal("非上游允许名单地址必须拒绝")
			}
			if len(dispatcher.inputs) != 0 {
				t.Fatalf("被拒地址不得发起出站: %+v", dispatcher.inputs)
			}
		})
	}
}
