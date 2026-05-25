package hermes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

func TestLoadPrivateKeyRequires0400PEMAndParsesEd25519(t *testing.T) {
	// 回归守护：file mount 的 JWT 私钥只允许 0400，group/world-readable 必须拒绝启动。
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pemBytes, err := EncodePrivateKeyPEM(privateKey)
	if err != nil {
		t.Fatalf("EncodePrivateKeyPEM: %v", err)
	}
	path := filepath.Join(t.TempDir(), "jwt.pem")
	if err := os.WriteFile(path, pemBytes, 0o400); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatalf("LoadPrivateKey 0400 PEM: %v", err)
	}
	if !got.Equal(privateKey) {
		t.Fatalf("loaded private key differs from original")
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if _, err := LoadPrivateKey(path); err == nil {
		t.Fatalf("LoadPrivateKey accepted 0644 key; want permission rejection")
	}
}

func TestKeyStoreRotationImmediateActiveAndRevocationRejectsLookup(t *testing.T) {
	// 回归守护：新插入 kid 必须立即可见，撤销后的 kid 不能再用于验签。
	store := newMemoryJWTKeyQueries()
	keys := NewKeyStore(store)
	_, publicKeyA, pemA := generatedPublicKeyPEM(t)
	_, publicKeyB, pemB := generatedPublicKeyPEM(t)
	now := time.Now().UTC()

	if err := keys.InsertPublicKey(context.Background(), "kid-a", pemA, AlgEdDSA, now.Add(time.Hour)); err != nil {
		t.Fatalf("InsertPublicKey kid-a: %v", err)
	}
	if err := keys.InsertPublicKey(context.Background(), "kid-b", pemB, AlgEdDSA, now.Add(time.Hour)); err != nil {
		t.Fatalf("InsertPublicKey kid-b: %v", err)
	}

	active, err := keys.GetActiveKeys(context.Background())
	if err != nil {
		t.Fatalf("GetActiveKeys: %v", err)
	}
	activeByKid := map[string]bool{}
	for _, entry := range active {
		activeByKid[entry.Kid] = true
	}
	if len(active) != 2 || !activeByKid["kid-a"] || !activeByKid["kid-b"] {
		t.Fatalf("active keys=%+v want both inserted kids visible", active)
	}
	got, err := keys.GetKeyByKid(context.Background(), "kid-b")
	if err != nil {
		t.Fatalf("GetKeyByKid kid-b: %v", err)
	}
	if got.Kid != "kid-b" || !got.PublicKey.Equal(publicKeyB) {
		t.Fatalf("kid-b entry=%+v want parsed kid-b public key", got)
	}

	if err := keys.RevokeKey(context.Background(), "kid-b"); err != nil {
		t.Fatalf("RevokeKey: %v", err)
	}
	if _, err := keys.GetKeyByKid(context.Background(), "kid-b"); err == nil {
		t.Fatalf("revoked kid-b lookup succeeded; want revocation rejection")
	}
	got, err = keys.GetKeyByKid(context.Background(), "kid-a")
	if err != nil {
		t.Fatalf("GetKeyByKid kid-a after revoking kid-b: %v", err)
	}
	if !got.PublicKey.Equal(publicKeyA) {
		t.Fatalf("kid-a public key changed after unrelated revoke")
	}
}

func generatedPublicKeyPEM(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey, string) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pemBytes, err := EncodePublicKeyPEM(publicKey)
	if err != nil {
		t.Fatalf("EncodePublicKeyPEM: %v", err)
	}
	return privateKey, publicKey, string(pemBytes)
}

type memoryJWTKeyQueries struct {
	keys map[string]dbhermes.HermesJwtKey
	now  time.Time
}

func newMemoryJWTKeyQueries() *memoryJWTKeyQueries {
	return &memoryJWTKeyQueries{keys: map[string]dbhermes.HermesJwtKey{}, now: time.Now().UTC()}
}

func (m *memoryJWTKeyQueries) InsertJWTKey(_ context.Context, arg dbhermes.InsertJWTKeyParams) (dbhermes.HermesJwtKey, error) {
	row := dbhermes.HermesJwtKey{
		Kid: arg.Kid, Alg: arg.Alg, PublicKeyPem: arg.PublicKeyPem,
		ValidFrom:  pgtype.Timestamptz{Time: m.now, Valid: true},
		ValidUntil: arg.ValidUntil,
		CreatedAt:  pgtype.Timestamptz{Time: m.now, Valid: true},
	}
	m.keys[arg.Kid] = row
	return row, nil
}

func (m *memoryJWTKeyQueries) GetActiveJWTKeys(context.Context) ([]dbhermes.HermesJwtKey, error) {
	out := make([]dbhermes.HermesJwtKey, 0, len(m.keys))
	for _, row := range m.keys {
		if row.RevokedAt.Valid {
			continue
		}
		if row.ValidUntil.Valid && !row.ValidUntil.Time.After(m.now) {
			continue
		}
		out = append([]dbhermes.HermesJwtKey{row}, out...)
	}
	return out, nil
}

func (m *memoryJWTKeyQueries) GetJWTKeyByKid(_ context.Context, kid string) (dbhermes.HermesJwtKey, error) {
	row, ok := m.keys[kid]
	if !ok {
		return dbhermes.HermesJwtKey{}, ErrNotFound
	}
	return row, nil
}

func (m *memoryJWTKeyQueries) RevokeJWTKey(_ context.Context, kid string) (int64, error) {
	row, ok := m.keys[kid]
	if !ok || row.RevokedAt.Valid {
		return 0, nil
	}
	row.RevokedAt = pgtype.Timestamptz{Time: m.now, Valid: true}
	m.keys[kid] = row
	return 1, nil
}
