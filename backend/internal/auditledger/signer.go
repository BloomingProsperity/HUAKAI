package auditledger

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	legacysign "github.com/BloomingProsperity/HUAKAI/internal/sign"
)

const (
	TrustLedgerPrivateKeyEnv = "HUAKAI_TRUST_LEDGER_ED25519_KEY_BASE64"
	TrustLedgerPubkeysEnv    = "HUAKAI_TRUST_LEDGER_PUBKEYS_JSON"
)

var (
	ErrInvalidLedgerPrivateKey = errors.New("auditledger: invalid ed25519 private key")
	ErrInvalidLedgerPublicKey  = errors.New("auditledger: invalid ed25519 public key")
	ErrInvalidLedgerSignature  = errors.New("auditledger: invalid ed25519 signature")
	ErrLedgerPubkeyNotFound    = errors.New("auditledger: pubkey fingerprint not found")
	ErrUnsupportedSigner       = errors.New("auditledger: unsupported signer")
)

// Signer 对 ledger payload 字节进行签名与验签。生产环境的 KMS 适配器
// 可以实现这个接口，而无需把私钥材料暴露给 writer。
type Signer interface {
	Sign(ctx context.Context, payload []byte) (signature []byte, pubkeyFingerprint string, err error)
	Verify(ctx context.Context, payload []byte, sig []byte, pubkeyFingerprint string) (bool, error)
}

type legacySigner interface {
	Sign([]byte) []byte
	Fingerprint() string
	PublicKey() ed25519.PublicKey
}

// PublicKeyRecord 是一个已发布的 trust-ledger 公钥在本地的表示。
// ValidUntil 是供 API 表面使用的元数据；验签会接受任何已配置的
// fingerprint，从而让历史 entry 始终可验证。
type PublicKeyRecord struct {
	Fingerprint  string
	PublicKey    ed25519.PublicKey
	Status       string
	ValidFrom    time.Time
	ValidUntil   time.Time
	PubkeyBase64 string
}

// LocalEd25519Signer 是面向 dev / test 及单节点部署的、与 KMS 兼容的
// 本地实现。
type LocalEd25519Signer struct {
	private ed25519.PrivateKey
	active  PublicKeyRecord
	pubkeys map[string]PublicKeyRecord
}

func NewLocalEd25519Signer(private ed25519.PrivateKey, records []PublicKeyRecord) (*LocalEd25519Signer, error) {
	if len(private) != ed25519.PrivateKeySize {
		return nil, ErrInvalidLedgerPrivateKey
	}
	pub, ok := private.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return nil, ErrInvalidLedgerPrivateKey
	}
	active := PublicKeyRecord{
		Fingerprint:  PubkeyFingerprint(pub),
		PublicKey:    append(ed25519.PublicKey(nil), pub...),
		Status:       "active",
		PubkeyBase64: base64.StdEncoding.EncodeToString(pub),
	}
	pubkeys := map[string]PublicKeyRecord{active.Fingerprint: active}
	for _, rec := range records {
		normalized, err := normalizePublicKeyRecord(rec)
		if err != nil {
			return nil, err
		}
		pubkeys[normalized.Fingerprint] = normalized
	}
	return &LocalEd25519Signer{
		private: append(ed25519.PrivateKey(nil), private...),
		active:  active,
		pubkeys: pubkeys,
	}, nil
}

func NewLocalEd25519SignerFromEnv(ctx context.Context, keys credentialstore.KeyProvider) (*LocalEd25519Signer, error) {
	material := strings.TrimSpace(os.Getenv(TrustLedgerPrivateKeyEnv))
	var raw []byte
	var err error
	if material != "" {
		raw, err = decodeBase64KeyMaterial(material)
		if err != nil {
			return nil, err
		}
	} else {
		if keys == nil {
			return nil, fmt.Errorf("%w: no env key or KeyProvider", credentialstore.ErrKeyUnavailable)
		}
		key, err := keys.CurrentKey(ctx)
		if err != nil {
			return nil, err
		}
		raw = key.Material
	}
	private, err := ed25519PrivateKeyFromMaterial(raw)
	if err != nil {
		return nil, err
	}
	records, err := LoadPublicKeysFromEnv()
	if err != nil {
		return nil, err
	}
	return NewLocalEd25519Signer(private, records)
}

