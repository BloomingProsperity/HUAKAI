package codexagent

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 用测试服务器地址构造铸身份器,绕开写死的官方端点。
func testRegistrar(url string) *RuntimeRegistrar {
	base := NewRuntimeRegistrar(nil)
	base.baseURL = url
	return base
}

func TestGenerateKeyMaterialRoundTrip(t *testing.T) {
	material, err := GenerateKeyMaterial()
	if err != nil {
		t.Fatalf("生成密钥失败: %v", err)
	}
	// 私钥必须能被导入路径按 PKCS#8 Ed25519 解回,否则铸出来的号自己都用不了。
	private, err := parsePrivateKey(material.PrivateKeyPKCS8Base64)
	if err != nil {
		t.Fatalf("私钥无法解回: %v", err)
	}
	defer wipe(private)

	// 公钥必须是 ssh-ed25519 线格式,且承载的 32 字节与私钥派生的公钥一致。
	if !strings.HasPrefix(material.PublicKeySSH, "ssh-ed25519 ") {
		t.Fatalf("公钥前缀不对: %q", material.PublicKeySSH)
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(material.PublicKeySSH, "ssh-ed25519 "))
	if err != nil {
		t.Fatalf("公钥 base64 解码失败: %v", err)
	}
	pub := sshEd25519Payload(t, blob)
	want := private.Public().(ed25519.PublicKey)
	if string(pub) != string(want) {
		t.Fatalf("公钥与私钥不匹配")
	}
}

// 解析 ssh-ed25519 公钥 blob:ssh-string("ssh-ed25519") ++ ssh-string(32 字节公钥)。
func sshEd25519Payload(t *testing.T, blob []byte) []byte {
	t.Helper()
	algo, rest := readSSHString(t, blob)
	if algo != "ssh-ed25519" {
		t.Fatalf("算法段不对: %q", algo)
	}
	key, tail := readSSHString(t, rest)
	if len(tail) != 0 {
		t.Fatalf("公钥 blob 尾部有多余字节")
	}
	return []byte(key)
}

func readSSHString(t *testing.T, in []byte) (string, []byte) {
	t.Helper()
	if len(in) < 4 {
		t.Fatalf("ssh-string 长度前缀不足")
	}
	n := binary.BigEndian.Uint32(in[:4])
	if int(n) > len(in)-4 {
		t.Fatalf("ssh-string 越界")
	}
	return string(in[4 : 4+n]), in[4+n:]
}

func TestRegisterRuntimeSendsSignedContract(t *testing.T) {
	var gotAuth, gotPath, gotMethod, gotFedramp, gotOriginator, gotUA string
	var gotBody map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotFedramp = r.Header.Get(fedRAMPRegistrationFlag)
		gotOriginator = r.Header.Get("originator")
		gotUA = r.Header.Get("User-Agent")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		_, _ = io.WriteString(w, `{"agent_runtime_id":"rt-abc123"}`)
	}))
	defer srv.Close()

	runtimeID, err := testRegistrar(srv.URL).RegisterRuntime(context.Background(), RegisterRuntimeInput{
		AccessToken:  "tok-session",
		PublicKeySSH: "ssh-ed25519 AAAA",
		ABOM:         AgentBillOfMaterials{AgentVersion: "9.9.9", AgentHarnessID: "codex-cli", RunningLocation: "cli-linux"},
		Capabilities: nil,
	})
	if err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if runtimeID != "rt-abc123" {
		t.Fatalf("runtime_id 解析错误: %q", runtimeID)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("方法应为 POST,实为 %s", gotMethod)
	}
	if gotPath != agentRegisterPath {
		t.Fatalf("端点路径错误: %s", gotPath)
	}
	if gotAuth != "Bearer tok-session" {
		t.Fatalf("Bearer 鉴权缺失或错误: %q", gotAuth)
	}
	if gotFedramp != "" {
		t.Fatalf("非 fedramp 号不应带 fedramp 头")
	}
	// 必须以第一方 codex 客户端身份出站,否则上游拒发 agent registry。
	if gotOriginator != codexOriginator {
		t.Fatalf("originator 头缺失或错误: %q", gotOriginator)
	}
	if !strings.HasPrefix(gotUA, codexOriginator+"/") {
		t.Fatalf("User-Agent 不是 codex 形态: %q", gotUA)
	}
	// 契约字段名必须与上游一致,少一个都铸不出号。
	for _, key := range []string{"abom", "agent_public_key", "capabilities", "ttl"} {
		if _, ok := gotBody[key]; !ok {
			t.Fatalf("请求体缺字段 %q", key)
		}
	}
	var pub string
	_ = json.Unmarshal(gotBody["agent_public_key"], &pub)
	if pub != "ssh-ed25519 AAAA" {
		t.Fatalf("公钥字段错误: %q", pub)
	}
	if string(gotBody["ttl"]) != "null" {
		t.Fatalf("ttl 应为 null,实为 %s", gotBody["ttl"])
	}
	if string(gotBody["capabilities"]) != "[]" {
		t.Fatalf("空能力应序列化为 [],实为 %s", gotBody["capabilities"])
	}
}

func TestRegisterRuntimeFedRAMPHeader(t *testing.T) {
	var gotFedramp string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFedramp = r.Header.Get(fedRAMPRegistrationFlag)
		_, _ = io.WriteString(w, `{"agentRuntimeId":"rt-fed"}`)
	}))
	defer srv.Close()

	runtimeID, err := testRegistrar(srv.URL).RegisterRuntime(context.Background(), RegisterRuntimeInput{
		AccessToken: "tok", PublicKeySSH: "ssh-ed25519 AAAA", IsFedRAMP: true,
	})
	if err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	if runtimeID != "rt-fed" {
		t.Fatalf("camelCase runtime_id 未解析: %q", runtimeID)
	}
	if gotFedramp != "true" {
		t.Fatalf("fedramp 号应带 %s: true", fedRAMPRegistrationFlag)
	}
}

func TestRegisterRuntimeRejectsBadInput(t *testing.T) {
	reg := testRegistrar("http://127.0.0.1:0")
	if _, err := reg.RegisterRuntime(context.Background(), RegisterRuntimeInput{PublicKeySSH: "ssh-ed25519 AAAA"}); err == nil {
		t.Fatalf("缺 access_token 应报错且不发请求")
	}
	if _, err := reg.RegisterRuntime(context.Background(), RegisterRuntimeInput{AccessToken: "tok"}); err == nil {
		t.Fatalf("缺公钥应报错且不发请求")
	}
}

func TestRegisterRuntimeUpstreamFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"error":"forbidden"}`)
	}))
	defer srv.Close()

	if _, err := testRegistrar(srv.URL).RegisterRuntime(context.Background(), RegisterRuntimeInput{
		AccessToken: "tok", PublicKeySSH: "ssh-ed25519 AAAA",
	}); err == nil {
		t.Fatalf("上游非 2xx 应报错")
	}
}

func TestRegisterRuntimeEmptyRuntimeID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"agent_runtime_id":""}`)
	}))
	defer srv.Close()

	if _, err := testRegistrar(srv.URL).RegisterRuntime(context.Background(), RegisterRuntimeInput{
		AccessToken: "tok", PublicKeySSH: "ssh-ed25519 AAAA",
	}); err == nil {
		t.Fatalf("上游未返回 runtime_id 应报错")
	}
}
