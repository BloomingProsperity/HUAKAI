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
	// Regression: bootstrap must authenticate the internal caller before issuing a gateway-signed runner JWT.
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

	unsigned := httptest.NewRequest(http.MethodPost, "/internal/runner/bootstrap", bytes.NewReader(body))
	setRunnerIdentityHeaders(unsigned, "7", "42")
	unsignedRec := httptest.NewRecorder()
	r.ServeHTTP(unsignedRec, unsigned)
	if unsignedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned status=%d body=%s want 401", unsignedRec.Code, unsignedRec.Body.String())
	}

	wrong := httptest.NewRequest(http.MethodPost, "/internal/runner/bootstrap", bytes.NewReader(body))
	signInternalRunnerRequest(wrong, body, "wrong-secret", time.Now().UTC(), "7", "42")
	wrongRec := httptest.NewRecorder()
	r.ServeHTTP(wrongRec, wrong)
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong HMAC status=%d body=%s want 401", wrongRec.Code, wrongRec.Body.String())
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
	// Regression: body tenant/user must not backfill audit identity; missing or zero internal headers cannot issue JWT.
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
	signInternalRunnerRequest(missingTenant, bodyWithIDs, "runner-secret", time.Now().UTC(), "", "42")
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
	// Regression: refresh audit identity must come from internal headers, not optional body fields.
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

	unsigned := httptest.NewRequest(http.MethodPost, "/internal/runner/refresh", bytes.NewReader(bodyWithIDs))
	setRunnerIdentityHeaders(unsigned, "7", "42")
	unsignedRec := httptest.NewRecorder()
	r.ServeHTTP(unsignedRec, unsigned)
	if unsignedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned refresh status=%d body=%s want 401", unsignedRec.Code, unsignedRec.Body.String())
	}
	if len(auditStore.auditArgs) != 0 {
		t.Fatalf("audit rows after unsigned refresh=%d want 0", len(auditStore.auditArgs))
	}

	wrong := httptest.NewRequest(http.MethodPost, "/internal/runner/refresh", bytes.NewReader(bodyWithIDs))
	signInternalRunnerRequest(wrong, bodyWithIDs, "wrong-secret", time.Now().UTC(), "7", "42")
	wrongRec := httptest.NewRecorder()
	r.ServeHTTP(wrongRec, wrong)
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong HMAC refresh status=%d body=%s want 401", wrongRec.Code, wrongRec.Body.String())
	}
	if len(auditStore.auditArgs) != 0 {
		t.Fatalf("audit rows after wrong-HMAC refresh=%d want 0", len(auditStore.auditArgs))
	}

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
	signInternalRunnerRequest(missingTenant, bodyWithIDs, "runner-secret", time.Now().UTC(), "", "42")
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

func TestInternalRunnerKeysRequiresHMACProof(t *testing.T) {
	// Regression: active JWT public keys must not be readable by callers that can only spoof internal headers.
	d, _, _, _ := newHermesInternalTestDeps(t)
	r := chi.NewRouter()
	mountRoutes(r, d, zap.NewNop())

	unsigned := httptest.NewRequest(http.MethodGet, "/internal/keys", nil)
	setRunnerIdentityHeaders(unsigned, "7", "42")
	unsignedRec := httptest.NewRecorder()
	r.ServeHTTP(unsignedRec, unsigned)
	if unsignedRec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned keys status=%d body=%s want 401", unsignedRec.Code, unsignedRec.Body.String())
	}

	wrong := httptest.NewRequest(http.MethodGet, "/internal/keys", nil)
	signInternalRunnerRequest(wrong, nil, "wrong-secret", time.Now().UTC(), "7", "42")
	wrongRec := httptest.NewRecorder()
	r.ServeHTTP(wrongRec, wrong)
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong HMAC keys status=%d body=%s want 401", wrongRec.Code, wrongRec.Body.String())
	}

	tampered := httptest.NewRequest(http.MethodGet, "/internal/keys", nil)
	signInternalRunnerRequest(tampered, nil, "runner-secret", time.Now().UTC(), "7", "42")
	tampered.Header.Set(hermes.HeaderTenant, "8")
	tamperedRec := httptest.NewRecorder()
	r.ServeHTTP(tamperedRec, tampered)
	if tamperedRec.Code != http.StatusUnauthorized {
		t.Fatalf("tampered tenant keys status=%d body=%s want 401", tamperedRec.Code, tamperedRec.Body.String())
	}

	valid := httptest.NewRequest(http.MethodGet, "/internal/keys", nil)
	signInternalRunnerRequest(valid, nil, "runner-secret", time.Now().UTC(), "7", "42")
	validRec := httptest.NewRecorder()
	r.ServeHTTP(validRec, valid)
	if validRec.Code != http.StatusOK {
		t.Fatalf("valid HMAC keys status=%d body=%s want 200", validRec.Code, validRec.Body.String())
	}
}

func TestBuildHermesChatBridgeRequiresDedicatedInternalTokenSecret(t *testing.T) {
	// Regression: /chat must fail closed when the runner shared secret exists but the bridge token secret is absent.
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

func setRunnerIdentityHeaders(req *http.Request, tenant, user string) {
	if tenant != "" {
		req.Header.Set(hermes.HeaderTenant, tenant)
	}
	if user != "" {
		req.Header.Set(hermes.HeaderUser, user)
	}
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
		hermesService:            hermes.NewService(auditStore),
		hermesKeyStore:           keyStore,
		hermesRunnerSharedSecret: []byte("runner-secret"),
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
