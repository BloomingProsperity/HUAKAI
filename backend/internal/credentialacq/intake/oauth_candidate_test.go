package intake

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

func TestOAuthCandidateRoundTripPreservesIdentityAndSubscription(t *testing.T) {
	want := credentialacq.CredentialCandidate{
		Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth,
		Payload:           []byte(`{"access_token":"access","refresh_token":"refresh"}`),
		ExternalAccountID: "account-1", ExternalSubjectID: "subject-1",
		ExternalAccountEmail: "owner@example.com", AccountIDSource: "oauth_token_response",
		Subscription: subscriptionprofile.FromRaw(
			subscriptionprofile.VendorGrok, "supergrok", subscriptionprofile.SourceOAuthResponse,
			subscriptionprofile.TrustIssuerResponse, subscriptionprofile.VerificationIssuerResponse,
			"subject-1", "",
		),
	}
	encoded, err := EncodeOAuthCandidate(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseOAuthCandidate(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Vendor != want.Vendor || got.AuthMode != want.AuthMode ||
		got.ExternalAccountID != want.ExternalAccountID || got.ExternalSubjectID != want.ExternalSubjectID ||
		got.Subscription.Label() != "grok:supergrok" {
		t.Fatalf("还原结果=%+v", got)
	}
}

func TestOAuthCandidateRejectsUnknownOrNonOAuthMode(t *testing.T) {
	tests := []credentialacq.CredentialCandidate{
		{Vendor: "unknown", AuthMode: "oauth", Payload: []byte(`{"access_token":"x"}`)},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey, Payload: []byte(`{"api_key":"x"}`)},
	}
	for _, candidate := range tests {
		if _, err := EncodeOAuthCandidate(candidate); !errors.Is(err, credentialacq.ErrInvalidTokenShape) {
			t.Fatalf("%s/%s err=%v，期望 ErrInvalidTokenShape", candidate.Vendor, candidate.AuthMode, err)
		}
	}
}
