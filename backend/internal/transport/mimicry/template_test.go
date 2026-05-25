package mimicry

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	utls "github.com/refraction-networking/utls"
)

const collectorFixture = "../../../../tools/fingerprint-collector/templates/anthropic-claude-code.json"
const mergedTemplateFixture = "../../../../tools/fingerprint-collector/templates/anthropic-claude-code.json"
const legacyCollectorFixture = "testdata/clienthello-template.json"
const codexTemplateFixture = "../../../../tools/fingerprint-collector/templates/codex-cli.json"
const kiroTemplateFixture = "../../../../tools/fingerprint-collector/templates/kiro-cli.json"
const geminiTemplateFixture = "../../../../tools/fingerprint-collector/templates/gemini-advanced.json"

func TestLoadFromCollectorOutput_AnthropicSample(t *testing.T) {
	tmpl, err := LoadFromCollectorOutput(collectorFixture)
	if err != nil {
		t.Fatalf("load collector fixture: %v", err)
	}
	want := AnthropicCLIMimicryV1Template()
	if tmpl.JA4 != want.JA4 || len(tmpl.CipherSuites) != len(want.CipherSuites) {
		t.Fatalf("collector fixture 未正确加载: %#v", tmpl)
	}
	if !containsUint16(tmpl.Extensions, 0) {
		t.Fatalf("collector fixture 缺少 SNI extension 0: %v", tmpl.Extensions)
	}
	if got, want := tmpl.SignatureAlgorithms, want.SignatureAlgorithms; !reflect.DeepEqual(got, want) {
		t.Fatalf("sig algos = %v, want %v", got, want)
	}
}

func TestLoadFromCollectorOutput_PhaseAMergedTemplate(t *testing.T) {
	tmpl, err := LoadFromCollectorOutput(mergedTemplateFixture)
	if err != nil {
		t.Fatalf("load merged template: %v", err)
	}
	if tmpl.ModeName != SidecarProfileAnthropicCLIMimicryV1 {
		t.Fatalf("mode_name = %q, want %q", tmpl.ModeName, SidecarProfileAnthropicCLIMimicryV1)
	}
}

func TestLoadFromCollectorOutput_R3RealTemplatesHaveHTTPLayer(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		protocol   string
		endpoint   string
		tlsBackend string
		grease     bool
	}{
		{
			name:       "codex",
			path:       codexTemplateFixture,
			protocol:   "h2_or_http1.1_reqwest_default",
			endpoint:   "https://chatgpt.com/backend-api/codex/responses",
			tlsBackend: "native-tls/openssl",
			grease:     false,
		},
		{
			name:       "kiro",
			path:       kiroTemplateFixture,
			protocol:   "http1.1",
			endpoint:   "https://q.us-east-1.amazonaws.com/",
			tlsBackend: "rustls",
			grease:     true,
		},
		{
			name:       "gemini",
			path:       geminiTemplateFixture,
			protocol:   "http1.1",
			endpoint:   "https://cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse",
			tlsBackend: "nodejs",
			grease:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpl, err := LoadFromCollectorOutput(tt.path)
			if err != nil {
				t.Fatalf("load %s template: %v", tt.name, err)
			}
			if tmpl.IsStub() {
				t.Fatalf("%s 模板已回填，不应再是 stub", tt.name)
			}
			if tmpl.TLSBackend != tt.tlsBackend {
				t.Fatalf("tls_backend = %q, want %q", tmpl.TLSBackend, tt.tlsBackend)
			}
			if tmpl.GREASE != tt.grease {
				t.Fatalf("grease = %v, want %v", tmpl.GREASE, tt.grease)
			}
			if tmpl.HTTPLayer.Protocol != tt.protocol {
				t.Fatalf("protocol = %q, want %q", tmpl.HTTPLayer.Protocol, tt.protocol)
			}
			if tmpl.HTTPLayer.Endpoint != tt.endpoint {
				t.Fatalf("endpoint = %q, want %q", tmpl.HTTPLayer.Endpoint, tt.endpoint)
			}
			if tmpl.HTTPLayer.UserAgent == "" {
				t.Fatal("user_agent 不应为空")
			}
			if len(tmpl.HTTPLayer.HeaderOrder) == 0 {
				t.Fatal("header_order 不应为空")
			}
			if tmpl.HTTPLayer.AuthMechanism == "" {
				t.Fatal("auth_mechanism 不应为空")
			}
		})
	}
}

