package hermes

import (
	"bytes"
	"context"
	"crypto/ed25519"
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
	RunnerURLEnv           = "HUAKAI_HERMES_RUNNER_URL"
	RunnerJWTPrivateKeyEnv = "HUAKAI_HERMES_JWT_PRIVATE_KEY_PATH"
	RunnerJWTKIDEnv        = "HUAKAI_HERMES_JWT_KID"
	RunnerJWTIssuerEnv     = "HUAKAI_HERMES_JWT_ISSUER"
	RunnerJWTAudienceEnv   = "HUAKAI_HERMES_JWT_AUDIENCE"
	HeaderAuthorization    = "Authorization"
	HeaderTenant           = "X-Hermes-Tenant"
	HeaderUser             = "X-Hermes-User"
)

type RunnerClient struct {
	baseURL       *url.URL
	jwtPrivateKey ed25519.PrivateKey
	jwtKID        string
	jwtIssuer     string
	jwtAudience   string
	httpClient    *http.Client
	now           func() time.Time
}

func NewRunnerClientFromEnv() (*RunnerClient, error) {
	runnerURL := strings.TrimSpace(os.Getenv(RunnerURLEnv))
	keyPath := strings.TrimSpace(os.Getenv(RunnerJWTPrivateKeyEnv))
	jwtKID := strings.TrimSpace(os.Getenv(RunnerJWTKIDEnv))
	// URL 是唯一启用信号。部署文件可以长期挂载密钥目录，但未配置运行器地址时不会误开启 Hermes。
	if runnerURL == "" {
		return nil, nil
	}
	var privateKey ed25519.PrivateKey
	if keyPath != "" {
		key, err := LoadPrivateKey(keyPath)
		if err != nil {
			return nil, err
		}
		privateKey = key
	}
	return NewRunnerClient(RunnerConfig{
		RunnerURL:     runnerURL,
		JWTPrivateKey: privateKey,
		JWTKID:        jwtKID,
		JWTIssuer:     os.Getenv(RunnerJWTIssuerEnv),
		JWTAudience:   os.Getenv(RunnerJWTAudienceEnv),
	})
}

func NewRunnerClient(cfg RunnerConfig) (*RunnerClient, error) {
	if strings.TrimSpace(cfg.RunnerURL) == "" {
		return nil, fmt.Errorf("%w: %s is required", ErrMisconfigured, RunnerURLEnv)
	}
	if len(cfg.JWTPrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: %s is required", ErrMisconfigured, RunnerJWTPrivateKeyEnv)
	}
	if strings.TrimSpace(cfg.JWTKID) == "" {
		return nil, fmt.Errorf("%w: %s is required", ErrMisconfigured, RunnerJWTKIDEnv)
	}
	u, err := url.Parse(strings.TrimSpace(cfg.RunnerURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("%w: invalid runner url", ErrMisconfigured)
	}
	client := cfg.HTTPClient
	if client == nil {
		// 有界的出口 client（连接/TLS/响应头超时，但不设总超时，避免 SSE
		// 流被截断）。绝不回退到无界的 http.DefaultClient——在那里一个生病的
		// runner 会拖垮共享的核心数据面。测试通过 cfg.HTTPClient 注入自己的 client。
		client = defaultRunnerHTTPClient()
	}
	return &RunnerClient{
		baseURL:       u,
		jwtPrivateKey: cfg.JWTPrivateKey, jwtKID: strings.TrimSpace(cfg.JWTKID),
		jwtIssuer: strings.TrimSpace(cfg.JWTIssuer), jwtAudience: strings.TrimSpace(cfg.JWTAudience),
		httpClient: client, now: time.Now,
	}, nil
}

func (c *RunnerClient) Chat(ctx context.Context, tenantID, userID int64, body []byte) (*http.Response, error) {
	if err := validateTenantUser(tenantID, userID); err != nil {
		return nil, err
	}
	return c.do(ctx, http.MethodPost, "/chat", "", tenantID, userID, body, "application/json")
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
	if c == nil || c.baseURL == nil || len(c.jwtPrivateKey) != ed25519.PrivateKeySize || strings.TrimSpace(c.jwtKID) == "" {
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
	if err := c.authenticate(req, tenantID, userID, body); err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRunnerFailure, err)
	}
	return resp, nil
}

func (c *RunnerClient) authenticate(req *http.Request, tenantID, userID int64, _ []byte) error {
	if req == nil {
		return ErrMisconfigured
	}
	req.Header.Set(HeaderTenant, strconv.FormatInt(tenantID, 10))
	req.Header.Set(HeaderUser, strconv.FormatInt(userID, 10))
	return c.signJWT(req, tenantID, userID)
}

func (c *RunnerClient) signJWT(req *http.Request, tenantID, userID int64) error {
	now := c.now().UTC()
	issuer := c.jwtIssuer
	if issuer == "" {
		issuer = DefaultJWTIssuer
	}
	audience := c.jwtAudience
	if audience == "" {
		audience = DefaultJWTAudience
	}
	token, err := Sign(c.jwtPrivateKey, c.jwtKID, Claims{
		Iss: issuer,
		Aud: audience,
		Sub: fmt.Sprintf("%d:%d", tenantID, userID),
		Iat: now.Unix(),
		Nbf: now.Unix(),
		Exp: now.Add(DefaultJWTTTL).Unix(),
	})
	if err != nil {
		return err
	}
	req.Header.Set(HeaderAuthorization, "Bearer "+token)
	return nil
}
