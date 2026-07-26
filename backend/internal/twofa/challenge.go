package twofa

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type challengePayload struct {
	TenantID    int64  `json:"tenant_id"`
	UserID      int64  `json:"user_id"`
	AuthVersion int    `json:"auth_version,omitempty"`
	ExpiresAt   int64  `json:"expires_at"`
	Nonce       string `json:"nonce"`
	KeyID       string `json:"key_id"`
}

func (s *Service) signChallenge(ctx context.Context, payload challengePayload) (string, error) {
	key, err := s.keys.CurrentKey(ctx)
	if err != nil {
		return "", err
	}
	defer privacy.Zeroize(key.Material)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("twofa: challenge nonce: %w", err)
	}
	payload.Nonce = base64.RawURLEncoding.EncodeToString(nonce)
	payload.KeyID = key.ID
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, key.Material)
	mac.Write([]byte(challengeMACPrefix))
	mac.Write([]byte(body))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "." + signature, nil
}

func (s *Service) verifyChallenge(ctx context.Context, id string) (challengePayload, error) {
	parts := strings.Split(strings.TrimSpace(id), ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return challengePayload{}, ErrChallengeInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return challengePayload{}, ErrChallengeInvalid
	}
	var payload challengePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return challengePayload{}, ErrChallengeInvalid
	}
	if payload.TenantID <= 0 || payload.UserID <= 0 || payload.AuthVersion <= 0 ||
		payload.ExpiresAt <= 0 || strings.TrimSpace(payload.KeyID) == "" {
		return challengePayload{}, ErrChallengeInvalid
	}
	key, err := s.keys.Key(ctx, payload.KeyID)
	if err != nil {
		return challengePayload{}, err
	}
	defer privacy.Zeroize(key.Material)
	mac := hmac.New(sha256.New, key.Material)
	mac.Write([]byte(challengeMACPrefix))
	mac.Write([]byte(parts[0]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return challengePayload{}, ErrChallengeInvalid
	}
	if !s.now().UTC().Before(time.Unix(payload.ExpiresAt, 0).UTC()) {
		return challengePayload{}, ErrChallengeExpired
	}
	return payload, nil
}
