package accounthealthview

import (
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

// Observability 只描述账号运营数据的来源与覆盖状态，不参与生产选号。
type Observability struct {
	Identity IdentityAxis `json:"identity"`
	Quota    QuotaAxis    `json:"quota"`
	Models   ModelsAxis   `json:"models"`
	Project  ProjectAxis  `json:"project"`
}

type IdentityAxis struct {
	ProviderCode                string `json:"provider_code"`
	ProviderAvailable           bool   `json:"provider_available"`
	AccountType                 string `json:"account_type"`
	CredentialFound             bool   `json:"credential_found"`
	CredentialSelectionState    string `json:"credential_selection_state"`
	ServingCredentialCandidates int32  `json:"serving_credential_candidates"`
	CredentialVendor            string `json:"credential_vendor,omitempty"`
	CredentialAuthMode          string `json:"credential_auth_mode,omitempty"`
	Persistence                 string `json:"persistence"`
}

type QuotaAxis struct {
	CollectionState string `json:"collection_state"`
	Source          string `json:"source"`
	AccountSpecific bool   `json:"account_specific"`
	Persistence     string `json:"persistence"`
}

type ModelsAxis struct {
	CollectionState string  `json:"collection_state"`
	Source          string  `json:"source"`
	AccountSpecific bool    `json:"account_specific"`
	LastCheckedAt   *string `json:"last_checked_at,omitempty"`
	Persistence     string  `json:"persistence"`
}

type ProjectAxis struct {
	Required    bool    `json:"required"`
	State       string  `json:"state"`
	Source      string  `json:"source"`
	ProjectRef  *string `json:"project_ref,omitempty"`
	Persistence string  `json:"persistence"`
}

// BuildObservability 将已经持久化的安全元数据投影为来源明确的运营视图。
func BuildObservability(row admindb.GetAdminProviderAccountHealthRow, now time.Time) Observability {
	now = now.UTC()
	providerCode := strings.ToLower(strings.TrimSpace(row.ProviderCode))
	accountType := credentialstore.Normalize(row.AccountType)
	vendor := credentialstore.Normalize(row.CredentialVendor)
	authMode := credentialstore.Normalize(row.CredentialAuthMode)
	credentialSelectionState := credentialSelectionState(row.ServingCredentialCandidates, vendor, authMode)
	credentialFound := credentialSelectionState == "resolved"
	if !credentialFound {
		vendor = ""
		authMode = ""
	}
	projectRequired := projectRequiredForMode(providerCode, accountType, vendor, authMode)
	storedProjectRef := trimmedString(row.CredentialProjectRef)
	var projectRef *string

	projectState := "not_required"
	projectSource := "none"
	if credentialSelectionState == "ambiguous" {
		projectState = "credential_ambiguous"
	} else if projectRequired {
		projectSource = "credential_metadata"
		switch {
		case !credentialFound:
			projectState = "credential_unavailable"
		case storedProjectRef == nil:
			projectState = "missing"
		default:
			projectState = "resolved"
			projectRef = storedProjectRef
		}
	}

	return Observability{
		Identity: IdentityAxis{
			ProviderCode: providerCode, ProviderAvailable: row.ProviderAvailable,
			AccountType: accountType, CredentialFound: credentialFound,
			CredentialSelectionState:    credentialSelectionState,
			ServingCredentialCandidates: row.ServingCredentialCandidates,
			CredentialVendor:            vendor, CredentialAuthMode: authMode, Persistence: "postgres",
		},
		Quota: QuotaAxis{
			CollectionState: quotaCollectionState(row, credentialSelectionState, vendor, authMode, now),
			Source:          quotaSource(vendor, authMode), AccountSpecific: true, Persistence: "postgres",
		},
		Models: ModelsAxis{
			CollectionState: modelCollectionState(row.ProviderAvailable, providerCode, row.ModelSyncLastCheckAt),
			Source:          modelCollectionSource(providerCode), AccountSpecific: false,
			LastCheckedAt: modelLastCheckedAt(providerCode, row.ModelSyncLastCheckAt), Persistence: "postgres",
		},
		Project: ProjectAxis{
			Required: projectRequired, State: projectState, Source: projectSource,
			ProjectRef: projectRef, Persistence: "postgres",
		},
	}
}

func credentialSelectionState(candidateCount int32, vendor, authMode string) string {
	switch {
	case candidateCount > 1:
		return "ambiguous"
	case candidateCount == 1 && vendor != "" && authMode != "":
		return "resolved"
	default:
		return "unavailable"
	}
}

func quotaCollectionState(row admindb.GetAdminProviderAccountHealthRow, selectionState, vendor, authMode string, now time.Time) string {
	if !row.ProviderAvailable {
		return "provider_unavailable"
	}
	if selectionState == "ambiguous" {
		return "credential_ambiguous"
	}
	if selectionState != "resolved" {
		return "credential_unavailable"
	}
	if vendor != credentialstore.VendorAnthropic || authMode != credentialstore.AuthModeClaudeAIOAuth {
		return "not_implemented_for_mode"
	}
	if activeQuotaWindow(row.SessionWindow5hEnd, row.SessionWindow5hUtilization, now) ||
		activeQuotaWindow(row.SessionWindow7dEnd, row.SessionWindow7dUtilization, now) {
		return "observed"
	}
	if quotaWindowsExpired(row.SessionWindow5hEnd, row.SessionWindow7dEnd, now) {
		return "expired"
	}
	return "no_snapshot"
}

func quotaSource(vendor, authMode string) string {
	if vendor == credentialstore.VendorAnthropic && authMode == credentialstore.AuthModeClaudeAIOAuth {
		return "anthropic_oauth_usage"
	}
	return "none"
}

func activeQuotaWindow(end pgtype.Timestamptz, utilization pgtype.Numeric, now time.Time) bool {
	if !end.Valid || !end.Time.After(now) {
		return false
	}
	value, err := utilization.Float64Value()
	return err == nil && value.Valid && value.Float64 >= 0 && value.Float64 <= 100
}

func quotaWindowsExpired(fiveHourEnd, sevenDayEnd pgtype.Timestamptz, now time.Time) bool {
	seen := false
	for _, end := range []pgtype.Timestamptz{fiveHourEnd, sevenDayEnd} {
		if !end.Valid {
			continue
		}
		seen = true
		if end.Time.After(now) {
			return false
		}
	}
	return seen
}

func modelCollectionState(providerAvailable bool, providerCode string, checkedAt pgtype.Timestamptz) string {
	if !providerAvailable {
		return "provider_unavailable"
	}
	if !providerCatalogSyncImplemented(providerCode) {
		return "not_implemented_for_provider"
	}
	if checkedAt.Valid {
		return "provider_catalog_observed"
	}
	return "no_snapshot"
}

func modelCollectionSource(providerCode string) string {
	if providerCatalogSyncImplemented(providerCode) {
		return "provider_catalog_sync"
	}
	return "none"
}

func modelLastCheckedAt(providerCode string, checkedAt pgtype.Timestamptz) *string {
	if !providerCatalogSyncImplemented(providerCode) {
		return nil
	}
	return formatPGTime(checkedAt)
}

func providerCatalogSyncImplemented(providerCode string) bool {
	switch providerCode {
	case credentialstore.VendorOpenAI, credentialstore.VendorAnthropic, credentialstore.VendorGemini:
		return true
	default:
		return false
	}
}

func projectRequiredForMode(providerCode, accountType, vendor, authMode string) bool {
	if vendor == credentialstore.VendorGemini {
		return authMode == credentialstore.AuthModeCodeAssist || authMode == credentialstore.AuthModeAntigravity
	}
	if vendor == credentialstore.VendorAntigravity {
		return authMode == credentialstore.AuthModeOAuth || authMode == credentialstore.AuthModeAntigravity
	}
	if vendor != "" || authMode != "" {
		return false
	}
	return providerCode == credentialstore.VendorAntigravity && accountType == credentialstore.AuthModeOAuth
}

func trimmedString(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
