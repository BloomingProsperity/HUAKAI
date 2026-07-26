package userkeyhttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/userkey"
)

const maxBatchRevokeIDs = 200

type batchRevokeRequest struct {
	IDs    []int64 `json:"ids"`
	Reason string  `json:"reason"`
}

type batchRevokeItem struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
	Retryable bool   `json:"retryable"`
}

type batchRevokeResponse struct {
	Outcome     string            `json:"outcome"`
	Revoked     []int64           `json:"revoked"`
	NotFound    []int64           `json:"not_found"`
	Failed      []int64           `json:"failed"`
	NotExecuted []int64           `json:"not_executed"`
	Results     []batchRevokeItem `json:"results"`
}

// newBatchRevokeHandler 逐条复用 owner 作用域和幂等的单 Key 撤销事务，并返回有序结果。
// 某条普通存储错误不会遮蔽此前成功项，也不会阻止后续独立 Key 继续处理。
func newBatchRevokeHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveSession(w, r, d)
		if !ok {
			return
		}
		var req batchRevokeRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		if code := validateBatchRevokeIDs(req.IDs); code != "" {
			writeError(w, http.StatusBadRequest, code, "ids must contain 1 to 200 unique positive integers")
			return
		}

		response := batchRevokeResponse{
			Revoked: make([]int64, 0, len(req.IDs)), NotFound: make([]int64, 0),
			Failed: make([]int64, 0), NotExecuted: make([]int64, 0),
			Results: make([]batchRevokeItem, 0, len(req.IDs)),
		}
		reqID := requestIDFromReq(r)
		for index, id := range req.IDs {
			result, err := d.Service.Revoke(r.Context(), userkey.RevokeRequest{
				TenantID: ident.TenantID, UserID: ident.UserID, APIKeyID: id,
				Reason: req.Reason, RequestID: reqID,
			})
			if err == nil {
				status := "revoked"
				if result.AlreadyRevoked {
					status = "already_revoked"
				}
				response.Revoked = append(response.Revoked, id)
				response.Results = append(response.Results, batchRevokeItem{
					ID: id, Status: status, Retryable: false,
				})
				continue
			}
			if errors.Is(err, userkey.ErrNotFound) {
				response.NotFound = append(response.NotFound, id)
				response.Results = append(response.Results, batchRevokeItem{
					ID: id, Status: "not_found", ErrorCode: "api_key_not_found", Retryable: false,
				})
				continue
			}

			code, retryable, stop := classifyBatchRevokeError(err)
			response.Failed = append(response.Failed, id)
			response.Results = append(response.Results, batchRevokeItem{
				ID: id, Status: "failed", ErrorCode: code, Retryable: retryable,
			})
			if stop {
				appendBatchRevokeNotExecuted(&response, req.IDs[index+1:], code, retryable)
				break
			}
		}
		response.Outcome = batchRevokeOutcome(response)
		writeJSON(w, http.StatusOK, response)
	}
}

func validateBatchRevokeIDs(ids []int64) string {
	if len(ids) == 0 || len(ids) > maxBatchRevokeIDs {
		return "invalid_ids"
	}
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return "invalid_ids"
		}
		if _, exists := seen[id]; exists {
			return "duplicate_ids"
		}
		seen[id] = struct{}{}
	}
	return ""
}

func classifyBatchRevokeError(err error) (code string, retryable, stop bool) {
	switch {
	case errors.Is(err, context.Canceled):
		return "request_cancelled", true, true
	case errors.Is(err, context.DeadlineExceeded):
		return "request_timeout", true, true
	case errors.Is(err, userkey.ErrServiceMisconfig):
		return "userkey_service_unavailable", true, true
	default:
		return "userkey_backend_error", true, false
	}
}

func appendBatchRevokeNotExecuted(response *batchRevokeResponse, ids []int64, code string, retryable bool) {
	for _, id := range ids {
		response.NotExecuted = append(response.NotExecuted, id)
		response.Results = append(response.Results, batchRevokeItem{
			ID: id, Status: "not_executed", ErrorCode: code, Retryable: retryable,
		})
	}
}

func batchRevokeOutcome(response batchRevokeResponse) string {
	successes := len(response.Revoked)
	incomplete := len(response.NotFound) + len(response.Failed) + len(response.NotExecuted)
	switch {
	case successes > 0 && incomplete == 0:
		return "success"
	case successes > 0:
		return "partial"
	default:
		return "failed"
	}
}
