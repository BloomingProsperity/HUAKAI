package accountintake

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountcreate"
)

func accountName(account AccountDefaults, index int) string {
	if account.ExactName != "" {
		return account.ExactName
	}
	return fmt.Sprintf("%s-%03d", strings.TrimSpace(account.NamePrefix), index+1)
}

func confirmationSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if cleaned := strings.TrimSpace(value); cleaned != "" {
			out[cleaned] = struct{}{}
		}
	}
	return out
}

func validPlanHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validateConfirmations(values []string) error {
	allowed := map[string]struct{}{
		"confirm_weak_identity":            {},
		"confirm_mixed_channel_risk":       {},
		"confirm_unverified_account_match": {},
		"confirm_weak_identity_match":      {},
		"confirm_credential_rotation":      {},
		"confirm_credential_recovery":      {},
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if _, ok := allowed[value]; !ok {
			return fmt.Errorf("%w: unknown confirmation %q", ErrInvalidInput, value)
		}
	}
	return nil
}

func missingConfirmations(required []string, confirmed map[string]struct{}) []string {
	out := make([]string, 0)
	for _, value := range required {
		if _, ok := confirmed[value]; !ok {
			out = appendUnique(out, value)
		}
	}
	return out
}

func hasConfirmation(values []string, target string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func executionErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrExecutionStale), errors.Is(err, credentialstore.ErrCredentialVersionConflict):
		return "plan_stale"
	case errors.Is(err, accountcreate.ErrMixedRiskConfirmRequired):
		return "mixed_channel_risk_confirmation_required"
	case errors.Is(err, accountcreate.ErrProtocolIncompatible):
		return "provider_protocol_incompatible"
	case errors.Is(err, ErrCodexLaneAbsent):
		return "codex_lane_not_configured"
	default:
		return "execution_failed"
	}
}

func executionErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrExecutionStale), errors.Is(err, credentialstore.ErrCredentialVersionConflict):
		return "执行前账号或凭据状态已变化，请重新预检"
	case errors.Is(err, accountcreate.ErrMixedRiskConfirmRequired):
		return "执行时发现新的渠道混用风险，请重新预检并明确确认"
	case errors.Is(err, accountcreate.ErrProtocolIncompatible):
		return "执行时 provider 协议或账号配置已不兼容，请重新预检"
	case errors.Is(err, ErrCodexLaneAbsent):
		return "执行时 Codex 路由车道已不可运行，请重新预检"
	default:
		return "该项写入失败，事务已回滚且未留下部分数据"
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func addExecutionSummary(summary *ExecutionSummary, status ExecutionStatus) {
	switch status {
	case StatusCreated:
		summary.Created++
	case StatusUpdated:
		summary.Updated++
	case StatusSkipped:
		summary.Skipped++
	case StatusConflict:
		summary.Conflict++
	case StatusFailed:
		summary.Failed++
	}
}
