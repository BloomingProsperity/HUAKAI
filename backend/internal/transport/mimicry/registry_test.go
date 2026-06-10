package mimicry

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const templateDirFixture = "../../../../tools/fingerprint-collector/templates"

func TestTemplateRegistry_RegisterLookupModes(t *testing.T) {
	registry := NewTemplateRegistry()
	tmpl := PhaseADefaultTemplate()
	if err := registry.Register(ModeMimicryClaudeCode, tmpl); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, ok := registry.Lookup(ModeMimicryClaudeCode)
	if !ok || got != tmpl {
		t.Fatalf("lookup = (%#v, %v), want registered template", got, ok)
	}
	if got := registry.Modes(); !reflect.DeepEqual(got, []TransportMode{ModeMimicryClaudeCode}) {
		t.Fatalf("modes = %v", got)
	}
}

func TestDefaultTemplateRegistryIncludesAnthropicCLIMimicryV1(t *testing.T) {
	registry := NewDefaultTemplateRegistry()
	tmpl, ok := registry.Lookup(ModeMimicryClaudeCode)
	if !ok {
		t.Fatalf("default registry missing Anthropic CLI mimicry mode; modes=%v", registry.Modes())
	}
	if tmpl.ModeName != SidecarProfileAnthropicCLIMimicryV1 {
		t.Fatalf("mode_name = %q, want %q", tmpl.ModeName, SidecarProfileAnthropicCLIMimicryV1)
	}
	if tmpl.TargetHost != "api.anthropic.com" {
		t.Fatalf("target_host = %q, want api.anthropic.com", tmpl.TargetHost)
	}
	if tmpl.IsStub() {
		t.Fatal("anthropic-cli-mimicry-v1 must be a real template, not a stub")
	}
	if tmpl.HTTPLayer.AuthMechanism != "oauth_bearer" {
		t.Fatalf("auth mechanism = %q, want oauth_bearer", tmpl.HTTPLayer.AuthMechanism)
	}
}

func TestAnthropicCLIMimicryProfileDiffersFromChatGPT(t *testing.T) {
	anthropic := AnthropicCLIMimicryV1Template()
	chatgpt, err := LoadFromCollectorOutput("../../../../tools/fingerprint-collector/templates/codex-cli.json")
	if err != nil {
		t.Fatalf("load chatgpt/codex template: %v", err)
	}
	if anthropic.JA3 == chatgpt.JA3 {
		t.Fatalf("wrong-profile fixture is not discriminating: Anthropic and ChatGPT JA3 both %q", anthropic.JA3)
	}
	if reflect.DeepEqual(anthropic.CipherSuites, chatgpt.CipherSuites) {
		t.Fatal("wrong-profile fixture is not discriminating: cipher suites are identical")
	}
}

func TestTemplateRegistry_RegisterRejectsDuplicate(t *testing.T) {
	registry := NewTemplateRegistry()
	if err := registry.Register(ModeMimicryClaudeCode, PhaseADefaultTemplate()); err != nil {
		t.Fatal(err)
	}
	err := registry.Register(ModeMimicryClaudeCode, PhaseADefaultTemplate())
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate register err = %v", err)
	}
}

func TestTemplateRegistry_LookupMissing(t *testing.T) {
	registry := NewTemplateRegistry()
	if got, ok := registry.Lookup(ModeMimicryKiro); ok || got != nil {
		t.Fatalf("missing lookup = (%#v, %v), want nil false", got, ok)
	}
}

