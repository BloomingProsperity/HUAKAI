package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/BloomingProsperity/HUAKAI/internal/config"
	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermes"
	"github.com/BloomingProsperity/HUAKAI/internal/hermeschat"
)

func TestInternalRunnerBootstrapRequiresHMACAndIssuesVerifiableJWT(t *testing.T) {
	// 回归守护：bootstrap 必须先验证 transition HMAC caller proof，不能裸发 runner JWT。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyStore := hermes.NewKeyStore(newMemoryGatewayJWTKeyQueries())
	now := time.Unix(1700000000, 0).UTC()
	d := &deps{
		cfg: &config.Config{BillingPolicyVersion: "test-1.0", RequestClass: "standard"},
		hermesBootstrapIssuer: &hermes.BootstrapIssuer{
			PrivateKey: privateKey,
			PublicKey:  publicKey,
			KeyStore:   keyStore,
			Now:        func() time.Time { return now },
		},
		hermesRunnerSharedSecret: []byte("runner-secret"),
	}
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	body := []byte(`{"runner_id":"runner-7","tenant_id":7,"actor_user_id":42}`)
	unauthorized := httptest.NewRecorder()
	r.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/internal/runner/bootstrap", bytes.NewReader(body)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status=%d body=%s want 401", unauthorized.Code, unauthorized.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/internal/runner/bootstrap", bytes.NewReader(body))
	signInternalRunnerRequest(req, body, "runner-secret", time.Now().UTC(), "7", "42")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed status=%d body=%s want 200", rec.Code, rec.Body.String())
	}
	token := extractJSONToken(t, rec.Body.String())
	claims, err := hermes.VerifyAt(publicKey, token, now)
	if err != nil {
		t.Fatalf("Verify bootstrap token: %v", err)
	}
	if claims.Sub != "runner-7" || claims.Kid == "" {
		t.Fatalf("claims=%+v want runner subject and kid", claims)
	}
}

func TestInternalRunnerBootstrapRequiresPositiveSignedAuditIdentity(t *testing.T) {
	// 回归守护：不能用 body tenant/user 补洞；签名 header 身份缺失或为 0 时不得签发 JWT。
	d, auditStore, _, _ := newHermesInternalTestDeps(t)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	bodyWithIDs := []byte(`{"runner_id":"runner-7","tenant_id":7,"actor_user_id":42}`)
	zeroTenant := httptest.NewRequest(http.MethodPost, "/internal/runner/bootstrap", bytes.NewReader(bodyWithIDs))
	signInternalRunnerRequest(zeroTenant, bodyWithIDs, "runner-secret", time.Now().UTC(), "0", "42")
	zeroRec := httptest.NewRecorder()
	r.ServeHTTP(zeroRec, zeroTenant)
	if zeroRec.Code != http.StatusUnauthorized {
		t.Fatalf("zero header tenant status=%d body=%s want 401", zeroRec.Code, zeroRec.Body.String())
	}
	if len(auditStore.auditArgs) != 0 {
		t.Fatalf("audit rows after rejected zero tenant=%d want 0", len(auditStore.auditArgs))
	}

	missingTenant := httptest.NewRequest(http.MethodPost, "/internal/runner/bootstrap", bytes.NewReader(bodyWithIDs))
	signInternalRunnerRequest(missingTenant, bodyWithIDs, "runner-secret", time.Now().UTC(), "7", "42")
	missingTenant.Header.Del(hermes.HeaderTenant)
	missingRec := httptest.NewRecorder()
	r.ServeHTTP(missingRec, missingTenant)
	if missingRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing header tenant status=%d body=%s want 401", missingRec.Code, missingRec.Body.String())
	}
	if len(auditStore.auditArgs) != 0 {
		t.Fatalf("audit rows after rejected missing tenant=%d want 0", len(auditStore.auditArgs))
	}

	bodyWithoutIDs := []byte(`{"runner_id":"runner-7"}`)
	complete := httptest.NewRequest(http.MethodPost, "/internal/runner/bootstrap", bytes.NewReader(bodyWithoutIDs))
	signInternalRunnerRequest(complete, bodyWithoutIDs, "runner-secret", time.Now().UTC(), "7", "42")
	completeRec := httptest.NewRecorder()
	r.ServeHTTP(completeRec, complete)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete signed status=%d body=%s want 200", completeRec.Code, completeRec.Body.String())
	}
	if len(auditStore.auditArgs) != 1 {
		t.Fatalf("audit rows after complete signed request=%d want 1", len(auditStore.auditArgs))
	}
	got := auditStore.auditArgs[0]
	if got.TenantID != 7 || got.ActorUserID != 42 || got.Action != hermes.ActionProfileRotate || got.Result != hermes.AuditResultSuccess {
		t.Fatalf("audit arg=%+v want tenant=7 actor=42 rotate success", got)
	}
}

