package hermes

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbhermes "github.com/BloomingProsperity/HUAKAI/internal/db/hermes"
)

type JWTKeyQueries interface {
	InsertJWTKey(context.Context, dbhermes.InsertJWTKeyParams) (dbhermes.HermesJwtKey, error)
	GetActiveJWTKeys(context.Context) ([]dbhermes.HermesJwtKey, error)
	GetJWTKeyByKid(context.Context, string) (dbhermes.HermesJwtKey, error)
	RevokeJWTKey(context.Context, string) (int64, error)
}

type KeyEntry struct {
	Kid          string
	Alg          string
	PublicKeyPEM string
	PublicKey    ed25519.PublicKey
	ValidFrom    time.Time
	ValidUntil   *time.Time
	RevokedAt    *time.Time
	CreatedAt    time.Time
}

type KeyStore struct {
	queries JWTKeyQueries
	now     func() time.Time
}

func NewKeyStore(queries JWTKeyQueries) *KeyStore {
	return &KeyStore{queries: queries, now: time.Now}
}

func LoadPrivateKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat hermes jwt private key: %w", err)
	}
	if info.Mode().Perm() != 0o400 {
		return nil, fmt.Errorf("%w: jwt private key must be 0400", ErrMisconfigured)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read hermes jwt private key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%w: jwt private key must be PEM", ErrInvalidInput)
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: parse jwt private key PEM: %v", ErrInvalidInput, err)
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: jwt private key must be Ed25519", ErrInvalidInput)
	}
	return privateKey, nil
}

func EncodePrivateKeyPEM(privateKey ed25519.PrivateKey) ([]byte, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: ed25519 private key required", ErrInvalidInput)
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func EncodePublicKeyPEM(publicKey ed25519.PublicKey) ([]byte, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: ed25519 public key required", ErrInvalidInput)
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

func ParsePublicKeyPEM(publicKeyPEM string) (ed25519.PublicKey, error) {
	block, _ := pem.Decode([]byte(publicKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("%w: jwt public key must be PEM", ErrInvalidInput)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: parse jwt public key PEM: %v", ErrInvalidInput, err)
	}
	publicKey, ok := key.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: jwt public key must be Ed25519", ErrInvalidInput)
	}
	return publicKey, nil
}

func (s *KeyStore) InsertPublicKey(ctx context.Context, kid, publicKeyPEM, alg string, validUntil time.Time) error {
	if s == nil || s.queries == nil {
		return ErrMisconfigured
	}
	if alg != AlgEdDSA {
		return fmt.Errorf("%w: unsupported jwt alg", ErrInvalidInput)
	}
	if _, err := ParsePublicKeyPEM(publicKeyPEM); err != nil {
		return err
	}
	params := dbhermes.InsertJWTKeyParams{Kid: kid, Alg: alg, PublicKeyPem: publicKeyPEM}
	if !validUntil.IsZero() {
		params.ValidUntil = pgtype.Timestamptz{Time: validUntil.UTC(), Valid: true}
	}
	if _, err := s.queries.InsertJWTKey(ctx, params); err != nil {
		return fmt.Errorf("insert hermes jwt public key: %w", err)
	}
	return nil
}

func (s *KeyStore) GetActiveKeys(ctx context.Context) ([]KeyEntry, error) {
	if s == nil || s.queries == nil {
		return nil, ErrMisconfigured
	}
	rows, err := s.queries.GetActiveJWTKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list active hermes jwt keys: %w", err)
	}
	out := make([]KeyEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := keyEntryFromRow(row, s.clock())
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	return out, nil
}

func (s *KeyStore) GetKeyByKid(ctx context.Context, kid string) (KeyEntry, error) {
	if s == nil || s.queries == nil {
		return KeyEntry{}, ErrMisconfigured
	}
	row, err := s.queries.GetJWTKeyByKid(ctx, kid)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
		return KeyEntry{}, ErrNotFound
	}
	if err != nil {
		return KeyEntry{}, fmt.Errorf("get hermes jwt key: %w", err)
	}
	return keyEntryFromRow(row, s.clock())
}

func (s *KeyStore) RevokeKey(ctx context.Context, kid string) error {
	if s == nil || s.queries == nil {
		return ErrMisconfigured
	}
	rows, err := s.queries.RevokeJWTKey(ctx, kid)
	if err != nil {
		return fmt.Errorf("revoke hermes jwt key: %w", err)
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *KeyStore) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func keyEntryFromRow(row dbhermes.HermesJwtKey, now time.Time) (KeyEntry, error) {
	if row.Alg != AlgEdDSA {
		return KeyEntry{}, fmt.Errorf("%w: unsupported jwt alg", ErrForbidden)
	}
	if row.RevokedAt.Valid {
		return KeyEntry{}, fmt.Errorf("%w: jwt key revoked", ErrForbidden)
	}
	if row.ValidFrom.Valid && row.ValidFrom.Time.After(now) {
		return KeyEntry{}, fmt.Errorf("%w: jwt key not yet valid", ErrForbidden)
	}
	if row.ValidUntil.Valid && !row.ValidUntil.Time.After(now) {
		return KeyEntry{}, fmt.Errorf("%w: jwt key expired", ErrForbidden)
	}
	publicKey, err := ParsePublicKeyPEM(row.PublicKeyPem)
	if err != nil {
		return KeyEntry{}, err
	}
	entry := KeyEntry{
		Kid: row.Kid, Alg: row.Alg, PublicKeyPEM: row.PublicKeyPem, PublicKey: publicKey,
		ValidFrom: row.ValidFrom.Time, CreatedAt: row.CreatedAt.Time,
	}
	if row.ValidUntil.Valid {
		t := row.ValidUntil.Time
		entry.ValidUntil = &t
	}
	if row.RevokedAt.Valid {
		t := row.RevokedAt.Time
		entry.RevokedAt = &t
	}
	return entry, nil
}
