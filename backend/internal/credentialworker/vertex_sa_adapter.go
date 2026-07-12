package credentialworker

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/vertexsa"
)

// vertexSAModeAdapter 把 Google Service Account(client_email + private_key)按 RFC 7523
// 铸成短期 access token 存回凭据(access_token + expires_at),供 materialization 走
// upstream_passthrough(access_token→"Bearer <tok>")+ project_id 产出 Vertex adapter 所需
// 凭据。此前 raw SA 在 metadataTokenAdapter 处得 ErrNoRefreshRequired→永不铸造=fail-closed(M1)。
// 无 SA 私钥材料(metadata-only)则回退 metadataTokenAdapter。
type vertexSAModeAdapter struct {
	client   *http.Client
	fallback metadataTokenAdapter
}

func (a vertexSAModeAdapter) RefreshCredential(ctx context.Context, in ModeRefreshInput) (ModeRefreshResult, error) {
	fields, err := payloadMap(in.Payload)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	clientEmail := stringField(fields, "client_email")
	privateKey := stringField(fields, "private_key")
	if clientEmail == "" || privateKey == "" {
		// 无 SA 私钥材料 → metadata_token_endpoint 路径(metadata-only)。
		return a.fallback.RefreshCredential(ctx, in)
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	client := a.client
	if client == nil {
		// vertexsa 自带 token_uri host allowlist(仅 *.googleapis.com);外层再叠 SSRF 保护客户端。
		client = auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
	}
	tok, err := vertexsa.Mint(ctx, client, vertexsa.ServiceAccount{
		ClientEmail:   clientEmail,
		PrivateKeyPEM: privateKey,
		TokenURI:      stringField(fields, "token_uri"),
		Scope:         stringField(fields, "scope"),
	}, now)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	fields["access_token"] = tok.AccessToken
	fields["expires_at"] = tok.ExpiresAt.Format(time.RFC3339)
	payload, err := json.Marshal(fields)
	if err != nil {
		return ModeRefreshResult{}, err
	}
	return ModeRefreshResult{Payload: payload, AccessExpiresAt: tok.ExpiresAt, Outcome: "refresh_succeeded"}, nil
}
