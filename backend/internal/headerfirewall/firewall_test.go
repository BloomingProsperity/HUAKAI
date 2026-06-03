package headerfirewall

import (
	"context"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/platformsettings"
)

func TestFilterResponseHeadersStripsBuiltInSensitiveHeadersAndKeepsOrdinaryHeaders(t *testing.T) {
	upstream := make(http.Header)
	upstream.Set("Set-Cookie", "session=redacted")
	upstream.Set("Set-Cookie2", "legacy=redacted")
	upstream.Set("Authorization", "Bearer redacted")
	upstream.Set("Proxy-Authenticate", "Basic")
	upstream.Set("WWW-Authenticate", "Bearer")
	upstream.Set("CF-Ray", "edge-redacted")
	upstream.Set("X-Amz-Request-Id", "aws-redacted")
	upstream.Set("X-Amzn-Trace-Id", "trace-redacted")
	upstream.Set("X-Real-IP", "192.0.2.10")
	upstream.Set("Server", "upstream")
	upstream.Set("Content-Type", "application/json")
	upstream.Set("X-Runner-Trace", "keep")

	filtered := FilterResponseHeaders(upstream, nil, nil)

	for _, name := range []string{
		"Set-Cookie",
		"Set-Cookie2",
		"Authorization",
		"Proxy-Authenticate",
		"WWW-Authenticate",
		"CF-Ray",
		"X-Amz-Request-Id",
		"X-Amzn-Trace-Id",
		"X-Real-IP",
		"Server",
	} {
		if filtered.Get(name) != "" {
			t.Fatalf("%s leaked through default response header firewall", name)
		}
	}
	if got := filtered.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want application/json", got)
	}
	if got := filtered.Get("X-Runner-Trace"); got != "keep" {
		t.Fatalf("X-Runner-Trace=%q want keep", got)
	}
}

func TestFilterResponseHeadersExtraDenyCanBeAllowedOnlyForNonBuiltInHeaders(t *testing.T) {
	upstream := make(http.Header)
	upstream.Set("Set-Cookie", "session=redacted")
	upstream.Set("X-Debug-Trace", "debug-redacted")
	upstream.Set("Content-Type", "text/plain")

	filtered := FilterResponseHeaders(upstream, []string{"X-Debug-"}, []string{"X-Debug-", "Set-Cookie"})

	if filtered.Get("Set-Cookie") != "" {
		t.Fatal("allow override must not bypass built-in Set-Cookie deny")
	}
	if got := filtered.Get("X-Debug-Trace"); got != "debug-redacted" {
		t.Fatalf("X-Debug-Trace=%q want debug-redacted because allow override beats extra deny", got)
	}
	if got := filtered.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type=%q want text/plain", got)
	}
}

func TestParseListTrimsEmptySettingAndSplitsCommaList(t *testing.T) {
	if got := ParseList(" "); len(got) != 0 {
		t.Fatalf("empty list len=%d want 0", len(got))
	}
	got := ParseList("X-Internal-,Server")
	if len(got) != 2 || got[0] != "X-Internal-" || got[1] != "Server" {
		t.Fatalf("parsed=%v want X-Internal-,Server", got)
	}
}

func TestPolicyFromPlatformSettingsFeedsExtraDenyAndAllowOverride(t *testing.T) {
	settings := settingStub{values: map[platformsettings.SettingKey]string{
		platformsettings.KeyResponseHeaderDenyExtra:     "X-Internal-,X-Debug-",
		platformsettings.KeyResponseHeaderAllowOverride: "X-Debug-",
	}}
	upstream := make(http.Header)
	upstream.Set("X-Internal-Trace", "drop")
	upstream.Set("X-Debug-Trace", "keep")
	upstream.Set("Content-Type", "application/json")

	policy := PolicyFromPlatformSettings(context.Background(), settings)
	filtered := FilterResponseHeaders(upstream, policy.ExtraDeny, policy.AllowOverride)

	if filtered.Get("X-Internal-Trace") != "" {
		t.Fatal("X-Internal-Trace leaked despite operator extra deny")
	}
	if got := filtered.Get("X-Debug-Trace"); got != "keep" {
		t.Fatalf("X-Debug-Trace=%q want keep from operator allow override", got)
	}
	if got := filtered.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type=%q want application/json", got)
	}
}

type settingStub struct {
	values map[platformsettings.SettingKey]string
}

func (s settingStub) Get(_ context.Context, key platformsettings.SettingKey) (platformsettings.StoredSetting, error) {
	return platformsettings.StoredSetting{Key: key, Value: s.values[key]}, nil
}
