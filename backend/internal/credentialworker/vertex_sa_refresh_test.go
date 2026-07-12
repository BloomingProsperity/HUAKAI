package credentialworker

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// vertexRedirectTransport 把发往官方 host 的铸造请求改路由到测试服务器,
// 从而在不放松 vertexsa 的 token_uri host 守卫前提下可测。
type vertexRedirectTransport struct{ target *url.URL }

func (rt vertexRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	clone.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func vertexTestKeyPEM(t *testing.T) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试 RSA key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(priv)
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}))
}

// TestVertexSAModeAdapterMintsAndStoresToken 咬住 M1:raw SA(client_email+private_key)
// 经 vertexSAModeAdapter 铸出 access_token 并存回 payload(+expires_at),供 materialization
// 走 access_token→"Bearer" 产出 Vertex adapter 所需凭据。此前 metadataTokenAdapter 对 raw SA
// 返回 ErrNoRefreshRequired→永不铸造=fail-closed。
// 变异:把注册改回 metadataTokenAdapter{} 或让 adapter 不铸造 → payload 无 access_token,本测试红。
func TestVertexSAModeAdapterMintsAndStoresToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "ya29.vertex-minted", "expires_in": 3600, "token_type": "Bearer"})
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: vertexRedirectTransport{target: target}}

	adapter := vertexSAModeAdapter{client: client}
	payload, _ := json.Marshal(map[string]any{
		"client_email": "svc@proj.iam.gserviceaccount.com",
		"private_key":  vertexTestKeyPEM(t),
		"project_id":   "my-proj",
	})
	res, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{Payload: payload, Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	if res.Outcome != "refresh_succeeded" || res.AccessExpiresAt.IsZero() {
		t.Fatalf("结果异常: outcome=%q expiresAt=%v", res.Outcome, res.AccessExpiresAt)
	}
	var out map[string]any
	if err := json.Unmarshal(res.Payload, &out); err != nil {
		t.Fatalf("payload 非 JSON: %v", err)
	}
	if out["access_token"] != "ya29.vertex-minted" {
		t.Fatalf("payload 未存入铸造的 access_token: %v", out["access_token"])
	}
	if out["project_id"] != "my-proj" {
		t.Fatalf("project_id 应保留: %v", out["project_id"])
	}
	if _, ok := out["expires_at"]; !ok {
		t.Fatal("payload 应带 expires_at")
	}
}

// TestVertexSAModeAdapterFallsBackWhenNoPrivateKey 验证:无 SA 私钥材料时回退
// metadataTokenAdapter(metadata-only 路径);既无私钥又无 metadata endpoint → 不需刷新。
func TestVertexSAModeAdapterFallsBackWhenNoPrivateKey(t *testing.T) {
	adapter := vertexSAModeAdapter{}
	payload, _ := json.Marshal(map[string]any{"project_id": "p"}) // 无 client_email/private_key/metadata endpoint
	_, err := adapter.RefreshCredential(context.Background(), ModeRefreshInput{Payload: payload, Now: time.Now().UTC()})
	if err != ErrNoRefreshRequired {
		t.Fatalf("无私钥无 metadata endpoint 应回退到 ErrNoRefreshRequired,得 %v", err)
	}
}
