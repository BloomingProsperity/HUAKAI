package hermes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	RunnerURLEnv          = "HUAKAI_HERMES_RUNNER_URL"
	RunnerSharedSecretEnv = "HUAKAI_HERMES_SHARED_SECRET"
	HeaderSignature       = "X-Hermes-Signature"
	HeaderTimestamp       = "X-Hermes-Timestamp"
	HeaderTenant          = "X-Hermes-Tenant"
	HeaderUser            = "X-Hermes-User"
)

type RunnerClient struct {
	baseURL      *url.URL
	sharedSecret []byte
	httpClient   *http.Client
	now          func() time.Time
}

func NewRunnerClientFromEnv() (*RunnerClient, error) {
	runnerURL := strings.TrimSpace(os.Getenv(RunnerURLEnv))
	sharedSecret := strings.TrimSpace(os.Getenv(RunnerSharedSecretEnv))
	if runnerURL == "" && sharedSecret == "" {
		return nil, nil
	}
	return NewRunnerClient(RunnerConfig{
		RunnerURL:    runnerURL,
		SharedSecret: sharedSecret,
	})
}

func NewRunnerClient(cfg RunnerConfig) (*RunnerClient, error) {
	if strings.TrimSpace(cfg.RunnerURL) == "" {
		return nil, fmt.Errorf("%w: %s is required", ErrMisconfigured, RunnerURLEnv)
	}
	if strings.TrimSpace(cfg.SharedSecret) == "" {
		return nil, fmt.Errorf("%w: %s is required", ErrMisconfigured, RunnerSharedSecretEnv)
	}
	u, err := url.Parse(strings.TrimSpace(cfg.RunnerURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("%w: invalid runner url", ErrMisconfigured)
	}
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &RunnerClient{
		baseURL: u, sharedSecret: []byte(cfg.SharedSecret),
		httpClient: client, now: time.Now,
	}, nil
}

func (c *RunnerClient) Chat(ctx context.Context, tenantID, userID int64, body []byte) (*http.Response, error) {
	if err := validateTenantUser(tenantID, userID); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, "/chat", "", tenantID, userID, body, "application/json")
}

func (c *RunnerClient) Conversations(ctx context.Context, tenantID, userID int64, rawQuery string) (*http.Response, error) {
	if err := validateTenantUser(tenantID, userID); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodGet, "/conversations", rawQuery, tenantID, userID, nil, "")
}

func (c *RunnerClient) ConversationMessages(ctx context.Context, tenantID, userID int64, id, rawQuery string) (*http.Response, error) {
	if err := validateTenantUser(tenantID, userID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("%w: conversation id is required", ErrInvalidInput)
	}
	path := "/conversations/" + url.PathEscape(id) + "/messages"
	return c.do(ctx, http.MethodGet, path, rawQuery, tenantID, userID, nil, "")
}

func (c *RunnerClient) Health(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/healthz", "", 0, 0, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: healthz status %d", ErrRunnerFailure, resp.StatusCode)
	}
	return nil
}

func (c *RunnerClient) do(ctx context.Context, method, path, rawQuery string, tenantID, userID int64, body []byte, contentType string) (*http.Response, error) {
	if c == nil || c.baseURL == nil || len(c.sharedSecret) == 0 {
		return nil, ErrMisconfigured
	}
	u := *c.baseURL
	u.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	u.RawQuery = rawQuery
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.sign(req, tenantID, userID, body)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRunnerFailure, err)
	}
	return resp, nil
}

func (c *RunnerClient) sign(req *http.Request, tenantID, userID int64, body []byte) {
	ts := fmt.Sprintf("%d", c.now().UTC().Unix())
	tenant := strconv.FormatInt(tenantID, 10)
	user := strconv.FormatInt(userID, 10)
	method := ""
	path := ""
	rawQuery := ""
	if req != nil {
		method = req.Method
		if req.URL != nil {
			path = req.URL.Path
			rawQuery = req.URL.RawQuery
		}
	}
	mac := hmac.New(sha256.New, c.sharedSecret)
	mac.Write([]byte(ts))
	mac.Write([]byte("\n"))
	mac.Write([]byte(method))
	mac.Write([]byte("\n"))
	mac.Write([]byte(path))
	mac.Write([]byte("\n"))
	mac.Write([]byte(rawQuery))
	mac.Write([]byte("\n"))
	mac.Write([]byte(tenant))
	mac.Write([]byte("\n"))
	mac.Write([]byte(user))
	mac.Write([]byte("\n"))
	mac.Write(body)
	req.Header.Set(HeaderTimestamp, ts)
	req.Header.Set(HeaderTenant, tenant)
	req.Header.Set(HeaderUser, user)
	req.Header.Set(HeaderSignature, hex.EncodeToString(mac.Sum(nil)))
}
