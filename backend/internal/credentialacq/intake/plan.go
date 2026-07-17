// Package intake 把不同凭据来源归一成不写库、不发网络的账号接入预检计划。
package intake

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

const (
	ContractVersion = "account-intake-v1"
	MaxCandidates   = 500
)

type SourceKind string

const (
	SourceCLI              SourceKind = "cli_import"
	SourceJSON             SourceKind = "json_import"
	SourceCSV              SourceKind = "csv_import"
	SourceClaudeSetupToken SourceKind = "claude_setup_token"
)

type Action string

const (
	ActionCreate   Action = "create"
	ActionUpdate   Action = "update"
	ActionSkip     Action = "skip"
	ActionConflict Action = "conflict"
	ActionFail     Action = "fail"
)

var (
	ErrTenantRequired  = errors.New("credential intake tenant required")
	ErrTooManyItems    = errors.New("credential intake exceeds item limit")
	ErrSourceInvalid   = errors.New("credential intake source invalid")
	ErrInventoryAbsent = errors.New("credential intake identity inventory unavailable")
)

type ExistingCredential struct {
	CredentialID          int64
	CredentialVersion     int32
	ProviderAccountID     int64
	ProviderAccountName   string
	Vendor                string
	AuthMode              string
	State                 string
	ExternalAccountID     string
	ExternalSubjectID     string
	ExternalAccountEmail  string
	AccountIDSource       string
	CredentialFingerprint string
}

type BuildInput struct {
	TenantID        int64
	SourceKind      SourceKind
	DefaultVendor   string
	DefaultAuthMode string
	Content         string
	Existing        []ExistingCredential
	Now             time.Time
}

type PreparedPlan struct {
	Plan       Plan
	Candidates []credentialacq.CredentialCandidate
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
	Index                     int                      `json:"index"`
	Vendor                    string                   `json:"vendor"`
	AuthMode                  string                   `json:"auth_mode"`
	Action                    Action                   `json:"action"`
	Code                      string                   `json:"code"`
	Message                   string                   `json:"message"`
	ExistingAccountID         int64                    `json:"existing_account_id,omitempty"`
	ExistingAccountName       string                   `json:"existing_account_name,omitempty"`
	ExistingCredentialID      int64                    `json:"existing_credential_id,omitempty"`
	ExistingCredentialVersion int32                    `json:"existing_credential_version,omitempty"`
	Identity                  IdentitySummary          `json:"identity"`
	Lifecycle                 LifecycleSummary         `json:"lifecycle"`
	FieldChanges              []string                 `json:"field_changes,omitempty"`
	Warnings                  []string                 `json:"warnings,omitempty"`
	RequiredConfirmations     []string                 `json:"required_confirmations,omitempty"`
	MixedChannelRisk          *mixedchannelrisk.Report `json:"mixed_channel_risk,omitempty"`
}

type IdentitySummary struct {
	ExternalAccountID      string `json:"external_account_id,omitempty"`
	ExternalAccountEmail   string `json:"external_account_email,omitempty"`
	SubjectIdentityPresent bool   `json:"subject_identity_present"`
	SubjectIdentityTrusted bool   `json:"subject_identity_trusted"`
	Source                 string `json:"source"`
	Strength               string `json:"strength"`
}

type LifecycleSummary struct {
	Refreshable        bool       `json:"refreshable"`
	HasRefreshMaterial bool       `json:"has_refresh_material"`
	AccessExpiresAt    *time.Time `json:"access_expires_at,omitempty"`
	Expired            bool       `json:"expired"`
}

type candidateIdentity struct {
	Summary               IdentitySummary
	AccountID             string
	SubjectID             string
	Email                 string
	SubjectTrusted        bool
	CredentialFingerprint string
	SharedAccountScope    bool
}

func Build(in BuildInput) (PreparedPlan, error) {
	if in.TenantID <= 0 {
		return PreparedPlan{}, ErrTenantRequired
	}
	candidates, err := parseSource(in.SourceKind, in.Content, in.DefaultVendor, in.DefaultAuthMode)
	if err != nil {
		return PreparedPlan{}, err
	}
	if len(candidates) > MaxCandidates {
		return PreparedPlan{}, ErrTooManyItems
	}
	return BuildCandidates(in, candidates), nil
}

func BuildCandidates(in BuildInput, candidates []credentialacq.CredentialCandidate) PreparedPlan {
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	plan := Plan{
		ContractVersion: ContractVersion,
		SourceKind:      in.SourceKind,
		InputCount:      len(candidates),
		Items:           make([]Item, 0, len(candidates)),
	}
	registry := credentialstore.DefaultHandlerRegistry()
	seenPayload := make(map[[32]byte]int)
	seenIdentity := make(map[string]int)
	seenUntrusted := make(map[string]int)
	for index, candidate := range candidates {
		candidate.Vendor = credentialstore.Normalize(candidate.Vendor)
		candidate.AuthMode = credentialstore.Normalize(candidate.AuthMode)
		candidates[index] = candidate
		item := planCandidate(index, in.TenantID, candidate, in.Existing, registry, now, seenPayload, seenIdentity, seenUntrusted)
		plan.Items = append(plan.Items, item)
		addSummary(&plan.Summary, item.Action)
	}
	return PreparedPlan{Plan: plan, Candidates: candidates}
}

