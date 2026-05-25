package audit

import (
	"errors"
	"fmt"
)

var ErrMismatchReceiptRequired = errors.New("audit: mismatch detector receipt required")

type MismatchVerdict struct {
	State             string   `json:"state"`
	DeltaMicroUSD     int64    `json:"delta_micro_usd"`
	MismatchDirection string   `json:"mismatch_direction"`
	FieldsMismatch    []string `json:"fields_mismatch"`
}

const (
	MismatchDirectionOverCharge  = "over_charge"
	MismatchDirectionUnderCharge = "under_charge"
	MismatchDirectionEqual       = "equal"
)

func (v MismatchVerdict) RefundEligible() bool {
	return v.State == ReceiptValidationStateMismatchPending &&
		v.MismatchDirection == MismatchDirectionOverCharge &&
		v.DeltaMicroUSD > 0
}

func DetectReceiptMismatch(derived, submitted *CostReceipt) (MismatchVerdict, error) {
	if derived == nil || submitted == nil {
		return MismatchVerdict{}, ErrMismatchReceiptRequired
	}
	if err := validateReceiptRequestID(derived.RequestID); err != nil {
		return MismatchVerdict{}, err
	}
	if err := validateReceiptRequestID(submitted.RequestID); err != nil {
		return MismatchVerdict{}, err
	}
	if derived.RequestID != submitted.RequestID {
		return MismatchVerdict{}, fmt.Errorf("%w: request_id mismatch", ErrReceiptInvalidDerivedData)
	}

	fields := make([]string, 0, 6)
	if derived.CostUSDMicros != submitted.CostUSDMicros {
		fields = append(fields, "cost_total_micro_usd")
	}
	if derived.Model != submitted.Model {
		fields = append(fields, "model")
	}
	if derived.InputTokens != submitted.InputTokens {
		fields = append(fields, "input_tokens")
	}
	if derived.OutputTokens != submitted.OutputTokens {
		fields = append(fields, "output_tokens")
	}
	if derived.CachedTokens != submitted.CachedTokens {
		fields = append(fields, "cached_tokens")
	}
	if derived.RateTableSnapshotID != submitted.RateTableSnapshotID {
		fields = append(fields, "rate_table_snapshot_id")
	}

	state := mismatchStateFromDerived(derived)
	if len(fields) > 0 {
		state = ReceiptValidationStateMismatchPending
	}
	delta, direction := mismatchCostDelta(derived.CostUSDMicros, submitted.CostUSDMicros)
	return MismatchVerdict{
		State:             state,
		DeltaMicroUSD:     delta,
		MismatchDirection: direction,
		FieldsMismatch:    fields,
	}, nil
}

func mismatchCostDelta(derived, submitted int64) (int64, string) {
	switch {
	case submitted > derived:
		return submitted - derived, MismatchDirectionOverCharge
	case derived > submitted:
		return derived - submitted, MismatchDirectionUnderCharge
	default:
		return 0, MismatchDirectionEqual
	}
}

func mismatchStateFromDerived(receipt *CostReceipt) string {
	switch NormalizeReceiptValidationState(receipt.ValidationState) {
	case ReceiptValidationStateMismatchPending:
		return ReceiptValidationStateMismatchPending
	case ReceiptValidationStateMismatchRefunded:
		return ReceiptValidationStateMismatchRefunded
	case ReceiptValidationStateProvisional:
		return ReceiptValidationStateProvisional
	case ReceiptValidationStateUnknown:
		return ReceiptValidationStateUnknown
	default:
		return ReceiptValidationStateValid
	}
}
