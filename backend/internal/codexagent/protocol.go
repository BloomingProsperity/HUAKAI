package codexagent

import (
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

const assertionPrefix = "AgentAssertion "

const maxTaskIDBytes = 4096

func buildAssertion(runtimeID, taskID string, privateKey ed25519.PrivateKey, now time.Time) (string, error) {
	runtimeID = strings.TrimSpace(runtimeID)
	taskID = strings.TrimSpace(taskID)
	if runtimeID == "" || taskID == "" || len(taskID) > maxTaskIDBytes || len(privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("codex agent: assertion material incomplete")
	}
	timestamp := now.UTC().Format(time.RFC3339)
	signature := ed25519.Sign(privateKey, []byte(runtimeID+":"+taskID+":"+timestamp))
	envelope, err := json.Marshal(struct {
		RuntimeID string `json:"agent_runtime_id"`
		TaskID    string `json:"task_id"`
		Timestamp string `json:"timestamp"`
		Signature string `json:"signature"`
	}{
		RuntimeID: runtimeID, TaskID: taskID, Timestamp: timestamp,
		Signature: base64.StdEncoding.EncodeToString(signature),
	})
	if err != nil {
		return "", errors.New("codex agent: assertion encoding failed")
	}
	return assertionPrefix + base64.RawURLEncoding.EncodeToString(envelope), nil
}

func registrationProof(runtimeID string, privateKey ed25519.PrivateKey, now time.Time) (string, string, error) {
	runtimeID = strings.TrimSpace(runtimeID)
	if runtimeID == "" || len(privateKey) != ed25519.PrivateKeySize {
		return "", "", errors.New("codex agent: registration material incomplete")
	}
	timestamp := now.UTC().Format(time.RFC3339)
	signature := ed25519.Sign(privateKey, []byte(runtimeID+":"+timestamp))
	return timestamp, base64.StdEncoding.EncodeToString(signature), nil
}

func decryptRegisteredTask(privateKey ed25519.PrivateKey, encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		return "", errors.New("codex agent: encrypted task is invalid")
	}
	digest := sha512.Sum512(privateKey.Seed())
	var curvePrivate [32]byte
	defer func() {
		clear(digest[:])
		clear(curvePrivate[:])
	}()
	copy(curvePrivate[:], digest[:32])
	curvePrivate[0] &= 248
	curvePrivate[31] &= 127
	curvePrivate[31] |= 64
	publicBytes, err := curve25519.X25519(curvePrivate[:], curve25519.Basepoint)
	if err != nil {
		return "", errors.New("codex agent: task decryption key derivation failed")
	}
	var curvePublic [32]byte
	copy(curvePublic[:], publicBytes)
	plaintext, ok := box.OpenAnonymous(nil, ciphertext, &curvePublic, &curvePrivate)
	defer privacy.Zeroize(plaintext)
	taskID := strings.TrimSpace(string(plaintext))
	if !ok || taskID == "" || len(taskID) > maxTaskIDBytes {
		return "", errors.New("codex agent: encrypted task cannot be opened")
	}
	return taskID, nil
}

func InvalidTaskResponse(status int, body []byte) bool {
	if status != http.StatusUnauthorized {
		return false
	}
	lower := strings.ToLower(string(body))
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(lower)
	for _, marker := range []string{
		`"code":"invalid_task_id"`, `"code":"task_not_found"`, `"code":"task_expired"`, `"error":"invalid_task_id"`,
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	for _, marker := range []string{
		"invalid task_id", "invalid task id", "task_id is invalid", "task id is invalid",
		"task not found", "task expired", "unknown task_id", "unknown task id",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
