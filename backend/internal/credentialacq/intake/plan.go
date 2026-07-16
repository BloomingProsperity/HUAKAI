// Package intake 把不同凭据来源归一成不写库、不发网络的账号接入预检计划。
package intake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const ContractVersion = "account-intake-v1"

type SourceKind string

const (
	SourceCodexCLI      SourceKind = "codex_cli"
	SourceJSON          SourceKind = "json"
	SourceCSV           SourceKind = "csv"
	SourceClaudeCookie  SourceKind = "claude_cookie"
	SourceSetupToken    SourceKind = "setup_token"
	SourceAgentIdentity SourceKind = "agent_identity"
	SourceRemoteSync    SourceKind = "remote_sync"
	SourceAccountBundle SourceKind = "account_bundle"
)

type Action string

const (
	ActionCreate   Action = "create"
	ActionUpdate   Action = "update"
	ActionSkip     Action = "skip"
	ActionConflict Action = "conflict"
	ActionFail     Action = "fail"
)

var ErrSourceDisabled = errors.New("credential intake source disabled")

type ExistingCredential struct {
	ProviderAccountID     int64
	ProviderAccountName   string
	Vendor                string
	AuthMode              string
	State                 string
	ExternalAccountID     string
	ExternalAccountEmail  string
	ExternalSubjectID     string
	CredentialFingerprint string
}

type BuildInput struct {
	SourceKind      SourceKind
	DefaultVendor   string
	DefaultAuthMode string
	Content         string
	Existing        []ExistingCredential
	Now             time.Time
}

type Plan struct {
	ContractVersion string     `json:"contract_version"`
	SourceKind      SourceKind `json:"source_kind"`
	InputCount      int        `json:"input_count"`
	Items           []Item     `json:"items"`
	Summary         Summary    `json:"summary"`
}

type Summary struct {
	Create   int `json:"create"`
	Update   int `json:"update"`
	Skip     int `json:"skip"`
	Conflict int `json:"conflict"`
	Fail     int `json:"fail"`
}

type Item struct {
	Index                 int              `json:"index"`
	Vendor                string           `json:"vendor"`
	AuthMode              string           `json:"auth_mode"`
	Action                Action           `json:"action"`
	Code                  string           `json:"code"`
	Message               string           `json:"message"`
	ExistingAccountID     int64            `json:"existing_account_id,omitempty"`
	ExistingAccountName   string           `json:"existing_account_name,omitempty"`
	Identity              IdentitySummary  `json:"identity"`
	Lifecycle             LifecycleSummary `json:"lifecycle"`
	FieldChanges          []string         `json:"field_changes,omitempty"`
	Warnings              []string         `json:"warnings,omitempty"`
	RequiredConfirmations []string         `json:"required_confirmations,omitempty"`
}

type IdentitySummary struct {
	ExternalAccountID      string `json:"external_account_id,omitempty"`
	ExternalAccountEmail   string `json:"external_account_email,omitempty"`
	SubjectIdentityPresent bool   `json:"subject_identity_present"`
	Source                 string `json:"source"`
	Strength               string `json:"strength"`
}

type LifecycleSummary struct {
	Refreshable        bool       `json:"refreshable"`
	HasRefreshMaterial bool       `json:"has_refresh_material"`
	AccessExpiresAt    *time.Time `json:"access_expires_at,omitempty"`
	Expired            bool       `json:"expired"`
}

