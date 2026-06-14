package hermes

import (
	"bytes"
	"context"
	"crypto/ed25519"
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
	RunnerURLEnv                  = "HUAKAI_HERMES_RUNNER_URL"
	RunnerInternalSharedSecretEnv = "HUAKAI_HERMES_INTERNAL_SHARED_SECRET"
	RunnerJWTPrivateKeyEnv        = "HUAKAI_HERMES_JWT_PRIVATE_KEY_PATH"
	RunnerJWTKIDEnv               = "HUAKAI_HERMES_JWT_KID"
	RunnerJWTIssuerEnv            = "HUAKAI_HERMES_JWT_ISSUER"
	RunnerJWTAudienceEnv          = "HUAKAI_HERMES_JWT_AUDIENCE"
	HeaderAuthorization           = "Authorization"
	HeaderSignature               = "X-Hermes-Signature"
	HeaderTimestamp               = "X-Hermes-Timestamp"
	HeaderTenant                  = "X-Hermes-Tenant"
	HeaderUser                    = "X-Hermes-User"
	RunnerHMACFreshnessLimit      = 5 * time.Minute
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
	if runnerURL == "" && keyPath == "" && jwtKID == "" {
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
		// Bounded egress client (connect/TLS/response-header timeouts, no total
		// timeout so SSE streams are not truncated). Never fall back to the
		// unbounded http.DefaultClient — a sick runner there can brown out the
		// shared core data plane. Tests inject their own client via cfg.HTTPClient.
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

func VerifyRunnerHMACRequest(req *http.Request, body []byte, sharedSecret []byte, now time.Time) bool {
	if req == nil || len(sharedSecret) == 0 {
		return false
	}
	signature := strings.TrimSpace(req.Header.Get(HeaderSignature))
	ts := strings.TrimSpace(req.Header.Get(HeaderTimestamp))
	tenant := strings.TrimSpace(req.Header.Get(HeaderTenant))
	user := strings.TrimSpace(req.Header.Get(HeaderUser))
	if signature == "" || ts == "" || tenant == "" || user == "" {
		return false
	}
	signedAt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if delta := now.UTC().Sub(time.Unix(signedAt, 0).UTC()); delta > RunnerHMACFreshnessLimit || delta < -RunnerHMACFreshnessLimit {
		return false
	}
	path := ""
	rawQuery := ""
	if req.URL != nil {
		path = req.URL.Path
		rawQuery = req.URL.RawQuery
	}
	mac := hmac.New(sha256.New, sharedSecret)
	mac.Write([]byte(ts))
	mac.Write([]byte("\n"))
	mac.Write([]byte(req.Method))
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
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expected))
}
