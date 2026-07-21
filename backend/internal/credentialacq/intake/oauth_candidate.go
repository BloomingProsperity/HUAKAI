package intake

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
)

const oauthCandidateVersion = 1
const maxOAuthCandidateBytes = 2 << 20

type oauthCandidateEnvelope struct {
	Version              int                             `json:"version"`
	Vendor               string                          `json:"vendor"`
	AuthMode             string                          `json:"auth_mode"`
	Payload              []byte                          `json:"payload"`
	ExternalAccountID    string                          `json:"external_account_id,omitempty"`
	ExternalSubjectID    string                          `json:"external_subject_id,omitempty"`
	ExternalAccountEmail string                          `json:"external_account_email,omitempty"`
	AccountIDSource      string                          `json:"account_id_source,omitempty"`
	Subscription         subscriptionprofile.Observation `json:"subscription,omitempty"`
}

// EncodeOAuthCandidate 只序列化已经完成 state、PKCE 和换码验证的候选凭据。
// 返回值必须立即进入短期加密暂存，不得写日志或明文持久化。
func EncodeOAuthCandidate(candidate credentialacq.CredentialCandidate) (string, error) {
	candidate.Vendor = credentialstore.Normalize(candidate.Vendor)
	candidate.AuthMode = credentialstore.Normalize(candidate.AuthMode)
	if len(candidate.Payload) == 0 || len(candidate.Payload) > maxOAuthCandidateBytes ||
		!json.Valid(candidate.Payload) ||
		!credentialacq.ModeAcquisitionReleased(candidate.Vendor, candidate.AuthMode) ||
		!credentialacq.SourceAllowedForMode(candidate.Vendor, candidate.AuthMode, credentialacq.FlowKindOAuth) {
		return "", credentialacq.ErrInvalidTokenShape
	}
	encoded, err := json.Marshal(oauthCandidateEnvelope{
		Version: oauthCandidateVersion, Vendor: candidate.Vendor, AuthMode: candidate.AuthMode,
		Payload:              candidate.Payload,
		ExternalAccountID:    strings.TrimSpace(candidate.ExternalAccountID),
		ExternalSubjectID:    strings.TrimSpace(candidate.ExternalSubjectID),
		ExternalAccountEmail: strings.TrimSpace(candidate.ExternalAccountEmail),
		AccountIDSource:      strings.TrimSpace(candidate.AccountIDSource),
		Subscription:         candidate.Subscription,
	})
	if err != nil || len(encoded) > maxOAuthCandidateBytes {
		return "", credentialacq.ErrInvalidTokenShape
	}
	return string(encoded), nil
}

// ParseOAuthCandidate 只为服务端 OAuth 加密暂存还原候选凭据，
// 不接受终端用户自报的通用 JSON。
func ParseOAuthCandidate(content string) (credentialacq.CredentialCandidate, error) {
	if strings.TrimSpace(content) == "" || len(content) > maxOAuthCandidateBytes {
		return credentialacq.CredentialCandidate{}, credentialacq.ErrInvalidImportBody
	}
	var envelope oauthCandidateEnvelope
	if err := json.Unmarshal([]byte(content), &envelope); err != nil ||
		envelope.Version != oauthCandidateVersion || len(envelope.Payload) == 0 ||
		!json.Valid(envelope.Payload) {
		return credentialacq.CredentialCandidate{}, credentialacq.ErrInvalidImportBody
	}
	vendor := credentialstore.Normalize(envelope.Vendor)
	mode := credentialstore.Normalize(envelope.AuthMode)
	if !credentialacq.ModeAcquisitionReleased(vendor, mode) ||
		!credentialacq.SourceAllowedForMode(vendor, mode, credentialacq.FlowKindOAuth) {
		return credentialacq.CredentialCandidate{}, fmt.Errorf("%w: OAuth 候选凭据模式不允许交互式获取", credentialacq.ErrFeatureDisabled)
	}
	return credentialacq.CredentialCandidate{
		Vendor: vendor, AuthMode: mode, Payload: append([]byte(nil), envelope.Payload...),
		ExternalAccountID:    strings.TrimSpace(envelope.ExternalAccountID),
		ExternalSubjectID:    strings.TrimSpace(envelope.ExternalSubjectID),
		ExternalAccountEmail: strings.TrimSpace(envelope.ExternalAccountEmail),
		AccountIDSource:      strings.TrimSpace(envelope.AccountIDSource),
		Subscription:         envelope.Subscription,
		RedactedContext:      map[string]any{"shape": "oauth_staged_candidate"},
	}, nil
}