func TestTemplateRegistry_LoadFromDirectoryScansRealTemplates(t *testing.T) {
	registry := NewTemplateRegistry()
	if err := registry.LoadFromDirectory(templateDirFixture); err != nil {
		t.Fatalf("load real template dir: %v", err)
	}
	for _, mode := range []TransportMode{
		ModeMimicryClaudeCode,
		ModeMimicryChatGPT,
		ModeMimicryGeminiAdvanced,
		ModeMimicryKiro,
	} {
		if _, ok := registry.Lookup(mode); !ok {
			t.Fatalf("mode %s 未从真实目录注册，modes=%v", mode, registry.Modes())
		}
	}
	claude, _ := registry.Lookup(ModeMimicryClaudeCode)
	codex, _ := registry.Lookup(ModeMimicryChatGPT)
	gemini, _ := registry.Lookup(ModeMimicryGeminiAdvanced)
	kiro, _ := registry.Lookup(ModeMimicryKiro)
	if claude.IsStub() {
		t.Fatal("anthropic 模板应是 real template")
	}
	if codex.IsStub() {
		t.Fatal("codex-cli 模板已回填，应是 real template")
	}
	if gemini.IsStub() {
		t.Fatal("gemini-advanced 模板已回填，应是 real template")
	}
	if kiro.IsStub() {
		t.Fatal("kiro-cli 模板已回填，应是 real template")
	}
}

func TestTemplateRegistry_LoadFromDirectoryFallsBackToModeName(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{
		"mode_name":"kiro_cli",
		"collected_at":"2026-05-14T00:00:00Z",
		"target_host":"kiro.dev",
		"ja3":"",
		"ja4":""
	}`)
	if err := os.WriteFile(filepath.Join(dir, "renamed-template.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewTemplateRegistry()
	if err := registry.LoadFromDirectory(dir); err != nil {
		t.Fatalf("load fallback fixture: %v", err)
	}
	if _, ok := registry.Lookup(ModeMimicryKiro); !ok {
		t.Fatalf("mode_name fallback 未注册 Kiro，modes=%v", registry.Modes())
	}
}

func TestClientHelloTemplate_ValidateStubVsReal(t *testing.T) {
	stub := &ClientHelloTemplate{
		ModeName:    "openai_codex_cli",
		CollectedAt: "2026-05-14T00:00:00Z",
		TargetHost:  "chatgpt.com",
	}
	if err := stub.Validate(); err != nil {
		t.Fatalf("stub template 应允许空 JA3/JA4: %v", err)
	}
	if !stub.IsStub() {
		t.Fatal("空 JA3/JA4 应标记为 stub")
	}
	real := PhaseADefaultTemplate()
	if err := real.Validate(); err != nil {
		t.Fatalf("real template validate: %v", err)
	}
	if real.IsStub() {
		t.Fatal("real template 不应标记为 stub")
	}
	partial := *stub
	partial.JA3 = "bad"
	if err := partial.Validate(); err == nil {
		t.Fatal("只填 JA3 不填 JA4 应被拒绝")
	}
}

// MUTATION: mimicry 两条出站(sidecar/uTLS)漏 MaxIdleConnsPerHost → 红(DM-17)。
func TestMimicryTransportsPoolTuning(t *testing.T) {
	srt := NewSidecarRoundTripper(NewSidecarClient("/tmp/x.sock"), "p1")
	tr, ok := srt.(*http.Transport)
	if !ok {
		t.Fatalf("sidecar RoundTripper 应是 *http.Transport, got %T", srt)
	}
	if tr.MaxIdleConnsPerHost != 64 || tr.MaxIdleConns != 256 {
		t.Fatalf("sidecar pool: per-host=%d total=%d, want 64/256", tr.MaxIdleConnsPerHost, tr.MaxIdleConns)
	}

	urt := NewRoundTripper(&ClientHelloTemplate{})
	wrapped, ok := urt.(*roundTripper)
	if !ok {
		t.Fatalf("uTLS RoundTripper 应是 *roundTripper, got %T", urt)
	}
	inner := wrapped.inner
	if inner.MaxIdleConnsPerHost != 64 || inner.MaxIdleConns != 256 {
		t.Fatalf("uTLS pool: per-host=%d total=%d, want 64/256", inner.MaxIdleConnsPerHost, inner.MaxIdleConns)
	}
}
