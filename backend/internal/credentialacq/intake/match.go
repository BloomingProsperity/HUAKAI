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

type candidateIdentity struct {
	Summary               IdentitySummary
	SubjectID             string
	SubjectTrusted        bool
	CredentialFingerprint string
	AccountIsSharedScope  bool
}

func matchExisting(identity candidateIdentity, lifecycle LifecycleSummary, candidate credentialacq.CredentialCandidate, existing []ExistingCredential) (*existingMatch, *existingConflict) {
	available := make([]*ExistingCredential, 0, len(existing))
	for index := range existing {
		current := &existing[index]
		if credentialstore.Normalize(current.Vendor) != candidate.Vendor {
			continue
		}
		available = append(available, current)
	}
	if !lifecycle.HasRefreshMaterial {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return identity.CredentialFingerprint != "" &&
				strings.EqualFold(strings.TrimSpace(current.CredentialFingerprint), identity.CredentialFingerprint)
		})
		return resolveExistingMatches(matches, candidate, "credential_fingerprint_ambiguous", "同一凭据指纹命中多个已有账号")
	}

	if identity.SubjectID != "" {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return sameIdentity(current.ExternalSubjectID, identity.SubjectID)
		})
		if !identity.SubjectTrusted {
			if len(matches) > 0 {
				return nil, conflictFromExisting(
					"subject_identity_unverified",
					"导入或手工声明的个人身份不能自动选择已有账号",
					matches[0],
				)
			}
			return nil, nil
		}
		if anyExisting(matches, func(current *ExistingCredential) bool {
			return !trustedIdentitySource(current.AccountIDSource)
		}) {
			return nil, conflictFromExisting(
				"subject_identity_source_untrusted",
				"已有账号的个人身份来源无法证明，必须人工消歧",
				matches[0],
			)
		}
		match, conflict := selectExistingModeMatch(matches, candidate, "subject_identity_ambiguous", "同一上游个人身份命中多个已有账号")
		if conflict != nil {
			return nil, conflict
		}
		if match != nil {
			if identity.Summary.ExternalAccountID != "" && strings.TrimSpace(match.ExternalAccountID) != "" &&
				!sameIdentity(match.ExternalAccountID, identity.Summary.ExternalAccountID) {
				return nil, conflictFromExisting("subject_scope_conflict", "同一上游个人身份关联了不同账号作用域", match)
			}
			return exactExistingMatch(match, candidate)
		}
		if identity.AccountIsSharedScope && identity.Summary.ExternalAccountID != "" {
			legacy := filterExisting(available, func(current *ExistingCredential) bool {
				return sameIdentity(current.ExternalAccountID, identity.Summary.ExternalAccountID) &&
					strings.TrimSpace(current.ExternalSubjectID) == ""
			})
			match, conflict := selectExistingModeMatch(legacy, candidate, "legacy_scope_ambiguous", "账号作用域命中多个缺少个人身份的旧账号")
			if conflict != nil {
				return nil, conflict
			}
			if match != nil {
				return legacyIdentityUpgrade(match, candidate)
			}
		}
		return nil, nil
	}

	if identity.Summary.ExternalAccountID != "" {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return sameIdentity(current.ExternalAccountID, identity.Summary.ExternalAccountID)
		})
		match, conflict := selectExistingModeMatch(matches, candidate, "account_scope_ambiguous", "同一上游账号作用域命中多个已有账号")
		if conflict != nil {
			return nil, conflict
		}
		if match != nil {
			if identity.AccountIsSharedScope && anyExisting(matches, func(current *ExistingCredential) bool {
				return strings.TrimSpace(current.ExternalSubjectID) != ""
			}) {
				return nil, conflictFromExisting("workspace_member_unknown", "导入项缺少个人身份，无法确定账号作用域中的具体成员", match)
			}
			if identity.AccountIsSharedScope {
				return legacyIdentityUpgrade(match, candidate)
			}
			return exactExistingMatch(match, candidate)
		}
		return nil, nil
	}

	if identity.Summary.ExternalAccountEmail != "" {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return strings.EqualFold(strings.TrimSpace(current.ExternalAccountEmail), identity.Summary.ExternalAccountEmail)
		})
		match, conflict := selectExistingModeMatch(matches, candidate, "email_identity_ambiguous", "同一邮箱命中多个已有账号")
		if conflict != nil {
			return nil, conflict
		}
		if match != nil {
			if anyExisting(matches, func(current *ExistingCredential) bool {
				return strings.TrimSpace(current.ExternalSubjectID) != "" || strings.TrimSpace(current.ExternalAccountID) != ""
			}) {
				return nil, conflictFromExisting("weak_identity_matches_strong_account", "导入项只有邮箱，无法证明与已有强身份账号相同", match)
			}
			return guardExistingRotation(match, &existingMatch{
				credential: match, code: "rotate_email_only_credential",
				message:       "将按邮箱弱身份轮换已有账号凭据",
				warnings:      []string{"weak_identity_match"},
				confirmations: []string{"confirm_weak_identity_match", "confirm_credential_rotation"},
			})
		}
	}
	return nil, nil
}

