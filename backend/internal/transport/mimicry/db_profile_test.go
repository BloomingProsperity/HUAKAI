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

func TestValidateProfileFieldsUsesRustIPCContract(t *testing.T) {
	if err := ValidateProfileFields(validProfileFields()); err != nil {
		t.Fatalf("完整动态 profile 应可交给 Rust IPC：%v", err)
	}
	for name, fields := range map[string]ProfileFields{
		"浏览器预设": {Name: "preset:chrome"},
		"字段不完整": {Name: "tenant-empty"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateProfileFields(fields); err == nil {
				t.Fatal("Rust 无法执行的 profile 不应被健康检查放行")
			}
		})
	}
}

// 变异守卫：去掉范围检查会让越界值静默截断后进入 Rust 控制帧。
func TestInlineTLSProfileFromFieldsRejectsOutOfRangeValues(t *testing.T) {
	f := validProfileFields()
	f.CipherSuites = []int{0x1301, 0x10000}
	if _, err := InlineTLSProfileFromFields(f); err == nil {
		t.Fatal("越界 cipher id 必须明确失败")
	}
	f = validProfileFields()
	f.EcPointFormats = []int{256}
	if _, err := InlineTLSProfileFromFields(f); err == nil {
		t.Fatal("越界 ec point format 必须明确失败")
	}
}
