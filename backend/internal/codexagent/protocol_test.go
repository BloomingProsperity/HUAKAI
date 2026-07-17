package codexagent

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

func TestBuildAssertionBindsRuntimeTaskAndSecondTimestamp(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 3, 4, 5, 0, time.FixedZone("offset", 8*3600))
	assertion, err := buildAssertion("runtime-1", "task-1", privateKey, now)
	if err != nil {
		t.Fatal(err)
	}
	encoded := assertion[len(assertionPrefix):]
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		RuntimeID string `json:"agent_runtime_id"`
		TaskID    string `json:"task_id"`
		Timestamp string `json:"timestamp"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.RuntimeID != "runtime-1" || envelope.TaskID != "task-1" || envelope.Timestamp != "2026-07-16T19:04:05Z" {
		t.Fatalf("envelope=%+v", envelope)
	}
	signature, err := base64.StdEncoding.DecodeString(envelope.Signature)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(publicKey, []byte("runtime-1:task-1:2026-07-16T19:04:05Z"), signature) {
		t.Fatal("签名没有绑定 runtime、task 和 UTC 秒级时间")
	}
	if ed25519.Verify(publicKey, []byte("runtime-1:task-2:2026-07-16T19:04:05Z"), signature) {
		t.Fatal("修改 task 后签名仍通过")
	}
}

func TestBuildAssertionRejectsOversizedTaskID(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildAssertion("runtime-1", strings.Repeat("x", maxTaskIDBytes+1), privateKey, time.Now()); err == nil {
		t.Fatal("超大 task ID 被写入鉴权头")
	}
}

func TestDecryptRegisteredTaskAcceptsAnonymousBoxEnvelope(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha512.Sum512(privateKey.Seed())
	var curvePrivate [32]byte
	copy(curvePrivate[:], digest[:32])
	curvePrivate[0] &= 248
	curvePrivate[31] &= 127
	curvePrivate[31] |= 64
	publicBytes, err := curve25519.X25519(curvePrivate[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	var curvePublic [32]byte
	copy(curvePublic[:], publicBytes)
	ciphertext, err := box.SealAnonymous(nil, []byte("task-encrypted"), &curvePublic, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decryptRegisteredTask(privateKey, base64.StdEncoding.EncodeToString(ciphertext))
	if err != nil || got != "task-encrypted" {
		t.Fatalf("task=%q err=%v", got, err)
	}
}

func TestInvalidTaskResponseRequiresUnauthorizedAndSpecificMarker(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   bool
	}{
		{http.StatusUnauthorized, `{"error":{"code":"task_expired"}}`, true},
		{http.StatusUnauthorized, `task not found`, true},
		{http.StatusUnauthorized, `invalid access token`, false},
		{http.StatusForbidden, `{"code":"invalid_task_id"}`, false},
	} {
		if got := InvalidTaskResponse(tc.status, []byte(tc.body)); got != tc.want {
			t.Fatalf("status=%d body=%q got=%v want=%v", tc.status, tc.body, got, tc.want)
		}
	}
}
