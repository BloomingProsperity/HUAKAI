package twofa

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

func GenerateTOTP(secret []byte, at time.Time, digits int, step time.Duration) (string, error) {
	if len(secret) == 0 || digits <= 0 || digits > 10 || step <= 0 {
		return "", ErrInvalidInput
	}
	counter := uint64(at.UTC().Unix() / int64(step.Seconds()))
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, secret)
	if _, err := mac.Write(msg[:]); err != nil {
		return "", err
	}
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint64(sum[offset])&0x7f)<<24 |
		(uint64(sum[offset+1])&0xff)<<16 |
		(uint64(sum[offset+2])&0xff)<<8 |
		(uint64(sum[offset+3]) & 0xff)
	modulo := pow10Uint64(digits)
	code := value % modulo
	return fmt.Sprintf("%0*d", digits, code), nil
}

func VerifyTOTP(secret []byte, code string, at time.Time, cfg TOTPConfig) bool {
	_, ok := VerifyTOTPStep(secret, code, at, cfg)
	return ok
}

// VerifyTOTPStep 与 VerifyTOTP 等价校验,但额外返回匹配到的**时间步计数器**
// (counter = candidateAt.Unix()/step)。调用方据此做单调"已消费时间步"防重放:
// 拒绝计数器小于等于上次成功消费值的码,从而堵住同一个(或更早的)6 位码在其
// 当前 ±Window 有效窗口内被重复使用(RFC 6238 §5.2)。未匹配时返回 ok=false,
// 此时 step 无意义。
func VerifyTOTPStep(secret []byte, code string, at time.Time, cfg TOTPConfig) (int64, bool) {
	code = strings.TrimSpace(code)
	if cfg.Digits <= 0 {
		cfg.Digits = DefaultTOTPDigits
	}
	if cfg.Step <= 0 {
		cfg.Step = DefaultTOTPStep
	}
	if cfg.Window < 0 || len(code) != cfg.Digits || !allDigits(code) {
		return 0, false
	}
	for offset := -cfg.Window; offset <= cfg.Window; offset++ {
		candidateAt := at.Add(time.Duration(offset) * cfg.Step)
		candidate, err := GenerateTOTP(secret, candidateAt, cfg.Digits, cfg.Step)
		if err != nil {
			return 0, false
		}
		if hmac.Equal([]byte(candidate), []byte(code)) {
			return candidateAt.UTC().Unix() / int64(cfg.Step.Seconds()), true
		}
	}
	return 0, false
}

func DecodeSecret(encoded string) ([]byte, error) {
	encoded = strings.ToUpper(strings.TrimSpace(encoded))
	encoded = strings.ReplaceAll(encoded, " ", "")
	if encoded == "" {
		return nil, ErrInvalidInput
	}
	secret, err := base32NoPadding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: secret", ErrInvalidInput)
	}
	return secret, nil
}

func encodeSecret(secret []byte) string {
	return base32NoPadding.EncodeToString(secret)
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func pow10Uint64(digits int) uint64 {
	value := uint64(1)
	for i := 0; i < digits; i++ {
		value *= 10
	}
	return value
}
