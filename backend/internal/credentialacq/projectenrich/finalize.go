package projectenrich

import (
	"context"
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
	if enricher != nil && credentialstore.Normalize(vendor) == credentialstore.VendorAntigravity {
		enriched, err := enricher.Enrich(ctx, vendor, candidate.Payload)
		if len(enriched.Payload) > 0 {
			candidate.Payload = enriched.Payload
		}
		if enriched.SubscriptionVerified {
			candidate.Subscription = subscriptionprofile.FromRaw(
				subscriptionprofile.VendorAntigravity,
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
				subscriptionprofile.VendorAntigravity,
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
		if err != nil {
			slog.WarnContext(ctx, "Antigravity 账号元数据补齐失败，凭据按待处理状态继续创建",
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
