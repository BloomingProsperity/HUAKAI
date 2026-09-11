package modelrate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

var (
	vendorPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	modelPattern  = regexp.MustCompile(`^[A-Za-z0-9._:+-]{1,128}$`)
	maxRate       = decimal.RequireFromString(MaxUSDPerMillion)
	zeroRate      = decimal.Zero
)

// ValidateUpsert 供管理入口和测试在落库前复用同一套身份与桶校验。
func ValidateUpsert(p UpsertParams) error {
	return validateUpsert(p)
}

func validateUpsert(p UpsertParams) error {
	if err := validateIdentity(p.Vendor, p.Model, p.Actor, p.ActorRole); err != nil {
		return err
	}
	if !p.Rates.HasAny() {
		return fmt.Errorf("%w: at least one rate bucket is required", ErrInvalidInput)
	}
	return validateBuckets(p.Rates)
}

func validateDelete(p DeleteParams) error {
	return validateIdentity(p.Vendor, p.Model, p.Actor, p.ActorRole)
}

func validateIdentity(vendor, model, actor, role string) error {
	if !vendorPattern.MatchString(normalizeVendor(vendor)) {
		return fmt.Errorf("%w: vendor", ErrInvalidInput)
	}
	if !modelPattern.MatchString(normalizeModel(model)) {
		return fmt.Errorf("%w: model", ErrInvalidInput)
	}
	if strings.TrimSpace(actor) == "" || strings.TrimSpace(role) == "" {
		return fmt.Errorf("%w: actor", ErrInvalidInput)
	}
	return nil
}

func validateBuckets(b RateBuckets) error {
	for _, value := range []*decimal.Decimal{b.Input, b.Output, b.CacheRead, b.CacheCreation, b.CacheCreation5m, b.CacheCreation1h} {
		if value == nil {
			continue
		}
		if value.IsNegative() {
			return fmt.Errorf("%w: rate must not be negative", ErrInvalidInput)
		}
		if value.GreaterThan(maxRate) {
			return fmt.Errorf("%w: rate must be at most %s USD per 1M tokens", ErrOutOfRange, MaxUSDPerMillion)
		}
		if value.LessThan(zeroRate) {
			return fmt.Errorf("%w: rate must not be negative", ErrInvalidInput)
		}
	}
	return nil
}
