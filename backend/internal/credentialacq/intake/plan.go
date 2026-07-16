// Package intake 把不同凭据来源归一成不写库、不发网络的账号接入预检计划。
package intake

import (
	"context"
	"crypto/sha256"
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

var (
	ErrSourceDisabled               = errors.New("credential intake source disabled")
	ErrIdentityInventoryUnavailable = errors.New("credential intake identity inventory unavailable")
	ErrTenantRequired               = errors.New("credential intake tenant required")
)

type ExistingCredential struct {
	ProviderAccountID     int64
	ProviderAccountName   string
	Vendor                string
	AuthMode              string
	State                 string
	ExternalAccountID     string
	ExternalAccountEmail  string
	ExternalSubjectID     string
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

type IdentityInventory interface {
	ListIdentityInventory(context.Context, int64, string) ([]credentialstore.CredentialIdentityMetadata, error)
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

func Build(in BuildInput) (Plan, error) {
	if in.TenantID <= 0 {
		return Plan{}, ErrTenantRequired
	}
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
	seenUntrustedIdentity := make(map[string]int)
	seenPayload := make(map[[32]byte]int)
	for index, candidate := range candidates {
		item := planCandidate(
			index, in.TenantID, candidate, in.Existing, registry, now,
			seenIdentity, seenUntrustedIdentity, seenPayload,
		)
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

// BuildFromInventory 使用租户范围内的无秘密凭据 inventory 生成预检计划。
func BuildFromInventory(ctx context.Context, inventory IdentityInventory, in BuildInput) (Plan, error) {
	if in.TenantID <= 0 {
		return Plan{}, ErrTenantRequired
	}
	if inventory == nil {
		return Plan{}, ErrIdentityInventoryUnavailable
	}
	rows, err := inventory.ListIdentityInventory(ctx, in.TenantID, "")
	if err != nil {
		return Plan{}, fmt.Errorf("%w: %v", ErrIdentityInventoryUnavailable, err)
	}
	in.Existing = ExistingFromIdentityMetadata(rows)
	return Build(in)
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
	tenantID int64,
	candidate credentialacq.CredentialCandidate,
	existing []ExistingCredential,
	registry *credentialstore.HandlerRegistry,
	now time.Time,
	seenIdentity map[string]int,
	seenUntrustedIdentity map[string]int,
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
	identity := extractIdentity(candidate, credentialstore.CredentialMaterialFingerprint(tenantID, candidate.Vendor, candidate.AuthMode, candidate.Payload))
	item.Identity = identity.Summary
	item.Lifecycle = extractLifecycle(candidate.Payload, handler, now)
	if identity.SubjectID != "" && !identity.SubjectTrusted {
		item.Warnings = append(item.Warnings, "unverified_subject_metadata")
		item.RequiredConfirmations = append(item.RequiredConfirmations, "confirm_unverified_subject_metadata")
	}
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

	if firstIndex, duplicate := seenPayload[payloadKey]; duplicate {
		item.Action = ActionSkip
		item.Code = "duplicate_input"
		item.Message = fmt.Sprintf("与本批第 %d 项重复", firstIndex+1)
		return item
	}
	untrustedIdentityKey := untrustedIdentityMatchKey(identity, candidate.Vendor)
	if untrustedIdentityKey != "" {
		if firstIndex, duplicate := seenUntrustedIdentity[untrustedIdentityKey]; duplicate {
			item.Action = ActionConflict
			item.Code = "unverified_identity_collision"
			item.Message = fmt.Sprintf("与本批第 %d 项声明了相同但未经验证的上游身份", firstIndex+1)
			item.RequiredConfirmations = append(item.RequiredConfirmations, "resolve_identity_conflict")
			return item
		}
	}
	identityKey := identityMatchKey(identity, candidate.Vendor, item.Lifecycle.HasRefreshMaterial)
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
		markExecutableIdentity(
			seenPayload, seenIdentity, seenUntrustedIdentity,
			payloadKey, identityKey, untrustedIdentityKey, index,
		)
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
	markExecutableIdentity(
		seenPayload, seenIdentity, seenUntrustedIdentity,
		payloadKey, identityKey, untrustedIdentityKey, index,
	)
	return item
}

func markExecutableIdentity(
	seenPayload map[[32]byte]int,
	seenIdentity map[string]int,
	seenUntrustedIdentity map[string]int,
	payloadKey [32]byte,
	identityKey string,
	untrustedIdentityKey string,
	index int,
) {
	seenPayload[payloadKey] = index
	if identityKey != "" {
		seenIdentity[identityKey] = index
	}
	if untrustedIdentityKey != "" {
		seenUntrustedIdentity[untrustedIdentityKey] = index
	}
}

func extractIdentity(candidate credentialacq.CredentialCandidate, fingerprint string) candidateIdentity {
	summary := IdentitySummary{
		ExternalAccountID:    strings.TrimSpace(candidate.ExternalAccountID),
		ExternalAccountEmail: strings.TrimSpace(candidate.ExternalAccountEmail),
		Source:               strings.TrimSpace(candidate.AccountIDSource),
	}
	fields := payloadFields(candidate.Payload)
	subjectID := firstNonEmpty(candidate.ExternalSubjectID, firstString(fields, "external_subject_id", "chatgpt_user_id"))
	explicitChatGPTAccountID := firstString(fields, "chatgpt_account_id")
	if summary.ExternalAccountID == "" {
		summary.ExternalAccountID = firstNonEmpty(explicitChatGPTAccountID, firstString(fields, "external_account_id", "account_id"))
	}
	if summary.ExternalAccountEmail == "" {
		summary.ExternalAccountEmail = firstString(fields, "external_account_email", "email")
	}
	if summary.Source == "" {
		if subjectID != "" || summary.ExternalAccountID != "" {
			summary.Source = accountident.SourceImportPayload
		} else {
			summary.Source = accountident.SourceManual
		}
	}
	summary.SubjectIdentityPresent = subjectID != ""
	subjectTrusted := trustedIdentitySource(summary.Source)
	summary.SubjectIdentityTrusted = subjectTrusted
	switch {
	case subjectID != "" && subjectTrusted:
		summary.Strength = "strong"
	case summary.ExternalAccountID != "":
		summary.Strength = "scoped"
	case subjectID != "":
		summary.Strength = "weak"
	case summary.ExternalAccountEmail != "":
		summary.Strength = "weak"
	default:
		summary.Strength = "opaque"
	}
	return candidateIdentity{
		Summary: summary, SubjectID: subjectID, CredentialFingerprint: fingerprint,
		SubjectTrusted:       subjectTrusted,
		AccountIsSharedScope: isSharedAccountScope(candidate, explicitChatGPTAccountID),
	}
}

func trustedIdentitySource(source string) bool {
	switch strings.TrimSpace(source) {
	case accountident.SourceAnthropicAccountID,
		accountident.SourceChatGPTJWTClaim,
		accountident.SourceGoogleIDTokenSub:
		return true
	default:
		return false
	}
}

// ExistingFromIdentityMetadata 把 credential store 的无秘密 inventory 转为预检输入。
func ExistingFromIdentityMetadata(rows []credentialstore.CredentialIdentityMetadata) []ExistingCredential {
	out := make([]ExistingCredential, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExistingCredential{
			ProviderAccountID: row.ProviderAccountID, ProviderAccountName: row.ProviderAccountName,
			Vendor: row.Vendor, AuthMode: row.AuthMode, State: row.State,
			ExternalAccountID: row.ExternalAccountID, ExternalSubjectID: row.ExternalSubjectID,
			ExternalAccountEmail: row.ExternalAccountEmail, AccountIDSource: row.ExternalIdentitySource,
			CredentialFingerprint: row.CredentialMaterialFingerprint,
		})
	}
	return out
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
	if identity.SubjectID != "" && identity.SubjectTrusted {
		return prefix + "/subject/" + identity.SubjectID
	}
	if identity.CredentialFingerprint != "" {
		return prefix + "/fingerprint/" + identity.CredentialFingerprint
	}
	if identity.Summary.ExternalAccountID != "" {
		return prefix + "/account/" + identity.Summary.ExternalAccountID
	}
	if identity.Summary.ExternalAccountEmail != "" {
		return prefix + "/email/" + strings.ToLower(identity.Summary.ExternalAccountEmail)
	}
	return ""
}

func untrustedIdentityMatchKey(identity candidateIdentity, vendor string) string {
	if identity.SubjectTrusted {
		return ""
	}
	prefix := credentialstore.Normalize(vendor)
	if identity.SubjectID != "" {
		return prefix + "/subject/" + identity.SubjectID
	}
	if identity.Summary.ExternalAccountID != "" {
		return prefix + "/account/" + identity.Summary.ExternalAccountID
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
