package credentialstore

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const EncryptionSchemeAES256GCM = "aes-256-gcm"

var (
	ErrKeyUnavailable = errors.New("credentialstore: key unavailable")
	ErrDecryptFailed  = errors.New("credentialstore: decrypt failed")
)

type Key struct {
	ID       string
	Material []byte
}

type KeyProvider interface {
	CurrentKey(context.Context) (Key, error)
	Key(context.Context, string) (Key, error)
}

type StaticKeyProvider struct {
	current string
	keys    map[string][]byte
}

func NewStaticKeyProvider(current string, material []byte) (*StaticKeyProvider, error) {
	current = strings.TrimSpace(current)
	if current == "" {
		return nil, fmt.Errorf("%w: key id is empty", ErrKeyUnavailable)
	}
	if len(material) != 32 {
		return nil, fmt.Errorf("%w: key %s must be 32 bytes", ErrKeyUnavailable, current)
	}
	copied := append([]byte(nil), material...)
	return &StaticKeyProvider{
		current: current,
		keys:    map[string][]byte{current: copied},
	}, nil
}

func (p *StaticKeyProvider) CurrentKey(ctx context.Context) (Key, error) {
	if p == nil {
		return Key{}, fmt.Errorf("%w: static provider is nil", ErrKeyUnavailable)
	}
	return p.Key(ctx, p.current)
}

func (p *StaticKeyProvider) Key(_ context.Context, keyID string) (Key, error) {
	if p == nil {
		return Key{}, fmt.Errorf("%w: static provider is nil", ErrKeyUnavailable)
	}
	keyID = strings.TrimSpace(keyID)
	material, ok := p.keys[keyID]
	if !ok || len(material) != 32 {
		return Key{}, fmt.Errorf("%w: key %s", ErrKeyUnavailable, keyID)
	}
	return Key{ID: keyID, Material: append([]byte(nil), material...)}, nil
}

type Cipher struct {
	keys KeyProvider
}

type AAD struct {
	TenantID          int64
	ProviderAccountID int64
	Vendor            string
	AuthMode          string
	Version           int32
	KeyID             string
}

type Envelope struct {
	Ciphertext       []byte
	Nonce            []byte
	KeyID            string
	EncryptionScheme string
	AADHash          string
}

func NewCipher(keys KeyProvider) *Cipher {
	return &Cipher{keys: keys}
}

func (c *Cipher) Encrypt(ctx context.Context, plaintext []byte, aad AAD) (Envelope, error) {
	if c == nil || c.keys == nil {
		return Envelope{}, fmt.Errorf("%w: cipher key provider missing", ErrKeyUnavailable)
	}
	key, err := c.keys.CurrentKey(ctx)
	if err != nil {
		return Envelope{}, err
	}
	aad.KeyID = key.ID
	block, err := aes.NewCipher(key.Material)
	if err != nil {
		return Envelope{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Envelope{}, fmt.Errorf("credentialstore: nonce: %w", err)
	}
	aadBytes := aad.Bytes()
	ciphertext := gcm.Seal(nil, nonce, plaintext, aadBytes)
	return Envelope{
		Ciphertext:       ciphertext,
		Nonce:            nonce,
		KeyID:            key.ID,
		EncryptionScheme: EncryptionSchemeAES256GCM,
		AADHash:          hashAAD(aadBytes),
	}, nil
}

func (c *Cipher) Decrypt(ctx context.Context, env Envelope, aad AAD) ([]byte, error) {
	if c == nil || c.keys == nil {
		return nil, fmt.Errorf("%w: cipher key provider missing", ErrKeyUnavailable)
	}
	if env.EncryptionScheme != "" && env.EncryptionScheme != EncryptionSchemeAES256GCM {
		return nil, fmt.Errorf("%w: unsupported scheme %q", ErrDecryptFailed, env.EncryptionScheme)
	}
	aad.KeyID = env.KeyID
	aadBytes := aad.Bytes()
	if env.AADHash != "" && !hmac.Equal([]byte(env.AADHash), []byte(hashAAD(aadBytes))) {
		return nil, fmt.Errorf("%w: aad hash mismatch", ErrDecryptFailed)
	}
	key, err := c.keys.Key(ctx, env.KeyID)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key.Material)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plaintext, err := gcm.Open(nil, env.Nonce, env.Ciphertext, aadBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptFailed, err)
	}
	return plaintext, nil
}

func (a AAD) Bytes() []byte {
	return []byte(fmt.Sprintf("tenant=%d;provider_account=%d;vendor=%s;auth_mode=%s;version=%d;key_id=%s",
		a.TenantID, a.ProviderAccountID, Normalize(a.Vendor), Normalize(a.AuthMode), a.Version, strings.TrimSpace(a.KeyID)))
}

func hashAAD(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func DecodeKeyMaterial(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: empty key material", ErrKeyUnavailable)
	}
	for _, decode := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		hex.DecodeString,
	} {
		out, err := decode(raw)
		if err == nil && len(out) == 32 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("%w: key material must decode to 32 bytes", ErrKeyUnavailable)
}

func HMACFingerprint(key Key, label string, secret []byte) string {
	if len(key.Material) != 32 || len(strings.TrimSpace(string(secret))) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, key.Material)
	mac.Write([]byte("huakai-credential-fingerprint:"))
	mac.Write([]byte(label))
	mac.Write([]byte{0})
	mac.Write(secret)
	return hex.EncodeToString(mac.Sum(nil))
}