func TestLoadFromCollectorOutput_GeminiAdvancedBackfilled(t *testing.T) {
	tmpl, err := LoadFromCollectorOutput(geminiTemplateFixture)
	if err != nil {
		t.Fatalf("load gemini template: %v", err)
	}
	if tmpl.IsStub() {
		t.Fatal("gemini-advanced 已回填真实指纹，不应再是 stub")
	}
	if tmpl.HTTPLayer.Protocol == "" || tmpl.HTTPLayer.Endpoint == "" {
		t.Fatalf("gemini http_layer 未完整加载: %#v", tmpl.HTTPLayer)
	}
	wantOrder := []string{
		"Content-Type",
		"User-Agent",
		"Authorization",
		"x-goog-api-client",
		"Accept",
		"Content-Length",
		"Accept-Encoding",
		"Host",
		"Connection",
	}
	if !reflect.DeepEqual(tmpl.HTTPLayer.HeaderOrder, wantOrder) {
		t.Fatalf("gemini header_order = %v, want %v", tmpl.HTTPLayer.HeaderOrder, wantOrder)
	}
}

func TestLoadFromCollectorOutput_TargetDirectory(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "openai_codex")
	if err := os.MkdirAll(targetDir, 0750); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(legacyCollectorFixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "clienthello-template.json"), data, 0640); err != nil {
		t.Fatal(err)
	}

	tmpl, err := LoadFromCollectorOutput(root, "openai_codex")
	if err != nil {
		t.Fatalf("load target collector output: %v", err)
	}
	if tmpl.ModeName != "openai_codex" {
		t.Fatalf("mode_name = %q, want openai_codex", tmpl.ModeName)
	}
}

func TestClientHelloTemplate_ValidateRejectsEmpty(t *testing.T) {
	if err := (&ClientHelloTemplate{}).Validate(); err == nil {
		t.Fatal("空模板应被拒绝")
	}
}

func TestClientHelloTemplate_ValidateRejectsMissingRequiredField(t *testing.T) {
	tmpl := PhaseADefaultTemplate()
	tmpl.CipherSuites = nil
	if err := tmpl.Validate(); err == nil {
		t.Fatal("缺少 cipher_suites 应被拒绝")
	}
}

func TestClientHelloTemplate_JA3ToUTLSSpecRoundTrip(t *testing.T) {
	tmpl, err := LoadFromCollectorOutput(collectorFixture)
	if err != nil {
		t.Fatal(err)
	}
	_, ciphers, exts, curves, err := parseJA3(tmpl.JA3)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := tmpl.UTLSSpec()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(spec.CipherSuites, ciphers) {
		t.Fatalf("cipher suites roundtrip = %v, want %v", spec.CipherSuites, ciphers)
	}
	if got := withoutPadding(extensionIDs(t, spec.Extensions)); !reflect.DeepEqual(got, exts) {
		t.Fatalf("extensions roundtrip = %v, want %v", got, exts)
	}
	if !reflect.DeepEqual(tmpl.EllipticCurves, curves) {
		t.Fatalf("curves roundtrip = %v, want %v", tmpl.EllipticCurves, curves)
	}
}

func TestLoadFromCollectorOutput_MissingFile(t *testing.T) {
	if _, err := LoadFromCollectorOutput("missing-clienthello-template.json"); err == nil {
		t.Fatal("missing file 应返回错误")
	}
}

func TestClientHelloTemplate_UTLSSpecRejectsInvalidJA3(t *testing.T) {
	tmpl := PhaseADefaultTemplate()
	tmpl.JA3 = "bad"
	if _, err := tmpl.UTLSSpec(); err == nil {
		t.Fatal("非法 JA3 不应构造 uTLS spec")
	}
}

func extensionIDs(t *testing.T, exts []utls.TLSExtension) []uint16 {
	t.Helper()
	out := make([]uint16, 0, len(exts))
	for _, ext := range exts {
		switch e := ext.(type) {
		case *utls.SNIExtension:
			out = append(out, 0)
		case *utls.StatusRequestExtension:
			out = append(out, 5)
		case *utls.SupportedCurvesExtension:
			out = append(out, 10)
		case *utls.SupportedPointsExtension:
			out = append(out, 11)
		case *utls.SignatureAlgorithmsExtension:
			out = append(out, 13)
		case *utls.ALPNExtension:
			out = append(out, 16)
		case *utls.SCTExtension:
			out = append(out, 18)
		case *utls.UtlsPaddingExtension:
			out = append(out, 21)
		case *utls.ExtendedMasterSecretExtension:
			out = append(out, 23)
		case *utls.SessionTicketExtension:
			out = append(out, 35)
		case *utls.SupportedVersionsExtension:
			out = append(out, 43)
		case *utls.PSKKeyExchangeModesExtension:
			out = append(out, 45)
		case *utls.KeyShareExtension:
			out = append(out, 51)
		case *utls.GREASEEncryptedClientHelloExtension:
			out = append(out, 65037)
		case *utls.RenegotiationInfoExtension:
			out = append(out, 65281)
		case *utls.GenericExtension:
			out = append(out, e.Id)
		default:
			t.Fatalf("未知 uTLS extension 类型 %T", ext)
		}
	}
	return out
}

func withoutPadding(in []uint16) []uint16 {
	out := in[:0]
	for _, id := range in {
		if id != 21 {
			out = append(out, id)
		}
	}
	return out
}
