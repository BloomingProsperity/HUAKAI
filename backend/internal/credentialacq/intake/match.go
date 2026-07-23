package intake

import (
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type existingConflict struct {
	code        string
	message     string
	accountID   int64
	accountName string
}

type existingMatch struct {
	credential    *ExistingCredential
	code          string
	message       string
	warnings      []string
	confirmations []string
}

func matchExisting(identity candidateIdentity, lifecycle LifecycleSummary, candidate credentialacq.CredentialCandidate, existing []ExistingCredential) (*existingMatch, *existingConflict) {
	candidateVendor, candidateAuthMode := credentialstore.CanonicalCredentialMode(candidate.Vendor, candidate.AuthMode)
	available := make([]*ExistingCredential, 0, len(existing))
	for index := range existing {
		current := &existing[index]
		currentVendor, _ := credentialstore.CanonicalCredentialMode(current.Vendor, current.AuthMode)
		if currentVendor == candidateVendor {
			available = append(available, current)
		}
	}

	if !lifecycle.HasRefreshMaterial {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return identity.CredentialFingerprint != "" &&
				strings.EqualFold(strings.TrimSpace(current.CredentialFingerprint), identity.CredentialFingerprint)
		})
		return resolveExistingMatches(matches, candidate, "credential_fingerprint_ambiguous", "同一凭据指纹命中多个已有账号")
	}

	if identity.SharedAccountScope && identity.SubjectID != "" {
		if identity.AccountID == "" {
			matches := filterExisting(available, func(current *ExistingCredential) bool {
				return sameIdentity(current.ExternalSubjectID, identity.SubjectID)
			})
			if len(matches) > 0 {
				return nil, conflictFromExisting("account_scope_missing", "导入项缺少账号范围，不能仅按个人身份选择共享账号", matches[0])
			}
			return nil, nil
		}
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return sameIdentity(current.ExternalAccountID, identity.AccountID) &&
				sameIdentity(current.ExternalSubjectID, identity.SubjectID)
		})
		if len(matches) > 0 && !identity.SubjectTrusted {
			return nil, conflictFromExisting("subject_identity_unverified", "导入声明的复合成员身份不能自动选择已有账号", matches[0])
		}
		if identity.SubjectTrusted {
			match, conflict := selectExistingModeMatch(matches, available, candidateVendor, candidateAuthMode, "account_member_identity_ambiguous", "同一账号范围与个人主体命中多个已有账号")
			if conflict != nil {
				return nil, conflict
			}
			if match != nil {
				return guardedExistingMatch(match, "rotate_existing_credential", "将轮换同一账号成员的同模式凭据", "confirm_credential_rotation")
			}
		}
		missingSubject := filterExisting(available, func(current *ExistingCredential) bool {
			return sameIdentity(current.ExternalAccountID, identity.AccountID) && strings.TrimSpace(current.ExternalSubjectID) == ""
		})
		if len(missingSubject) > 0 {
			return nil, conflictFromExisting("existing_member_identity_missing", "已有共享账号缺少个人主体，必须先人工补全身份", missingSubject[0])
		}
		missingScope := filterExisting(available, func(current *ExistingCredential) bool {
			return sameIdentity(current.ExternalSubjectID, identity.SubjectID) && strings.TrimSpace(current.ExternalAccountID) == ""
		})
		if len(missingScope) > 0 {
			return nil, conflictFromExisting("existing_account_scope_missing", "已有个人主体缺少账号范围，必须先人工补全身份", missingScope[0])
		}
		return nil, nil
	}

	if identity.SubjectID != "" {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return sameIdentity(current.ExternalSubjectID, identity.SubjectID)
		})
		if len(matches) > 0 && !identity.SubjectTrusted {
			return nil, conflictFromExisting("subject_identity_unverified", "导入声明的个人身份不能自动选择已有账号", matches[0])
		}
		if identity.SubjectTrusted {
			match, conflict := selectExistingModeMatch(matches, available, candidateVendor, candidateAuthMode, "subject_identity_ambiguous", "同一上游个人身份命中多个已有账号")
			if conflict != nil {
				return nil, conflict
			}
			if match != nil {
				return guardedExistingMatch(match, "rotate_existing_credential", "将轮换已存在账号的同模式凭据", "confirm_credential_rotation")
			}
		}
		if identity.SharedAccountScope {
			return nil, nil
		}
	}

	if identity.AccountID != "" {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return sameIdentity(current.ExternalAccountID, identity.AccountID)
		})
		if identity.SharedAccountScope && identity.SubjectID == "" && anyExisting(matches, func(current *ExistingCredential) bool {
			return strings.TrimSpace(current.ExternalSubjectID) != ""
		}) {
			return nil, conflictFromExisting("workspace_member_unknown", "导入项缺少个人身份，无法确定共享账号作用域中的具体成员", matches[0])
		}
		match, conflict := selectExistingModeMatch(matches, available, candidateVendor, candidateAuthMode, "account_scope_ambiguous", "同一上游账号作用域命中多个已有账号")
		if conflict != nil {
			return nil, conflict
		}
		if match != nil {
			planned, blocked := guardedExistingMatch(match, "rotate_account_scope_credential", "将按上游账号作用域轮换已有凭据", "confirm_unverified_account_match", "confirm_credential_rotation")
			if blocked != nil {
				return nil, blocked
			}
			planned.warnings = append(planned.warnings, "unverified_account_match")
			return planned, nil
		}
	}

	if identity.Email != "" {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return strings.EqualFold(strings.TrimSpace(current.ExternalAccountEmail), identity.Email)
		})
		match, conflict := selectExistingModeMatch(matches, available, candidateVendor, candidateAuthMode, "email_identity_ambiguous", "同一邮箱命中多个已有账号")
		if conflict != nil {
			return nil, conflict
		}
		if match != nil {
			if anyExisting(matches, func(current *ExistingCredential) bool {
				return strings.TrimSpace(current.ExternalSubjectID) != "" || strings.TrimSpace(current.ExternalAccountID) != ""
			}) {
				return nil, conflictFromExisting("weak_identity_matches_strong_account", "导入项只有邮箱，无法证明与已有强身份账号相同", match)
			}
			planned, blocked := guardedExistingMatch(match, "rotate_email_only_credential", "将按邮箱弱身份轮换已有凭据", "confirm_weak_identity_match", "confirm_credential_rotation")
			if blocked != nil {
				return nil, blocked
			}
			planned.warnings = append(planned.warnings, "weak_identity_match")
			return planned, nil
		}
	}
	return nil, nil
}

