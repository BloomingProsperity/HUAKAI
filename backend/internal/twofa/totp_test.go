package twofa

import (
	"testing"
	"time"
)

func TestGenerateTOTPFollowsRFC6238SHA1Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	cases := []struct {
		unix int64
		want string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, tc := range cases {
		got, err := GenerateTOTP(secret, time.Unix(tc.unix, 0).UTC(), 8, 30*time.Second)
		if err != nil {
			t.Fatalf("GenerateTOTP(%d): %v", tc.unix, err)
		}
		if got != tc.want {
			t.Fatalf("GenerateTOTP(%d)=%q want %q", tc.unix, got, tc.want)
		}
		if !VerifyTOTP(secret, tc.want, time.Unix(tc.unix, 0).UTC(), TOTPConfig{
			Digits: 8,
			Step:   30 * time.Second,
			Window: 0,
		}) {
			t.Fatalf("VerifyTOTP rejected exact RFC vector %q at %d", tc.want, tc.unix)
		}
	}
}

func TestVerifyTOTPAllowsOnlyConfiguredAdjacentWindow(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(90, 0).UTC()
	previous, err := GenerateTOTP(secret, now.Add(-30*time.Second), 6, 30*time.Second)
	if err != nil {
		t.Fatalf("previous code: %v", err)
	}
	current, err := GenerateTOTP(secret, now, 6, 30*time.Second)
	if err != nil {
		t.Fatalf("current code: %v", err)
	}
	old, err := GenerateTOTP(secret, now.Add(-90*time.Second), 6, 30*time.Second)
	if err != nil {
		t.Fatalf("old code: %v", err)
	}
	cfg := TOTPConfig{Digits: 6, Step: 30 * time.Second, Window: 1}
	if !VerifyTOTP(secret, previous, now, cfg) {
		t.Fatal("adjacent previous code should verify with ±1 window")
	}
	if !VerifyTOTP(secret, current, now, cfg) {
		t.Fatal("current code should verify")
	}
	if VerifyTOTP(secret, old, now, cfg) {
		t.Fatal("code outside ±1 window verified")
	}
}
