package controlhttp

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type AdminModelAliasesDeps struct {
	Store adminModelAliasesStore
}

type adminModelAliasesStore interface {
	BulkImportModelAliases(context.Context, registry.BulkImportModelAliasesParams) ([]registry.ModelAliasImportResult, error)
	ListModelCapabilityBindings(context.Context, int64) ([]registry.ModelCapabilityBinding, error)
}

type aliasBulkImportResponseBody struct {
	Object  string                            `json:"object"`
	Results []registry.ModelAliasImportResult `json:"results"`
}

type capabilityBindingsResponseBody struct {
	Object  string                            `json:"object"`
	ModelID int64                             `json:"model_id"`
	Data    []registry.ModelCapabilityBinding `json:"data"`
}

func NewAdminModelAliasBulkImportHandler(d AdminModelAliasesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			modelWriteError(w, http.StatusServiceUnavailable, "gateway_not_configured", "model alias dependency unset")
			return
		}
		params, ok := parseAliasBulkImportBody(w, r)
		if !ok {
			return
		}
		// 审计归属(actor)一律取自已认证身份, 绝不信任请求体 —— 否则 platform-admin 可在 body 设 actor
		// 伪造别名导入审计快照的归属(类 routes AdminID-from-identity / modelbinding actor 范式)。adminGate
		// 已把身份注入 context; 未注入(异常/未经 gate)时置空, 不回退信任 body。Reason 是合法用户备注, 保留 body。
		if ident, ok := admin.IdentityFromContext(r.Context()); ok {
			params.Actor = fmt.Sprintf("admin-token:%d", ident.TokenID)
		} else {
			params.Actor = ""
		}
		results, err := d.Store.BulkImportModelAliases(r.Context(), params)
		if err != nil {
			writeModelAliasStoreError(w, err)
			return
		}
		modelWriteJSON(w, http.StatusOK, aliasBulkImportResponseBody{
			Object:  "model_alias_bulk_import_result",
			Results: results,
		})
	}
}

func NewAdminModelCapabilityBindingsHandler(d AdminModelAliasesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			modelWriteError(w, http.StatusServiceUnavailable, "gateway_not_configured", "model capability binding dependency unset")
			return
		}
		modelID, ok := parseModelIDParam(w, r)
		if !ok {
			return
		}
		bindings, err := d.Store.ListModelCapabilityBindings(r.Context(), modelID)
		if err != nil {
			writeModelAliasStoreError(w, err)
			return
		}
		modelWriteJSON(w, http.StatusOK, capabilityBindingsResponseBody{
			Object:  "model_capability_binding_list",
			ModelID: modelID,
			Data:    bindings,
		})
	}
}

func parseAliasBulkImportBody(w http.ResponseWriter, r *http.Request) (registry.BulkImportModelAliasesParams, bool) {
	if r.Body == nil {
		modelWriteError(w, http.StatusBadRequest, "invalid_json", "request body required")
		return registry.BulkImportModelAliasesParams{}, false
	}
	limited := http.MaxBytesReader(w, r.Body, 1<<20)
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/csv") || strings.Contains(contentType, "application/csv") {
		params, err := parseAliasBulkImportCSV(limited)
		if err != nil {
			modelWriteError(w, http.StatusBadRequest, "invalid_csv", err.Error())
			return registry.BulkImportModelAliasesParams{}, false
		}
		return params, true
	}

	var params registry.BulkImportModelAliasesParams
	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields() // 严格请求契约: 拒未知字段(JSON 分支; CSV 分支不受影响)
	if err := dec.Decode(&params); err != nil {
		if errors.Is(err, io.EOF) {
			modelWriteError(w, http.StatusBadRequest, "invalid_json", "request body required")
			return registry.BulkImportModelAliasesParams{}, false
		}
		// 文案硬编码(对齐 routeadmin), 不回 err.Error() —— 防把 DisallowUnknownFields 的 "unknown field X" 字段名暴露。
		modelWriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return registry.BulkImportModelAliasesParams{}, false
	}
	if len(params.Aliases) == 0 {
		modelWriteError(w, http.StatusBadRequest, "invalid_aliases", "aliases must contain at least one row")
		return registry.BulkImportModelAliasesParams{}, false
	}
	return params, true
}

func parseAliasBulkImportCSV(r io.Reader) (registry.BulkImportModelAliasesParams, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return registry.BulkImportModelAliasesParams{}, err
	}
	if len(records) < 2 {
		return registry.BulkImportModelAliasesParams{}, fmt.Errorf("csv must include header and at least one row")
	}
	header := map[string]int{}
	for i, col := range records[0] {
		header[strings.ToLower(strings.TrimSpace(col))] = i
	}
	get := func(row []string, name string) string {
		idx, ok := header[name]
		if !ok || idx >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[idx])
	}

	params := registry.BulkImportModelAliasesParams{
		Aliases: make([]registry.ModelAliasImport, 0, len(records)-1),
	}
	for i, row := range records[1:] {
		if len(row) == 0 || strings.TrimSpace(strings.Join(row, "")) == "" {
			continue
		}
		modelID, err := parseOptionalCSVInt(get(row, "model_id"))
		if err != nil {
			return registry.BulkImportModelAliasesParams{}, fmt.Errorf("row %d model_id: %w", i+2, err)
		}
		tenantID, err := parseOptionalCSVInt(get(row, "tenant_id"))
		if err != nil {
			return registry.BulkImportModelAliasesParams{}, fmt.Errorf("row %d tenant_id: %w", i+2, err)
		}
		params.Aliases = append(params.Aliases, registry.ModelAliasImport{
			TenantID: tenantID,
			Scope:    get(row, "scope"),
			ModelID:  modelID,
			Alias:    get(row, "alias"),
			Display:  get(row, "display"),
			Status:   get(row, "status"),
			Source:   get(row, "source"),
		})
	}
	if len(params.Aliases) == 0 {
		return registry.BulkImportModelAliasesParams{}, fmt.Errorf("csv aliases must contain at least one row")
	}
	return params, nil
}

func parseOptionalCSVInt(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func writeModelAliasStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, registry.ErrUnknownModel):
		modelWriteError(w, http.StatusNotFound, "model_not_found", "model not found")
	case errors.Is(err, registry.ErrInvalidModelCapability):
		modelWriteError(w, http.StatusBadRequest, "invalid_capability", err.Error())
	default:
		modelWriteError(w, http.StatusServiceUnavailable, "model_admin_store_failed", "model admin backend unavailable")
	}
}
