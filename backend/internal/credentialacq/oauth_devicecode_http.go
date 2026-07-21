package credentialacq

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/oauthwire"
)

func postJSON(ctx context.Context, client *http.Client, rawURL string, body map[string]any, out any) error {
	return oauthwire.PostJSON(ctx, client, rawURL, body, out)
}

func postJSONStatus(ctx context.Context, client *http.Client, rawURL string, body map[string]any, out any) (int, error) {
	return oauthwire.PostJSONStatus(ctx, client, rawURL, body, out)
}

func postFormJSON(ctx context.Context, client *http.Client, rawURL string, form url.Values, out any) (int, error) {
	return oauthwire.PostFormJSON(ctx, client, rawURL, form, out)
}

func normalizedTokenPayload(response map[string]any, accessToken string) ([]byte, error) {
	return oauthwire.NormalizeTokenPayload(response, accessToken)
}

func mergeRedactedContext(base, extra map[string]any) map[string]any {
	return oauthwire.MergeRedactedContext(base, extra)
}

func issuedAtFromPayload(payload map[string]any, fallback time.Time) time.Time {
	return oauthwire.IssuedAt(payload, fallback)
}

func stringFromPayload(payload map[string]any, key string) string {
	return oauthwire.String(payload, key)
}

func intFromPayload(payload map[string]any, key string) int {
	return oauthwire.Int(payload, key)
}

func firstPositive(values ...int) int {
	return oauthwire.FirstPositive(values...)
}

func sleepDeviceContext(ctx context.Context, duration time.Duration) error {
	return oauthwire.Sleep(ctx, duration)
}

func resolveOpenAICodexOAuthTokenURL(deviceTokenURL string) string {
	return oauthwire.ResolveTokenURL(deviceTokenURL, openAICodexOAuthTokenURL)
}

func defaultDeviceCodeHTTPClient() *http.Client {
	return oauthwire.DefaultHTTPClient()
}
