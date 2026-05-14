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
const legacyCollectorFixture = "../../../../tools/fingerprint-collector/output/clienthello-template.json"

func TestLoadFromCollectorOutput_AnthropicSample(t *testing.T) {
	tmpl, err := LoadFromCollectorOutput(collectorFixture)
	if err != nil {
		t.Fatalf("load collector fixture: %v", err)
	}
	if tmpl.JA4 == "" || len(tmpl.CipherSuites) != 17 {
		t.Fatalf("collector fixture 未正确加载: %#v", tmpl)
	}
	if got, want := tmpl.SignatureAlgorithms, anthropicSigAlgos(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sig algos = %v, want %v", got, want)
	}
}

func TestLoadFromCollectorOutput_PhaseAMergedTemplate(t *testing.T) {
	tmpl, err := LoadFromCollectorOutput(mergedTemplateFixture)
	if err != nil {
		t.Fatalf("load merged template: %v", err)
	}
	if tmpl.ModeName != "anthropic-claude-code" {
		t.Fatalf("mode_name = %q, want anthropic-claude-code", tmpl.ModeName)
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