func Build(in BuildInput) (Plan, error) {
	source := SourceKind(strings.TrimSpace(string(in.SourceKind)))
	candidates, err := parseSource(source, in.Content, in.DefaultVendor, in.DefaultAuthMode)
	if err != nil {
		return Plan{}, err
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	registry := credentialstore.DefaultHandlerRegistry()
	plan := Plan{
		ContractVersion: ContractVersion,
		SourceKind:      source,
		InputCount:      len(candidates),
		Items:           make([]Item, 0, len(candidates)),
	}
	seenIdentity := make(map[string]int)
	seenPayload := make(map[[32]byte]int)
	for index, candidate := range candidates {
		item := planCandidate(index, candidate, in.Existing, registry, now, seenIdentity, seenPayload)
		plan.Items = append(plan.Items, item)
		switch item.Action {
		case ActionCreate:
			plan.Summary.Create++
		case ActionUpdate:
			plan.Summary.Update++
		case ActionSkip:
			plan.Summary.Skip++
		case ActionConflict:
			plan.Summary.Conflict++
		case ActionFail:
			plan.Summary.Fail++
		}
	}
	return plan, nil
}

func parseSource(source SourceKind, content, vendor, authMode string) ([]credentialacq.CredentialCandidate, error) {
	switch source {
	case SourceCodexCLI:
		return credentialacq.ParseImportContent(content, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	case SourceJSON:
		return credentialacq.ParseImportContent(content,
			firstNonEmpty(vendor, credentialstore.VendorOpenAI),
			firstNonEmpty(authMode, credentialstore.AuthModeCodexCLIOAuth))
	case SourceCSV:
		return credentialacq.ParseCSVImportContent(content,
			firstNonEmpty(vendor, credentialstore.VendorOpenAI),
			firstNonEmpty(authMode, credentialstore.AuthModeCodexCLIOAuth))
	case SourceClaudeCookie, SourceSetupToken, SourceAgentIdentity, SourceRemoteSync, SourceAccountBundle:
		return nil, fmt.Errorf("%w: %s", ErrSourceDisabled, source)
	default:
		return nil, credentialacq.ErrInvalidImportBody
	}
}

func planCandidate(
	index int,
	candidate credentialacq.CredentialCandidate,
	existing []ExistingCredential,
	registry *credentialstore.HandlerRegistry,
	now time.Time,
	seenIdentity map[string]int,
	seenPayload map[[32]byte]int,
) Item {
	candidate.Vendor = credentialstore.Normalize(candidate.Vendor)
	candidate.AuthMode = credentialstore.Normalize(candidate.AuthMode)
	item := Item{
		Index: index, Vendor: candidate.Vendor, AuthMode: candidate.AuthMode,
		Action: ActionFail, Code: "invalid_credential", Message: "凭据形态不符合该账号模式",
	}
	handler, err := registry.MustLookup(candidate.Vendor, candidate.AuthMode)
	if err != nil {
		item.Code = "unknown_credential_mode"
		item.Message = "未识别的 vendor/auth_mode"
		return item
	}
	if err := handler.ValidatePayload(candidate.Payload); err != nil {
		return item
	}

	payloadKey := sha256.Sum256(append([]byte(credentialstore.ModeKey(candidate.Vendor, candidate.AuthMode)+"\x00"), candidate.Payload...))
	identity := extractIdentity(candidate, hex.EncodeToString(payloadKey[:]))
	item.Identity = identity.Summary
	item.Lifecycle = extractLifecycle(candidate.Payload, handler, now)
	if item.Lifecycle.Expired && !item.Lifecycle.HasRefreshMaterial {
		item.Action = ActionFail
		item.Code = "expired_without_refresh"
		item.Message = "访问凭据已过期且没有刷新材料"
		return item
	}
	if item.Lifecycle.Expired {
		item.Warnings = append(item.Warnings, "access_expired_refresh_required")
	}
	if item.Lifecycle.Refreshable && !item.Lifecycle.HasRefreshMaterial {
		item.Warnings = append(item.Warnings, "refresh_material_missing")
	}

	identityKey := identityMatchKey(identity, candidate.Vendor, item.Lifecycle.HasRefreshMaterial)
	if firstIndex, duplicate := seenPayload[payloadKey]; duplicate {
		item.Action = ActionSkip
		item.Code = "duplicate_input"
		item.Message = fmt.Sprintf("与本批第 %d 项重复", firstIndex+1)
		return item
	}
	if identityKey != "" {
		if firstIndex, duplicate := seenIdentity[identityKey]; duplicate {
			item.Action = ActionSkip
			item.Code = "duplicate_identity"
			item.Message = fmt.Sprintf("与本批第 %d 项属于同一上游账号", firstIndex+1)
			return item
		}
	}

	match, conflict := matchExisting(identity, item.Lifecycle, candidate, existing)
	if conflict != nil {
		item.Action = ActionConflict
		item.Code = conflict.code
		item.Message = conflict.message
		item.ExistingAccountID = conflict.accountID
		item.ExistingAccountName = conflict.accountName
		item.RequiredConfirmations = append(item.RequiredConfirmations, "resolve_identity_conflict")
		return item
	}
	if match != nil {
		item.Action = ActionUpdate
		item.Code = match.code
		item.Message = match.message
		item.ExistingAccountID = match.credential.ProviderAccountID
		item.ExistingAccountName = match.credential.ProviderAccountName
		item.FieldChanges = []string{"credential_payload", "credential_version", "credential_lifecycle"}
		item.Warnings = append(item.Warnings, match.warnings...)
		item.RequiredConfirmations = append(item.RequiredConfirmations, match.confirmations...)
		markExecutableIdentity(seenPayload, seenIdentity, payloadKey, identityKey, index)
		return item
	}

	item.Action = ActionCreate
	item.Code = "create_account"
	item.Message = "将创建独立 provider account 并写入加密凭据"
	item.FieldChanges = []string{"provider_account", "account_credential", "channel_health_default"}
	if item.Identity.Strength == "weak" || item.Identity.Strength == "opaque" {
		item.Warnings = append(item.Warnings, "weak_identity")
		item.RequiredConfirmations = append(item.RequiredConfirmations, "confirm_weak_identity")
	}
	markExecutableIdentity(seenPayload, seenIdentity, payloadKey, identityKey, index)
	return item
}

func markExecutableIdentity(seenPayload map[[32]byte]int, seenIdentity map[string]int, payloadKey [32]byte, identityKey string, index int) {
	seenPayload[payloadKey] = index
	if identityKey != "" {
		seenIdentity[identityKey] = index
	}
}

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
		return resolveExactMatches(matches, candidate, "credential_fingerprint_ambiguous", "同一凭据指纹命中多个已有账号")
	}

	if identity.SubjectID != "" {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return sameIdentity(current.ExternalSubjectID, identity.SubjectID)
		})
		if len(matches) > 1 {
			return nil, conflictFromExisting("subject_identity_ambiguous", "同一上游个人身份命中多个已有账号", matches[0])
		}
		if len(matches) == 1 {
			match := matches[0]
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
			if len(legacy) > 1 {
				return nil, conflictFromExisting("legacy_scope_ambiguous", "账号作用域命中多个缺少个人身份的旧账号", legacy[0])
			}
			if len(legacy) == 1 {
				return legacyIdentityUpgrade(legacy[0], candidate)
			}
		}
		return nil, nil
	}

	if identity.Summary.ExternalAccountID != "" {
		matches := filterExisting(available, func(current *ExistingCredential) bool {
			return sameIdentity(current.ExternalAccountID, identity.Summary.ExternalAccountID)
		})
		if len(matches) > 1 {
			return nil, conflictFromExisting("account_scope_ambiguous", "同一上游账号作用域命中多个已有账号", matches[0])
		}
		if len(matches) == 1 {
			match := matches[0]
			if identity.AccountIsSharedScope && strings.TrimSpace(match.ExternalSubjectID) != "" {
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
		if len(matches) > 1 {
			return nil, conflictFromExisting("email_identity_ambiguous", "同一邮箱命中多个已有账号", matches[0])
		}
		if len(matches) == 1 {
			match := matches[0]
			if strings.TrimSpace(match.ExternalSubjectID) != "" || strings.TrimSpace(match.ExternalAccountID) != "" {
				return nil, conflictFromExisting("weak_identity_matches_strong_account", "导入项只有邮箱，无法证明与已有强身份账号相同", match)
			}
			if credentialstore.Normalize(match.AuthMode) != candidate.AuthMode {
				return nil, conflictFromExisting("identity_mode_conflict", "同一弱身份账号已使用其它 auth_mode", match)
			}
			return &existingMatch{
				credential: match, code: "rotate_email_only_credential",
				message:       "将按邮箱弱身份轮换已有账号凭据",
				warnings:      []string{"weak_identity_match"},
				confirmations: []string{"confirm_weak_identity_match", "confirm_credential_rotation"},
			}, nil
		}
	}
	return nil, nil
}

func resolveExactMatches(matches []*ExistingCredential, candidate credentialacq.CredentialCandidate, conflictCode, conflictMessage string) (*existingMatch, *existingConflict) {
	if len(matches) > 1 {
		return nil, conflictFromExisting(conflictCode, conflictMessage, matches[0])
	}
	if len(matches) == 1 {
		return exactExistingMatch(matches[0], candidate)
	}
	return nil, nil
}

func exactExistingMatch(match *ExistingCredential, candidate credentialacq.CredentialCandidate) (*existingMatch, *existingConflict) {
	if credentialstore.Normalize(match.AuthMode) != candidate.AuthMode {
		return nil, conflictFromExisting("identity_mode_conflict", "同一上游身份已使用其它 auth_mode", match)
	}
	return &existingMatch{
		credential: match, code: "rotate_existing_credential",
		message:       "将轮换已存在账号的同模式凭据",
		confirmations: []string{"confirm_credential_rotation"},
	}, nil
}

func legacyIdentityUpgrade(match *ExistingCredential, candidate credentialacq.CredentialCandidate) (*existingMatch, *existingConflict) {
	if credentialstore.Normalize(match.AuthMode) != candidate.AuthMode {
		return nil, conflictFromExisting("identity_mode_conflict", "旧账号作用域已使用其它 auth_mode", match)
	}
	return &existingMatch{
		credential: match, code: "upgrade_legacy_identity",
		message:       "将为缺少个人身份的旧账号补齐身份并轮换凭据",
		warnings:      []string{"legacy_identity_upgrade"},
		confirmations: []string{"confirm_legacy_identity_upgrade", "confirm_credential_rotation"},
	}, nil
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

func sameIdentity(left, right string) bool {
	return strings.TrimSpace(left) != "" && strings.TrimSpace(left) == strings.TrimSpace(right)
}

func conflictFromExisting(code, message string, existing *ExistingCredential) *existingConflict {
	return &existingConflict{
		code: code, message: message,
		accountID: existing.ProviderAccountID, accountName: existing.ProviderAccountName,
	}
}

func extractIdentity(candidate credentialacq.CredentialCandidate, fingerprint string) candidateIdentity {
	summary := IdentitySummary{
		ExternalAccountID:    strings.TrimSpace(candidate.ExternalAccountID),
		ExternalAccountEmail: strings.TrimSpace(candidate.ExternalAccountEmail),
		Source:               strings.TrimSpace(candidate.AccountIDSource),
	}
	fields := payloadFields(candidate.Payload)
	subjectID := firstString(fields, "external_subject_id", "chatgpt_user_id")
	explicitChatGPTAccountID := firstString(fields, "chatgpt_account_id")
	if summary.ExternalAccountID == "" {
		summary.ExternalAccountID = firstNonEmpty(explicitChatGPTAccountID, firstString(fields, "external_account_id", "account_id"))
	}
	if summary.ExternalAccountEmail == "" {
		summary.ExternalAccountEmail = firstString(fields, "external_account_email", "email")
	}
	if candidate.Vendor == credentialstore.VendorOpenAI {
		if claims, err := accountident.ParseJWTClaimsUnverified(firstString(fields, "id_token")); err == nil {
			subjectID = firstNonEmpty(subjectID, firstString(claims, "sub"))
		}
		if summary.ExternalAccountID == "" {
			extracted := accountident.ExtractChatGPT(firstString(fields, "id_token"), firstString(fields, "account_id"))
			summary.ExternalAccountID = strings.TrimSpace(extracted.AccountID)
			summary.ExternalAccountEmail = firstNonEmpty(summary.ExternalAccountEmail, extracted.Email)
			if summary.Source == "" {
				summary.Source = extracted.Source
			}
		}
	}
	if summary.Source == "" {
		if subjectID != "" || summary.ExternalAccountID != "" {
			summary.Source = "import_payload"
		} else {
			summary.Source = accountident.SourceManual
		}
	}
	summary.SubjectIdentityPresent = subjectID != ""
	switch {
	case subjectID != "":
		summary.Strength = "strong"
	case summary.ExternalAccountID != "":
		summary.Strength = "scoped"
	case summary.ExternalAccountEmail != "":
		summary.Strength = "weak"
	default:
		summary.Strength = "opaque"
	}
	return candidateIdentity{
		Summary: summary, SubjectID: subjectID, CredentialFingerprint: fingerprint,
		AccountIsSharedScope: isSharedAccountScope(candidate, explicitChatGPTAccountID),
	}
}

func isSharedAccountScope(candidate credentialacq.CredentialCandidate, explicitChatGPTAccountID string) bool {
	if candidate.Vendor != credentialstore.VendorOpenAI {
		return false
	}
	if strings.TrimSpace(explicitChatGPTAccountID) != "" {
		return true
	}
	switch candidate.AuthMode {
	case credentialstore.AuthModeChatGPTOAuth, credentialstore.AuthModeCodexCLIOAuth, credentialstore.AuthModeCodexWebOAuth:
		return true
	default:
		return false
	}
}

func extractLifecycle(raw []byte, handler credentialstore.ModeHandler, now time.Time) LifecycleSummary {
	fields := payloadFields(raw)
	summary := LifecycleSummary{
		Refreshable:        handler.Refreshable(),
		HasRefreshMaterial: firstString(fields, "refresh_token") != "",
	}
	if rawExpiry := firstString(fields, "expires_at", "access_expires_at", "expiry"); rawExpiry != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, rawExpiry); err == nil {
			parsed = parsed.UTC()
			summary.AccessExpiresAt = &parsed
			summary.Expired = !parsed.After(now)
		}
	}
	return summary
}

func payloadFields(raw []byte) map[string]any {
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return map[string]any{}
	}
	return fields
}

func firstString(fields map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := fields[name].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func identityMatchKey(identity candidateIdentity, vendor string, hasRefreshMaterial bool) string {
	prefix := credentialstore.Normalize(vendor)
	if !hasRefreshMaterial && identity.CredentialFingerprint != "" {
		return prefix + "/fingerprint/" + identity.CredentialFingerprint
	}
	if identity.SubjectID != "" {
		return prefix + "/subject/" + identity.SubjectID
	}
	if identity.Summary.ExternalAccountID != "" {
		return prefix + "/account/" + identity.Summary.ExternalAccountID
	}
	if identity.Summary.ExternalAccountEmail != "" {
		return prefix + "/email/" + strings.ToLower(identity.Summary.ExternalAccountEmail)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
