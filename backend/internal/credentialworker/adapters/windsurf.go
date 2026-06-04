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

// WindsurfManualTokenRefresh validates the stored manual token shape and then
// reports that there is no automatic OAuth refresh path in this.
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
