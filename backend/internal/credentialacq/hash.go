package credentialacq

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func HashPreview(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
