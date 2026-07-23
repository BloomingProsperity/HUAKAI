//go:build integration_live

package antigravity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/antigravity"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

const liveCredentialFileEnv = "HUAKAI_LIVE_ANTIGRAVITY_CREDENTIAL_FILE"

func TestLiveAntigravityModelDiscoveryAndInference(t *testing.T) {
	credentialFile := strings.TrimSpace(os.Getenv(liveCredentialFileEnv))
	if credentialFile == "" {
		t.Fatalf("%s 必须指向真实 OAuth JSON 或 refresh token 文本", liveCredentialFileEnv)
	}
	refreshTokens, err := loadRefreshTokens(credentialFile)
	if err != nil {
		t.Fatalf("读取活体凭据: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	accessToken, err := refreshFirstUsableToken(ctx, refreshTokens)
	if err != nil {
		t.Fatalf("刷新 %d 个候选凭据均失败: %v", len(refreshTokens), err)
	}

	projectID, tier, err := (&antigravity.ProjectResolver{}).ResolveProjectMetadata(ctx, accessToken)
	if err != nil {
		t.Fatalf("解析 Antigravity 项目: %v", err)
	}
	if strings.TrimSpace(projectID) == "" {
		t.Fatal("上游未返回 project_id")
	}
	t.Logf("活体账号项目已识别，套餐=%q", tier)

	registry := registrydefault.Build()
	baseFactory := transport.NewFactory()
	standard, err := baseFactory.For(transport.ProviderAntigravity, transport.TransportModeStandard)
	if err != nil {
		t.Fatalf("构造标准协商出口: %v", err)
	}
	standardH1, err := baseFactory.For(transport.ProviderAntigravity, transport.TransportModeStandardH1)
	if err != nil {
		t.Fatalf("构造标准 H1 出口: %v", err)
	}
	standardRecorder := &protocolRecorder{inner: standard}
	h1Recorder := &protocolRecorder{inner: standardH1}
	factory := transport.NewFactory()
	factory.SetStandard(standardRecorder)
	factory.SetStandardH1(h1Recorder)
	dispatcher := &gateway.UpstreamDispatcher{
		Adapters:         registry,
		TransportFactory: factory,
		Timeouts: gateway.TimeoutConfig{
			RequestTotalTimeout: 30 * time.Second,
			HeaderToFirstByte:   20 * time.Second,
		},
	}
	account := provider.AccountInfo{AccountID: 1, Platform: "antigravity", AccountType: "oauth"}
	credential := provider.Credential{
		Type:  provider.CredentialTypeOAuthAccessToken,
		Value: accessToken,
		Extra: map[string]string{"project_id": projectID},
	}
	modelsBody := dispatchLive(t, ctx, dispatcher, gateway.DispatchInput{
		ProtocolFamily: registrydefault.ProtocolAntigravitySession,
		HTTPMethod:     http.MethodPost,
		EndpointPath:   "/v1internal:fetchAvailableModels",
		InboundBody:    []byte(`{}`),
		Account:        account,
		Credential:     credential,
	}, "模型发现")
	models := cloudCodeModelIDs(modelsBody)
	if len(models) == 0 {
		t.Fatal("模型发现 HTTP 200，但响应中没有可识别模型")
	}
	model := preferredLiveModel(models)
	t.Logf("模型发现成功，模型数=%d，推理模型=%q，协商协议=%s", len(models), model, standardRecorder.lastProto)

	prompt := []byte(`{"contents":[{"role":"user","parts":[{"text":"Reply with OK only."}]}],"generationConfig":{"maxOutputTokens":8}}`)
	for _, tc := range []struct {
		name string
		mode transport.TransportMode
	}{
		{name: "自动标准H1"},
		{name: "标准协商对照", mode: transport.TransportModeStandard},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h1Recorder.lastProto = ""
			standardRecorder.lastProto = ""
			body := dispatchLive(t, ctx, dispatcher, gateway.DispatchInput{
				ProtocolFamily:       registrydefault.ProtocolAntigravitySession,
				UpstreamModelID:      model,
				InboundBody:          prompt,
				Account:              account,
				Credential:           credential,
				TransportMode:        tc.mode,
				NonStreamingBuffered: true,
			}, "真实推理")
			if !liveResponseHasContent(body) {
				t.Fatalf("真实推理返回 2xx，但没有候选内容: %s", boundedDiagnostic(body))
			}
			if tc.mode == "" {
				if h1Recorder.lastProto != "HTTP/1.1" {
					t.Fatalf("自动推理协议=%q，期望 HTTP/1.1", h1Recorder.lastProto)
				}
			} else if standardRecorder.lastProto == "" {
				t.Fatal("标准协商对照没有记录上游协议")
			}
			t.Logf("真实推理协议=%s", firstNonEmpty(h1Recorder.lastProto, standardRecorder.lastProto))
		})
	}

	h1Recorder.lastProto = ""
	streamBody := dispatchLive(t, ctx, dispatcher, gateway.DispatchInput{
		ProtocolFamily:     registrydefault.ProtocolAntigravitySession,
		UpstreamModelID:    model,
		InboundBody:        prompt,
		Account:            account,
		Credential:         credential,
		ClientStreamIntent: true,
	}, "流式真实推理")
	if h1Recorder.lastProto != "HTTP/1.1" || !liveResponseHasContent(streamBody) {
		t.Fatalf("流式推理协议=%q，响应=%s", h1Recorder.lastProto, boundedDiagnostic(streamBody))
	}
	t.Logf("流式真实推理协议=%s", h1Recorder.lastProto)
}

