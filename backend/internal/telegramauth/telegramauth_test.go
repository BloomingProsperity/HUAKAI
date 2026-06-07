package telegramauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

func TestVerifyWidgetAcceptsValidHMAC(t *testing.T) {
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	params := signedTelegramWidgetParams("123456:bot-secret", map[string]string{
		"id":         "424242",
		"first_name": "Ada",
		"last_name":  "Lovelace",
		"username":   "ada_dev",
		"auth_date":  strconv.FormatInt(now.Add(-time.Minute).Unix(), 10),
	})

	identity, err := VerifyWidget(params, "123456:bot-secret", now, 24*time.Hour)
	if err != nil {
		t.Fatalf("VerifyWidget valid HMAC: %v", err)
	}
	if identity.Provider != userauth.SocialProviderTelegram ||
		identity.Subject != "424242" ||
		identity.DisplayName != "Ada Lovelace" ||
		identity.EmailVerified {
		t.Fatalf("Telegram identity mismatch: %+v", identity)
	}
	if identity.Email != userauth.SyntheticOAuthEmail(userauth.SocialProviderTelegram, "424242") {
		t.Fatalf("Telegram synthetic email=%q", identity.Email)
	}
}

func TestVerifyWidgetRejectsTamperedParam(t *testing.T) {
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	params := signedTelegramWidgetParams("123456:bot-secret", map[string]string{
		"id":        "424242",
		"username":  "ada_dev",
		"auth_date": strconv.FormatInt(now.Unix(), 10),
	})
	params["username"] = "attacker"

	if _, err := VerifyWidget(params, "123456:bot-secret", now, 24*time.Hour); err == nil {
		t.Fatal("VerifyWidget accepted tampered Telegram username")
	}
}

func TestVerifyWidgetRejectsTamperedHash(t *testing.T) {
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	params := signedTelegramWidgetParams("123456:bot-secret", map[string]string{
		"id":        "424242",
		"username":  "ada_dev",
		"auth_date": strconv.FormatInt(now.Unix(), 10),
	})
	if strings.HasPrefix(params["hash"], "0") {
		params["hash"] = "1" + params["hash"][1:]
	} else {
		params["hash"] = "0" + params["hash"][1:]
	}

	if _, err := VerifyWidget(params, "123456:bot-secret", now, 24*time.Hour); err == nil {
		t.Fatal("VerifyWidget accepted tampered Telegram hash")
	}
}

func TestVerifyWidgetRejectsExpiredAuthDate(t *testing.T) {
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	params := signedTelegramWidgetParams("123456:bot-secret", map[string]string{
		"id":        "424242",
		"username":  "ada_dev",
		"auth_date": strconv.FormatInt(now.Add(-25*time.Hour).Unix(), 10),
	})

	if _, err := VerifyWidget(params, "123456:bot-secret", now, 24*time.Hour); err == nil {
		t.Fatal("VerifyWidget accepted stale Telegram auth_date")
	}
}

func signedTelegramWidgetParams(botToken string, params map[string]string) map[string]string {
	out := make(map[string]string, len(params)+1)
	for k, v := range params {
		out[k] = v
	}
	secret := sha256.Sum256([]byte(botToken))
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(telegramWidgetCheckString(out)))
	out["hash"] = hex.EncodeToString(mac.Sum(nil))
	return out
}

func telegramWidgetCheckString(params map[string]string) string {
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
