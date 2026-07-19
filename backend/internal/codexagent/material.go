// Package codexagent 实现 Codex Agent Identity 凭据的校验、短时签名与任务登记协议。
package codexagent

import (
	"context"
	"crypto/ed25519"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

const (
	DefaultTaskServiceURL = "https://auth.openai.com/api/accounts"
	maxPayloadBytes       = 256 << 10
	maxResponseBytes      = 64 << 10
)

var (
	ErrInvalidMaterial = errors.New("codexagent: 凭据材料无效")
	ErrTaskUnavailable = errors.New("codexagent: 任务标识不可用")
)

type document struct {
	RuntimeID  string
	PrivateKey ed25519.PrivateKey
	TaskID     string
	Fields     map[string]json.RawMessage
}

// TaskBroker 只向部署代码固定注入的任务服务发起登记，不读取凭据中的目标地址。
type TaskBroker struct {
	client  *http.Client
	baseURL string
	now     func() time.Time
}

// NewTaskBroker 创建使用官方固定任务服务的登记器。
func NewTaskBroker(client *http.Client) *TaskBroker {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	clone := *client
	if clone.Timeout <= 0 || clone.Timeout > 30*time.Second {
		clone.Timeout = 30 * time.Second
	}
	clone.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &TaskBroker{client: &clone, baseURL: DefaultTaskServiceURL, now: time.Now}
}

// ValidatePayload 校验加密载荷结构；requireTask 控制是否要求已登记任务。
func ValidatePayload(raw []byte, requireTask bool) error {
	doc, err := decodeDocument(raw, requireTask)
	if doc.PrivateKey != nil {
		wipe(doc.PrivateKey)
	}
	return err
}

// BuildAuthorization 为单次出站请求生成新鲜的授权断言。
func BuildAuthorization(raw []byte, now time.Time) (string, map[string]string, error) {
	doc, err := decodeDocument(raw, true)
	if err != nil {
		return "", nil, err
	}
	defer wipe(doc.PrivateKey)
	if now.IsZero() {
		now = time.Now()
	}
	timestamp := now.UTC().Format(time.RFC3339)
	message := []byte(doc.RuntimeID + ":" + doc.TaskID + ":" + timestamp)
	signature := ed25519.Sign(doc.PrivateKey, message)
	envelope := struct {
		Runtime   string `json:"agent_runtime_id"`
		Task      string `json:"task_id"`
		IssuedAt  string `json:"timestamp"`
		Signature string `json:"signature"`
	}{
		Runtime: doc.RuntimeID, Task: doc.TaskID, IssuedAt: timestamp,
		Signature: base64.StdEncoding.EncodeToString(signature),
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return "", nil, fmt.Errorf("%w: 无法生成授权载荷", ErrInvalidMaterial)
	}
	extra := collectRuntimeMetadata(doc.Fields)
	extra["auth_header"] = "Authorization"
	return "AgentAssertion " + base64.RawURLEncoding.EncodeToString(encoded), extra, nil
}

// EnsureTask 保留有效的已有任务；缺失时向固定任务服务登记并返回新载荷。
func (b *TaskBroker) EnsureTask(ctx context.Context, raw []byte) ([]byte, error) {
	doc, err := decodeDocument(raw, false)
	if err != nil {
		return nil, err
	}
	defer wipe(doc.PrivateKey)
	if doc.TaskID != "" {
		return append([]byte(nil), raw...), nil
	}
	return b.register(ctx, doc)
}

// RenewTask 强制重新登记任务，用于上游明确拒绝旧任务后的恢复。
func (b *TaskBroker) RenewTask(ctx context.Context, raw []byte) ([]byte, error) {
	doc, err := decodeDocument(raw, false)
	if err != nil {
		return nil, err
	}
	defer wipe(doc.PrivateKey)
	return b.register(ctx, doc)
}

func (b *TaskBroker) register(ctx context.Context, doc document) ([]byte, error) {
	if b == nil || b.client == nil || strings.TrimSpace(b.baseURL) == "" {
		return nil, fmt.Errorf("%w: 任务登记器未配置", ErrTaskUnavailable)
	}
	if err := validateRuntimeID(doc.RuntimeID); err != nil {
		return nil, err
	}
	now := time.Now
	if b.now != nil {
		now = b.now
	}
	timestamp := now().UTC().Format(time.RFC3339)
	signature := ed25519.Sign(doc.PrivateKey, []byte(doc.RuntimeID+":"+timestamp))
	body, err := json.Marshal(struct {
		IssuedAt  string `json:"timestamp"`
		Signature string `json:"signature"`
	}{IssuedAt: timestamp, Signature: base64.StdEncoding.EncodeToString(signature)})
	if err != nil {
		return nil, fmt.Errorf("%w: 无法生成任务登记载荷", ErrInvalidMaterial)
	}
	endpoint := strings.TrimRight(b.baseURL, "/") + "/v1/agent/" + doc.RuntimeID + "/task/register"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%w: 无法创建任务登记请求", ErrTaskUnavailable)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: 任务登记请求失败", ErrTaskUnavailable)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%w: 任务服务返回状态 %d", ErrTaskUnavailable, resp.StatusCode)
	}
	response, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(response) > maxResponseBytes {
		return nil, fmt.Errorf("%w: 任务服务响应无效", ErrTaskUnavailable)
	}
	var result struct {
		TaskSnake      string `json:"task_id"`
		TaskCamel      string `json:"taskId"`
		EncryptedSnake string `json:"encrypted_task_id"`
		EncryptedCamel string `json:"encryptedTaskId"`
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return nil, fmt.Errorf("%w: 任务服务响应不是有效 JSON", ErrTaskUnavailable)
	}
	taskID := firstNonEmpty(result.TaskSnake, result.TaskCamel)
	if taskID == "" {
		encrypted := firstNonEmpty(result.EncryptedSnake, result.EncryptedCamel)
		if encrypted == "" {
			return nil, fmt.Errorf("%w: 任务服务未返回任务标识", ErrTaskUnavailable)
		}
		taskID, err = openEncryptedTask(doc.PrivateKey, encrypted)
		if err != nil {
			return nil, err
		}
	}
	doc.Fields["task_id"], _ = json.Marshal(taskID)
	updated, err := json.Marshal(doc.Fields)
	if err != nil {
		return nil, fmt.Errorf("%w: 无法保存任务标识", ErrInvalidMaterial)
	}
	return updated, nil
}

