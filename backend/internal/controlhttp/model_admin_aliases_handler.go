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
	UpsertModelCapabilityBinding(context.Context, registry.UpsertModelCapabilityBindingParams) (registry.ModelCapabilityBinding, error)
}

// capabilityBindingUpsertRequest 是 PUT /v1/admin/models/{id}/capability-bindings 的请求体。
// **不含 source 字段** —— provenance 由服务端强制为 "operator", 不取自 body: vendor-sync 用 source 做行协调
// (model_sync_writer 按 source 过滤/保护其同步行), 若放任 body 设 source, 运营写入可伪装成某 vendor-sync 来源
// 被同步逻辑误清/误保护。model_id 取自 path。tenant_id 是【目标租户】(scope=tenant 用; global 省略=NULL),
// 非 actor 身份。Enabled 用 *bool 强制显式存在: upsert 的 ON CONFLICT DO UPDATE SET enabled=EXCLUDED.enabled
// 下, 省略 enabled 会按 Go 零值 false 把已存在的 enabled 行【静默翻成 disabled】(read-omit-write footgun), 故 nil→400。
type capabilityBindingUpsertRequest struct {
	Scope            string          `json:"scope"`
	Capability       string          `json:"capability"`
	CapabilityValue  *string         `json:"capability_value,omitempty"`
	CapabilityParams json.RawMessage `json:"capability_params,omitempty"`
	Enabled          *bool           `json:"enabled"`
	TenantID         int64           `json:"tenant_id,omitempty"`
}

type capabilityBindingUpsertResponseBody struct {
	Object  string                          `json:"object"`
	Binding registry.ModelCapabilityBinding `json:"binding"`
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

// NewAdminModelCapabilityBindingUpsertHandler 处理 PUT /v1/admin/models/{id}/capability-bindings: upsert 一条
// per-(tenant|global) scope 的能力绑定。补全此前仅 GET 的 capability-binding admin 面。能力名/scope/tenant_id
// 必填性由 store(UpsertModelCapabilityBinding)权威校验; source 服务端强制 "operator"(不取 body)。
func NewAdminModelCapabilityBindingUpsertHandler(d AdminModelAliasesDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Store == nil {
			modelWriteError(w, http.StatusServiceUnavailable, "gateway_not_configured", "model capability binding dependency unset")
			return
		}
		modelID, ok := parseModelIDParam(w, r)
		if !ok {
			return
		}
		req, ok := parseCapabilityBindingUpsertBody(w, r)
		if !ok {
			return
		}
		binding, err := d.Store.UpsertModelCapabilityBinding(r.Context(), registry.UpsertModelCapabilityBindingParams{
			TenantID:         req.TenantID,
			Scope:            req.Scope,
			ModelID:          modelID,
			Capability:       req.Capability,
			CapabilityValue:  req.CapabilityValue,
			CapabilityParams: req.CapabilityParams,
			Enabled:          *req.Enabled,
			Source:           "operator", // 强制运营来源, 不取 body, 防伪装 vendor-sync 来源
		})
		if err != nil {
			writeModelAliasStoreError(w, err)
			return
		}
		modelWriteJSON(w, http.StatusOK, capabilityBindingUpsertResponseBody{
			Object:  "model_capability_binding",
			Binding: binding,
		})
	}
}

func parseCapabilityBindingUpsertBody(w http.ResponseWriter, r *http.Request) (capabilityBindingUpsertRequest, bool) {
	if r.Body == nil {
		modelWriteError(w, http.StatusBadRequest, "invalid_json", "request body required")
		return capabilityBindingUpsertRequest{}, false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields() // 严格契约: 拒未知字段(含 body 内 source, 防伪装 vendor-sync provenance)
	var req capabilityBindingUpsertRequest
	if err := dec.Decode(&req); err != nil {
		// 文案硬编码, 不回 err.Error() —— 防把 DisallowUnknownFields 的 "unknown field X" 字段名暴露。
		modelWriteError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return capabilityBindingUpsertRequest{}, false
	}
	if req.Enabled == nil {
		// 显式存在守卫: 防省略 enabled 时按零值 false 静默把已有 enabled 绑定翻成 disabled。
		modelWriteError(w, http.StatusBadRequest, "invalid_capability_binding", "enabled field is required")
		return capabilityBindingUpsertRequest{}, false
	}
	// capability_params 是参数 map(jsonb object): 给了且非空/非 null 时守住必须是 JSON 对象, 拒标量/数组 ——
	// 否则非 object jsonb 落库, 污染 GET 回显与未来消费者(FU-2)。decoder 已验有效 JSON, 此处只判顶层类型。
	if len(req.CapabilityParams) > 0 {
		trimmed := strings.TrimSpace(string(req.CapabilityParams))
		if trimmed != "" && trimmed != "null" && !strings.HasPrefix(trimmed, "{") {
			modelWriteError(w, http.StatusBadRequest, "invalid_capability_binding", "capability_params must be a JSON object")
			return capabilityBindingUpsertRequest{}, false
		}
	}
	return req, true
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
