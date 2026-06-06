package meexporthttp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	sessionauth "github.com/BloomingProsperity/HUAKAI/internal/auth"
	dbbilling "github.com/BloomingProsperity/HUAKAI/internal/db/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/exporthttp"
)

const (
	defaultMaxRows       = 100000
	defaultFetchPageSize = 1000
	defaultFlushEvery    = 1000
)

var usageCSVHeader = []string{"request_id", "model", "tokens_input", "tokens_output", "cost_usd", "created_at", "status"}

type UsageStore interface {
	ListUsageRecords(context.Context, dbbilling.ListUsageRecordsParams) ([]dbbilling.ListUsageRecordsRow, error)
}

type Deps struct {
	Store UsageStore

	MaxRows       int
	FetchPageSize int
	FlushEvery    int
}

type usageExportRecord struct {
	RequestID    string `json:"request_id"`
	Model        string `json:"model"`
	TokensInput  int32  `json:"tokens_input"`
	TokensOutput int32  `json:"tokens_output"`
	CostUSD      string `json:"cost_usd"`
	CreatedAt    string `json:"created_at"`
	Status       string `json:"status"`
}

type jsonExportResponse struct {
	Items     []usageExportRecord `json:"items"`
	Truncated bool                `json:"truncated,omitempty"`
	RowLimit  int                 `json:"row_limit,omitempty"`
}

func MountRoutes(r chi.Router, d Deps) {
	r.Get("/usage/export.csv", NewHandler(d))
}

func NewHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, ok := sessionauth.SessionFromContext(r.Context())
		if !ok || ident.TenantID <= 0 || ident.UserID <= 0 {
			writeJSONError(w, http.StatusUnauthorized, "session_token_required", "session bearer token is required")
			return
		}
		if d.Store == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "me usage export dependency unset")
			return
		}
		format, ok := parseFormat(w, r)
		if !ok {
			return
		}
		window, ok := exporthttp.ParseExportRange(w, r)
		if !ok {
			return
		}
		maxRows := d.maxRows()
		rows, truncated, err := collectSelfRows(r.Context(), d.Store, ident, window, maxRows, d.fetchPageSize())
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "usage_export_failed", "usage export query failed")
			return
		}
		if truncated {
			slog.WarnContext(r.Context(), "me usage export truncated",
				slog.Int64("tenant_id", ident.TenantID),
				slog.Int64("user_id", ident.UserID),
				slog.Int("row_limit", maxRows),
			)
		}
		switch format {
		case "json":
			writeJSONExport(w, rows, truncated, maxRows)
		default:
			writeCSVExport(w, rows, truncated, maxRows, d.flushEvery())
		}
	}
}

func collectSelfRows(ctx context.Context, store UsageStore, ident sessionauth.SessionIdentity, window exporthttp.Range, maxRows, pageSize int) ([]dbbilling.ListUsageRecordsRow, bool, error) {
	tenantID := ident.TenantID
	fromTs := tsParam(window.From)
	toTs := tsParam(window.To)
	out := make([]dbbilling.ListUsageRecordsRow, 0)
	var cursorTS pgtype.Timestamptz
	var cursorID int64
	hasCursor := false
	for {
		batch, err := store.ListUsageRecords(ctx, dbbilling.ListUsageRecordsParams{
			TenantID:        &tenantID,
			FromTs:          fromTs,
			ToTs:            toTs,
			HasCursor:       hasCursor,
			CursorCreatedAt: cursorTS,
			CursorID:        cursorID,
			PageLimit:       int32(pageSize),
		})
		if err != nil {
			return nil, false, err
		}
		if len(batch) == 0 {
			return out, false, nil
		}
		for _, row := range batch {
			if row.TenantID != ident.TenantID || row.UserID != ident.UserID {
				continue
			}
			out = append(out, row)
			if len(out) > maxRows {
				return out[:maxRows], true, nil
			}
		}
		if len(batch) < pageSize {
			return out, false, nil
		}
		last := batch[len(batch)-1]
		if !last.CreatedAt.Valid || last.ID <= 0 {
			return out, false, errors.New("usage export cursor row missing settled timestamp or id")
		}
		hasCursor = true
		cursorTS = last.CreatedAt
		cursorID = last.ID
	}
}