func TestInternalRunnerRefreshRequiresPositiveSignedAuditIdentity(t *testing.T) {
	// 回归守护：refresh 的审计身份必须来自已签名 header，不能由可省略 body 字段决定是否审计。
	d, auditStore, _, now := newHermesInternalTestDeps(t)
	issuedAt := *now
	token, err := d.hermesBootstrapIssuer.IssueBootstrapJWT(context.Background(), "runner-7")
	if err != nil {
		t.Fatalf("IssueBootstrapJWT: %v", err)
	}
	*now = issuedAt.Add(14 * time.Minute)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	bodyWithIDs := []byte(`{"token":"` + token + `","tenant_id":7,"actor_user_id":42}`)
	zeroTenant := httptest.NewRequest(http.MethodPost, "/internal/runner/refresh", bytes.NewReader(bodyWithIDs))
	signInternalRunnerRequest(zeroTenant, bodyWithIDs, "runner-secret", time.Now().UTC(), "0", "42")
	zeroRec := httptest.NewRecorder()
	r.ServeHTTP(zeroRec, zeroTenant)
	if zeroRec.Code != http.StatusUnauthorized {
		t.Fatalf("zero header tenant refresh status=%d body=%s want 401", zeroRec.Code, zeroRec.Body.String())
	}
	if len(auditStore.auditArgs) != 0 {
		t.Fatalf("audit rows after rejected zero tenant refresh=%d want 0", len(auditStore.auditArgs))
	}

	missingTenant := httptest.NewRequest(http.MethodPost, "/internal/runner/refresh", bytes.NewReader(bodyWithIDs))
	signInternalRunnerRequest(missingTenant, bodyWithIDs, "runner-secret", time.Now().UTC(), "7", "42")
	missingTenant.Header.Del(hermes.HeaderTenant)
	missingRec := httptest.NewRecorder()
	r.ServeHTTP(missingRec, missingTenant)
	if missingRec.Code != http.StatusUnauthorized {
		t.Fatalf("missing header tenant refresh status=%d body=%s want 401", missingRec.Code, missingRec.Body.String())
	}
	if len(auditStore.auditArgs) != 0 {
		t.Fatalf("audit rows after rejected missing tenant refresh=%d want 0", len(auditStore.auditArgs))
	}

	bodyWithoutIDs := []byte(`{"token":"` + token + `"}`)
	complete := httptest.NewRequest(http.MethodPost, "/internal/runner/refresh", bytes.NewReader(bodyWithoutIDs))
	signInternalRunnerRequest(complete, bodyWithoutIDs, "runner-secret", time.Now().UTC(), "7", "42")
	completeRec := httptest.NewRecorder()
	r.ServeHTTP(completeRec, complete)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete signed refresh status=%d body=%s want 200", completeRec.Code, completeRec.Body.String())
	}
	if len(auditStore.auditArgs) != 1 {
		t.Fatalf("audit rows after complete signed refresh=%d want 1", len(auditStore.auditArgs))
	}
	got := auditStore.auditArgs[0]
	if got.TenantID != 7 || got.ActorUserID != 42 || got.Action != hermes.ActionProfileRotate || got.Result != hermes.AuditResultSuccess {
		t.Fatalf("refresh audit arg=%+v want tenant=7 actor=42 rotate success", got)
	}
}

