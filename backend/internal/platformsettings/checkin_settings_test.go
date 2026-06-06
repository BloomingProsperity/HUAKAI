package platformsettings

import "testing"

func TestCheckinSettingDefaultsAreOptInAndSmallReward(t *testing.T) {
	if got, _ := DefaultValue(KeyCheckinEnabled); got != "false" {
		t.Fatalf("checkin_enabled default=%q want false", got)
	}
	if got, _ := DefaultValue(KeyCheckinMinCents); got != "1" {
		t.Fatalf("checkin_min_cents default=%q want 1", got)
	}
	if got, _ := DefaultValue(KeyCheckinMaxCents); got != "20" {
		t.Fatalf("checkin_max_cents default=%q want 20", got)
	}
	for _, key := range []SettingKey{KeyCheckinEnabled, KeyCheckinMinCents, KeyCheckinMaxCents} {
		if !IsAllowedKey(key) {
			t.Fatalf("%s missing from allowed platform settings", key)
		}
	}
}

func TestCheckinSettingsValidateTypes(t *testing.T) {
	if _, err := ValidateValue(KeyCheckinEnabled, "yes"); err == nil {
		t.Fatal("checkin_enabled accepted non-bool value")
	}
	if got, err := ValidateValue(KeyCheckinEnabled, "true"); err != nil || got != "true" {
		t.Fatalf("checkin_enabled true got=%q err=%v", got, err)
	}
	if _, err := ValidateValue(KeyCheckinMinCents, "0"); err == nil {
		t.Fatal("checkin_min_cents accepted zero reward")
	}
	if got, err := ValidateValue(KeyCheckinMaxCents, "20"); err != nil || got != "20" {
		t.Fatalf("checkin_max_cents 20 got=%q err=%v", got, err)
	}
}

func TestReferralRewardSettingDefaultsAreOptIn(t *testing.T) {
	if got, _ := DefaultValue(KeyReferralRewardEnabled); got != "false" {
		t.Fatalf("referral_reward_enabled default=%q want false", got)
	}
	if got, _ := DefaultValue(KeyReferralRewardCents); got != "50" {
		t.Fatalf("referral_reward_cents default=%q want 50", got)
	}
	for _, key := range []SettingKey{KeyReferralRewardEnabled, KeyReferralRewardCents} {
		if !IsAllowedKey(key) {
			t.Fatalf("%s missing from allowed platform settings", key)
		}
	}
}

func TestReferralRewardSettingsValidateTypes(t *testing.T) {
	if _, err := ValidateValue(KeyReferralRewardEnabled, "yes"); err == nil {
		t.Fatal("referral_reward_enabled accepted non-bool value")
	}
	if got, err := ValidateValue(KeyReferralRewardEnabled, "true"); err != nil || got != "true" {
		t.Fatalf("referral_reward_enabled true got=%q err=%v", got, err)
	}
	if _, err := ValidateValue(KeyReferralRewardCents, "-1"); err == nil {
		t.Fatal("referral_reward_cents accepted negative reward")
	}
	if got, err := ValidateValue(KeyReferralRewardCents, "0"); err != nil || got != "0" {
		t.Fatalf("referral_reward_cents 0 got=%q err=%v", got, err)
	}
	if got, err := ValidateValue(KeyReferralRewardCents, "50"); err != nil || got != "50" {
		t.Fatalf("referral_reward_cents 50 got=%q err=%v", got, err)
	}
}
