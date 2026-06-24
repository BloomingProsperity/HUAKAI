package mimicry

import (
	"testing"

	utls "github.com/refraction-networking/utls"
)

// TestClientHelloIDForPreset 守护内置浏览器 preset 映射:浏览器名应解析到 uTLS
// 内置 ClientHello,空值或未知名应 fail-loud,避免手写 cipher 数组。
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
	// chrome 映射到 uTLS 内置的真实 Chrome ClientHello。
	id, ok := clientHelloIDForPreset("chrome")
	if !ok || id != utls.HelloChrome_Auto {
		t.Fatalf("chrome -> %v,%v want HelloChrome_Auto", id, ok)
	}
}

// TestTemplateFromProfileFields_PresetByName 守护 DB profile 的 preset 捷径:
// name=preset:<browser> 可转换为 ClientHelloTemplate。变异证伪:若忽略 preset
// 名称,代码会落入完整性检查并因空 cipher 列表报错。
func TestTemplateFromProfileFields_PresetByName(t *testing.T) {
	tmpl, err := TemplateFromProfileFields(ProfileFields{Name: "preset:chrome", ExpectedJA3Hash: "x"})
	if err != nil {
		t.Fatalf("preset profile should not error (no ciphers needed): %v", err)
	}
	if tmpl.Preset != "chrome" {
		t.Fatalf("Preset=%q want chrome", tmpl.Preset)
	}
	// 前后空格和大小写只影响匹配,不改写原始 preset 值。
	tmpl2, err := TemplateFromProfileFields(ProfileFields{Name: "preset: Firefox "})
	if err != nil || tmpl2.Preset != "Firefox" {
		t.Fatalf("preset trim failed: %q err=%v", tmpl2.Preset, err)
	}
	// 普通非 preset profile 仍必须提供完整 TLS 字段。
	if _, err := TemplateFromProfileFields(ProfileFields{Name: "tenant-x"}); err == nil {
		t.Fatal("non-preset incomplete profile must still fail loud")
	}
}
