package orphanreconcilehttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/mediatask"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	maxRecoveryReasonBytes   = 512
	maxRecoveryEvidenceBytes = 512
)

type attachSubmissionRequest struct {
	ProviderTaskID string `json:"provider_task_id"`
	Reason         string `json:"reason"`
	Evidence       string `json:"evidence"`
}

type confirmNotAcceptedRequest struct {
	Reason   string `json:"reason"`
	Evidence string `json:"evidence"`
}

type submissionRecoveryResponse struct {
	OrphanID       int64  `json:"orphan_id"`
	TaskID         int64  `json:"task_id"`
	TaskStatus     string `json:"task_status"`
	OrphanStatus   string `json:"orphan_status"`
	ProviderTaskID string `json:"provider_task_id,omitempty"`
	Advanced       bool   `json:"advanced"`
}

func newAttachHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, orphanID, ok := resolveMutationIdentity(w, r, d)
		if !ok {
			return
		}
		var req attachSubmissionRequest
		if !decodeStrictJSON(w, r, &req) {
			return
		}
		req.ProviderTaskID = strings.TrimSpace(req.ProviderTaskID)
		req.Reason = strings.TrimSpace(req.Reason)
		req.Evidence = strings.TrimSpace(req.Evidence)
		if req.ProviderTaskID == "" || !validRecoveryText(req.Reason, maxRecoveryReasonBytes) ||
			!validRecoveryText(req.Evidence, maxRecoveryEvidenceBytes) {
			writeError(w, http.StatusBadRequest, "invalid_submission_recovery", "provider_task_id, reason and evidence are required")
			return
		}
		result, advanced, err := d.Store.AttachUnknownSubmission(
			r.Context(), orphanID, req.ProviderTaskID, nowUTC(),
			buildSubmissionRecoveryAccess(ident, d.PlatformTenantID),
			buildSubmissionRecoveryAudit(
				ident, "orphan_provider_task_attached",
				req.Reason, req.Evidence, middleware.GetReqID(r.Context()),
			),
		)
		if err != nil {
			writeSubmissionRecoveryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, submissionRecoveryResponse{
			OrphanID: result.OrphanID, TaskID: result.TaskID,
			TaskStatus: string(result.TaskStatus), OrphanStatus: result.OrphanStatus,
			ProviderTaskID: result.ProviderTaskID, Advanced: advanced,
		})
	}
}

func newConfirmNotAcceptedHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, orphanID, ok := resolveMutationIdentity(w, r, d)
		if !ok {
			return
		}
		var req confirmNotAcceptedRequest
		if !decodeStrictJSON(w, r, &req) {
			return
		}
		req.Reason = strings.TrimSpace(req.Reason)
		req.Evidence = strings.TrimSpace(req.Evidence)
		if !validRecoveryText(req.Reason, maxRecoveryReasonBytes) ||
			!validRecoveryText(req.Evidence, maxRecoveryEvidenceBytes) {
			writeError(w, http.StatusBadRequest, "invalid_submission_recovery", "reason and evidence are required")
			return
		}
		result, advanced, err := d.Store.RequestUnknownSubmissionRelease(
			r.Context(), orphanID, nowUTC(),
			buildSubmissionRecoveryAccess(ident, d.PlatformTenantID),
			buildSubmissionRecoveryAudit(
				ident, "orphan_release_requested",
				req.Reason, req.Evidence, middleware.GetReqID(r.Context()),
			),
		)
		if err != nil {
			writeSubmissionRecoveryError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, submissionRecoveryResponse{
			OrphanID: result.OrphanID, TaskID: result.TaskID,
			TaskStatus: string(result.TaskStatus), OrphanStatus: result.OrphanStatus,
			Advanced: advanced,
		})
	}
}

func resolveMutationIdentity(w http.ResponseWriter, r *http.Request, d Deps) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || d.Store == nil {
		writeError(w, http.StatusServiceUnavailable, "orphan_not_configured", "orphan reconcile dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	if ident.Role != admin.RolePlatformAdmin && ident.Role != admin.RoleTenantOperator {
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
	orphanID, ok := pathID(w, r)
	return ident, orphanID, ok
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxReconcileBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON value")
		return false
	}
	return true
}

func validRecoveryText(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || privacy.ContainsForbiddenRawData([]byte(value)) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return false
		}
	}
	return true
}

func buildSubmissionRecoveryAccess(
	ident admin.AdminIdentity,
	platformTenantID int64,
) mediatask.SubmissionRecoveryAccessHook {
	return func(_ context.Context, result mediatask.SubmissionRecoveryResult) error {
		return authorizeOrphanMutation(ident, platformTenantID, result.TenantID)
	}
}

func buildSubmissionRecoveryAudit(
	ident admin.AdminIdentity,
	action string,
	reason string,
	evidence string,
	requestID string,
) mediatask.SubmissionRecoveryAuditHook {
	return func(ctx context.Context, tx pgx.Tx, result mediatask.SubmissionRecoveryResult) error {
		payload, _ := json.Marshal(map[string]any{
			"task_id": result.TaskID, "user_id": result.UserID,
			"provider": result.Provider, "provider_task_id": result.ProviderTaskID,
			"estimated_cents": result.EstimatedCents, "evidence": evidence,
			"task_status": result.TaskStatus, "orphan_status": result.OrphanStatus,
		})
		tenantID := result.TenantID
		targetID := result.OrphanID
		var requestIDPtr *string
		if requestID != "" {
			requestIDPtr = &requestID
		}
		_, err := admindb.New(tx).InsertAdminAuditEvent(ctx, admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: ident.AuditActor(), ActorRole: auditActorRole(ident),
			Action: action, TargetType: auditTargetType, TargetID: &targetID,
			RequestID: requestIDPtr, Reason: &reason, Payload: payload,
		})
		return err
	}
}

func writeSubmissionRecoveryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, admin.ErrAdminForbidden):
		writeError(w, http.StatusForbidden, "admin_forbidden_scope", "orphan is not in operator tenant scope")
	case errors.Is(err, admin.ErrAdminBackend):
		writeError(w, http.StatusServiceUnavailable, "orphan_scope_unavailable", "platform tenant scope is not configured")
	case errors.Is(err, mediatask.ErrNotFound):
		writeError(w, http.StatusNotFound, "orphan_not_found", "media task orphan not found")
	case errors.Is(err, mediatask.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_provider_task_id", "provider_task_id is invalid")
	case errors.Is(err, mediatask.ErrProviderTaskIDConflict):
		writeError(w, http.StatusConflict, "provider_task_id_conflict", "provider_task_id already belongs to another task")
	case errors.Is(err, mediatask.ErrSubmissionClaimClosed):
		writeError(w, http.StatusConflict, "submission_claim_closed", "billing claim is no longer reserving")
	case errors.Is(err, mediatask.ErrSubmissionNotUnknown):
		writeError(w, http.StatusConflict, "submission_not_unknown", "submission is not awaiting this recovery action")
	default:
		writeError(w, http.StatusServiceUnavailable, "orphan_backend_error", "orphan reconcile backend unavailable")
	}
}
