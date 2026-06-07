package telegramauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func VerifyWidget(params map[string]string, botToken string, now time.Time, maxAge time.Duration) (userauth.VerifiedIdentity, error) {
	botToken = strings.TrimSpace(botToken)
	if botToken == "" || len(params) == 0 {
		return userauth.VerifiedIdentity{}, userauth.ErrInvalidInput
	}
	providedHash := strings.TrimSpace(params["hash"])
	subject := strings.TrimSpace(params["id"])
	authDateRaw := strings.TrimSpace(params["auth_date"])
	if providedHash == "" || subject == "" || authDateRaw == "" {
		return userauth.VerifiedIdentity{}, userauth.ErrInvalidInput
	}
	authUnix, err := strconv.ParseInt(authDateRaw, 10, 64)
	if err != nil || authUnix <= 0 {
		return userauth.VerifiedIdentity{}, userauth.ErrInvalidInput
	}
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	authTime := time.Unix(authUnix, 0).UTC()
	if maxAge > 0 && now.Sub(authTime) > maxAge {
		return userauth.VerifiedIdentity{}, userauth.ErrSocialLoginRejected
	}
	if authTime.After(now.Add(time.Minute)) {
		return userauth.VerifiedIdentity{}, userauth.ErrSocialLoginRejected
	}

	secret := sha256.Sum256([]byte(botToken))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(widgetDataCheckString(params)))
	expected := mac.Sum(nil)
	got, err := hex.DecodeString(providedHash)
	if err != nil || !hmac.Equal(got, expected) {
		return userauth.VerifiedIdentity{}, userauth.ErrSocialLoginRejected
	}

	return userauth.VerifiedIdentity{
		Provider:      userauth.SocialProviderTelegram,
		Subject:       subject,
		Email:         userauth.SyntheticOAuthEmail(userauth.SocialProviderTelegram, subject),
		DisplayName:   telegramDisplayName(params),
		EmailVerified: false,
	}, nil
}

func widgetDataCheckString(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		if key != "hash" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+params[key])
	}
	return strings.Join(lines, "\n")
}

func telegramDisplayName(params map[string]string) string {
	first := strings.TrimSpace(params["first_name"])
	last := strings.TrimSpace(params["last_name"])
	name := strings.TrimSpace(strings.Join([]string{first, last}, " "))
	if name != "" {
		return name
	}
	return strings.TrimSpace(params["username"])
}
