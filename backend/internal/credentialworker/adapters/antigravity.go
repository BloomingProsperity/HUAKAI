package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type AntigravityRefresh struct {
	Gemini                  GeminiRefresh
	RequireRefreshTokenOnly bool
	HTTPClient              *http.Client
}

func (r AntigravityRefresh) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("antigravity refresh account %d: %w", accountID, err)
	}
	if strings.TrimSpace(credentialString(cred, "refresh_token")) == "" {
		return nil, time.Time{}, fmt.Errorf("antigravity refresh account %d: refresh_token is required", accountID)
	}
	g := r.Gemini
	if g.HTTPClient == nil {
		g.HTTPClient = r.HTTPClient
	}
	payload, expiresAt, err := g.RefreshForProvider(ctx, accountID, "antigravity", currentCredential)
	if err != nil {
		return nil, time.Time{}, err
	}
	updated, err := parseCredential(payload)
	if err != nil {
		return nil, time.Time{}, err
	}
	if credentialString(updated, "project_id") == "" {
		if previous := credentialString(cred, "project_id"); previous != "" {
			updated["project_id"] = previous
			updated["project_metadata_status"] = "preserved_stale"
		} else {
			updated["project_metadata_status"] = "operator_attention"
		}
	}
	if credentialString(updated, "plan") == "" {
		if previous := credentialString(cred, "plan"); previous != "" {
			updated["plan"] = previous
		}
	}
	out, err := json.Marshal(updated)
	return out, expiresAt, err
}