func (s *LocalEd25519Signer) Sign(_ context.Context, payload []byte) ([]byte, string, error) {
	if s == nil || len(s.private) != ed25519.PrivateKeySize {
		return nil, "", ErrSignerNil
	}
	return ed25519.Sign(s.private, payload), s.active.Fingerprint, nil
}

func (s *LocalEd25519Signer) Fingerprint() string {
	if s == nil {
		return ""
	}
	return s.active.Fingerprint
}

func (s *LocalEd25519Signer) PublicKey() ed25519.PublicKey {
	if s == nil {
		return nil
	}
	return append(ed25519.PublicKey(nil), s.active.PublicKey...)
}

func (s *LocalEd25519Signer) Verify(_ context.Context, payload []byte, sig []byte, fp string) (bool, error) {
	if s == nil {
		return false, ErrSignerNil
	}
	fp = strings.TrimSpace(fp)
	rec, ok := s.pubkeys[fp]
	if !ok {
		return false, fmt.Errorf("%w: %s", ErrLedgerPubkeyNotFound, fp)
	}
	if len(rec.PublicKey) != ed25519.PublicKeySize {
		return false, ErrInvalidLedgerPublicKey
	}
	if len(sig) != ed25519.SignatureSize {
		return false, fmt.Errorf("%w: length", ErrInvalidLedgerSignature)
	}
	return ed25519.Verify(rec.PublicKey, payload, sig), nil
}

func (s *LocalEd25519Signer) ActiveRecord() PublicKeyRecord {
	if s == nil {
		return PublicKeyRecord{}
	}
	return clonePublicKeyRecord(s.active)
}

func (s *LocalEd25519Signer) PublicKeys() []PublicKeyRecord {
	if s == nil {
		return nil
	}
	out := make([]PublicKeyRecord, 0, len(s.pubkeys))
	for _, rec := range s.pubkeys {
		out = append(out, clonePublicKeyRecord(rec))
	}
	return out
}

func PubkeyFingerprint(pub ed25519.PublicKey) string {
	if len(pub) != ed25519.PublicKeySize {
		return ""
	}
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

func normalizeSigner(s any) (Signer, error) {
	if s == nil {
		return nil, ErrSignerNil
	}
	if signer, ok := s.(Signer); ok {
		return signer, nil
	}
	if signer, ok := s.(legacySigner); ok {
		return legacySignerAdapter{inner: signer}, nil
	}
	return nil, ErrUnsupportedSigner
}

type legacySignerAdapter struct {
	inner legacySigner
}

func (a legacySignerAdapter) Sign(_ context.Context, payload []byte) ([]byte, string, error) {
	if a.inner == nil {
		return nil, "", ErrSignerNil
	}
	return a.inner.Sign(payload), a.inner.Fingerprint(), nil
}

func (a legacySignerAdapter) Verify(_ context.Context, payload []byte, sig []byte, fp string) (bool, error) {
	if a.inner == nil {
		return false, ErrSignerNil
	}
	if fp != "" && fp != a.inner.Fingerprint() {
		return false, fmt.Errorf("%w: %s", ErrLedgerPubkeyNotFound, fp)
	}
	if err := legacysign.Verify(a.inner.PublicKey(), payload, sig); err != nil {
		return false, nil
	}
	return true, nil
}

func (a legacySignerAdapter) Fingerprint() string {
	if a.inner == nil {
		return ""
	}
	return a.inner.Fingerprint()
}

func signerFingerprint(ctx context.Context, signer Signer) (string, error) {
	if signer == nil {
		return "", ErrSignerNil
	}
	if fp, ok := signer.(interface{ Fingerprint() string }); ok {
		out := strings.TrimSpace(fp.Fingerprint())
		if out != "" {
			return out, nil
		}
	}
	_, fp, err := signer.Sign(ctx, nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(fp) == "" {
		return "", ErrInvalidLedgerPublicKey
	}
	return fp, nil
}

func ed25519PrivateKeyFromMaterial(raw []byte) (ed25519.PrivateKey, error) {
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return append(ed25519.PrivateKey(nil), raw...), nil
	default:
		return nil, ErrInvalidLedgerPrivateKey
	}
}

func decodeBase64KeyMaterial(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrInvalidLedgerPrivateKey
	}
	for _, decode := range []func(string) ([]byte, error){
		base64.StdEncoding.DecodeString,
		base64.RawStdEncoding.DecodeString,
		base64.URLEncoding.DecodeString,
		base64.RawURLEncoding.DecodeString,
	} {
		out, err := decode(raw)
		if err == nil && (len(out) == ed25519.SeedSize || len(out) == ed25519.PrivateKeySize) {
			return out, nil
		}
	}
	return nil, ErrInvalidLedgerPrivateKey
}

