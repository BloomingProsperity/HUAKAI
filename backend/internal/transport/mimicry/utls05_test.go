package mimicry

import (
	"testing"

	utls "github.com/refraction-networking/utls"
)

// UTLS-05:内置浏览器的 uTLS ClientHello preset(指纹来自 uTLS 真实值,
// 不手写 cipher 数组)。
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
	// chrome 映射到 uTLS 真实的 Chrome ClientHello(与 CLIProxyAPI 对齐)。
	id, ok := clientHelloIDForPreset("chrome")
	if !ok || id != utls.HelloChrome_Auto {
		t.Fatalf("chrome -> %v,%v want HelloChrome_Auto", id, ok)
	}
}

// 名为 "preset:chrome" 的 DB profile(无手写 cipher)会转换成 preset 形式的
// ClientHelloTemplate。变异守护:若忽略 preset 名,就会落到完整性检查上,
// 因 cipher 列表为空而报错。
func TestTemplateFromProfileFields_PresetByName(t *testing.T) {
	tmpl, err := TemplateFromProfileFields(ProfileFields{Name: "preset:chrome", ExpectedJA3Hash: "x"})
	if err != nil {
		t.Fatalf("preset profile should not error (no ciphers needed): %v", err)
	}
	if tmpl.Preset != "chrome" {
		t.Fatalf("Preset=%q want chrome", tmpl.Preset)
	}
	// 尾随空格 / 大小写已处理
	tmpl2, err := TemplateFromProfileFields(ProfileFields{Name: "preset: Firefox "})
	if err != nil || tmpl2.Preset != "Firefox" {
		t.Fatalf("preset trim failed: %q err=%v", tmpl2.Preset, err)
	}
	// 普通(非 preset)profile 仍要求字段完整
	if _, err := TemplateFromProfileFields(ProfileFields{Name: "tenant-x"}); err == nil {
		t.Fatal("non-preset incomplete profile must still fail loud")
	}
}
