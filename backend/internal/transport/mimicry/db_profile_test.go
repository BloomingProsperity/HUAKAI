package mimicry

import "testing"

func validProfileFields() ProfileFields {
	return ProfileFields{
		ID:                   77,
		Name:                 "tenant-chrome",
		GreaseEnabled:        true,
		CipherSuites:         []int{0x1301, 0x1302, 0xc02b},
		SupportedCurves:      []int{29, 23, 24},
		EcPointFormats:       []int{0},
		SignatureAlgorithms:  []int{0x0403, 0x0804},
		AlpnProtocols:        []string{"h2", "http/1.1"},
		TLSSupportedVersions: []int{0x0304, 0x0303},
		KeyShareGroups:       []int{29, 23},
		PskModes:             []int{1},
		ExtensionsOrder:      []int{0, 23, 65281, 10, 11, 13, 16, 43, 45, 51},
		ExpectedJA3Hash:      "abc123",
	}
}

func TestInlineTLSProfileFromFieldsPreservesDynamicWireFields(t *testing.T) {
	fields := validProfileFields()
	profile, err := InlineTLSProfileFromFields(fields)
	if err != nil {
		t.Fatalf("InlineTLSProfileFromFields: %v", err)
	}
	if profile.ID != "db-profile-77" || !profile.GreaseEnabled {
		t.Fatalf("标量字段错误: %+v", profile)
	}
	if len(profile.CipherSuites) != 3 || profile.CipherSuites[2] != 0xc02b {
		t.Fatalf("cipher_suites 丢失: %v", profile.CipherSuites)
	}
	if len(profile.ExtensionsOrder) != 10 || profile.ExtensionsOrder[2] != 65281 {
		t.Fatalf("extensions_order 丢失: %v", profile.ExtensionsOrder)
	}
	if len(profile.ALPNProtocols) != 2 || profile.ALPNProtocols[0] != "h2" {
		t.Fatalf("ALPN 丢失: %v", profile.ALPNProtocols)
	}
}

func TestInlineTLSProfileFromFieldsRejectsPresetAndIncompleteProfile(t *testing.T) {
	for name, fields := range map[string]ProfileFields{
		"preset":     {ID: 9, Name: "preset:chrome"},
		"incomplete": {ID: 10, Name: "empty"},
	} {
		t.Run(name, func(t *testing.T) {
			if profile, err := InlineTLSProfileFromFields(fields); err == nil || profile != nil {
				t.Fatalf("不可执行 profile 必须明确失败，profile=%+v err=%v", profile, err)
			}
		})
	}
}

func TestTemplateFromProfileFields_Valid(t *testing.T) {
	tmpl, err := TemplateFromProfileFields(validProfileFields())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tmpl.ModeName != "tenant-chrome" || !tmpl.GREASE || tmpl.JA3 != "abc123" {
		t.Fatalf("scalar fields wrong: %+v", tmpl)
	}
	if len(tmpl.CipherSuites) != 3 || tmpl.CipherSuites[0] != 0x1301 || tmpl.CipherSuites[2] != 0xc02b {
		t.Fatalf("cipher suites not widened correctly: %v", tmpl.CipherSuites)
	}
	if len(tmpl.EllipticCurves) != 3 || tmpl.EllipticCurves[0] != 29 {
		t.Fatalf("curves wrong: %v", tmpl.EllipticCurves)
	}
	if len(tmpl.ECPointFormats) != 1 || tmpl.ECPointFormats[0] != 0 {
		t.Fatalf("ec point formats wrong: %v", tmpl.ECPointFormats)
	}
	if len(tmpl.Extensions) != 10 || tmpl.Extensions[2] != 65281 {
		t.Fatalf("extensions order wrong: %v", tmpl.Extensions)
	}
	if len(tmpl.SupportedVersions) != 2 || tmpl.SupportedVersions[0] != 0x0304 {
		t.Fatalf("versions wrong: %v", tmpl.SupportedVersions)
	}
}

// 变异守卫:去掉 uint16 范围检查会让一个越界的 cipher id 静默截断后写入
// ClientHello(损坏 JA3 / 破坏握手)-> 这条(期望返回 error 的)断言会变红。
func TestTemplateFromProfileFields_RejectsOutOfRangeUint16(t *testing.T) {
	f := validProfileFields()
	f.CipherSuites = []int{0x1301, 0x10000} // 65536 > uint16 最大值
	if _, err := TemplateFromProfileFields(f); err == nil {
		t.Fatal("expected out-of-range cipher id to fail loud (-> caller falls back to builtin), got nil")
	}
}

func TestTemplateFromProfileFields_RejectsOutOfRangeUint8(t *testing.T) {
	f := validProfileFields()
	f.EcPointFormats = []int{256} // > uint8 最大值
	if _, err := TemplateFromProfileFields(f); err == nil {
		t.Fatal("expected out-of-range ec point format to fail loud, got nil")
	}
}

func TestTemplateFromProfileFields_RejectsIncomplete(t *testing.T) {
	f := validProfileFields()
	f.CipherSuites = nil
	if _, err := TemplateFromProfileFields(f); err == nil {
		t.Fatal("expected incomplete profile (no cipher suites) to fail loud, got nil")
	}
}
