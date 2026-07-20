package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
)

type AntigravityRefresh struct {
	Gemini                  GeminiRefresh
	RequireRefreshTokenOnly bool
	HTTPClient              *http.Client
	ProjectResolver         ProjectIDResolver
}

type ProjectIDResolver interface {
	ResolveProjectID(context.Context, string) (string, error)
}

type ProjectMetadataResolver interface {
	ResolveProjectMetadata(context.Context, string) (string, string, error)
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
	accessToken := credentialString(updated, "access_token")
	if accessToken == "" {
		return nil, time.Time{}, fmt.Errorf("antigravity refresh account %d: access_token is empty", accountID)
	}
	// 运行时优先读取 session_token；刷新成功后必须与新 access_token 同步，
	// 否则陈旧会话令牌会遮蔽已经更新的访问令牌。
	updated["session_token"] = accessToken
	currentProject := credentialString(updated, "project_id")
	resolvedProject, resolvedTier := "", ""
	metadataAttempted := false
	resolveFailed := false
	if resolver, ok := r.ProjectResolver.(ProjectMetadataResolver); ok {
		metadataAttempted = true
		projectID, tier, resolveErr := resolver.ResolveProjectMetadata(ctx, accessToken)
		if resolveErr != nil {
			resolveFailed = true
		} else {
			resolvedProject = strings.TrimSpace(projectID)
			resolvedTier = strings.TrimSpace(tier)
		}
	} else if currentProject == "" && r.ProjectResolver != nil {
		metadataAttempted = true
		projectID, resolveErr := r.ProjectResolver.ResolveProjectID(ctx, accessToken)
		if resolveErr != nil {
			resolveFailed = true
		} else {
			resolvedProject = strings.TrimSpace(projectID)
		}
	}
	switch {
	case resolvedProject != "" && currentProject != "" && resolvedProject != currentProject:
		return nil, time.Time{}, fmt.Errorf("%w: account_id=%d", projectenrich.ErrProjectMetadataConflict, accountID)
	case resolvedProject != "":
		updated["project_id"] = resolvedProject
		updated["project_metadata_status"] = "resolved"
	case currentProject != "" && (metadataAttempted || resolveFailed):
		updated["project_metadata_status"] = "preserved_stale"
	case currentProject == "":
		return nil, time.Time{}, fmt.Errorf("%w: account_id=%d", projectenrich.ErrProjectMetadataUnavailable, accountID)
	}
	if resolvedTier != "" {
		updated["subscription_tier_raw"] = resolvedTier
		updated["subscription_metadata_status"] = "resolved"
	} else if previousTier := credentialString(cred, "subscription_tier_raw"); previousTier != "" {
		updated["subscription_tier_raw"] = previousTier
		if metadataAttempted {
			updated["subscription_metadata_status"] = "preserved_stale"
		}
	} else if metadataAttempted {
		updated["subscription_metadata_status"] = "missing"
	}
	if credentialString(updated, "plan") == "" {
		if previous := credentialString(cred, "plan"); previous != "" {
			updated["plan"] = previous
		}
	}
	out, err := json.Marshal(updated)
	return out, expiresAt, err
}
