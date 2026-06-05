// HUAKAI · iKun

package paymenthttp

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

var (
	ErrRefundRequestNotFound     = errors.New("paymenthttp: refund request not found")
	ErrRefundRequestInvalidInput = errors.New("paymenthttp: invalid refund request input")
	ErrRefundRequestUnavailable  = errors.New("paymenthttp: refund request recorder unavailable")
)

type refundRequestMoneyService interface {
	GetOrder(context.Context, int64, int64) (payment.Order, error)
	RefundOrder(context.Context, payment.RefundOrderInput) (payment.RefundResult, error)
}

type refundRequestDecisionRequest struct {
	TenantID int64  `json:"tenant_id"`
	Reason   string `json:"reason,omitempty"`
}

func newAdminListRefundRequestsHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		recorder := d.RefundRequests
		if recorder == nil {
			writeRefundRequestError(w, ErrRefundRequestUnavailable)
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		requests, err := recorder.ListPendingRefundRequests(r.Context(), tenantID)
		if err != nil {
			writeRefundRequestError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"refund_requests": toRefundRequestViews(requests)})
	}
}

func newAdminApproveRefundRequestHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		recorder := d.RefundRequests
		if recorder == nil {
			writeRefundRequestError(w, ErrRefundRequestUnavailable)
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req refundRequestDecisionRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		rr, err := recorder.ApproveRefundRequest(r.Context(), req.TenantID, id, ident.TokenID)
		if err != nil {
			writeRefundRequestError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"refund_request": toRefundRequestView(rr)})
	}
}

func newAdminRejectRefundRequestHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := resolveAdmin(w, r, d)
		if !ok {
			return
		}
		recorder := d.RefundRequests
		if recorder == nil {
			writeRefundRequestError(w, ErrRefundRequestUnavailable)
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req refundRequestDecisionRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		rr, err := recorder.RejectRefundRequest(r.Context(), req.TenantID, id, req.Reason, ident.TokenID)
		if err != nil {
			writeRefundRequestError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"refund_request": toRefundRequestView(rr)})
	}
}

func (m *memoryRefundRequestRecorder) ListPendingRefundRequests(_ context.Context, tenantID int64) ([]RefundRequest, error) {
	if tenantID <= 0 {
		return nil, ErrRefundRequestInvalidInput
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RefundRequest, 0)
	for _, req := range m.byID {
		if req.TenantID == tenantID && req.Status == RefundRequestPending {
			out = append(out, req)
		}
	}
	sortRefundRequests(out)
	return out, nil
}

func (m *memoryRefundRequestRecorder) ApproveRefundRequest(ctx context.Context, tenantID, requestID, adminActorID int64) (RefundRequest, error) {
	if tenantID <= 0 || requestID <= 0 || adminActorID <= 0 {
		return RefundRequest{}, ErrRefundRequestInvalidInput
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	req, ok := m.byID[requestID]
	if !ok || req.TenantID != tenantID {
		return RefundRequest{}, ErrRefundRequestNotFound
	}
	if req.Status != RefundRequestPending {
		return req, nil
	}
	if m.refund == nil {
		return RefundRequest{}, ErrRefundRequestUnavailable
	}
	order, err := m.refund.GetOrder(ctx, tenantID, req.OrderID)
	if err != nil {
		return RefundRequest{}, err
	}
	if _, err := m.refund.RefundOrder(ctx, payment.RefundOrderInput{
		TenantID:       tenantID,
		OrderID:        req.OrderID,
		AmountCents:    order.AmountCents,
		IdempotencyKey: refundRequestIdempotencyKey(req.ID),
		Reason:         req.Reason,
		ActorKind:      payment.ActorKindAdmin,
		ActorID:        adminActorID,
	}); err != nil {
		return RefundRequest{}, err
	}
	now := time.Now().UTC()
	req.Status = RefundRequestApproved
	req.DecidedAt = &now
	req.DecidedBy = adminActorID
	m.saveLocked(req)
	return req, nil
}

func (m *memoryRefundRequestRecorder) RejectRefundRequest(_ context.Context, tenantID, requestID int64, reason string, adminActorID int64) (RefundRequest, error) {
	if tenantID <= 0 || requestID <= 0 || adminActorID <= 0 {
		return RefundRequest{}, ErrRefundRequestInvalidInput
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	req, ok := m.byID[requestID]
	if !ok || req.TenantID != tenantID {
		return RefundRequest{}, ErrRefundRequestNotFound
	}
	if req.Status != RefundRequestPending {
		return req, nil
	}
	now := time.Now().UTC()
	req.Status = RefundRequestRejected
	req.DecidedAt = &now
	req.DecidedBy = adminActorID
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		req.Reason = trimmed
	}
	m.saveLocked(req)
	return req, nil
}

func (m *memoryRefundRequestRecorder) saveLocked(req RefundRequest) {
	m.byID[req.ID] = req
	m.byKey[refundRequestKey{tenantID: req.TenantID, orderID: req.OrderID}] = req
}

func toRefundRequestViews(requests []RefundRequest) []refundRequestView {
	out := make([]refundRequestView, 0, len(requests))
	for _, req := range requests {
		out = append(out, toRefundRequestView(req))
	}
	return out
}

func sortRefundRequests(requests []RefundRequest) {
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].CreatedAt.Equal(requests[j].CreatedAt) {
			return requests[i].ID < requests[j].ID
		}
		return requests[i].CreatedAt.Before(requests[j].CreatedAt)
	})
}

func refundRequestIdempotencyKey(requestID int64) string {
	return "refund-req:" + strconv.FormatInt(requestID, 10)
}

func writeRefundRequestError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRefundRequestInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_refund_request", "refund request input is invalid")
	case errors.Is(err, ErrRefundRequestNotFound):
		writeJSONError(w, http.StatusNotFound, "refund_request_not_found", "refund request not found")
	case errors.Is(err, ErrRefundRequestUnavailable):
		writeJSONError(w, http.StatusServiceUnavailable, "refund_requests_unavailable", "refund request backend unavailable")
	default:
		writePaymentError(w, err)
	}
}
