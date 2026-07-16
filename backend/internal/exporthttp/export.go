package exporthttp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/payment"
)

const (
	defaultMaxRows    = 100000
	defaultFlushEvery = 1000
	maxExportWindow   = 366 * 24 * time.Hour
)

var (
	paymentCSVHeader = []string{"order_id", "user_id", "provider", "status", "amount", "currency", "created_at", "out_trade_no", "order_kind"}
	usageCSVHeader   = []string{"request_id", "user_id", "model", "tokens_input", "tokens_output", "cost_usd", "created_at"}
)

type Auth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

type PaymentExporter interface {
	ExportOrders(context.Context, payment.OrderExportFilter) ([]payment.Order, error)
}

type UsageExporter interface {
	ListUsageRecords(context.Context, dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error)
}

type Deps struct {
	Auth     Auth
	Payments PaymentExporter
	Usage    UsageExporter
	Orders   OrdersExporterDep  // 供 /v1/admin/orders/export.csv 使用
	Refunds  RefundsExporterDep // 供 /v1/admin/refunds/export.csv 使用

	MaxRows    int
	FlushEvery int
}

type exportRange struct {
	from time.Time
	to   time.Time
}

type Range struct {
	From time.Time
	To   time.Time
}

func MountRoutes(r chi.Router, d Deps) {
	r.Get("/v1/admin/payments/export.csv", NewPaymentsExportHandler(d))
	r.Get("/v1/admin/usage/export.csv", NewUsageExportHandler(d))
	// OPS-005：订单与退款的 CSV 导出（只读，无计费副作用）
	r.Get("/v1/admin/orders/export.csv", NewOrdersExportHandler(d))
	r.Get("/v1/admin/refunds/export.csv", NewRefundsExportHandler(d))
}

func NewPaymentsExportHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenantScope(w, r, d.Auth)
		if !ok {
			return
		}
		if d.Payments == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "payment export dependency unset")
			return
		}
		window, ok := parseExportRange(w, r)
		if !ok {
			return
		}
		maxRows := d.maxRows()
		status := payment.OrderStatus(strings.TrimSpace(r.URL.Query().Get("status")))
		rows, err := d.Payments.ExportOrders(r.Context(), payment.OrderExportFilter{
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
			slog.WarnContext(r.Context(), "payment export truncated",
				slog.Int64("tenant_id", tenantID),
				slog.Int("row_limit", maxRows),
				slog.Int("returned_rows", len(rows)),
			)
			rows = rows[:maxRows]
		}
		setCSVHeaders(w, "payments-export.csv", truncated)
		writer := csv.NewWriter(w)
		if !writeCSVRecord(w, writer, paymentCSVHeader) {
			return
		}
		flushEvery := d.flushEvery()
		written := 0
		for _, order := range rows {
			if order.TenantID != tenantID {
				continue
			}
			written++
			if !writeCSVRecord(w, writer, paymentOrderRecord(order)) {
				return
			}
			flushCSVPeriodically(w, writer, written, flushEvery)
		}
		if truncated {
			if !writeCSVRecord(w, writer, truncationNotice(len(paymentCSVHeader), maxRows)) {
				return
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			slog.WarnContext(r.Context(), "payment export csv write failed", slog.String("error", err.Error()))
		}
	}
}

func NewUsageExportHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tenantID, ok := resolveTenantScope(w, r, d.Auth)
		if !ok {
			return
		}
		if d.Usage == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "usage export dependency unset")
			return
		}
		window, ok := parseExportRange(w, r)
		if !ok {
			return
		}
		maxRows := d.maxRows()
		rows, err := d.Usage.ListUsageRecords(r.Context(), dbbilling.ListUsageRecordsParams{
			TenantID:  &tenantID,
			FromTs:    tsParam(window.from),
			ToTs:      tsParam(window.to),
			PageLimit: int32(maxRows + 1),
		})
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "usage_export_failed", "usage export query failed")
			return
		}
		truncated := len(rows) > maxRows
		if truncated {
			slog.WarnContext(r.Context(), "usage export truncated",
				slog.Int64("tenant_id", tenantID),
				slog.Int("row_limit", maxRows),
				slog.Int("returned_rows", len(rows)),
			)
			rows = rows[:maxRows]
		}
		setCSVHeaders(w, "usage-export.csv", truncated)
		writer := csv.NewWriter(w)
		if !writeCSVRecord(w, writer, usageCSVHeader) {
			return
		}
		flushEvery := d.flushEvery()
		written := 0
		for _, row := range rows {
			if row.TenantID != tenantID {
				continue
			}
			written++
			if !writeCSVRecord(w, writer, usageRecord(row)) {
				return
			}
			flushCSVPeriodically(w, writer, written, flushEvery)
		}
		if truncated {
			if !writeCSVRecord(w, writer, truncationNotice(len(usageCSVHeader), maxRows)) {
				return
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			slog.WarnContext(r.Context(), "usage export csv write failed", slog.String("error", err.Error()))
		}
	}
}

func (d Deps) maxRows() int {
	if d.MaxRows > 0 && d.MaxRows < defaultMaxRows {
		return d.MaxRows
	}
	return defaultMaxRows
}

