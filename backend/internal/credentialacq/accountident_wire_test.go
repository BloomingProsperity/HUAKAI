package credentialacq

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
)

func TestAttachIdentityCarriesSubjectIntoCandidate(t *testing.T) {
	candidate := CredentialCandidate{}
	AttachIdentity(&candidate, accountident.Identity{
		AccountID: "workspace-1", SubjectID: "person-7", Email: "person@example.com",
		Source: accountident.SourceChatGPTJWTClaim,
	})
	if candidate.ExternalAccountID != "workspace-1" ||
		candidate.ExternalSubjectID != "person-7" ||
		candidate.ExternalAccountEmail != "person@example.com" ||
		candidate.AccountIDSource != accountident.SourceChatGPTJWTClaim {
		t.Fatalf("candidate=%+v，身份字段未完整贯穿", candidate)
	}
}