type pubkeysEnvFile struct {
	Active  json.RawMessage       `json:"active"`
	Rotated []publicKeyJSONRecord `json:"rotated"`
	Pubkeys []publicKeyJSONRecord `json:"pubkeys"`
}

type publicKeyJSONRecord struct {
	Fingerprint  string `json:"fingerprint"`
	PubkeyBase64 string `json:"pubkey_base64"`
	Status       string `json:"status"`
	ValidFrom    string `json:"valid_from"`
	ValidUntil   string `json:"valid_until"`
}

func LoadPublicKeysFromEnv() ([]PublicKeyRecord, error) {
	raw := strings.TrimSpace(os.Getenv(TrustLedgerPubkeysEnv))
	if raw == "" {
		return nil, nil
	}
	var doc pubkeysEnvFile
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, err
	}
	var records []PublicKeyRecord
	if len(doc.Active) > 0 && string(doc.Active) != "null" {
		var rec publicKeyJSONRecord
		if err := json.Unmarshal(doc.Active, &rec); err == nil && rec.PubkeyBase64 != "" {
			if rec.Status == "" {
				rec.Status = "active"
			}
			records = append(records, publicKeyRecordFromJSON(rec))
		}
	}
	for _, rec := range doc.Rotated {
		if rec.Status == "" {
			rec.Status = "rotated"
		}
		records = append(records, publicKeyRecordFromJSON(rec))
	}
	for _, rec := range doc.Pubkeys {
		records = append(records, publicKeyRecordFromJSON(rec))
	}
	out := make([]PublicKeyRecord, 0, len(records))
	for _, rec := range records {
		normalized, err := normalizePublicKeyRecord(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func publicKeyRecordFromJSON(rec publicKeyJSONRecord) PublicKeyRecord {
	return PublicKeyRecord{
		Fingerprint:  strings.TrimSpace(rec.Fingerprint),
		PubkeyBase64: strings.TrimSpace(rec.PubkeyBase64),
		Status:       strings.TrimSpace(rec.Status),
		ValidFrom:    parseOptionalTime(rec.ValidFrom),
		ValidUntil:   parseOptionalTime(rec.ValidUntil),
	}
}

func normalizePublicKeyRecord(rec PublicKeyRecord) (PublicKeyRecord, error) {
	if rec.PubkeyBase64 != "" && len(rec.PublicKey) == 0 {
		pub, err := decodePublicKey(rec.PubkeyBase64)
		if err != nil {
			return PublicKeyRecord{}, err
		}
		rec.PublicKey = pub
	}
	if len(rec.PublicKey) != ed25519.PublicKeySize {
		return PublicKeyRecord{}, ErrInvalidLedgerPublicKey
	}
	rec.PublicKey = append(ed25519.PublicKey(nil), rec.PublicKey...)
	computed := PubkeyFingerprint(rec.PublicKey)
	if rec.Fingerprint == "" {
		rec.Fingerprint = computed
	}
	if rec.Fingerprint != computed {
		return PublicKeyRecord{}, fmt.Errorf("%w: fingerprint mismatch", ErrInvalidLedgerPublicKey)
	}
	if rec.PubkeyBase64 == "" {
		rec.PubkeyBase64 = base64.StdEncoding.EncodeToString(rec.PublicKey)
	}
	if rec.Status == "" {
		rec.Status = "active"
	}
	return rec, nil
}

func decodePublicKey(raw string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, ErrInvalidLedgerPublicKey
	}
	return append(ed25519.PublicKey(nil), decoded...), nil
}

func parseOptionalTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}
	}
	return ts.UTC()
}

func clonePublicKeyRecord(in PublicKeyRecord) PublicKeyRecord {
	out := in
	out.PublicKey = append(ed25519.PublicKey(nil), in.PublicKey...)
	return out
}