func decodeDocument(raw []byte, requireTask bool) (document, error) {
	if len(raw) == 0 || len(raw) > maxPayloadBytes {
		return document{}, fmt.Errorf("%w: 载荷为空或过大", ErrInvalidMaterial)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return document{}, fmt.Errorf("%w: 载荷必须是 JSON 对象", ErrInvalidMaterial)
	}
	runtimeID := stringField(fields, "agent_runtime_id")
	if err := validateRuntimeID(runtimeID); err != nil {
		return document{}, err
	}
	accountID := stringField(fields, "account_id")
	userID := stringField(fields, "chatgpt_user_id")
	if accountID == "" || userID == "" {
		return document{}, fmt.Errorf("%w: account_id 和 chatgpt_user_id 必填", ErrInvalidMaterial)
	}
	privateKey, err := parsePrivateKey(stringField(fields, "agent_private_key"))
	if err != nil {
		return document{}, err
	}
	taskID := stringField(fields, "task_id")
	if requireTask && taskID == "" {
		wipe(privateKey)
		return document{}, fmt.Errorf("%w: task_id 缺失", ErrTaskUnavailable)
	}
	return document{RuntimeID: runtimeID, PrivateKey: privateKey, TaskID: taskID, Fields: fields}, nil
}

func parsePrivateKey(encoded string) (ed25519.PrivateKey, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("%w: agent_private_key 缺失", ErrInvalidMaterial)
	}
	der, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: agent_private_key 不是有效 Base64", ErrInvalidMaterial)
	}
	defer wipe(der)
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("%w: agent_private_key 不是有效 PKCS#8", ErrInvalidMaterial)
	}
	privateKey, ok := parsed.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: agent_private_key 必须是 Ed25519 私钥", ErrInvalidMaterial)
	}
	return append(ed25519.PrivateKey(nil), privateKey...), nil
}

func openEncryptedTask(privateKey ed25519.PrivateKey, encoded string) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return "", fmt.Errorf("%w: 加密任务标识不是有效 Base64", ErrTaskUnavailable)
	}
	defer wipe(ciphertext)
	digest := sha512.Sum512(privateKey.Seed())
	var secret [32]byte
	copy(secret[:], digest[:32])
	secret[0] &= 248
	secret[31] &= 127
	secret[31] |= 64
	publicBytes, err := curve25519.X25519(secret[:], curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("%w: 无法派生任务解密密钥", ErrTaskUnavailable)
	}
	var public [32]byte
	copy(public[:], publicBytes)
	plaintext, ok := box.OpenAnonymous(nil, ciphertext, &public, &secret)
	wipe(secret[:])
	if !ok {
		return "", fmt.Errorf("%w: 无法解密任务标识", ErrTaskUnavailable)
	}
	defer wipe(plaintext)
	taskID := strings.TrimSpace(string(plaintext))
	if taskID == "" {
		return "", fmt.Errorf("%w: 解密后的任务标识为空", ErrTaskUnavailable)
	}
	return taskID, nil
}

func validateRuntimeID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 {
		return fmt.Errorf("%w: agent_runtime_id 缺失或过长", ErrInvalidMaterial)
	}
	for _, r := range value {
		if r == '/' || r == '\\' || r == '?' || r == '#' || unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("%w: agent_runtime_id 含非法路径字符", ErrInvalidMaterial)
		}
	}
	return nil
}

func collectRuntimeMetadata(fields map[string]json.RawMessage) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"account_id", "chatgpt_account_id", "chatgpt_user_id", "user_agent", "originator", "oai_device_id"} {
		if value := stringField(fields, key); value != "" {
			out[key] = value
		}
	}
	if out["chatgpt_account_id"] == "" && out["account_id"] != "" {
		out["chatgpt_account_id"] = out["account_id"]
	}
	return out
}

func stringField(fields map[string]json.RawMessage, key string) string {
	var value string
	if raw, ok := fields[key]; ok && json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// IsTaskInvalidResponse 只接受上游明确给出的任务失效错误码，普通 401 不触发任务重建。
func IsTaskInvalidResponse(statusCode int, body []byte) bool {
	if statusCode != http.StatusUnauthorized || len(body) == 0 || len(body) > maxResponseBytes {
		return false
	}
	var envelope struct {
		Code  string          `json:"code"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	if isTaskInvalidCode(envelope.Code) {
		return true
	}
	var nested struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(envelope.Error, &nested) == nil && isTaskInvalidCode(nested.Code) {
		return true
	}
	var direct string
	return json.Unmarshal(envelope.Error, &direct) == nil && isTaskInvalidCode(direct)
}

func isTaskInvalidCode(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "invalid_task_id", "task_not_found", "task_expired":
		return true
	default:
		return false
	}
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