func resolveExistingMatches(matches []*ExistingCredential, candidate credentialacq.CredentialCandidate, conflictCode, conflictMessage string) (*existingMatch, *existingConflict) {
	match, conflict := selectExistingModeMatch(matches, candidate, conflictCode, conflictMessage)
	if conflict != nil {
		return nil, conflict
	}
	if match != nil {
		return exactExistingMatch(match, candidate)
	}
	return nil, nil
}

func selectExistingModeMatch(matches []*ExistingCredential, candidate credentialacq.CredentialCandidate, ambiguityCode, ambiguityMessage string) (*ExistingCredential, *existingConflict) {
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
		return credentialstore.Normalize(current.AuthMode) == candidate.AuthMode
	})
	switch len(modeMatches) {
	case 0:
		return nil, conflictFromExisting("identity_mode_conflict", "同一上游身份已存在，但没有相同 auth_mode 的凭据", matches[0])
	case 1:
		return modeMatches[0], nil
	default:
		return nil, conflictFromExisting(ambiguityCode, ambiguityMessage, modeMatches[0])
	}
}

func exactExistingMatch(match *ExistingCredential, candidate credentialacq.CredentialCandidate) (*existingMatch, *existingConflict) {
	if credentialstore.Normalize(match.AuthMode) != candidate.AuthMode {
		return nil, conflictFromExisting("identity_mode_conflict", "同一上游身份已使用其它 auth_mode", match)
	}
	return guardExistingRotation(match, &existingMatch{
		credential: match, code: "rotate_existing_credential",
		message:       "将轮换已存在账号的同模式凭据",
		confirmations: []string{"confirm_credential_rotation"},
	})
}

func legacyIdentityUpgrade(match *ExistingCredential, candidate credentialacq.CredentialCandidate) (*existingMatch, *existingConflict) {
	if credentialstore.Normalize(match.AuthMode) != candidate.AuthMode {
		return nil, conflictFromExisting("identity_mode_conflict", "旧账号作用域已使用其它 auth_mode", match)
	}
	return guardExistingRotation(match, &existingMatch{
		credential: match, code: "upgrade_legacy_identity",
		message:       "将为缺少个人身份的旧账号补齐身份并轮换凭据",
		warnings:      []string{"legacy_identity_upgrade"},
		confirmations: []string{"confirm_legacy_identity_upgrade", "confirm_credential_rotation"},
	})
}

func guardExistingRotation(match *ExistingCredential, planned *existingMatch) (*existingMatch, *existingConflict) {
	switch credentialstore.Normalize(match.State) {
	case credentialstore.StateActive:
		return planned, nil
	case credentialstore.StateExpired, credentialstore.StateNeedsRotation, credentialstore.StateTempUnschedulable:
		planned.warnings = append(planned.warnings, "credential_recovery_required")
		planned.confirmations = append(planned.confirmations, "confirm_credential_recovery")
		return planned, nil
	case credentialstore.StateRefreshing, credentialstore.StateRefreshingWithGrace:
		return nil, conflictFromExisting(
			"credential_refresh_in_progress",
			"已有凭据正在刷新，完成后才能重新预检",
			match,
		)
	case credentialstore.StateRevoked:
		return nil, conflictFromExisting(
			"credential_revoked_requires_explicit_reactivation",
			"已有凭据已撤销，普通导入不得重新激活",
			match,
		)
	case credentialstore.StateOperatorAttention:
		return nil, conflictFromExisting(
			"credential_operator_attention_requires_resolution",
			"已有凭据需要运营处理，解决阻断原因后才能轮换",
			match,
		)
	default:
		return nil, conflictFromExisting(
			"credential_state_unknown",
			"已有凭据状态未知，不能自动轮换",
			match,
		)
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
	return &existingConflict{
		code: code, message: message,
		accountID: existing.ProviderAccountID, accountName: existing.ProviderAccountName,
	}
}
