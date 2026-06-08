package mimicry

import "testing"

func TestValidateProfileFields(t *testing.T) {
	// a known preset is valid (no cipher arrays needed)
	if err := ValidateProfileFields(ProfileFields{Name: "preset:chrome"}); err != nil {
		t.Fatalf("preset:chrome should be valid: %v", err)
	}
	// an unknown preset is invalid
	if err := ValidateProfileFields(ProfileFields{Name: "preset:netscape"}); err == nil {
		t.Fatal("unknown preset should be invalid")
	}
	// out-of-range cipher id -> invalid (converter range check)
	if err := ValidateProfileFields(ProfileFields{
		CipherSuites: []int{0x10000}, SupportedCurves: []int{29}, TLSSupportedVersions: []int{0x0304},
	}); err == nil {
		t.Fatal("out-of-range cipher must be invalid")
	}
	// incomplete custom profile -> invalid
	if err := ValidateProfileFields(ProfileFields{Name: "tenant-x"}); err == nil {
		t.Fatal("incomplete custom profile must be invalid")
	}
}
