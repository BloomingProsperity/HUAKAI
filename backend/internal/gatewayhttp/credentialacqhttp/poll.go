package credentialacqhttp

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/projectenrich"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func newCredentialAcqPollHandler(deps AdminCredentialAcquisitionDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveCredentialAcqAdmin(w, r, deps)
		if !ok {
			return
		}
		flowID := chi.URLParam(r, "flowID")
		session, err := deps.Sessions.Get(r.Context(), flowID)
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		if !credentialAcqFlowMatchesPathAccount(w, r, session) {
			return
		}
		if !credentialacq.ModeAcquisitionReleased(session.Vendor, session.AuthMode) {
			writeCredentialAcqSealedAdminAudit(r, deps, ident.AuditActor(), ident.Role, session, "poll")
			writeCredentialAcqError(w, credentialacq.ErrFeatureDisabled)
			return
		}
		if session.Status == credentialacq.StatusFinalized && session.ResultAccountCredentialID > 0 {
			writeAuditJSON(w, http.StatusOK, credentialacq.FinalizeResult{Session: session, Credential: credentialstore.CredentialMetadata{ID: session.ResultAccountCredentialID}, AlreadyFinalized: true})
			return
		}
		actorID, requestID := ident.AuditActor(), middleware.GetReqID(r.Context())
		candidate, polled, err := credentialacq.PollDeviceCodeFlow(r.Context(), deps.Sessions, session, deps.DeviceCodePoller, deps.CredentialAudit, actorID, requestID)
		if errors.Is(err, credentialacq.ErrDevicePollPending) {
			retry := int((credentialacq.DevicePollRetryAfter(err) + time.Second - 1) / time.Second)
			writeAuditJSON(w, http.StatusAccepted, map[string]any{"flow": polled, "retry_after_seconds": retry})
			return
		}
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		result, err := projectenrich.Finalize(r.Context(), deps.ProjectEnricher, deps.Sessions, deps.Credentials, deps.CredentialAudit, polled, candidate, actorID, requestID)
		if err != nil {
			writeCredentialAcqError(w, err)
			return
		}
		writeCredentialAcqAdminAudit(r, deps, actorID, ident.Role, result.Session, credentialacq.EventCompleted, "完成设备授权 credential acquisition")
		writeAuditJSON(w, http.StatusOK, result)
	}
}
