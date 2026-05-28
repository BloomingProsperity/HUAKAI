package trust

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const (
	HeaderUpstreamProvider       = "X-Huakai-Upstream-Provider"
	HeaderUpstreamModel          = "X-Huakai-Upstream-Model"
	HeaderStatus                 = "X-Huakai-Trust-Status"
	HeaderRequestID              = "X-Huakai-Request-Id"
	HeaderTrustSignature         = "X-Huakai-Trust-Signature"
	HeaderTrustPubkeyFingerprint = "X-Huakai-Trust-Pubkey-Fingerprint"
	HeaderTrustSchema            = "X-Huakai-Trust-Schema"
)

type Status string

const (
	StatusVerified   Status = "verified"
	StatusSignedOnly Status = "signed-only"
	StatusUnverified Status = "unverified"
	StatusMissing    Status = "missing"
	StatusMismatch   Status = "mismatch"
)

type ResponseMetadata struct {
	Provider  string
	Model     string
	RequestID string
}

func IsValidStatus(raw string) bool {
	switch Status(raw) {
	case StatusVerified, StatusSignedOnly, StatusUnverified, StatusMissing, StatusMismatch:
		return true
	default:
		return false
	}
}

func UpgradeStatusOnSignature(prev Status, sigPresent bool) Status {
	if sigPresent && prev == StatusUnverified {
		return StatusSignedOnly
	}
	return prev
}

func MetadataFromHCSF(env *proto.HCSF) ResponseMetadata {
	if env == nil {
		return ResponseMetadata{}
	}
	out := ResponseMetadata{
		Provider:  env.RequestMeta.Provider,
		Model:     env.RequestMeta.UpstreamModel,
		RequestID: env.RequestMeta.RequestID,
	}
	out.Provider = providerFromHopChain(env.Accounting.HopChain, out.Provider)
	out.Model = modelFromChain(env.Accounting.ModelChain, out.Model)
	if out.Model == "" && env.BufferedResponse != nil {
		out.Model = env.BufferedResponse.Model
	}
	return out
}

func MetadataFromLedgerEntry(entry auditledger.LedgerEntry) ResponseMetadata {
	return ResponseMetadata{
		Provider:  providerFromHopChain(entry.HopChain, ""),
		Model:     modelFromChain(entry.ModelChain, ""),
		RequestID: entry.RequestID,
	}
}

func WriteResponseHeaders(h http.Header, meta ResponseMetadata, result auditledger.AuditLedgerResult) Status {
	status := ResponseStatus(meta, result)
	if h == nil {
		return status
	}
	h.Set(HeaderUpstreamProvider, meta.Provider)
	h.Set(HeaderUpstreamModel, meta.Model)
	h.Set(HeaderRequestID, meta.RequestID)
	h.Set(HeaderStatus, string(status))
	return status
}

func ResponseStatus(meta ResponseMetadata, result auditledger.AuditLedgerResult) Status {
	if result.State == auditledger.LedgerResultStatePersisted && ledgerMismatch(meta, result) {
		return StatusMismatch
	}
	if meta.Provider == "" || meta.Model == "" || meta.RequestID == "" {
		return StatusMissing
	}
	return StatusUnverified
}

func ledgerMismatch(meta ResponseMetadata, result auditledger.AuditLedgerResult) bool {
	if result.UpstreamProvider != "" && result.UpstreamProvider != meta.Provider {
		return true
	}
	if result.UpstreamModel != "" && result.UpstreamModel != meta.Model {
		return true
	}
	if result.RequestID != "" && result.RequestID != meta.RequestID {
		return true
	}
	return false
}

func providerFromHopChain(hops []proto.HopAttestation, fallback string) string {
	for _, hop := range hops {
		if hop.Hop == proto.HopProvider {
			return hop.Provider
		}
	}
	return fallback
}

func modelFromChain(model *proto.ModelChain, fallback string) string {
	if model == nil {
		return fallback
	}
	if model.RouteDecided != "" {
		return model.RouteDecided
	}
	if model.Requested != "" {
		return model.Requested
	}
	return fallback
}
