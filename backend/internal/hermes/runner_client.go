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
	RunnerURLEnv             = "HUAKAI_HERMES_RUNNER_URL"
	RunnerSharedSecretEnv    = "HUAKAI_HERMES_SHARED_SECRET"
	RunnerJWTPrivateKeyEnv   = "HUAKAI_HERMES_JWT_PRIVATE_KEY_PATH"
	RunnerJWTKIDEnv          = "HUAKAI_HERMES_JWT_KID"
	RunnerJWTIssuerEnv       = "HUAKAI_HERMES_JWT_ISSUER"
	RunnerJWTAudienceEnv     = "HUAKAI_HERMES_JWT_AUDIENCE"
	RunnerClientAuthModeEnv  = "HUAKAI_HERMES_CLIENT_AUTH_MODE"
	RunnerClientAuthModeHMAC = "hmac"
	RunnerClientAuthModeJWT  = "jwt"
	HeaderAuthorization      = "Authorization"
	HeaderSignature          = "X-Hermes-Signature"
	HeaderTimestamp          = "X-Hermes-Timestamp"
	HeaderTenant             = "X-Hermes-Tenant"
	HeaderUser               = "X-Hermes-User"
	RunnerHMACFreshnessLimit = 5 * time.Minute
)

type RunnerClient struct {
	baseURL        *url.URL
	sharedSecret   []byte
	jwtPrivateKey  ed25519.PrivateKey
	jwtKID         string
	jwtIssuer      string
	jwtAudience    string
	clientAuthMode string
	httpClient     *http.Client
	now            func() time.Time
}

func NewRunnerClientFromEnv() (*RunnerClient, error) {
	runnerURL := strings.TrimSpace(os.Getenv(RunnerURLEnv))
	sharedSecret := strings.TrimSpace(os.Getenv(RunnerSharedSecretEnv))
	keyPath := strings.TrimSpace(os.Getenv(RunnerJWTPrivateKeyEnv))
	jwtKID := strings.TrimSpace(os.Getenv(RunnerJWTKIDEnv))
	if runnerURL == "" && sharedSecret == "" && keyPath == "" && jwtKID == "" {
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
		RunnerURL:      runnerURL,
		SharedSecret:   sharedSecret,
		JWTPrivateKey:  privateKey,
		JWTKID:         jwtKID,
		JWTIssuer:      os.Getenv(RunnerJWTIssuerEnv),
		JWTAudience:    os.Getenv(RunnerJWTAudienceEnv),
		ClientAuthMode: os.Getenv(RunnerClientAuthModeEnv),
	})
}

func NewRunnerClient(cfg RunnerConfig) (*RunnerClient, error) {
	if strings.TrimSpace(cfg.RunnerURL) == "" {
		return nil, fmt.Errorf("%w: %s is required", ErrMisconfigured, RunnerURLEnv)
	}
	hasHMAC := strings.TrimSpace(cfg.SharedSecret) != ""
	hasJWT := len(cfg.JWTPrivateKey) == ed25519.PrivateKeySize || strings.TrimSpace(cfg.JWTKID) != ""
	if !hasHMAC && !hasJWT {
		return nil, fmt.Errorf("%w: runner HMAC secret or JWT key is required", ErrMisconfigured)
	}
	if hasJWT {
		if len(cfg.JWTPrivateKey) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("%w: %s is required for JWT mode", ErrMisconfigured, RunnerJWTPrivateKeyEnv)
		}
		if strings.TrimSpace(cfg.JWTKID) == "" {
			return nil, fmt.Errorf("%w: %s is required for JWT mode", ErrMisconfigured, RunnerJWTKIDEnv)
		}
	}
	clientAuthMode, err := resolveRunnerClientAuthMode(cfg.ClientAuthMode, hasHMAC, hasJWT)
	if err != nil {
		return nil, err
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
		jwtPrivateKey: cfg.JWTPrivateKey, jwtKID: strings.TrimSpace(cfg.JWTKID),
		jwtIssuer: strings.TrimSpace(cfg.JWTIssuer), jwtAudience: strings.TrimSpace(cfg.JWTAudience),
		clientAuthMode: clientAuthMode, httpClient: client, now: time.Now,
	}, nil
}

func resolveRunnerClientAuthMode(raw string, hasHMAC, hasJWT bool) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		if hasHMAC {
			return RunnerClientAuthModeHMAC, nil
		}
		return RunnerClientAuthModeJWT, nil
	}
	switch mode {
	case RunnerClientAuthModeHMAC:
		if !hasHMAC {
			return "", fmt.Errorf("%w: %s=hmac requires %s", ErrMisconfigured, RunnerClientAuthModeEnv, RunnerSharedSecretEnv)
		}
		return RunnerClientAuthModeHMAC, nil
	case RunnerClientAuthModeJWT:
		if !hasJWT {
			return "", fmt.Errorf("%w: %s=jwt requires runner JWT key", ErrMisconfigured, RunnerClientAuthModeEnv)
		}
		return RunnerClientAuthModeJWT, nil
	default:
		return "", fmt.Errorf("%w: %s must be hmac or jwt", ErrMisconfigured, RunnerClientAuthModeEnv)
	}
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
	if c == nil || c.baseURL == nil || (len(c.sharedSecret) == 0 && len(c.jwtPrivateKey) != ed25519.PrivateKeySize) {
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

func (c *RunnerClient) sign(req *http.Request, tenantID, userID int64, body []byte) {
	c.signHMAC(req, tenantID, userID, body)
}

func (c *RunnerClient) authenticate(req *http.Request, tenantID, userID int64, body []byte) error {
	if req == nil {
		return ErrMisconfigured
	}
	req.Header.Set(HeaderTenant, strconv.FormatInt(tenantID, 10))
	req.Header.Set(HeaderUser, strconv.FormatInt(userID, 10))
	if c.clientAuthMode == RunnerClientAuthModeJWT {
		return c.signJWT(req, tenantID, userID)
	}
	c.signHMAC(req, tenantID, userID, body)
	return nil
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

func (c *RunnerClient) signHMAC(req *http.Request, tenantID, userID int64, body []byte) {
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
	mac := hmac.New(sha256.New, sharedSecret)
	mac.Write([]byte(ts))
	mac.Write([]byte("\n"))
	mac.Write([]byte(req.Method))
	mac.Write([]byte("\n"))
	path := ""
	rawQuery := ""
	if req.URL != nil {
		path = req.URL.Path
		rawQuery = req.URL.RawQuery
	}
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
