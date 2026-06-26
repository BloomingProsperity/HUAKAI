package mimicry

import "testing"

func TestValidateProfileFields(t *testing.T) {
	// 已知 preset 有效(无需 cipher 数组)
	if err := ValidateProfileFields(ProfileFields{Name: "preset:chrome"}); err != nil {
		t.Fatalf("preset:chrome should be valid: %v", err)
	}
	// 未知 preset 无效
	if err := ValidateProfileFields(ProfileFields{Name: "preset:netscape"}); err == nil {
		t.Fatal("unknown preset should be invalid")
	}
	// 越界 cipher id -> 无效(转换器的范围检查)
	if err := ValidateProfileFields(ProfileFields{
		CipherSuites: []int{0x10000}, SupportedCurves: []int{29}, TLSSupportedVersions: []int{0x0304},
	}); err == nil {
		t.Fatal("out-of-range cipher must be invalid")
	}
	// 字段不完整的自定义 profile -> 无效
	if err := ValidateProfileFields(ProfileFields{Name: "tenant-x"}); err == nil {
		t.Fatal("incomplete custom profile must be invalid")
	}
}