func TestBuildHermesChatBridgeRequiresDedicatedInternalTokenSecret(t *testing.T) {
	// Regression: /chat must fail closed when the runner shared secret exists but the bridge token secret is absent.
	t.Setenv(hermes.RunnerSharedSecretEnv, "runner-shared-secret")
	t.Setenv(hermeschat.InternalTokenSecretEnv, "")

	bridge, err := buildHermesChatBridge(hermes.NewService(&hermesAuditStoreSpy{}), nil)
	if !errors.Is(err, hermes.ErrMisconfigured) || bridge != nil {
		t.Fatalf("bridge=%v err=%v want misconfigured nil bridge without %s", bridge, err, hermeschat.InternalTokenSecretEnv)
	}

	t.Setenv(hermeschat.InternalTokenSecretEnv, "dedicated-internal-token-secret")
	bridge, err = buildHermesChatBridge(hermes.NewService(&hermesAuditStoreSpy{}), nil)
	if err != nil || bridge == nil {
		t.Fatalf("bridge=%v err=%v want bridge with explicit %s", bridge, err, hermeschat.InternalTokenSecretEnv)
	}
}

func signInternalRunnerRequest(req *http.Request, body []byte, secret string, now time.Time, tenant, user string) {
	timestamp := "1700000000"
	if !now.IsZero() {
		timestamp = strconv.FormatInt(now.Unix(), 10)
	}
	req.Header.Set(hermes.HeaderTimestamp, timestamp)
	req.Header.Set(hermes.HeaderTenant, tenant)
	req.Header.Set(hermes.HeaderUser, user)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("\n"))
	mac.Write([]byte(req.Method))
	mac.Write([]byte("\n"))
	mac.Write([]byte(req.URL.Path))
	mac.Write([]byte("\n"))
	mac.Write([]byte(req.URL.RawQuery))
	mac.Write([]byte("\n"))
	mac.Write([]byte(tenant))
	mac.Write([]byte("\n"))
	mac.Write([]byte(user))
	mac.Write([]byte("\n"))
	mac.Write(body)
	req.Header.Set(hermes.HeaderSignature, hex.EncodeToString(mac.Sum(nil)))
}

func extractJSONToken(t *testing.T, body string) string {
	t.Helper()
	const marker = `"token":"`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("body=%s missing token", body)
	}
	start += len(marker)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		t.Fatalf("body=%s unterminated token", body)
	}
	return body[start : start+end]
}

func newHermesInternalTestDeps(t *testing.T) (*deps, *hermesAuditStoreSpy, ed25519.PublicKey, *time.Time) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyStore := hermes.NewKeyStore(newMemoryGatewayJWTKeyQueries())
	now := time.Unix(1700000000, 0).UTC()
	auditStore := &hermesAuditStoreSpy{}
	d := &deps{
		cfg: &config.Config{BillingPolicyVersion: "test-1.0", RequestClass: "standard"},
		hermesBootstrapIssuer: &hermes.BootstrapIssuer{
			PrivateKey: privateKey,
			PublicKey:  publicKey,
			KeyStore:   keyStore,
			Now:        func() time.Time { return now },
		},
		hermesRunnerSharedSecret: []byte("runner-secret"),
		hermesService:            hermes.NewService(auditStore),
	}
	return d, auditStore, publicKey, &now
}

type memoryGatewayJWTKeyQueries struct {
	keys map[string]dbhermes.HermesJwtKey
}

func newMemoryGatewayJWTKeyQueries() *memoryGatewayJWTKeyQueries {
	return &memoryGatewayJWTKeyQueries{keys: make(map[string]dbhermes.HermesJwtKey)}
}

func (m *memoryGatewayJWTKeyQueries) InsertJWTKey(_ context.Context, arg dbhermes.InsertJWTKeyParams) (dbhermes.HermesJwtKey, error) {
	now := time.Unix(1700000000, 0).UTC()
	row := dbhermes.HermesJwtKey{
		Kid:          arg.Kid,
		Alg:          arg.Alg,
		PublicKeyPem: arg.PublicKeyPem,
		ValidFrom:    pgtype.Timestamptz{Time: now, Valid: true},
		ValidUntil:   arg.ValidUntil,
		CreatedAt:    pgtype.Timestamptz{Time: now, Valid: true},
	}
	m.keys[arg.Kid] = row
	return row, nil
}

func (m *memoryGatewayJWTKeyQueries) GetActiveJWTKeys(context.Context) ([]dbhermes.HermesJwtKey, error) {
	rows := make([]dbhermes.HermesJwtKey, 0, len(m.keys))
	for _, row := range m.keys {
		rows = append(rows, row)
	}
	return rows, nil
}

