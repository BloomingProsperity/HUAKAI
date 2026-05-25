package voucher

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"strings"
)

func NormalizeCode(code string) string {
	fields := strings.Fields(strings.TrimSpace(code))
	return strings.ToUpper(strings.Join(fields, ""))
}

func NormalizeIdempotencyKey(key string) string {
	return strings.TrimSpace(key)
}

func CodeHash(tenantID int64, normalized string) ([]byte, string) {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", tenantID, normalized)))
	full := hex.EncodeToString(sum[:])
	return sum[:], full[:8]
}

func SourceIPHash(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])[:12]
}

func GenerateCode() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "HV-" + enc[:6] + "-" + enc[6:12] + "-" + enc[12:18], nil
}
