package exporthttp

import (
	"context"
	"encoding/csv"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

// ordersCSVHeader —— 排除所有敏感/密钥/PII 列（CMB-5）。
var ordersCSVHeader = []string{
	"order_id", "user_id", "status", "provider", "order_kind",
	"amount", "currency", "out_trade_no", "created_at",
}

// refundsCSVHeader —— 不含密钥，除 user_id/order_id 外不含 PII。
var refundsCSVHeader = []string{
	"refund_id", "order_id", "user_id",
	"amount", "currency", "reason", "actor_kind", "created_at",
}

// OrdersExporterDep 是只读的订单导出数据源。
// 由 *payment.Service 实现。
type OrdersExporterDep interface {
	ExportOrders(context.Context, payment.OrderExportFilter) ([]payment.Order, error)
}

// RefundsExporterDep 是只读的退款导出数据源。
// 由 *payment.Service 实现。
type RefundsExporterDep interface {
	ExportRefunds(context.Context, payment.RefundExportFilter) ([]payment.RefundRecord, error)
}

// NewOrdersExportHandler returns GET /v1/admin/orders/export.csv.
// Read-only; no billing side effects. Excludes sensitive columns (CMB-5).
func NewOrdersExportHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenantScope(w, r, d.Auth)
		if !ok {
			return
		}
		if d.Orders == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "orders export dependency unset")
			return
		}
		window, ok := parseExportRange(w, r)
		if !ok {
			return
		}
		maxRows := d.maxRows()
		status := payment.OrderStatus(strings.TrimSpace(r.URL.Query().Get("status")))
		rows, err := d.Orders.ExportOrders(r.Context(), payment.OrderExportFilter{
			TenantID: tenantID,
			Status:   status,
			From:     &window.from,
			To:       &window.to,
			Limit:    maxRows + 1,
		})
		if err != nil {
			writePaymentExportError(w, err)
			return
		}
		truncated := len(rows) > maxRows
		if truncated {
			slog.WarnContext(r.Context(), "orders export truncated",
				slog.Int64("tenant_id", tenantID),
				slog.Int("row_limit", maxRows),
			)
			rows = rows[:maxRows]
		}
		setCSVHeaders(w, "orders-export.csv", truncated)
		writer := csv.NewWriter(w)
		if !writeCSVRecord(w, writer, ordersCSVHeader) {
			return
		}
		flushEvery := d.flushEvery()
		written := 0
		for _, o := range rows {
			if o.TenantID != tenantID {
				continue
			}
			written++
			if !writeCSVRecord(w, writer, orderExportRecord(o)) {
				return
			}
			flushCSVPeriodically(w, writer, written, flushEvery)
		}
		if truncated {
			if !writeCSVRecord(w, writer, truncationNotice(len(ordersCSVHeader), maxRows)) {
				return
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			slog.WarnContext(r.Context(), "orders export csv write failed", slog.String("error", err.Error()))
		}
	}
}

// NewRefundsExportHandler returns GET /v1/admin/refunds/export.csv.
// Read-only; no billing side effects. Excludes sensitive columns (CMB-5).
func NewRefundsExportHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenantScope(w, r, d.Auth)
		if !ok {
			return
		}
		if d.Refunds == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "refunds export dependency unset")
			return
		}
		window, ok := parseExportRange(w, r)
		if !ok {
			return
		}
		maxRows := d.maxRows()
		rows, err := d.Refunds.ExportRefunds(r.Context(), payment.RefundExportFilter{
			TenantID: tenantID,
			From:     &window.from,
			To:       &window.to,
			Limit:    maxRows + 1,
		})
		if err != nil {
			writePaymentExportError(w, err)
			return
		}
		truncated := len(rows) > maxRows
		if truncated {
			slog.WarnContext(r.Context(), "refunds export truncated",
				slog.Int64("tenant_id", tenantID),
				slog.Int("row_limit", maxRows),
			)
			rows = rows[:maxRows]
		}
		setCSVHeaders(w, "refunds-export.csv", truncated)
		writer := csv.NewWriter(w)
		if !writeCSVRecord(w, writer, refundsCSVHeader) {
			return
		}
		flushEvery := d.flushEvery()
		written := 0
		for _, ref := range rows {
			if ref.TenantID != tenantID {
				continue
			}
			written++
			if !writeCSVRecord(w, writer, refundExportRecord(ref)) {
				return
			}
			flushCSVPeriodically(w, writer, written, flushEvery)
		}
		if truncated {
			if !writeCSVRecord(w, writer, truncationNotice(len(refundsCSVHeader), maxRows)) {
				return
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			slog.WarnContext(r.Context(), "refunds export csv write failed", slog.String("error", err.Error()))
		}
	}
}

// orderExportRecord builds a CSV row for an order.
// Deliberately omits provider_order_ref, request_fingerprint, compliance_*, failure_message (CMB-5).
func orderExportRecord(o payment.Order) []string {
	return []string{
		strconv.FormatInt(o.ID, 10),
		strconv.FormatInt(o.UserID, 10),
		string(o.Status),
		string(o.ProviderKind),
		o.OrderKind,
		centsToAmount(o.AmountCents),
		strings.TrimSpace(o.CurrencyCode),
		SafeCSVCell(o.OutTradeNo),
		formatTime(o.CreatedAt),
	}
}

// refundExportRecord builds a CSV row for a refund. No key/secret columns.
func refundExportRecord(ref payment.RefundRecord) []string {
	return []string{
		strconv.FormatInt(ref.ID, 10),
		strconv.FormatInt(ref.OrderID, 10),
		strconv.FormatInt(ref.UserID, 10),
		centsToAmount(ref.AmountCents),
		strings.TrimSpace(ref.CurrencyCode),
		SafeCSVCell(ref.Reason),
		SafeCSVCell(ref.ActorKind),
		formatTime(ref.CreatedAt),
	}
}
