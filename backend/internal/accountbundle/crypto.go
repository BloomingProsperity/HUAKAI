package accountbundle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	kdfName                  = "argon2id"
	cipherName               = "aes-256-gcm"
	kdfTime           uint32 = 3
	kdfMemoryKiB      uint32 = 64 * 1024
	kdfThreads        uint8  = 2
	passwordMinBytes         = 12
	passwordMaxBytes         = 1024
	maxPlaintextBytes        = 8 << 20
)

var bundleAAD = []byte("huakai-account-bundle/v1")

func seal(content payloadContent, password string) (Envelope, error) {
	if err := validatePassword(password); err != nil {
		return Envelope{}, err
	}
	contentRaw, err := json.Marshal(content)
	if err != nil || len(contentRaw) > maxPlaintextBytes {
		return Envelope{}, ErrInvalidInput
	}
	digest := sha256.Sum256(contentRaw)
	plain, err := json.Marshal(payload{
		Format: EnvelopeFormat, Version: EnvelopeVersion,
		ContentSHA256: hex.EncodeToString(digest[:]), Content: content,
	})
	privacy.Zeroize(contentRaw)
	if err != nil || len(plain) > maxPlaintextBytes {
		privacy.Zeroize(plain)
		return Envelope{}, ErrInvalidInput
	}
	defer privacy.Zeroize(plain)
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return Envelope{}, err
	}
	passwordBytes := []byte(password)
	defer privacy.Zeroize(passwordBytes)
	key := argon2.IDKey(passwordBytes, salt, kdfTime, kdfMemoryKiB, kdfThreads, 32)
	defer privacy.Zeroize(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return Envelope{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return Envelope{}, err
	}
	ciphertext := aead.Seal(nil, nonce, plain, bundleAAD)
	cipherDigest := sha256.Sum256(ciphertext)
	return Envelope{
		Format: EnvelopeFormat, Version: EnvelopeVersion,
		KDF:              KDFSpec{Name: kdfName, Salt: salt, Time: kdfTime, MemoryKiB: kdfMemoryKiB, Threads: kdfThreads},
		Cipher:           CipherSpec{Name: cipherName, Nonce: nonce, Ciphertext: ciphertext},
		CiphertextSHA256: hex.EncodeToString(cipherDigest[:]),
	}, nil
}

func open(envelope Envelope, password string) (payloadContent, error) {
	if err := validateEnvelope(envelope); err != nil {
		return payloadContent{}, err
	}
	if err := validatePassword(password); err != nil {
		return payloadContent{}, err
	}
	digest := sha256.Sum256(envelope.Cipher.Ciphertext)
	wantDigest, _ := hex.DecodeString(envelope.CiphertextSHA256)
	if subtle.ConstantTimeCompare(digest[:], wantDigest) != 1 {
		return payloadContent{}, ErrIntegrity
	}
	passwordBytes := []byte(password)
	defer privacy.Zeroize(passwordBytes)
	key := argon2.IDKey(passwordBytes, envelope.KDF.Salt, envelope.KDF.Time, envelope.KDF.MemoryKiB, envelope.KDF.Threads, 32)
	defer privacy.Zeroize(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return payloadContent{}, ErrIntegrity
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return payloadContent{}, ErrIntegrity
	}
	plain, err := aead.Open(nil, envelope.Cipher.Nonce, envelope.Cipher.Ciphertext, bundleAAD)
	if err != nil {
		return payloadContent{}, ErrPassword
	}
	defer privacy.Zeroize(plain)
	var decoded payload
	if err := json.Unmarshal(plain, &decoded); err != nil || decoded.Format != EnvelopeFormat || decoded.Version != EnvelopeVersion {
		return payloadContent{}, ErrIntegrity
	}
	contentRaw, err := json.Marshal(decoded.Content)
	if err != nil {
		return payloadContent{}, ErrIntegrity
	}
	defer privacy.Zeroize(contentRaw)
	contentDigest := sha256.Sum256(contentRaw)
	wantContent, err := hex.DecodeString(decoded.ContentSHA256)
	if err != nil || subtle.ConstantTimeCompare(contentDigest[:], wantContent) != 1 {
		return payloadContent{}, ErrIntegrity
	}
	return decoded.Content, nil
}

func validatePassword(password string) error {
	if len(password) < passwordMinBytes || len(password) > passwordMaxBytes {
		return fmt.Errorf("%w: password length must be between %d and %d bytes", ErrInvalidInput, passwordMinBytes, passwordMaxBytes)
	}
	return nil
}

func validateEnvelope(envelope Envelope) error {
	if envelope.Format != EnvelopeFormat || envelope.Version != EnvelopeVersion ||
		envelope.KDF.Name != kdfName || envelope.KDF.Time != kdfTime || envelope.KDF.MemoryKiB != kdfMemoryKiB ||
		envelope.KDF.Threads != kdfThreads || len(envelope.KDF.Salt) != 16 ||
		envelope.Cipher.Name != cipherName || len(envelope.Cipher.Nonce) != 12 ||
		len(envelope.Cipher.Ciphertext) == 0 || len(envelope.Cipher.Ciphertext) > maxPlaintextBytes+1024 ||
		len(envelope.CiphertextSHA256) != sha256.Size*2 {
		return ErrIntegrity
	}
	if _, err := hex.DecodeString(envelope.CiphertextSHA256); err != nil {
		return ErrIntegrity
	}
	return nil
}