func parseFormat(w http.ResponseWriter, r *http.Request) (string, bool) {
	raw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	switch raw {
	case "", "csv":
		return "csv", true
	case "json":
		return "json", true
	default:
		writeJSONError(w, http.StatusBadRequest, "invalid_format", "format must be csv or json")
		return "", false
	}
}

func writeCSVExport(w http.ResponseWriter, rows []dbbilling.ListUsageRecordsRow, truncated bool, maxRows, flushEvery int) {
	exporthttp.SetCSVHeaders(w, "me-usage-export.csv", truncated)
	writer := csv.NewWriter(w)
	if !writeCSVRecord(w, writer, usageCSVHeader) {
		return
	}
	written := 0
	for _, row := range rows {
		written++
		if !writeCSVRecord(w, writer, usageRecord(row)) {
			return
		}
		exporthttp.FlushCSVPeriodically(w, writer, written, flushEvery)
	}
	if truncated {
		if !writeCSVRecord(w, writer, exporthttp.TruncationNotice(len(usageCSVHeader), maxRows)) {
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		slog.Warn("me usage export csv write failed", slog.String("error", err.Error()))
	}
}

func writeJSONExport(w http.ResponseWriter, rows []dbbilling.ListUsageRecordsRow, truncated bool, maxRows int) {
	w.Header().Set("Content-Type", "application/json")
	items := make([]usageExportRecord, 0, len(rows))
	for _, row := range rows {
		items = append(items, usageJSONRecord(row))
	}
	resp := jsonExportResponse{Items: items}
	if truncated {
		resp.Truncated = true
		resp.RowLimit = maxRows
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func writeCSVRecord(w http.ResponseWriter, writer *csv.Writer, record []string) bool {
	out := make([]string, len(record))
	for i, cell := range record {
		out[i] = exporthttp.SafeCSVCell(cell)
	}
	if err := writer.Write(out); err != nil {
		http.Error(w, "csv write failed", http.StatusInternalServerError)
		return false
	}
	return true
}

func usageRecord(row dbbilling.ListUsageRecordsRow) []string {
	return []string{
		strings.TrimSpace(row.RequestID),
		strings.TrimSpace(row.RequestedModel),
		strconv.FormatInt(int64(row.TokensInput), 10),
		strconv.FormatInt(int64(row.TokensOutput), 10),
		row.ActualCost.StringFixed(8),
		formatTimestamptz(row.CreatedAt),
		usageStatus(row),
	}
}

func usageJSONRecord(row dbbilling.ListUsageRecordsRow) usageExportRecord {
	return usageExportRecord{
		RequestID:    strings.TrimSpace(row.RequestID),
		Model:        strings.TrimSpace(row.RequestedModel),
		TokensInput:  row.TokensInput,
		TokensOutput: row.TokensOutput,
		CostUSD:      row.ActualCost.StringFixed(8),
		CreatedAt:    formatTimestamptz(row.CreatedAt),
		Status:       usageStatus(row),
	}
}

func usageStatus(row dbbilling.ListUsageRecordsRow) string {
	if row.PendingReconciliation {
		return "pending_reconciliation"
	}
	return strings.TrimSpace(row.EndClass)
}

func formatTimestamptz(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

func tsParam(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

func (d Deps) maxRows() int {
	if d.MaxRows > 0 && d.MaxRows < defaultMaxRows {
		return d.MaxRows
	}
	return defaultMaxRows
}

func (d Deps) fetchPageSize() int {
	if d.FetchPageSize > 0 {
		return d.FetchPageSize
	}
	return defaultFetchPageSize
}

func (d Deps) flushEvery() int {
	if d.FlushEvery > 0 {
		return d.FlushEvery
	}
	return defaultFlushEvery
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]map[string]string{
		"error": {"code": code, "message": message},
	})
}