func (m *memoryGatewayJWTKeyQueries) GetJWTKeyByKid(_ context.Context, kid string) (dbhermes.HermesJwtKey, error) {
	row, ok := m.keys[kid]
	if !ok {
		return dbhermes.HermesJwtKey{}, hermes.ErrNotFound
	}
	return row, nil
}

func (m *memoryGatewayJWTKeyQueries) RevokeJWTKey(_ context.Context, kid string) (int64, error) {
	row, ok := m.keys[kid]
	if !ok {
		return 0, nil
	}
	row.RevokedAt = pgtype.Timestamptz{Time: time.Unix(1700000000, 0).UTC(), Valid: true}
	m.keys[kid] = row
	return 1, nil
}

type hermesAuditStoreSpy struct {
	auditArgs []dbhermes.InsertAuditEventParams
}

func (s *hermesAuditStoreSpy) AppendMessage(context.Context, dbhermes.AppendMessageParams) (int64, error) {
	return 1, nil
}

func (s *hermesAuditStoreSpy) CreateConversation(context.Context, dbhermes.CreateConversationParams) (int64, error) {
	return 1, nil
}

func (s *hermesAuditStoreSpy) CreateProfile(context.Context, dbhermes.CreateProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}

func (s *hermesAuditStoreSpy) DeleteProfile(context.Context, dbhermes.DeleteProfileParams) (int64, error) {
	return 0, nil
}

func (s *hermesAuditStoreSpy) DisableHermes(context.Context, dbhermes.DisableHermesParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

func (s *hermesAuditStoreSpy) GetAPIKeyOwner(context.Context, dbhermes.GetAPIKeyOwnerParams) (int64, error) {
	return 0, nil
}

func (s *hermesAuditStoreSpy) GetConversation(context.Context, dbhermes.GetConversationParams) (dbhermes.HermesConversation, error) {
	return dbhermes.HermesConversation{}, nil
}

func (s *hermesAuditStoreSpy) ListConversationsByOwner(context.Context, dbhermes.ListConversationsByOwnerParams) ([]dbhermes.HermesConversation, error) {
	return nil, nil
}

func (s *hermesAuditStoreSpy) ListMessagesByConversation(context.Context, dbhermes.ListMessagesByConversationParams) ([]dbhermes.HermesMessage, error) {
	return nil, nil
}

func (s *hermesAuditStoreSpy) GetProfile(context.Context, dbhermes.GetProfileParams) (dbhermes.HermesApiProfile, error) {
	return dbhermes.HermesApiProfile{}, nil
}

func (s *hermesAuditStoreSpy) GetSettings(context.Context, dbhermes.GetSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}

func (s *hermesAuditStoreSpy) InsertAuditEvent(_ context.Context, arg dbhermes.InsertAuditEventParams) (dbhermes.HermesAuditEvent, error) {
	s.auditArgs = append(s.auditArgs, arg)
	return dbhermes.HermesAuditEvent{
		ID:          int64(len(s.auditArgs)),
		TenantID:    arg.TenantID,
		ActorUserID: arg.ActorUserID,
		Action:      arg.Action,
		Result:      arg.Result,
	}, nil
}

func (s *hermesAuditStoreSpy) ListProfilesByOwner(context.Context, dbhermes.ListProfilesByOwnerParams) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *hermesAuditStoreSpy) ListProfilesByTenant(context.Context, int64) ([]dbhermes.HermesApiProfile, error) {
	return nil, nil
}

func (s *hermesAuditStoreSpy) ProfileInUse(context.Context, dbhermes.ProfileInUseParams) (bool, error) {
	return false, nil
}

func (s *hermesAuditStoreSpy) SoftDeleteConversation(context.Context, dbhermes.SoftDeleteConversationParams) (int64, error) {
	return 0, nil
}

func (s *hermesAuditStoreSpy) UpdateConversationLastMessageAt(context.Context, dbhermes.UpdateConversationLastMessageAtParams) (int64, error) {
	return 1, nil
}

func (s *hermesAuditStoreSpy) UpsertSettings(context.Context, dbhermes.UpsertSettingsParams) (dbhermes.HermesSetting, error) {
	return dbhermes.HermesSetting{}, nil
}
