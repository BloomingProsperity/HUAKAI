package mimicry

import (
	"testing"

	utls "github.com/refraction-networking/utls"
)

// UTLS-05: builtin-browser uTLS ClientHello presets (real fingerprints from uTLS,
// no hand-authored cipher arrays).
func TestClientHelloIDForPreset(t *testing.T) {
	for _, p := range []string{"chrome", "Chrome", "  firefox  ", "safari", "edge", "ios"} {
		if _, ok := clientHelloIDForPreset(p); !ok {
			t.Errorf("preset %q should resolve", p)
		}
	}
	for _, p := range []string{"", "  ", "netscape", "custom", "lynx"} {
		if _, ok := clientHelloIDForPreset(p); ok {
			t.Errorf("preset %q should NOT resolve", p)
		}
	}
	// chrome maps to uTLS's real Chrome ClientHello (parity with CLIProxyAPI).
	id, ok := clientHelloIDForPreset("chrome")
	if !ok || id != utls.HelloChrome_Auto {
		t.Fatalf("chrome -> %v,%v want HelloChrome_Auto", id, ok)
	}
}

// A DB profile named "preset:chrome" (no hand-authored ciphers) converts to a
// preset ClientHelloTemplate. MUTATION GUARD: ignoring the preset name falls
// through to the completeness check and errors on the empty cipher list.
func TestTemplateFromProfileFields_PresetByName(t *testing.T) {
	tmpl, err := TemplateFromProfileFields(ProfileFields{Name: "preset:chrome", ExpectedJA3Hash: "x"})
	if err != nil {
		t.Fatalf("preset profile should not error (no ciphers needed): %v", err)
	}
	if tmpl.Preset != "chrome" {
		t.Fatalf("Preset=%q want chrome", tmpl.Preset)
	}
	// trailing space / case handled
	tmpl2, err := TemplateFromProfileFields(ProfileFields{Name: "preset: Firefox "})
	if err != nil || tmpl2.Preset != "Firefox" {
		t.Fatalf("preset trim failed: %q err=%v", tmpl2.Preset, err)
	}
	// a normal (non-preset) profile still requires complete fields
	if _, err := TemplateFromProfileFields(ProfileFields{Name: "tenant-x"}); err == nil {
		t.Fatal("non-preset incomplete profile must still fail loud")
	}
}