func resolveExistingMatches(matches []*ExistingCredential, candidate credentialacq.CredentialCandidate, conflictCode, conflictMessage string) (*existingMatch, *existingConflict) {
	vendor, authMode := credentialstore.CanonicalCredentialMode(candidate.Vendor, candidate.AuthMode)
	match, conflict := selectExistingModeMatch(matches, matches, vendor, authMode, conflictCode, conflictMessage)
	if conflict != nil || match == nil {
		return nil, conflict
	}
	return guardedExistingMatch(match, "rotate_existing_credential", "将轮换已存在账号的同模式凭据", "confirm_credential_rotation")
}

func selectExistingModeMatch(matches, inventory []*ExistingCredential, candidateVendor, candidateAuthMode, ambiguityCode, ambiguityMessage string) (*ExistingCredential, *existingConflict) {
	if len(matches) == 0 {
		return nil, nil
	}
	firstByAccount := make(map[int64]*ExistingCredential, len(matches))
	for _, match := range matches {
		if _, exists := firstByAccount[match.ProviderAccountID]; !exists {
			firstByAccount[match.ProviderAccountID] = match
		}
	}
	if len(firstByAccount) > 1 {
		return nil, conflictFromExisting(ambiguityCode, ambiguityMessage, matches[0])
	}
	modeMatches := filterExisting(matches, func(current *ExistingCredential) bool {
		vendor, authMode := credentialstore.CanonicalCredentialMode(current.Vendor, current.AuthMode)
		return vendor == candidateVendor && authMode == candidateAuthMode
	})
	switch len(modeMatches) {
	case 0:
		return nil, conflictFromExisting("identity_mode_conflict", "同一上游身份已存在，但没有相同 auth_mode 的凭据", matches[0])
	case 1:
		accountModeMatches := filterExisting(inventory, func(current *ExistingCredential) bool {
			vendor, authMode := credentialstore.CanonicalCredentialMode(current.Vendor, current.AuthMode)
			return current.ProviderAccountID == modeMatches[0].ProviderAccountID && vendor == candidateVendor && authMode == candidateAuthMode
		})
		if len(accountModeMatches) > 1 {
			return nil, conflictFromExisting(ambiguityCode, "同一账号存在多条等价模式凭据，必须人工消歧", accountModeMatches[0])
		}
		return modeMatches[0], nil
	default:
		return nil, conflictFromExisting(ambiguityCode, "同一账号同一 auth_mode 存在多条凭据，必须人工消歧", modeMatches[0])
	}
}

func guardedExistingMatch(match *ExistingCredential, code, message string, confirmations ...string) (*existingMatch, *existingConflict) {
	planned := &existingMatch{
		credential: match, code: code, message: message,
		confirmations: append([]string(nil), confirmations...),
	}
	switch credentialstore.Normalize(match.State) {
	case credentialstore.StateActive:
		return planned, nil
	case credentialstore.StateExpired, credentialstore.StateNeedsRotation, credentialstore.StateTempUnschedulable:
		planned.warnings = append(planned.warnings, "credential_recovery_required")
		planned.confirmations = append(planned.confirmations, "confirm_credential_recovery")
		return planned, nil
	case credentialstore.StateRefreshing, credentialstore.StateRefreshingWithGrace:
		return nil, conflictFromExisting("credential_refresh_in_progress", "已有凭据正在刷新，完成后才能重新预检", match)
	case credentialstore.StateRevoked:
		return nil, conflictFromExisting("credential_revoked_requires_explicit_reactivation", "已有凭据已撤销，普通导入不得重新激活", match)
	case credentialstore.StateOperatorAttention:
		return nil, conflictFromExisting("credential_operator_attention_requires_resolution", "已有凭据需要运营处理，解决阻断原因后才能轮换", match)
	default:
		return nil, conflictFromExisting("credential_state_unknown", "已有凭据状态未知，不能自动轮换", match)
	}
}

func filterExisting(values []*ExistingCredential, keep func(*ExistingCredential) bool) []*ExistingCredential {
	out := make([]*ExistingCredential, 0, len(values))
	for _, value := range values {
		if keep(value) {
			out = append(out, value)
		}
	}
	return out
}

func anyExisting(values []*ExistingCredential, match func(*ExistingCredential) bool) bool {
	for _, value := range values {
		if match(value) {
			return true
		}
	}
	return false
}

func sameIdentity(left, right string) bool {
	return strings.TrimSpace(left) != "" && strings.TrimSpace(left) == strings.TrimSpace(right)
}

func conflictFromExisting(code, message string, existing *ExistingCredential) *existingConflict {
	if existing == nil {
		return &existingConflict{code: code, message: message}
	}
	return &existingConflict{
		code: code, message: message,
		accountID: existing.ProviderAccountID, accountName: existing.ProviderAccountName,
	}
}
