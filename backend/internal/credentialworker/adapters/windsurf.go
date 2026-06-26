package adapters

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrWindsurfManualTokenRefreshRequired = errors.New("windsurf refresh: manual token re-entry required")
	ErrInvalidCredentialMaterial          = errors.New("credentialworker: invalid credential material")
)

// WindsurfManualTokenRefresh 校验已存储的手动 token 的形态，然后
// 报告这里不存在自动 OAuth refresh 路径。
type WindsurfManualTokenRefresh struct{}

func (WindsurfManualTokenRefresh) RefreshForProvider(_ context.Context, accountID int64, _ string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("windsurf refresh account %d: %w", accountID, err)
	}
	if firstNonEmpty(credentialString(cred, "session_token"), credentialString(cred, "access_token")) == "" {
		return nil, time.Time{}, fmt.Errorf("windsurf refresh account %d: %w: session_token or access_token required", accountID, ErrInvalidCredentialMaterial)
	}
	return nil, time.Time{}, ErrWindsurfManualTokenRefreshRequired
}
