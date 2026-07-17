package codexagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const officialRegistrationBaseURL = "https://auth.openai.com/api/accounts"

type registrationClient struct {
	client        *http.Client
	proxyResolver provider.ProxyResolver
	baseURL       string
	now           func() time.Time
}

func newRegistrationClient(client *http.Client, proxyResolver provider.ProxyResolver) *registrationClient {
	if client == nil {
		client = &http.Client{}
	}
	return &registrationClient{client: client, proxyResolver: proxyResolver, baseURL: officialRegistrationBaseURL, now: time.Now}
}

func (c *registrationClient) register(ctx context.Context, accountID int64, material identityMaterial) (string, error) {
	timestamp, signature, err := registrationProof(material.RuntimeID, material.privateKey, c.now())
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{"timestamp": timestamp, "signature": signature})
	if err != nil {
		return "", errors.New("codex agent: registration request encoding failed")
	}
	endpoint := strings.TrimRight(c.baseURL, "/") + "/v1/agent/" + url.PathEscape(material.RuntimeID) + "/task/register"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", errors.New("codex agent: registration request construction failed")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	client, err := c.clientForAccount(ctx, accountID)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.New("codex agent: registration request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("codex agent: registration returned status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return "", errors.New("codex agent: registration response is invalid")
	}
	var result struct {
		TaskID             string `json:"task_id"`
		TaskIDAlt          string `json:"taskId"`
		EncryptedTaskID    string `json:"encrypted_task_id"`
		EncryptedTaskIDAlt string `json:"encryptedTaskId"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", errors.New("codex agent: registration response is invalid")
	}
	if taskID := firstNonEmpty(result.TaskID, result.TaskIDAlt); taskID != "" && len(taskID) <= maxTaskIDBytes {
		return taskID, nil
	}
	encrypted := firstNonEmpty(result.EncryptedTaskID, result.EncryptedTaskIDAlt)
	if encrypted == "" {
		return "", errors.New("codex agent: registration response omitted task")
	}
	return decryptRegisteredTask(material.privateKey, encrypted)
}

func (c *registrationClient) clientForAccount(ctx context.Context, accountID int64) (*http.Client, error) {
	clone := *c.client
	clone.Timeout = 30 * time.Second
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := clone.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if standard, ok := transport.(*http.Transport); ok {
		transport = standard.Clone()
		transport.(*http.Transport).ResponseHeaderTimeout = 15 * time.Second
	}
	if c.proxyResolver == nil {
		clone.Transport = transport
		return &clone, nil
	}
	proxyURL, err := c.proxyResolver.Resolve(ctx, accountID)
	if errors.Is(err, provider.ErrAccountNotFound) {
		clone.Transport = transport
		return &clone, nil
	}
	if err != nil {
		return nil, fmt.Errorf("codex agent: proxy resolve failed: %w", err)
	}
	clone.Transport = provider.WrapTransportWithProxy(transport, proxyURL)
	return &clone, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
