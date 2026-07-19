package codexagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

func TestBuildAuthorizationSignsFreshRuntimeTaskTuple(t *testing.T) {
	privateKey, encoded := testPrivateKey(t)
	now := time.Date(2026, 7, 19, 8, 9, 10, 0, time.UTC)
	payload := testPayload(t, encoded, "task-9")

	value, extra, err := BuildAuthorization(payload, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(value, "AgentAssertion ") || extra["chatgpt_account_id"] != "account-7" {
		t.Fatalf("value=%q extra=%v", value, extra)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "AgentAssertion "))
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Runtime   string `json:"agent_runtime_id"`
		Task      string `json:"task_id"`
		IssuedAt  string `json:"timestamp"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil {
		t.Fatal(err)
	}
	message := []byte("runtime-7:task-9:" + now.Format(time.RFC3339))
	if envelope.Runtime != "runtime-7" || envelope.Task != "task-9" || !ed25519.Verify(privateKey.Public().(ed25519.PublicKey), message, signature) {
		t.Fatalf("授权断言没有覆盖运行实例、任务和时间：%+v", envelope)
	}
}

func TestEnsureTaskRegistersAndPersistsReturnedTask(t *testing.T) {
	privateKey, encoded := testPrivateKey(t)
	now := time.Date(2026, 7, 19, 9, 0, 0, 0, time.UTC)
	var gotPath string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotPath = req.URL.Path
		body, _ := io.ReadAll(req.Body)
		var signed struct {
			IssuedAt  string `json:"timestamp"`
			Signature string `json:"signature"`
		}
		if err := json.Unmarshal(body, &signed); err != nil {
			t.Fatal(err)
		}
		signature, _ := base64.StdEncoding.DecodeString(signed.Signature)
		if signed.IssuedAt != now.Format(time.RFC3339) || !ed25519.Verify(privateKey.Public().(ed25519.PublicKey), []byte("runtime-7:"+signed.IssuedAt), signature) {
			t.Fatalf("任务登记签名无效：%+v", signed)
		}
		return jsonHTTPResponse(`{"task_id":"task-new"}`), nil
	})}
	broker := NewTaskBroker(client)
	broker.baseURL = "https://auth.example.test/base"
	broker.now = func() time.Time { return now }
	updated, err := broker.EnsureTask(context.Background(), testPayload(t, encoded, ""))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/base/v1/agent/runtime-7/task/register" {
		t.Fatalf("path=%q", gotPath)
	}
	var fields map[string]any
	if err := json.Unmarshal(updated, &fields); err != nil || fields["task_id"] != "task-new" {
		t.Fatalf("updated=%s err=%v", updated, err)
	}
}

func TestEnsureTaskOpensEncryptedTaskID(t *testing.T) {
	privateKey, encoded := testPrivateKey(t)
	public := taskBoxPublicKey(t, privateKey)
	ciphertext, err := box.SealAnonymous(nil, []byte("task-encrypted"), public, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	response := `{"encrypted_task_id":"` + base64.StdEncoding.EncodeToString(ciphertext) + `"}`
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonHTTPResponse(response), nil
	})}
	broker := NewTaskBroker(client)
	broker.baseURL = "https://auth.example.test"
	updated, err := broker.EnsureTask(context.Background(), testPayload(t, encoded, ""))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	_ = json.Unmarshal(updated, &fields)
	if fields["task_id"] != "task-encrypted" {
		t.Fatalf("updated=%s", updated)
	}
}

func TestValidatePayloadRejectsPathInjectionAndWrongKeyType(t *testing.T) {
	_, encoded := testPrivateKey(t)
	badRuntime := strings.Replace(string(testPayload(t, encoded, "task")), "runtime-7", "../escape", 1)
	if err := ValidatePayload([]byte(badRuntime), true); err == nil {
		t.Fatal("带路径分隔符的 runtime id 未被拒绝")
	}
	badKey := strings.Replace(string(testPayload(t, encoded, "task")), encoded, base64.StdEncoding.EncodeToString([]byte("not-pkcs8")), 1)
	if err := ValidatePayload([]byte(badKey), true); err == nil {
		t.Fatal("非 PKCS#8 Ed25519 私钥未被拒绝")
	}
}

func TestIsTaskInvalidResponseRequiresExplicitStructuredCode(t *testing.T) {
	accepted := []string{
		`{"error":{"code":"invalid_task_id"}}`,
		`{"code":"task_not_found"}`,
		`{"error":"task_expired"}`,
	}
	for _, body := range accepted {
		if !IsTaskInvalidResponse(http.StatusUnauthorized, []byte(body)) {
			t.Fatalf("明确任务失效未被识别：%s", body)
		}
	}
	rejected := []struct {
		status int
		body   string
	}{
		{http.StatusForbidden, `{"error":{"code":"invalid_task_id"}}`},
		{http.StatusUnauthorized, `{"error":{"code":"invalid_token"}}`},
		{http.StatusUnauthorized, `{"message":"task expired"}`},
		{http.StatusUnauthorized, `not-json invalid_task_id`},
	}
	for _, item := range rejected {
		if IsTaskInvalidResponse(item.status, []byte(item.body)) {
			t.Fatalf("非明确任务失效被误识别：status=%d body=%s", item.status, item.body)
		}
	}
}

func testPrivateKey(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return privateKey, base64.StdEncoding.EncodeToString(der)
}

func testPayload(t *testing.T, privateKey, taskID string) []byte {
	t.Helper()
	payload := map[string]any{
		"agent_runtime_id": "runtime-7", "agent_private_key": privateKey,
		"account_id": "account-7", "chatgpt_user_id": "user-7", "email": "operator@example.test",
	}
	if taskID != "" {
		payload["task_id"] = taskID
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func taskBoxPublicKey(t *testing.T, privateKey ed25519.PrivateKey) *[32]byte {
	t.Helper()
	digest := sha512.Sum512(privateKey.Seed())
	secret := digest[:32]
	secret[0] &= 248
	secret[31] &= 127
	secret[31] |= 64
	publicBytes, err := curve25519.X25519(secret, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	var public [32]byte
	copy(public[:], publicBytes)
	return &public
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func jsonHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
