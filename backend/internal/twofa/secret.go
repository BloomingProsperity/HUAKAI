package twofa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func (s *Service) encryptSecret(ctx context.Context, tenantID, userID int64, secret []byte) ([]byte, error) {
	env, err := credentialstore.NewCipher(s.keys).Encrypt(ctx, secret, secretAAD(tenantID, userID))
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (s *Service) decryptSecret(ctx context.Context, settings Settings) ([]byte, error) {
	var env credentialstore.Envelope
	if err := json.Unmarshal(settings.SecretEnc, &env); err != nil {
		return nil, fmt.Errorf("%w: secret envelope", ErrInvalidInput)
	}
	return credentialstore.NewCipher(s.keys).Decrypt(ctx, env, secretAAD(settings.TenantID, settings.UserID))
}

func secretAAD(tenantID, userID int64) credentialstore.AAD {
	return credentialstore.AAD{
		TenantID: tenantID, ProviderAccountID: userID,
		Vendor: "huakai-twofa", AuthMode: "totp-secret", Version: 1,
	}
}

func (s *Service) generateBackupCodes(tenantID, userID int64, count int) ([]string, [][]byte, error) {
	codes := make([]string, 0, count)
	hashes := make([][]byte, 0, count)
	seen := map[string]struct{}{}
	for len(codes) < count {
		code, err := generateBackupCode()
		if err != nil {
			return nil, nil, err
		}
		normalized := normalizeBackupCode(code)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		hash, _ := hashBackupCode(tenantID, userID, code)
		codes = append(codes, code)
		hashes = append(hashes, hash)
	}
	return codes, hashes, nil
}

func generateBackupCode() (string, error) {
	raw := make([]byte, backupCodeBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("twofa: generate backup code: %w", err)
	}
	encoded := encodeSecret(raw)
	if len(encoded) < 16 {
		return "", ErrInvalidInput
	}
	return strings.Join([]string{encoded[0:4], encoded[4:8], encoded[8:12], encoded[12:16]}, "-"), nil
}

func hashBackupCode(tenantID, userID int64, code string) ([]byte, bool) {
	normalized := normalizeBackupCode(code)
	if len(normalized) < 8 {
		return nil, false
	}
	h := sha256.New()
	h.Write([]byte(backupCodeHashPrefix))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(tenantID, 10)))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(userID, 10)))
	h.Write([]byte{0})
	h.Write([]byte(normalized))
	return h.Sum(nil), true
}

func normalizeBackupCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	var b strings.Builder
	for _, r := range code {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}