func (d Deps) flushEvery() int {
	if d.FlushEvery > 0 {
		return d.FlushEvery
	}
	return defaultFlushEvery
}

func resolveTenantScope(w http.ResponseWriter, r *http.Request, auth Auth) (int64, bool) {
	if auth == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "admin auth resolver unset")
		return 0, false
	}
	ident, err := auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return 0, false
	}
	if ident.Role != admin.RoleTenantOperator && ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return 0, false
	}
	if ident.ScopeTenantID <= 0 {
		writeJSONError(w, http.StatusBadRequest, "tenant_scope_required", "tenant-scoped admin credential required for CSV export")
		return 0, false
	}
	return ident.ScopeTenantID, true
}

func parseExportRange(w http.ResponseWriter, r *http.Request) (exportRange, bool) {
	from, ok := parseRequiredTime(w, r.URL.Query().Get("from"), "from")
	if !ok {
		return exportRange{}, false
	}
	to, ok := parseRequiredTime(w, r.URL.Query().Get("to"), "to")
	if !ok {
		return exportRange{}, false
	}
	if from.After(to) {
		writeJSONError(w, http.StatusBadRequest, "invalid_date_range", "from must be before or equal to to")
		return exportRange{}, false
	}
	if to.Sub(from) > maxExportWindow {
		writeJSONError(w, http.StatusBadRequest, "date_range_too_large", "export date range must be 366 days or less")
		return exportRange{}, false
	}
	return exportRange{from: from, to: to}, true
}

func ParseExportRange(w http.ResponseWriter, r *http.Request) (Range, bool) {
	window, ok := parseExportRange(w, r)
	if !ok {
		return Range{}, false
	}
	return Range{From: window.from, To: window.to}, true
}

func parseRequiredTime(w http.ResponseWriter, raw, name string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		writeJSONError(w, http.StatusBadRequest, name+"_required", name+" query parameter is required")
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, name+"_invalid", name+" query parameter must be RFC3339")
		return time.Time{}, false
	}
	return t.UTC(), true
}

func setCSVHeaders(w http.ResponseWriter, filename string, truncated bool) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if truncated {
		w.Header().Set("X-Truncated", "true")
	}
}

func SetCSVHeaders(w http.ResponseWriter, filename string, truncated bool) {
	setCSVHeaders(w, filename, truncated)
}

func writeCSVRecord(w http.ResponseWriter, writer *csv.Writer, record []string) bool {
	if err := writer.Write(safeCSVRecord(record)); err != nil {
		http.Error(w, "csv write failed", http.StatusInternalServerError)
		return false
	}
	return true
}

func safeCSVRecord(record []string) []string {
	out := make([]string, len(record))
	for i, cell := range record {
		out[i] = SafeCSVCell(cell)
	}
	return out
}

func SafeCSVCell(cell string) string {
	if cell == "" {
		return ""
	}
	switch cell[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + cell
	default:
		return cell
	}
}

func flushCSVPeriodically(w http.ResponseWriter, writer *csv.Writer, written, every int) {
	if every <= 0 || written%every != 0 {
		return
	}
	writer.Flush()
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func FlushCSVPeriodically(w http.ResponseWriter, writer *csv.Writer, written, every int) {
	flushCSVPeriodically(w, writer, written, every)
}

func paymentOrderRecord(order payment.Order) []string {
	return []string{
		strconv.FormatInt(order.ID, 10),
		strconv.FormatInt(order.UserID, 10),
		string(order.ProviderKind),
		string(order.Status),
		centsToAmount(order.AmountCents),
		strings.TrimSpace(order.CurrencyCode),
		formatTime(order.CreatedAt),
		order.OutTradeNo,
		order.OrderKind,
	}
}

func usageRecord(row dbbilling.ListUsageRecordsRow) []string {
	return []string{
		strings.TrimSpace(row.RequestID),
		strconv.FormatInt(row.UserID, 10),
		strings.TrimSpace(row.RequestedModel),
		strconv.FormatInt(int64(row.TokensInput), 10),
		strconv.FormatInt(int64(row.TokensOutput), 10),
		row.ActualCost.StringFixed(8),
		formatTimestamptz(row.CreatedAt),
	}
}

func truncationNotice(width, maxRows int) []string {
	record := make([]string, width)
	record[0] = "# truncated"
	if width > 1 {
		record[1] = "row_limit"
	}
	if width > 2 {
		record[2] = strconv.Itoa(maxRows)
	}
	return record
}

func TruncationNotice(width, maxRows int) []string {
	return truncationNotice(width, maxRows)
}

func centsToAmount(cents int64) string {
	return decimal.NewFromInt(cents).Div(decimal.NewFromInt(100)).StringFixed(2)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatTimestamptz(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return formatTime(ts.Time)
}

func tsParam(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func writePaymentExportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, payment.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_export_request", "payment export request is invalid")
	case errors.Is(err, payment.ErrStoreNotConfigured):
		writeJSONError(w, http.StatusServiceUnavailable, "payment_backend_unavailable", "payment backend unavailable")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "payment_export_failed", "payment export query failed")
	}
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
