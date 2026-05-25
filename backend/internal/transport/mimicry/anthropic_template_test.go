package mimicry

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

const anthropicCLIMimicryV1Fixture = "testdata/anthropic-cli-mimicry-v1.json"
const runtimeAnthropicTemplateFixture = "../../../../tools/fingerprint-collector/templates/anthropic-claude-code.json"

func TestAnthropicCLIMimicryV1CapturedTemplateLoads(t *testing.T) {
	tmpl, err := LoadFromCollectorOutput(anthropicCLIMimicryV1Fixture)
	if err != nil {
		t.Fatalf("load captured anthropic cli template: %v", err)
	}
	if tmpl.ModeName != SidecarProfileAnthropicCLIMimicryV1 {
		t.Fatalf("mode_name = %q, want %q", tmpl.ModeName, SidecarProfileAnthropicCLIMimicryV1)
	}
	if tmpl.JA3 == "" {
		t.Fatal("ja3 input string must be non-empty")
	}
	if tmpl.JA4 == "" {
		t.Fatal("ja4 hash must be non-empty")
	}
	if !containsUint16(tmpl.Extensions, 0) {
		t.Fatalf("extensions missing server_name/SNI extension 0: %v", tmpl.Extensions)
	}
	if !strings.HasPrefix(tmpl.JA4, "t13d") {
		t.Fatalf("ja4 = %q, want t13d prefix showing SNI is present", tmpl.JA4)
	}
	for _, want := range []uint16{0x1301, 0x1302, 0x1303} {
		if !containsUint16(tmpl.CipherSuites, want) {
			t.Fatalf("cipher_suites missing TLS 1.3 cipher 0x%04x: %v", want, tmpl.CipherSuites)
		}
	}

	var raw struct {
		JA3 struct {
			Hash string `json:"hash"`
		} `json:"ja3"`
		JA4 struct {
			Hash string `json:"hash"`
		} `json:"ja4"`
	}
	data, err := os.ReadFile(anthropicCLIMimicryV1Fixture)
	if err != nil {
		t.Fatalf("read raw captured template: %v", err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode raw captured template: %v", err)
	}
	if raw.JA3.Hash == "" {
		t.Fatal("raw ja3.hash must be non-empty")
	}
	if raw.JA4.Hash == "" {
		t.Fatal("raw ja4.hash must be non-empty")
	}
}

func TestAnthropicCLIMimicryV1BuiltinMatchesCapturedTemplate(t *testing.T) {
	captured, err := LoadFromCollectorOutput(anthropicCLIMimicryV1Fixture)
	if err != nil {
		t.Fatalf("load captured anthropic cli template: %v", err)
	}
	builtin := AnthropicCLIMimicryV1Template()
	runtimeTemplate, err := LoadFromCollectorOutput(runtimeAnthropicTemplateFixture)
	if err != nil {
		t.Fatalf("load runtime anthropic cli template: %v", err)
	}
	assertAnthropicTemplateFieldsMatch(t, "builtin", builtin, captured)
	assertAnthropicTemplateFieldsMatch(t, "runtime", runtimeTemplate, captured)
}

func assertAnthropicTemplateFieldsMatch(t *testing.T, name string, got, captured *ClientHelloTemplate) {
	t.Helper()
	checks := []struct {
		field string
		got   any
		want  any
	}{
		{"ja3", got.JA3, captured.JA3},
		{"ja4", got.JA4, captured.JA4},
		{"cipher_suites", got.CipherSuites, captured.CipherSuites},
		{"extensions", got.Extensions, captured.Extensions},
		{"supported_versions", got.SupportedVersions, captured.SupportedVersions},
		{"curves", got.EllipticCurves, captured.EllipticCurves},
		{"sig_algos", got.SignatureAlgorithms, captured.SignatureAlgorithms},
		{"alpn_protocols", got.ALPNProtocols, captured.ALPNProtocols},
		{"ec_point_formats", got.ECPointFormats, captured.ECPointFormats},
		{"key_share_groups", got.KeyShareGroups, captured.KeyShareGroups},
		{"psk_modes", got.PSKModes, captured.PSKModes},
		{"padding_len", got.PaddingLen, captured.PaddingLen},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Fatalf("%s %s = %#v, want captured %#v", name, check.field, check.got, check.want)
		}
	}
}

func containsUint16(values []uint16, want uint16) bool {
	for _, got := range values {
		if got == want {
			return true
		}
	}
	return false
}
