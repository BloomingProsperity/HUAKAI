package userauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

func GenerateInviteCode() (string, string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	code := "hki_" + base64.RawURLEncoding.EncodeToString(raw)
	return code, HashInviteCode(code), nil
}

func HashInviteCode(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func AcquireInviteAdvisoryLock(ctx context.Context, tx db.DBTX, codeHash string) error {
	if tx == nil {
		return errors.New("userauth: invite lock requires transaction db")
	}
	if strings.TrimSpace(codeHash) == "" {
		return ErrInviteInvalid
	}
	_, err := tx.Exec(ctx, `
SELECT pg_advisory_xact_lock(hashtext('user_invite:' || $1::text))
`, strings.TrimSpace(codeHash))
	return err
}
