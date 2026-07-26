package browsersession

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/usersession"
)

const (
	ModeHeader        = "X-HUAKAI-Session-Mode"
	ModeBrowser       = "browser"
	CSRFHeader        = "X-HUAKAI-CSRF"
	RefreshCookieName = "__Host-huakai_refresh"
	CSRFCookieName    = "__Host-huakai_csrf"

	csrfBindingPrefix = "huakai-browser-csrf-v1:"
)

var (
	ErrRefreshCookieMissing = errors.New("browsersession: refresh cookie missing")
	ErrCSRFInvalid          = errors.New("browsersession: csrf proof invalid")
	ErrRefreshBodyConflict  = errors.New("browsersession: browser refresh token must not be sent in body")
)

// Tokens 是会话 HTTP 合同。浏览器模式不会序列化长期刷新令牌，只返回短期访问令牌和
// 与刷新 Cookie 绑定的 CSRF 证明；非浏览器调用保持原有 JSON 令牌合同。
type Tokens struct {
	SessionToken  string                    `json:"session_token"`
	RefreshToken  string                    `json:"refresh_token,omitempty"`
	SessionExpiry time.Time                 `json:"session_expires_at"`
	RefreshExpiry time.Time                 `json:"refresh_expires_at"`
	Family        usersession.SessionFamily `json:"family"`
	Generation    int                       `json:"generation"`
	CSRFToken     string                    `json:"csrf_token,omitempty"`
}

func IsBrowser(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get(ModeHeader)), ModeBrowser)
}

// Deliver 把新会话交付给 API 客户端或浏览器。浏览器刷新 Cookie 使用 __Host- 前缀，
// 因而只能由当前 HTTPS 主机写入，不能被子域通过 Domain 属性覆盖。
func Deliver(w http.ResponseWriter, r *http.Request, issued usersession.IssuedTokens) Tokens {
	noStore(w)
	out := Tokens{
		SessionToken:  issued.SessionToken,
		RefreshToken:  issued.RefreshToken,
		SessionExpiry: issued.SessionExpiry,
		RefreshExpiry: issued.RefreshExpiry,
		Family:        issued.Family,
		Generation:    issued.Generation,
	}
	if !IsBrowser(r) {
		return out
	}

	csrf := deriveCSRF(issued.RefreshToken)
	out.RefreshToken = ""
	out.CSRFToken = csrf
	setCookie(w, RefreshCookieName, issued.RefreshToken, issued.RefreshExpiry, true)
	setCookie(w, CSRFCookieName, csrf, issued.RefreshExpiry, false)
	return out
}

// ResolveRefresh 在浏览器模式下只接受 HttpOnly Cookie，并要求自定义请求头、可读
// Cookie 和刷新令牌派生值三者一致。非浏览器模式继续使用请求体中的刷新令牌。
func ResolveRefresh(r *http.Request, bodyToken string) (token string, browser bool, err error) {
	if !IsBrowser(r) {
		return strings.TrimSpace(bodyToken), false, nil
	}
	if strings.TrimSpace(bodyToken) != "" {
		return "", true, ErrRefreshBodyConflict
	}
	refreshCookie, err := r.Cookie(RefreshCookieName)
	if err != nil || strings.TrimSpace(refreshCookie.Value) == "" {
		return "", true, ErrRefreshCookieMissing
	}
	csrfCookie, err := r.Cookie(CSRFCookieName)
	if err != nil || strings.TrimSpace(csrfCookie.Value) == "" {
		return "", true, ErrCSRFInvalid
	}
	expected := deriveCSRF(refreshCookie.Value)
	header := strings.TrimSpace(r.Header.Get(CSRFHeader))
	if !constantTimeEqual(header, expected) || !constantTimeEqual(csrfCookie.Value, expected) {
		return "", true, ErrCSRFInvalid
	}
	return refreshCookie.Value, true, nil
}

func Clear(w http.ResponseWriter) {
	if w == nil {
		return
	}
	noStore(w)
	expired := time.Unix(1, 0).UTC()
	setExpiredCookie(w, RefreshCookieName, expired, true)
	setExpiredCookie(w, CSRFCookieName, expired, false)
}

func deriveCSRF(refreshToken string) string {
	sum := sha256.Sum256([]byte(csrfBindingPrefix + refreshToken))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func constantTimeEqual(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return hmac.Equal([]byte(left), []byte(right))
}

func setCookie(w http.ResponseWriter, name, value string, expires time.Time, httpOnly bool) {
	maxAge := int(time.Until(expires).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires.UTC(),
		MaxAge:   maxAge,
		HttpOnly: httpOnly,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func setExpiredCookie(w http.ResponseWriter, name string, expires time.Time, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Expires:  expires,
		MaxAge:   -1,
		HttpOnly: httpOnly,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func noStore(w http.ResponseWriter) {
	if w == nil {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}
