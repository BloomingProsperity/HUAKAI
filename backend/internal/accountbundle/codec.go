package accountbundle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"golang.org/x/crypto/scrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const kdfName = "scrypt-n32768-r8-p1"

func EncodeRecovery(manifest Manifest, passphrase string, now time.Time) (json.RawMessage, error) {
	if len(strings.TrimSpace(passphrase)) < 20 {
		return nil, ErrPassphraseWeak
	}
	if err := validateManifest(manifest, ModeRecovery, now); err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	defer privacy.Zeroize(plaintext)
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := scrypt.Key([]byte(passphrase), salt, 32768, 8, 1, 64)
	if err != nil {
		return nil, err
	}
	defer privacy.Zeroize(key)
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	aad := recoveryAAD(manifest.BundleID, manifest.CreatedAt, manifest.ExpiresAt)
	ciphertext := gcm.Seal(nil, nonce, plaintext, aad)
	envelope := Envelope{
		Version: EnvelopeVersion, BundleID: manifest.BundleID,
		CreatedAt: manifest.CreatedAt.UTC(), ExpiresAt: manifest.ExpiresAt.UTC(), KDF: kdfName,
		Salt: base64.RawStdEncoding.EncodeToString(salt), Nonce: base64.RawStdEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawStdEncoding.EncodeToString(ciphertext),
	}
	signature := signEnvelope(key[32:], envelope)
	envelope.Signature = base64.RawStdEncoding.EncodeToString(signature)
	return json.Marshal(envelope)
}

func DecodeRecovery(raw json.RawMessage, passphrase string, now time.Time) (Manifest, error) {
	if len(raw) == 0 || len(raw) > MaxBundleBytes || len(strings.TrimSpace(passphrase)) < 20 {
		return Manifest{}, ErrInvalidBundle
	}
	var envelope Envelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.Version != EnvelopeVersion || envelope.KDF != kdfName {
		return Manifest{}, ErrInvalidBundle
	}
	if !envelope.ExpiresAt.After(now.UTC()) {
		return Manifest{}, ErrBundleExpired
	}
	if !envelope.ExpiresAt.After(envelope.CreatedAt) || envelope.ExpiresAt.Sub(envelope.CreatedAt) > MaxRecoveryTTL {
		return Manifest{}, ErrInvalidBundle
	}
	salt, err := base64.RawStdEncoding.DecodeString(envelope.Salt)
	if err != nil || len(salt) != 32 {
		return Manifest{}, ErrInvalidBundle
	}
	key, err := scrypt.Key([]byte(passphrase), salt, 32768, 8, 1, 64)
	if err != nil {
		return Manifest{}, err
	}
	defer privacy.Zeroize(key)
	signature, err := base64.RawStdEncoding.DecodeString(envelope.Signature)
	if err != nil || subtle.ConstantTimeCompare(signature, signEnvelope(key[32:], envelope)) != 1 {
		return Manifest{}, ErrSignatureMismatch
	}
	nonce, err := base64.RawStdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return Manifest{}, ErrInvalidBundle
	}
	ciphertext, err := base64.RawStdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return Manifest{}, ErrInvalidBundle
	}
	block, err := aes.NewCipher(key[:32])
	if err != nil {
		return Manifest{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return Manifest{}, ErrInvalidBundle
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, recoveryAAD(envelope.BundleID, envelope.CreatedAt, envelope.ExpiresAt))
	if err != nil {
		return Manifest{}, ErrSignatureMismatch
	}
	defer privacy.Zeroize(plaintext)
	var manifest Manifest
	if json.Unmarshal(plaintext, &manifest) != nil || manifest.BundleID != envelope.BundleID {
		return Manifest{}, ErrInvalidBundle
	}
	if err := validateManifest(manifest, ModeRecovery, now); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func EncodeStructure(manifest Manifest, now time.Time) (json.RawMessage, error) {
	if err := validateManifest(manifest, ModeStructure, now); err != nil {
		return nil, err
	}
	for _, account := range manifest.Accounts {
		if len(account.Credential) > 0 || account.Vendor != "" || account.AuthMode != "" {
			return nil, ErrInvalidBundle
		}
	}
	return json.Marshal(manifest)
}

func DecodeStructure(raw json.RawMessage, now time.Time) (Manifest, error) {
	if len(raw) == 0 || len(raw) > MaxBundleBytes {
		return Manifest{}, ErrInvalidBundle
	}
	var manifest Manifest
	if json.Unmarshal(raw, &manifest) != nil {
		return Manifest{}, ErrInvalidBundle
	}
	if err := validateManifest(manifest, ModeStructure, now); err != nil {
		return Manifest{}, err
	}
	for _, account := range manifest.Accounts {
		if len(account.Credential) > 0 || account.Vendor != "" || account.AuthMode != "" {
			return Manifest{}, ErrInvalidBundle
		}
	}
	return manifest, nil
}

func validateManifest(manifest Manifest, mode string, now time.Time) error {
	if manifest.Version != ManifestVersion || manifest.Mode != mode || strings.TrimSpace(manifest.BundleID) == "" || len(manifest.Accounts) == 0 || len(manifest.Accounts) > 500 {
		return ErrInvalidBundle
	}
	if manifest.CreatedAt.IsZero() || manifest.CreatedAt.After(now.UTC().Add(time.Minute)) {
		return ErrInvalidBundle
	}
	if mode == ModeRecovery && (manifest.ExpiresAt.IsZero() || !manifest.ExpiresAt.After(now.UTC()) || manifest.ExpiresAt.Sub(manifest.CreatedAt) > MaxRecoveryTTL) {
		return ErrBundleExpired
	}
	return nil
}

func recoveryAAD(bundleID string, createdAt, expiresAt time.Time) []byte {
	return []byte(EnvelopeVersion + "\x00" + bundleID + "\x00" + createdAt.UTC().Format(time.RFC3339Nano) + "\x00" + expiresAt.UTC().Format(time.RFC3339Nano))
}

func signEnvelope(key []byte, envelope Envelope) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(recoveryAAD(envelope.BundleID, envelope.CreatedAt, envelope.ExpiresAt))
	mac.Write([]byte{0})
	mac.Write([]byte(envelope.KDF))
	mac.Write([]byte{0})
	mac.Write([]byte(envelope.Salt))
	mac.Write([]byte{0})
	mac.Write([]byte(envelope.Nonce))
	mac.Write([]byte{0})
	mac.Write([]byte(envelope.Ciphertext))
	return mac.Sum(nil)
}
