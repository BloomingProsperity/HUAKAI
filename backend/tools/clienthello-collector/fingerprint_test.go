package main

import (
	"strings"
	"testing"
)

func TestComputeJA4ALPNTokenFollowsHUAKAICompatibility(t *testing.T) {
	tests := []struct {
		name string
		alpn []string
		want string
	}{
		{name: "empty", alpn: nil, want: "00"},
		{name: "single h2", alpn: []string{"h2"}, want: "h2"},
		{name: "single http1", alpn: []string{"http/1.1"}, want: "ht"},
		{name: "h2 then http1", alpn: []string{"h2", "http/1.1"}, want: "ht"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ja4Token(computeJA4(clientHelloWithALPN(tt.alpn)).Hash)
			if got != tt.want {
				t.Fatalf("ja4 ALPN token for %v = %q, want %q", tt.alpn, got, tt.want)
			}
		})
	}
}

func TestComputeJA4DualALPNDiffersFromFirstALPNOnlyBaseline(t *testing.T) {
	firstOnly := computeJA4(clientHelloWithALPN([]string{"h2"})).Hash
	dual := computeJA4(clientHelloWithALPN([]string{"h2", "http/1.1"})).Hash

	if firstOnly == dual {
		t.Fatalf("fixture is not discriminating: single and dual ALPN both produced %q", dual)
	}
	if token := ja4Token(dual); token != "ht" {
		t.Fatalf("dual ALPN JA4 token = %q in %q, want ht", token, dual)
	}
	if dual != "t13d0103_ht_67e912e3e2b5_0ed113d2eb58" {
		t.Fatalf("dual ALPN JA4 = %q, want Gemini-compatible ht hash", dual)
	}
}

func clientHelloWithALPN(alpn []string) *clientHello {
	extensions := []extension{
		{Type: extServerName, SNIHostname: "api.anthropic.com"},
		{Type: extSupportedVersions, SupportedVersions: []uint16{0x0304}},
	}
	if alpn != nil {
		extensions = append(extensions, extension{Type: extALPN, ALPNProtocols: alpn})
	}
	return &clientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301},
		Extensions:    extensions,
	}
}

func ja4Token(hash string) string {
	parts := strings.Split(hash, "_")
	if len(parts) != 4 {
		return ""
	}
	return parts[1]
}
