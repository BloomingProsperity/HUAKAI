package hermes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"
)

type BootstrapIssuer struct {
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
	KeyStore   *KeyStore
	Issuer     string
	Audience   string
	TTL        time.Duration
	Now        func() time.Time
}

func NewBootstrapIssuerFromEnv(keyStore *KeyStore) (*BootstrapIssuer, error) {
	path := strings.TrimSpace(os.Getenv(RunnerJWTPrivateKeyEnv))
	if path == "" {
		return nil, nil
	}
	privateKey, err := LoadPrivateKey(path)
	if err != nil {
		return nil, err
	}
	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("%w: ed25519 public key required", ErrMisconfigured)
	}
	return &BootstrapIssuer{
		PrivateKey: privateKey,
		PublicKey:  publicKey,
		KeyStore:   keyStore,
		Issuer:     os.Getenv(RunnerJWTIssuerEnv),
		Audience:   os.Getenv(RunnerJWTAudienceEnv),
		TTL:        DefaultJWTTTL,
	}, nil
}

func (i *BootstrapIssuer) IssueBootstrapJWT(ctx context.Context, runnerID string) (string, error) {
	if err := i.validate(); err != nil {
		return "", err
	}
	if strings.TrimSpace(runnerID) == "" {
		return "", fmt.Errorf("%w: runner_id is required", ErrInvalidInput)
	}
	kid, err := GenerateKID(i.publicKey(), i.clock())
	if err != nil {
		return "", err
	}
	pemBytes, err := EncodePublicKeyPEM(i.publicKey())
	if err != nil {
		return "", err
	}
	if err := i.KeyStore.InsertPublicKey(ctx, kid, string(pemBytes), AlgEdDSA, time.Time{}); err != nil {
		return "", err
	}
	return i.sign(kid, strings.TrimSpace(runnerID))
}

func (i *BootstrapIssuer) RefreshJWT(ctx context.Context, oldJWT string) (string, error) {
	if err := i.validate(); err != nil {
		return "", err
	}
	kid, err := KIDFromToken(oldJWT)
	if err != nil {
		return "", err
	}
	entry, err := i.KeyStore.GetKeyByKid(ctx, kid)
	if err != nil {
		if strings.Contains(err.Error(), "revoked") || strings.Contains(err.Error(), "expired") {
			return "", fmt.Errorf("%w: jwt key inactive", ErrForbidden)
		}
		return "", err
	}
	now := i.clock()
	claims, err := verifyAt(entry.PublicKey, oldJWT, now, i.issuer(), i.audience())
	if err != nil {
		return "", err
	}
	if time.Unix(claims.Exp, 0).UTC().After(now.Add(JWTRefreshLead)) {
		return "", fmt.Errorf("%w: jwt refresh requested before lead window", ErrForbidden)
	}
	return i.sign(claims.Kid, claims.Sub)
}

func GenerateKID(publicKey ed25519.PublicKey, now time.Time) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", fmt.Errorf("%w: ed25519 public key required", ErrInvalidInput)
	}
	var nonce [4]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	hash := sha256.Sum256(publicKey)
	return fmt.Sprintf("hermes-%d-%s-%s", now.UTC().Unix(), hex.EncodeToString(hash[:4]), hex.EncodeToString(nonce[:])), nil
}

func KIDFromToken(token string) (string, error) {
	header, _, _, _, err := parseJWT(token)
	if err != nil {
		return "", err
	}
	if header.Kid == "" {
		return "", fmt.Errorf("%w: jwt kid required", ErrForbidden)
	}
	if header.Alg != AlgEdDSA {
		return "", fmt.Errorf("%w: unsupported jwt alg", ErrForbidden)
	}
	return header.Kid, nil
}

func (i *BootstrapIssuer) sign(kid, subject string) (string, error) {
	now := i.clock()
	claims := Claims{
		Iss: i.issuer(),
		Aud: i.audience(),
		Sub: subject,
		Iat: now.Unix(),
		Nbf: now.Unix(),
		Exp: now.Add(i.ttl()).Unix(),
	}
	return Sign(i.PrivateKey, kid, claims)
}

func (i *BootstrapIssuer) validate() error {
	if i == nil || i.KeyStore == nil {
		return ErrMisconfigured
	}
	if len(i.PrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: ed25519 private key required", ErrMisconfigured)
	}
	if len(i.publicKey()) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: ed25519 public key required", ErrMisconfigured)
	}
	return nil
}

func (i *BootstrapIssuer) publicKey() ed25519.PublicKey {
	if i == nil {
		return nil
	}
	if len(i.PublicKey) == ed25519.PublicKeySize {
		return i.PublicKey
	}
	if pub, ok := i.PrivateKey.Public().(ed25519.PublicKey); ok {
		return pub
	}
	return nil
}

func (i *BootstrapIssuer) issuer() string {
	if i != nil && strings.TrimSpace(i.Issuer) != "" {
		return strings.TrimSpace(i.Issuer)
	}
	return DefaultJWTIssuer
}

func (i *BootstrapIssuer) audience() string {
	if i != nil && strings.TrimSpace(i.Audience) != "" {
		return strings.TrimSpace(i.Audience)
	}
	return DefaultJWTAudience
}

func (i *BootstrapIssuer) ttl() time.Duration {
	if i != nil && i.TTL > 0 {
		return i.TTL
	}
	return DefaultJWTTTL
}

func (i *BootstrapIssuer) clock() time.Time {
	if i != nil && i.Now != nil {
		return i.Now().UTC()
	}
	return time.Now().UTC()
}
