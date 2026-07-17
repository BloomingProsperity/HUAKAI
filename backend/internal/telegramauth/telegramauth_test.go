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

// TestVerifyWidgetRejectsFutureAuthDate 锁定未来时间戳拒绝：即使 HMAC 正确，
// auth_date 晚于 now 一分钟以上的 payload 也不能被接受。
// 变异:删 telegramauth.go 里 authTime.After(now.Add(time.Minute)) 这条守卫 → 本用例放行,断言红。
func TestVerifyWidgetRejectsFutureAuthDate(t *testing.T) {
	now := time.Date(2026, 6, 7, 10, 0, 0, 0, time.UTC)
	// auth_date 在 now 之后 10 分钟:HMAC 仍由测试用真 token 正确签出,唯一的拒绝理由是未来戳。
	params := signedTelegramWidgetParams("123456:bot-secret", map[string]string{
		"id":        "424242",
		"username":  "ada_dev",
		"auth_date": strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10),
	})

	if _, err := VerifyWidget(params, "123456:bot-secret", now, 24*time.Hour); err == nil {
		t.Fatal("VerifyWidget 接受了 auth_date 在未来的 payload(应拒绝,防时钟偏移/伪造)")
	}

	// 判别性自证:同一构造、把 auth_date 拉回当下 → 必须接受。证明拒绝的是「未来戳」这一维度,
	// 而非构造本身有问题(否则就是恒拒的非判别测试)。
	okParams := signedTelegramWidgetParams("123456:bot-secret", map[string]string{
		"id":        "424242",
		"username":  "ada_dev",
		"auth_date": strconv.FormatInt(now.Unix(), 10),
	})
	if _, err := VerifyWidget(okParams, "123456:bot-secret", now, 24*time.Hour); err != nil {
		t.Fatalf("当下时间戳应被接受,得 err=%v", err)
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
