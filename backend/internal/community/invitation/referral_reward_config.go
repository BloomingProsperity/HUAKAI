package invitation

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	referralRewardEnv           = "HUAKAI_REFERRAL_REWARD_USD_MICROS"
	referralTierSilverEnv       = "HUAKAI_REFERRAL_TIER_SILVER_THRESHOLD"
	referralTierGoldEnv         = "HUAKAI_REFERRAL_TIER_GOLD_THRESHOLD"
	referralTierPlatinumEnv     = "HUAKAI_REFERRAL_TIER_PLATINUM_THRESHOLD"
	referralRewardRequestPrefix = "referral_reward"
	referralDefaultSilver       = 3
	referralDefaultGold         = 10
	referralDefaultPlatinum     = 50
	referralMicrosPerCent       = 10_000
)

func referralRewardConfigFromEnv() (referralRewardConfig, error) {
	reward, err := parseReferralInt64Env(referralRewardEnv, 0)
	if err != nil || reward < 0 || reward%referralMicrosPerCent != 0 {
		return referralRewardConfig{}, ErrInvalidInput
	}
	thresholds, err := referralTierThresholdsFromEnv()
	if err != nil {
		return referralRewardConfig{}, err
	}
	return referralRewardConfig{RewardUSDMicros: reward, TierThresholds: thresholds}, nil
}

func referralTierThresholdsFromEnv() (referralTierThresholds, error) {
	thresholds := referralTierThresholds{Silver: referralDefaultSilver, Gold: referralDefaultGold, Platinum: referralDefaultPlatinum}
	var err error
	if thresholds.Silver, err = parseReferralIntEnv(referralTierSilverEnv, thresholds.Silver); err != nil {
		return referralTierThresholds{}, err
	}
	if thresholds.Gold, err = parseReferralIntEnv(referralTierGoldEnv, thresholds.Gold); err != nil {
		return referralTierThresholds{}, err
	}
	if thresholds.Platinum, err = parseReferralIntEnv(referralTierPlatinumEnv, thresholds.Platinum); err != nil {
		return referralTierThresholds{}, err
	}
	if thresholds.Silver <= 0 || thresholds.Gold < thresholds.Silver || thresholds.Platinum < thresholds.Gold {
		return referralTierThresholds{}, ErrInvalidInput
	}
	return thresholds, nil
}

func parseReferralInt64Env(name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func parseReferralIntEnv(name string, fallback int) (int, error) {
	value, err := parseReferralInt64Env(name, int64(fallback))
	if err != nil || value > int64(^uint(0)>>1) {
		return 0, ErrInvalidInput
	}
	return int(value), nil
}

func normalizeQualifyReferralInput(input qualifyReferralInput) qualifyReferralInput {
	if input.QualifiedAt.IsZero() {
		input.QualifiedAt = time.Now().UTC()
	} else {
		input.QualifiedAt = input.QualifiedAt.UTC()
	}
	if input.TierThresholds == (referralTierThresholds{}) {
		input.TierThresholds = referralTierThresholds{Silver: referralDefaultSilver, Gold: referralDefaultGold, Platinum: referralDefaultPlatinum}
	}
	return input
}

func validateQualifyReferralInput(input qualifyReferralInput) error {
	if input.TenantID <= 0 || input.RefereeUserID <= 0 || input.BillingEventID <= 0 || input.RewardUSDMicros < 0 {
		return ErrInvalidInput
	}
	if input.RewardUSDMicros%referralMicrosPerCent != 0 {
		return ErrInvalidInput
	}
	if input.TierThresholds.Silver <= 0 || input.TierThresholds.Gold < input.TierThresholds.Silver ||
		input.TierThresholds.Platinum < input.TierThresholds.Gold {
		return ErrInvalidInput
	}
	return nil
}

func tierForQualifiedReferralCount(count int, thresholds referralTierThresholds) string {
	switch {
	case count >= thresholds.Platinum:
		return "platinum"
	case count >= thresholds.Gold:
		return "gold"
	case count >= thresholds.Silver:
		return "silver"
	default:
		return "none"
	}
}

func rewardMicrosToCents(micros int64) (int64, error) {
	if micros <= 0 || micros%referralMicrosPerCent != 0 {
		return 0, ErrInvalidInput
	}
	return micros / referralMicrosPerCent, nil
}

func trimAuditReason(reason string) string {
	const max = 512
	reason = strings.TrimSpace(reason)
	if len(reason) <= max {
		return reason
	}
	return reason[:max]
}
