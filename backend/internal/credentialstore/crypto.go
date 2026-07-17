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
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
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
	defer privacy.Zeroize(key.Material)
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
	defer privacy.Zeroize(key.Material)
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

// CredentialMaterialFingerprint 为账号接入去重生成租户域隔离的稳定单向指纹。
// 只选择运行时真正使用的高熵认证材料，不把显示名、邮箱或普通业务字段纳入。
func CredentialMaterialFingerprint(tenantID int64, vendor, authMode string, payload []byte) string {
	if tenantID <= 0 {
		return ""
	}
	fields, err := parsePayloadFields(payload)
	if err != nil {
		return ""
	}
	material := credentialFingerprintMaterial(vendor, authMode, fields)
	if len(material) == 0 {
		return ""
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("huakai-credential-material-fingerprint:v1\x00"))
	_, _ = hash.Write([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00",
		tenantID, Normalize(vendor), Normalize(authMode))))
	_, _ = hash.Write(material)
	return hex.EncodeToString(hash.Sum(nil))
}

func credentialFingerprintMaterial(vendor, authMode string, fields map[string]json.RawMessage) []byte {
	if len(fields) == 0 {
		return nil
	}
	vendor = Normalize(vendor)
	authMode = Normalize(authMode)
	if fieldString(fields, "private_key") != "" {
		return canonicalFingerprintFields(fields, "client_email", "private_key", "project_id", "token_uri")
	}
	if fieldString(fields, "aws_secret_access_key") != "" {
		return canonicalFingerprintFields(fields, "aws_access_key_id", "aws_secret_access_key")
	}
	if handler, ok := DefaultHandlerRegistry().Lookup(vendor, authMode); ok {
		switch handler.RuntimeKind() {
		case RuntimeAPIKey:
			if key := firstFingerprintField(fields, "api_key", "azure_api_key", "access_token"); key != "" {
				return []byte("runtime_key\x00" + key)
			}
		case RuntimeOAuthAccessToken:
			if token := fieldString(fields, "refresh_token"); token != "" {
				return []byte("refresh_token\x00" + token)
			}
			if token := firstFingerprintField(fields, "access_token", "setup_token"); token != "" {
				return []byte("runtime_token\x00" + token)
			}
		case RuntimeSessionToken:
			if token := fieldString(fields, "refresh_token"); token != "" {
				return []byte("refresh_token\x00" + token)
			}
			if token := firstFingerprintField(fields, "github_access_token", "session_token", "access_token", "cookie"); token != "" {
				return []byte("runtime_token\x00" + token)
			}
		case RuntimeAWSSigV4:
			return canonicalFingerprintFields(fields, "aws_access_key_id", "aws_secret_access_key")
		case RuntimeUpstreamPassthrough:
			if token := fieldString(fields, "refresh_token"); token != "" {
				return []byte("refresh_token\x00" + token)
			}
			if token := firstFingerprintField(fields, "api_key", "auth_header_value", "access_token"); token != "" {
				return []byte("runtime_passthrough\x00" + token)
			}
		}
	}
	return nil
}

func canonicalFingerprintFields(fields map[string]json.RawMessage, names ...string) []byte {
	selected := make(map[string]string, len(names))
	for _, name := range names {
		if value := fieldString(fields, name); value != "" {
			selected[name] = value
		}
	}
	if len(selected) == 0 {
		return nil
	}
	normalized, err := json.Marshal(selected)
	if err != nil {
		return nil
	}
	return append([]byte("structured_material\x00"), normalized...)
}

func firstFingerprintField(fields map[string]json.RawMessage, names ...string) string {
	for _, name := range names {
		if value := fieldString(fields, name); value != "" {
			return value
		}
	}
	return ""
}