type protocolRecorder struct {
	inner     http.RoundTripper
	lastProto string
}

func (r *protocolRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := r.inner.RoundTrip(req)
	if resp != nil {
		r.lastProto = resp.Proto
	}
	return resp, err
}

func dispatchLive(t *testing.T, ctx context.Context, dispatcher *gateway.UpstreamDispatcher, input gateway.DispatchInput, phase string) []byte {
	t.Helper()
	result, err := dispatcher.Dispatch(ctx, input)
	if err != nil {
		t.Fatalf("%s出站失败: %v", phase, err)
	}
	if result == nil || result.UpstreamReader == nil {
		t.Fatalf("%s返回空结果", phase)
	}
	defer result.Close()
	body, err := io.ReadAll(io.LimitReader(result.UpstreamReader, 2<<20))
	if err != nil {
		t.Fatalf("读取%s响应: %v", phase, err)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		t.Fatalf("%s HTTP %d: %s", phase, result.StatusCode, boundedDiagnostic(body))
	}
	return body
}

func refreshFirstUsableToken(ctx context.Context, refreshTokens []string) (string, error) {
	cfg := antigravity.DefaultOAuthConfig()
	client := auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
	var lastStatus int
	for _, refreshToken := range refreshTokens {
		form := url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {refreshToken},
			"client_id":     {cfg.ClientID},
			"client_secret": {cfg.ClientSecret},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		var payload struct {
			AccessToken string `json:"access_token"`
		}
		decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload)
		_ = resp.Body.Close()
		lastStatus = resp.StatusCode
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && decodeErr == nil && strings.TrimSpace(payload.AccessToken) != "" {
			return strings.TrimSpace(payload.AccessToken), nil
		}
	}
	return "", fmt.Errorf("没有候选 refresh token 被公开客户端接受，最后状态=%d", lastStatus)
}

func loadRefreshTokens(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	var node any
	if json.Unmarshal(raw, &node) == nil {
		collectRefreshTokens(node, seen, &out)
	} else {
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("文件中没有 refresh_token")
	}
	return out, nil
}

func collectRefreshTokens(node any, seen map[string]bool, out *[]string) {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			if strings.EqualFold(key, "refresh_token") {
				if token, ok := child.(string); ok {
					token = strings.TrimSpace(token)
					if token != "" && !seen[token] {
						seen[token] = true
						*out = append(*out, token)
					}
				}
			}
			collectRefreshTokens(child, seen, out)
		}
	case []any:
		for _, child := range value {
			collectRefreshTokens(child, seen, out)
		}
	}
}

func cloudCodeModelIDs(raw []byte) []string {
	var root struct {
		Models map[string]json.RawMessage `json:"models"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return nil
	}
	out := make([]string, 0, len(root.Models))
	for id := range root.Models {
		if id = strings.TrimSpace(id); id != "" {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func preferredLiveModel(models []string) string {
	for _, needle := range []string{"gemini-2.5-flash", "gemini-3-flash"} {
		for _, model := range models {
			if strings.Contains(strings.ToLower(model), needle) {
				return model
			}
		}
	}
	return models[0]
}

func liveResponseHasContent(raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 2 && (bytes.Contains(trimmed, []byte(`"candidates"`)) || bytes.Contains(trimmed, []byte(`"text"`)))
}

func boundedDiagnostic(raw []byte) string {
	const limit = 400
	clean := strings.ReplaceAll(strings.TrimSpace(string(raw)), "\n", " ")
	if len(clean) > limit {
		clean = clean[:limit]
	}
	return clean
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
