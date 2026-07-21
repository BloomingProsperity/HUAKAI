package projectenrich

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

func Finalize(
	ctx context.Context,
	enricher Enricher,
	sessions *credentialacq.PostgresSessionStore,
	credentials credentialacq.CredentialCreator,
	audit credentialacq.CredentialAuditWriter,
	session credentialacq.Session,
	candidate credentialacq.CredentialCandidate,
	actorID string,
	requestID string,
) (credentialacq.FinalizeResult, error) {
	vendor := firstNonEmpty(candidate.Vendor, session.Vendor)
	authMode := firstNonEmpty(candidate.AuthMode, session.AuthMode)
	profile := ProfileForMode(vendor, authMode)
	if enricher != nil && profile != "" {
		enriched, err := enricher.Enrich(ctx, profile, candidate.Payload)
		if len(enriched.Payload) > 0 {
			candidate.Payload = enriched.Payload
		}
		if enriched.SubscriptionVerified {
			candidate.Subscription = subscriptionprofile.FromRaw(
				subscriptionVendor(profile),
				enriched.SubscriptionTierRaw,
				subscriptionprofile.SourceProviderAPI,
				subscriptionprofile.TrustVerifiedAPI,
				subscriptionprofile.VerificationVerified,
				candidate.ExternalSubjectID,
				candidate.ExternalAccountID,
			)
		}
		if enriched.SubscriptionConflict {
			candidate.Subscription = subscriptionprofile.FromRaw(
				subscriptionVendor(profile),
				"",
				subscriptionprofile.SourceProviderAPI,
				subscriptionprofile.TrustVerifiedAPI,
				subscriptionprofile.VerificationVerified,
				candidate.ExternalSubjectID,
				candidate.ExternalAccountID,
			)
			candidate.Subscription.Status = subscriptionprofile.StatusConflict
			candidate.Subscription.ErrorClass = "subscription_metadata_conflict"
		}
		if err != nil && !errors.Is(err, ErrSubscriptionMetadataDeferred) {
			return credentialacq.FinalizeResult{}, err
		}
		if err != nil {
			slog.WarnContext(ctx, "Cloud Code 账号元数据补齐失败，凭据按待处理状态继续创建",
				"tenant_id", session.TenantID,
				"provider_account_id", session.ProviderAccountID,
				"flow_id", session.ID,
				"request_id", requestID,
				"error", err,
			)
		}
	}

	finalizer := credentialacq.NewFinalizer(sessions, credentialstore.DefaultHandlerRegistry(), credentials, audit)
	return finalizer.Finalize(ctx, session.ID, candidate, actorID, requestID)
}

// ProfileForMode 把凭据身份映射到项目初始化合同，供应商与认证模式缺一不可。
func ProfileForMode(vendor, authMode string) string {
	vendor = credentialstore.Normalize(vendor)
	authMode = credentialstore.Normalize(authMode)
	switch {
	case vendor == credentialstore.VendorAntigravity && authMode == credentialstore.AuthModeOAuth:
		return ProfileAntigravity
	case vendor == credentialstore.VendorGemini && authMode == credentialstore.AuthModeAntigravity:
		return ProfileAntigravity
	case vendor == credentialstore.VendorGemini && authMode == credentialstore.AuthModeCodeAssist:
		return ProfileGeminiCodeAssist
	default:
		return ""
	}
}

func subscriptionVendor(profile string) string {
	if profile == ProfileGeminiCodeAssist {
		return subscriptionprofile.VendorGemini
	}
	return subscriptionprofile.VendorAntigravity
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
