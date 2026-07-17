package accounthealthview

import (
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

func TestBuildObservabilityClaudeQuotaStates(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		windowEnd pgtype.Timestamptz
		usage     pgtype.Numeric
		want      string
	}{
		{name: "有效快照", windowEnd: observationTime(now.Add(time.Hour)), usage: observationNumeric(31.5), want: "observed"},
		{name: "过期快照", windowEnd: observationTime(now.Add(-time.Minute)), usage: observationNumeric(31.5), want: "expired"},
		{name: "活动窗口缺 utilization", windowEnd: observationTime(now.Add(time.Hour)), want: "no_snapshot"},
		{name: "没有快照", want: "no_snapshot"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := observationRow("anthropic", credentialstore.AuthModeClaudeAIOAuth)
			row.SessionWindow5hEnd = tc.windowEnd
			row.SessionWindow5hUtilization = tc.usage
			got := BuildObservability(row, now)
			if got.Quota.CollectionState != tc.want || got.Quota.Source != "anthropic_oauth_usage" || !got.Quota.AccountSpecific {
				t.Fatalf("quota=%+v want state=%q/source=anthropic_oauth_usage/account_specific", got.Quota, tc.want)
			}
		})
	}
}

func TestBuildObservabilityDistinguishesProviderCatalogFromAccountModels(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	row := observationRow("gemini", credentialstore.AuthModeCodeAssist)
	row.ModelSyncLastCheckAt = observationTime(now.Add(-time.Hour))

	got := BuildObservability(row, now)
	if got.Models.CollectionState != "provider_catalog_observed" || got.Models.Source != "provider_catalog_sync" {
		t.Fatalf("models=%+v want provider catalog snapshot", got.Models)
	}
	if got.Models.AccountSpecific {
		t.Fatalf("provider catalog 不得标记为账号级模型：%+v", got.Models)
	}
	if got.Models.LastCheckedAt == nil || *got.Models.LastCheckedAt != "2026-07-16T11:00:00Z" {
		t.Fatalf("last_checked_at=%v", got.Models.LastCheckedAt)
	}
}

func TestBuildObservabilityProjectAndCoverageStates(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)

	codeAssist := observationRow("gemini", credentialstore.AuthModeCodeAssist)
	missing := BuildObservability(codeAssist, now)
	if !missing.Project.Required || missing.Project.State != "missing" || missing.Project.Source != "credential_metadata" {
		t.Fatalf("Code Assist project=%+v want required/missing", missing.Project)
	}
	projectRef := "  project-7  "
	codeAssist.CredentialProjectRef = &projectRef
	resolved := BuildObservability(codeAssist, now)
	if resolved.Project.State != "resolved" || resolved.Project.ProjectRef == nil || *resolved.Project.ProjectRef != "project-7" {
		t.Fatalf("Code Assist project=%+v want resolved project-7", resolved.Project)
	}
	apiKey := observationRow("gemini", credentialstore.AuthModeAIStudioAPIKey)
	apiKey.CredentialProjectRef = &projectRef
	notRequired := BuildObservability(apiKey, now)
	if notRequired.Project.Required || notRequired.Project.State != "not_required" || notRequired.Project.ProjectRef != nil {
		t.Fatalf("非 project 模式不得回显无关 project_ref：%+v", notRequired.Project)
	}

	kimi := observationRow("kimi", credentialstore.AuthModeAPIKey)
	kimi.ModelSyncLastCheckAt = observationTime(now.Add(-time.Hour))
	kimiView := BuildObservability(kimi, now)
	if kimiView.Quota.CollectionState != "not_implemented_for_mode" || kimiView.Quota.Source != "none" {
		t.Fatalf("Kimi quota=%+v want explicit not implemented", kimiView.Quota)
	}
	if kimiView.Models.CollectionState != "not_implemented_for_provider" || kimiView.Models.Source != "none" {
		t.Fatalf("Kimi models=%+v want explicit not implemented", kimiView.Models)
	}
	if kimiView.Models.LastCheckedAt != nil {
		t.Fatalf("未实现的 provider 不得回显不可信模型同步时间：%+v", kimiView.Models)
	}

	antigravity := observationRow("antigravity", credentialstore.AuthModeOAuth)
	antigravity.CredentialVendor = ""
	antigravity.CredentialAuthMode = ""
	antigravity.ServingCredentialCandidates = 0
	antigravity.AccountType = credentialstore.AuthModeOAuth
	antigravityView := BuildObservability(antigravity, now)
	if antigravityView.Identity.CredentialFound || !antigravityView.Project.Required || antigravityView.Project.State != "credential_unavailable" {
		t.Fatalf("Antigravity 无凭据状态=%+v", antigravityView)
	}

	ambiguous := observationRow("gemini", credentialstore.AuthModeCodeAssist)
	ambiguous.ServingCredentialCandidates = 2
	ambiguousView := BuildObservability(ambiguous, now)
	if ambiguousView.Identity.CredentialFound || ambiguousView.Identity.CredentialSelectionState != "ambiguous" ||
		ambiguousView.Identity.CredentialVendor != "" || ambiguousView.Identity.CredentialAuthMode != "" ||
		ambiguousView.Quota.CollectionState != "credential_ambiguous" || ambiguousView.Project.State != "credential_ambiguous" {
		t.Fatalf("多条可服务凭据必须显式冲突且不得随便选一条：%+v", ambiguousView)
	}
}

func TestBuildObservabilityProviderUnavailableIsNotGreen(t *testing.T) {
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	row := observationRow("anthropic", credentialstore.AuthModeClaudeAIOAuth)
	row.ProviderAvailable = false
	row.ModelSyncLastCheckAt = observationTime(now.Add(-time.Hour))
	row.SessionWindow5hEnd = observationTime(now.Add(time.Hour))
	row.SessionWindow5hUtilization = observationNumeric(12)

	got := BuildObservability(row, now)
	if got.Quota.CollectionState != "provider_unavailable" || got.Models.CollectionState != "provider_unavailable" {
		t.Fatalf("删除/不可用 provider 不得返回已观测：%+v", got)
	}
}

func observationRow(providerCode, authMode string) admindb.GetAdminProviderAccountHealthRow {
	accountType := credentialstore.AuthModeOAuth
	if authMode == credentialstore.AuthModeAPIKey || authMode == credentialstore.AuthModeAIStudioAPIKey {
		accountType = credentialstore.AuthModeAPIKey
	}
	return admindb.GetAdminProviderAccountHealthRow{
		ProviderCode: providerCode, ProviderAvailable: true,
		AccountType: accountType, CredentialVendor: providerCode, CredentialAuthMode: authMode,
		ServingCredentialCandidates: 1,
	}
}

func observationTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func observationNumeric(value float64) pgtype.Numeric {
	var out pgtype.Numeric
	if err := out.Scan(strconv.FormatFloat(value, 'f', -1, 64)); err != nil {
		panic(err)
	}
	return out
}
