package grok

import (
	"context"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func TestGrokSession_BuildRequest_SessionTokenWithCFClearance(t *testing.T) {
	a := &GrokSessionAdapter{}
	in := provider.BuildInput{
		InboundBody: []byte(`{"message":"hi"}`),
		Credential: provider.Credential{
			Type:  provider.CredentialTypeSessionToken,
			Value: "ssotok123",
			Extra: map[string]string{"cf_clearance": "cfval456"},
		},
	}
	req, err := a.BuildRequest(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	// MUTATION GUARD (headers): dropping applyGrokWebHeaders leaves these empty ->
	// grok's WAF fingerprints us as a non-browser client -> red.
	if req.Header.Get("Origin") != "https://grok.com" {
		t.Fatalf("Origin=%q want https://grok.com", req.Header.Get("Origin"))
	}
	if req.Header.Get("X-Statsig-Id") != grokStatsigID {
		t.Fatalf("X-Statsig-Id missing/wrong: %q", req.Header.Get("X-Statsig-Id"))
	}
	if !strings.Contains(req.Header.Get("User-Agent"), "Chrome/133") {
		t.Fatalf("User-Agent not grok-web Chrome: %q", req.Header.Get("User-Agent"))
	}
	// MUTATION GUARD (cookie): dropping the cf_clearance branch drops the 5s-shield
	// bypass token -> red.
	cookie := req.Header.Get("Cookie")
	for _, want := range []string{"sso=ssotok123", "sso-rw=ssotok123", "cf_clearance=cfval456"} {
		if !strings.Contains(cookie, want) {
			t.Fatalf("Cookie %q missing %q", cookie, want)
		}
	}
}

func TestGrokSession_BuildRequest_RejectsAPIKey(t *testing.T) {
	a := &GrokSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		Credential: provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-x"},
	})
	if err == nil {
		t.Fatal("apikey must be rejected on the grok web-session path")
	}
}

func TestGrokSession_BuildRequest_RejectsEmptyValue(t *testing.T) {
	a := &GrokSessionAdapter{}
	_, err := a.BuildRequest(context.Background(), provider.BuildInput{
		Credential: provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "  "},
	})
	if err == nil {
		t.Fatal("empty sso token must be rejected")
	}
}

func TestGrokSession_BuildRequest_PassthroughCookieVerbatim(t *testing.T) {
	a := &GrokSessionAdapter{}
	const full = "sso=a; sso-rw=a; cf_clearance=b; extra=c"
	req, err := a.BuildRequest(context.Background(), provider.BuildInput{
		InboundBody: []byte("{}"),
		Credential:  provider.Credential{Type: provider.CredentialTypeUpstreamPassthrough, Value: full},
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Cookie") != full {
		t.Fatalf("upstream_passthrough Cookie=%q want verbatim %q", req.Header.Get("Cookie"), full)
	}
}

func TestBuildGrokCookie_CFClearancePrefixNotDoubled(t *testing.T) {
	c := buildGrokCookie(provider.Credential{
		Type:  provider.CredentialTypeSessionToken,
		Value: "s",
		Extra: map[string]string{"cf_clearance": "cf_clearance=raw"},
	})
	if strings.Contains(c, "cf_clearance=cf_clearance=") {
		t.Fatalf("cf_clearance prefix doubled: %q", c)
	}
	if !strings.Contains(c, "cf_clearance=raw") {
		t.Fatalf("cf_clearance value lost: %q", c)
	}
}