func ExistingFromIdentityMetadata(rows []credentialstore.CredentialIdentityMetadata) []ExistingCredential {
	out := make([]ExistingCredential, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExistingCredential{
			CredentialID: row.CredentialID, CredentialVersion: row.CredentialVersion,
			ProviderAccountID: row.ProviderAccountID, ProviderAccountName: row.ProviderAccountName,
			Vendor: row.Vendor, AuthMode: row.AuthMode, State: row.State,
			ExternalAccountID: row.ExternalAccountID, ExternalSubjectID: row.ExternalSubjectID,
			ExternalAccountEmail: row.ExternalAccountEmail, AccountIDSource: row.ExternalIdentitySource,
			CredentialFingerprint: row.CredentialMaterialFingerprint,
		})
	}
	return out
}

func parseSource(source SourceKind, content, vendor, authMode string) ([]credentialacq.CredentialCandidate, error) {
	switch source {
	case SourceCLI:
		return credentialacq.ParseImportContent(content, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
	case SourceJSON:
		return credentialacq.ParseImportContent(content,
			firstNonEmpty(vendor, credentialstore.VendorOpenAI),
			firstNonEmpty(authMode, credentialstore.AuthModeCodexCLIOAuth))
	case SourceCSV:
		return credentialacq.ParseCSVImportContent(content,
			firstNonEmpty(vendor, credentialstore.VendorOpenAI),
			firstNonEmpty(authMode, credentialstore.AuthModeCodexCLIOAuth))
	case SourceClaudeSetupToken:
		return credentialacq.ParseClaudeSetupTokenContent(content)
	default:
		return nil, fmt.Errorf("%w: %s", ErrSourceInvalid, source)
	}
}

func planCandidate(
	index int,
	tenantID int64,
	candidate credentialacq.CredentialCandidate,
	existing []ExistingCredential,
	registry *credentialstore.HandlerRegistry,
	now time.Time,
	seenPayload map[[32]byte]int,
	seenIdentity map[string]int,
	seenUntrusted map[string]int,
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
	identity := extractIdentity(tenantID, candidate)
	item.Identity = identity.Summary
	item.Lifecycle = extractLifecycle(candidate.Payload, handler, now)
	if item.Lifecycle.Expired && !item.Lifecycle.HasRefreshMaterial {
		item.Code = "expired_without_refresh"
		item.Message = "访问凭据已过期且没有刷新材料"
		return item
	}
	if item.Lifecycle.Expired {
		item.Warnings = append(item.Warnings, "access_expired_refresh_required")
	}
	if firstIndex, duplicate := seenPayload[payloadKey]; duplicate {
		item.Action = ActionSkip
		item.Code = "duplicate_input"
		item.Message = fmt.Sprintf("与本批第 %d 项完全重复", firstIndex+1)
		return item
	}
	identityKey := identityMatchKey(identity, candidate.Vendor, item.Lifecycle.HasRefreshMaterial)
	untrustedKey := untrustedIdentityKey(identity, candidate.Vendor)
	if untrustedKey != "" {
		if firstIndex, duplicate := seenUntrusted[untrustedKey]; duplicate {
			item.Action = ActionConflict
			item.Code = "unverified_identity_collision"
			item.Message = fmt.Sprintf("与本批第 %d 项声明了相同但未经验证的上游身份", firstIndex+1)
			item.RequiredConfirmations = []string{"resolve_identity_conflict"}
			return item
		}
	}
	if identityKey != "" {
		if firstIndex, duplicate := seenIdentity[identityKey]; duplicate {
			item.Action = ActionSkip
			item.Code = "duplicate_identity"
			item.Message = fmt.Sprintf("与本批第 %d 项属于同一可判定凭据身份", firstIndex+1)
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
		item.RequiredConfirmations = []string{"resolve_identity_conflict"}
		return item
	}
	if match != nil {
		item.Action = ActionUpdate
		item.Code = match.code
		item.Message = match.message
		item.ExistingAccountID = match.credential.ProviderAccountID
		item.ExistingAccountName = match.credential.ProviderAccountName
		item.ExistingCredentialID = match.credential.CredentialID
		item.ExistingCredentialVersion = match.credential.CredentialVersion
		item.FieldChanges = []string{"credential_payload", "credential_version", "credential_lifecycle", "identity_metadata"}
		item.Warnings = append(item.Warnings, match.warnings...)
		item.RequiredConfirmations = append(item.RequiredConfirmations, match.confirmations...)
		markSeen(seenPayload, seenIdentity, seenUntrusted, payloadKey, identityKey, untrustedKey, index)
		return item
	}

	item.Action = ActionCreate
	item.Code = "create_account"
	item.Message = "将创建独立 provider account 并写入加密凭据"
	item.FieldChanges = []string{"provider_account", "account_credential", "channel_health_default"}
	if identity.Summary.Strength == "weak" || identity.Summary.Strength == "opaque" {
		item.Warnings = append(item.Warnings, "weak_identity")
		item.RequiredConfirmations = append(item.RequiredConfirmations, "confirm_weak_identity")
	}
	markSeen(seenPayload, seenIdentity, seenUntrusted, payloadKey, identityKey, untrustedKey, index)
	return item
}

func extractIdentity(tenantID int64, candidate credentialacq.CredentialCandidate) candidateIdentity {
	fields := payloadFields(candidate.Payload)
	accountID := firstNonEmpty(candidate.ExternalAccountID, firstString(fields, "external_account_id", "chatgpt_account_id", "account_id"))
	subjectID := firstNonEmpty(candidate.ExternalSubjectID, firstString(fields, "external_subject_id", "chatgpt_user_id"))
	email := firstNonEmpty(candidate.ExternalAccountEmail, firstString(fields, "external_account_email", "email"))
	source := firstNonEmpty(candidate.AccountIDSource, accountident.SourceManual)
	trusted := trustedIdentitySource(source)
	strength := "opaque"
	switch {
	case subjectID != "" && trusted:
		strength = "strong"
	case accountID != "":
		strength = "scoped"
	case subjectID != "" || email != "":
		strength = "weak"
	}
	return candidateIdentity{
		Summary: IdentitySummary{
			ExternalAccountID:      redactedIdentityHint("account", accountID),
			ExternalAccountEmail:   redactedIdentityHint("email", strings.ToLower(email)),
			SubjectIdentityPresent: subjectID != "", SubjectIdentityTrusted: trusted,
			Source: source, Strength: strength,
		},
		AccountID: accountID, SubjectID: subjectID, Email: email, SubjectTrusted: trusted,
		CredentialFingerprint: credentialstore.CredentialMaterialFingerprint(tenantID, candidate.Vendor, candidate.AuthMode, candidate.Payload),
		SharedAccountScope: candidate.Vendor == credentialstore.VendorOpenAI &&
			(candidate.AuthMode == credentialstore.AuthModeChatGPTOAuth ||
				candidate.AuthMode == credentialstore.AuthModeCodexCLIOAuth ||
				candidate.AuthMode == credentialstore.AuthModeCodexWebOAuth),
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

func trustedIdentitySource(source string) bool {
	switch strings.TrimSpace(source) {
	case accountident.SourceAnthropicAccountID,
		accountident.SourceChatGPTJWTClaim,
		accountident.SourceOpenAITokenBody,
		accountident.SourceGoogleIDTokenSub:
		return true
	default:
		return false
	}
}

func identityMatchKey(identity candidateIdentity, vendor string, hasRefreshMaterial bool) string {
	prefix := credentialstore.Normalize(vendor)
	if !hasRefreshMaterial && identity.CredentialFingerprint != "" {
		return prefix + "/fingerprint/" + identity.CredentialFingerprint
	}
	if identity.SubjectID != "" && identity.SubjectTrusted {
		return prefix + "/subject/" + identity.SubjectID
	}
	if identity.CredentialFingerprint != "" {
		return prefix + "/fingerprint/" + identity.CredentialFingerprint
	}
	if identity.AccountID != "" {
		return prefix + "/account/" + identity.AccountID
	}
	if identity.Email != "" {
		return prefix + "/email/" + strings.ToLower(identity.Email)
	}
	return ""
}

func untrustedIdentityKey(identity candidateIdentity, vendor string) string {
	if identity.SubjectTrusted {
		return ""
	}
	prefix := credentialstore.Normalize(vendor)
	if identity.SubjectID != "" {
		return prefix + "/subject/" + identity.SubjectID
	}
	if identity.AccountID != "" {
		return prefix + "/account/" + identity.AccountID
	}
	return ""
}

func redactedIdentityHint(kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(kind + "\x00" + value))
	return fmt.Sprintf("%s_%x", kind, sum[:6])
}

func markSeen(
	seenPayload map[[32]byte]int,
	seenIdentity map[string]int,
	seenUntrusted map[string]int,
	payloadKey [32]byte,
	identityKey string,
	untrustedKey string,
	index int,
) {
	seenPayload[payloadKey] = index
	if identityKey != "" {
		seenIdentity[identityKey] = index
	}
	if untrustedKey != "" {
		seenUntrusted[untrustedKey] = index
	}
}

func addSummary(summary *Summary, action Action) {
	switch action {
	case ActionCreate:
		summary.Create++
	case ActionUpdate:
		summary.Update++
	case ActionSkip:
		summary.Skip++
	case ActionConflict:
		summary.Conflict++
	case ActionFail:
		summary.Fail++
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
