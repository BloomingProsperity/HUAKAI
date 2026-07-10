package adminhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/registrydefault"
	"github.com/BloomingProsperity/HUAKAI/internal/servingcapability"
)

var (
	errProviderCatalogCodeConflict   = errors.New("provider catalog code already exists")
	errProviderCatalogNotFound       = errors.New("provider catalog provider not found")
	errProviderCatalogActiveAccounts = errors.New("provider catalog provider has active accounts")
	errProviderCatalogTxPoolUnset    = errors.New("provider catalog transaction pool unset")
)

type providerCatalogCreateParams struct {
	TenantID         int64
	Code             string
	DisplayName      string
	UpstreamProtocol string
	Enabled          bool
}

type providerCatalogUpdateParams struct {
	TenantID         int64
	Code             string
	DisplayName      string
	UpstreamProtocol string
	Enabled          bool
}

type providerCatalogDeleteParams struct {
	TenantID int64
	Code     string
}

type providerCatalogMutationRequest struct {
	Code             string `json:"code,omitempty"`
	DisplayName      string `json:"display_name,omitempty"`
	UpstreamProtocol string `json:"upstream_protocol,omitempty"`
	Enabled          *bool  `json:"enabled,omitempty"`
	Reason           string `json:"reason,omitempty"`
}

type providerCatalogDeleteResponse struct {
	Object  string `json:"object"`
	ID      int64  `json:"id"`
	Code    string `json:"code"`
	Deleted bool   `json:"deleted"`
}

type providerCatalogDB interface {
	adminProviderCatalogQueries
	InsertProvider(context.Context, admindb.InsertProviderParams) (admindb.InsertProviderRow, error)
	UpdateProvider(context.Context, admindb.UpdateProviderParams) (admindb.UpdateProviderRow, error)
	CountActiveProviderAccountsForProvider(context.Context, admindb.CountActiveProviderAccountsForProviderParams) (int64, error)
	SoftDeleteProvider(context.Context, admindb.SoftDeleteProviderParams) (admindb.SoftDeleteProviderRow, error)
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type providerCatalogStoreAdapter struct {
	base providerCatalogDB
	pool *pgxpool.Pool
}

func NewProviderCatalogStoreAdapter(base providerCatalogDB, pool *pgxpool.Pool) adminProviderCatalogStore {
	return providerCatalogStoreAdapter{base: base, pool: pool}
}

func NewProviderCatalogCreateHandler(d AdminProviderCatalogDeps) http.HandlerFunc {
	return newCreateProviderCatalogHandler(d)
}

func NewProviderCatalogUpdateHandler(d AdminProviderCatalogDeps) http.HandlerFunc {
	return newUpdateProviderCatalogHandler(d)
}

func NewProviderCatalogDeleteHandler(d AdminProviderCatalogDeps) http.HandlerFunc {
	return newDeleteProviderCatalogHandler(d)
}

func (s providerCatalogStoreAdapter) ListAdminProvidersByTenant(ctx context.Context, arg admindb.ListAdminProvidersByTenantParams) ([]admindb.ListAdminProvidersByTenantRow, error) {
	if s.base == nil {
		return nil, errProviderCatalogTxPoolUnset
	}
	return s.base.ListAdminProvidersByTenant(ctx, arg)
}

func (s providerCatalogStoreAdapter) CreateProviderCatalogWithAudit(ctx context.Context, arg providerCatalogCreateParams, audit admindb.InsertAdminAuditEventParams) (providerCatalogItem, error) {
	if s.pool == nil {
		return providerCatalogItem{}, errProviderCatalogTxPoolUnset
	}
	var item providerCatalogItem
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, err := q.InsertProvider(ctx, admindb.InsertProviderParams{
			TenantID: arg.TenantID, Code: arg.Code, DisplayName: arg.DisplayName,
			UpstreamProtocol: arg.UpstreamProtocol, Enabled: arg.Enabled,
		})
		if err != nil {
			return normalizeProviderCatalogDBError(err)
		}
		audit.TargetID = &row.ID
		if _, err := q.InsertAdminAuditEvent(ctx, audit); err != nil {
			return err
		}
		item = providerCatalogItemFromInsertRow(row)
		return nil
	})
	return item, err
}

func (s providerCatalogStoreAdapter) UpdateProviderCatalogWithAudit(ctx context.Context, arg providerCatalogUpdateParams, audit admindb.InsertAdminAuditEventParams) (providerCatalogItem, error) {
	if s.pool == nil {
		return providerCatalogItem{}, errProviderCatalogTxPoolUnset
	}
	var item providerCatalogItem
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, err := q.UpdateProvider(ctx, admindb.UpdateProviderParams{
			TenantID: arg.TenantID, Code: arg.Code, DisplayName: arg.DisplayName,
			UpstreamProtocol: arg.UpstreamProtocol, Enabled: arg.Enabled,
		})
		if err != nil {
			return normalizeProviderCatalogDBError(err)
		}
		audit.TargetID = &row.ID
		if _, err := q.InsertAdminAuditEvent(ctx, audit); err != nil {
			return err
		}
		item = providerCatalogItemFromUpdateRow(row)
		return nil
	})
	return item, err
}

func (s providerCatalogStoreAdapter) DeleteProviderCatalogWithAudit(ctx context.Context, arg providerCatalogDeleteParams, audit admindb.InsertAdminAuditEventParams) (providerCatalogItem, error) {
	if s.pool == nil {
		return providerCatalogItem{}, errProviderCatalogTxPoolUnset
	}
	var item providerCatalogItem
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		active, err := q.CountActiveProviderAccountsForProvider(ctx, admindb.CountActiveProviderAccountsForProviderParams{
			TenantID: arg.TenantID, Code: arg.Code,
		})
		if err != nil {
			return err
		}
		if active > 0 {
			return errProviderCatalogActiveAccounts
		}
		row, err := q.SoftDeleteProvider(ctx, admindb.SoftDeleteProviderParams{
			TenantID: arg.TenantID, Code: arg.Code,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				active, countErr := q.CountActiveProviderAccountsForProvider(ctx, admindb.CountActiveProviderAccountsForProviderParams{
					TenantID: arg.TenantID, Code: arg.Code,
				})
				if countErr != nil {
					return countErr
				}
				if active > 0 {
					return errProviderCatalogActiveAccounts
				}
			}
			return normalizeProviderCatalogDBError(err)
		}
		audit.TargetID = &row.ID
		if _, err := q.InsertAdminAuditEvent(ctx, audit); err != nil {
			return err
		}
		item = providerCatalogItemFromSoftDeleteRow(row)
		return nil
	})
	return item, err
}

func MountProviderCatalogRoutes(r chi.Router, d AdminProviderCatalogDeps) {
	r.Get("/", NewProviderCatalogListHandler(d))
	r.Post("/", newCreateProviderCatalogHandler(d))
	r.Put("/{code}", newUpdateProviderCatalogHandler(d))
	r.Delete("/{code}", newDeleteProviderCatalogHandler(d))
}

func newCreateProviderCatalogHandler(d AdminProviderCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := providerCatalogStore(d)
		ident, tenantID, ok := resolveProviderCatalogMutationAdmin(w, r, d, store)
		if !ok {
			return
		}
		var req providerCatalogMutationRequest
		if !decodeProviderCatalogMutationJSON(w, r, &req, true) {
			return
		}
		arg, ok := validateProviderCatalogCreateRequest(w, tenantID, req)
		if !ok {
			return
		}
		if !requireProviderCatalogServingReadiness(w, arg.UpstreamProtocol, arg.Enabled) {
			return
		}
		audit, ok := buildProviderCatalogAuditParams(w, r, ident, tenantID, "create_provider", req.Reason, map[string]any{
			"tenant_id": tenantID, "code": arg.Code, "display_name": arg.DisplayName,
			"upstream_protocol": arg.UpstreamProtocol, "enabled": arg.Enabled,
		})
		if !ok {
			return
		}
		item, err := store.CreateProviderCatalogWithAudit(r.Context(), arg, audit)
		if err != nil {
			writeProviderCatalogMutationError(w, err, "provider_create_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusCreated, item)
	}
}

func newUpdateProviderCatalogHandler(d AdminProviderCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := providerCatalogStore(d)
		ident, tenantID, ok := resolveProviderCatalogMutationAdmin(w, r, d, store)
		if !ok {
			return
		}
		code := strings.TrimSpace(chi.URLParam(r, "code"))
		if code == "" {
			writeError(w, http.StatusBadRequest, "provider_code_required", "provider code is required")
			return
		}
		var req providerCatalogMutationRequest
		if !decodeProviderCatalogMutationJSON(w, r, &req, true) {
			return
		}
		arg, ok := validateProviderCatalogUpdateRequest(w, tenantID, code, req)
		if !ok {
			return
		}
		if !requireProviderCatalogServingReadiness(w, arg.UpstreamProtocol, arg.Enabled) {
			return
		}
		audit, ok := buildProviderCatalogAuditParams(w, r, ident, tenantID, "update_provider", req.Reason, map[string]any{
			"tenant_id": tenantID, "code": arg.Code, "display_name": arg.DisplayName,
			"upstream_protocol": arg.UpstreamProtocol, "enabled": arg.Enabled,
		})
		if !ok {
			return
		}
		item, err := store.UpdateProviderCatalogWithAudit(r.Context(), arg, audit)
		if err != nil {
			writeProviderCatalogMutationError(w, err, "provider_update_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusOK, item)
	}
}

func newDeleteProviderCatalogHandler(d AdminProviderCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := providerCatalogStore(d)
		ident, tenantID, ok := resolveProviderCatalogMutationAdmin(w, r, d, store)
		if !ok {
			return
		}
		code := strings.TrimSpace(chi.URLParam(r, "code"))
		if code == "" {
			writeError(w, http.StatusBadRequest, "provider_code_required", "provider code is required")
			return
		}
		var req providerCatalogMutationRequest
		if !decodeProviderCatalogMutationJSON(w, r, &req, false) {
			return
		}
		arg := providerCatalogDeleteParams{TenantID: tenantID, Code: code}
		audit, ok := buildProviderCatalogAuditParams(w, r, ident, tenantID, "delete_provider", req.Reason, map[string]any{
			"tenant_id": tenantID, "code": code, "deleted": true,
		})
		if !ok {
			return
		}
		item, err := store.DeleteProviderCatalogWithAudit(r.Context(), arg, audit)
		if err != nil {
			writeProviderCatalogMutationError(w, err, "provider_delete_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusOK, providerCatalogDeleteResponse{
			Object:  "admin_provider_deleted",
			ID:      item.ID,
			Code:    item.Code,
			Deleted: true,
		})
	}
}

func resolveProviderCatalogMutationAdmin(w http.ResponseWriter, r *http.Request, d AdminProviderCatalogDeps, store adminProviderCatalogStore) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || store == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
			"admin provider catalog mutation dependency unset")
		return admin.AdminIdentity{}, 0, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		writeAdminAuthError(w, err)
		return admin.AdminIdentity{}, 0, false
	}
	switch ident.Role {
	case admin.RoleTenantOperator, admin.RolePlatformAdmin:
	default:
		writeError(w, http.StatusForbidden, "admin_forbidden", "admin role required")
		return admin.AdminIdentity{}, 0, false
	}
	tenantID, ok := parseAdminCatalogTenant(w, r, ident)
	if !ok {
		return admin.AdminIdentity{}, 0, false
	}
	return ident, tenantID, true
}

func validateProviderCatalogCreateRequest(w http.ResponseWriter, tenantID int64, req providerCatalogMutationRequest) (providerCatalogCreateParams, bool) {
	code := strings.TrimSpace(req.Code)
	displayName := strings.TrimSpace(req.DisplayName)
	protocol := req.UpstreamProtocol
	if code == "" {
		writeError(w, http.StatusBadRequest, "provider_code_required", "provider code is required")
		return providerCatalogCreateParams{}, false
	}
	if displayName == "" {
		writeError(w, http.StatusBadRequest, "provider_display_name_required", "provider display_name is required")
		return providerCatalogCreateParams{}, false
	}
	enabled, ok := validateProviderCatalogEnabled(w, req.Enabled)
	if !ok {
		return providerCatalogCreateParams{}, false
	}
	if !isCanonicalProviderCatalogMutationProtocol(protocol) {
		writeError(w, http.StatusBadRequest, "invalid_upstream_protocol", "upstream_protocol is not supported")
		return providerCatalogCreateParams{}, false
	}
	return providerCatalogCreateParams{
		TenantID: tenantID, Code: code, DisplayName: displayName,
		UpstreamProtocol: protocol, Enabled: enabled,
	}, true
}

func validateProviderCatalogUpdateRequest(w http.ResponseWriter, tenantID int64, code string, req providerCatalogMutationRequest) (providerCatalogUpdateParams, bool) {
	displayName := strings.TrimSpace(req.DisplayName)
	protocol := req.UpstreamProtocol
	if displayName == "" {
		writeError(w, http.StatusBadRequest, "provider_display_name_required", "provider display_name is required")
		return providerCatalogUpdateParams{}, false
	}
	enabled, ok := validateProviderCatalogEnabled(w, req.Enabled)
	if !ok {
		return providerCatalogUpdateParams{}, false
	}
	if !isCanonicalProviderCatalogMutationProtocol(protocol) {
		writeError(w, http.StatusBadRequest, "invalid_upstream_protocol", "upstream_protocol is not supported")
		return providerCatalogUpdateParams{}, false
	}
	return providerCatalogUpdateParams{
		TenantID: tenantID, Code: code, DisplayName: displayName,
		UpstreamProtocol: protocol, Enabled: enabled,
	}, true
}

func validateProviderCatalogEnabled(w http.ResponseWriter, enabled *bool) (bool, bool) {
	if enabled == nil {
		writeError(w, http.StatusBadRequest, "provider_enabled_required", "provider enabled is required")
		return false, false
	}
	return *enabled, true
}

func requireProviderCatalogServingReadiness(w http.ResponseWriter, family string, enabled bool) bool {
	return requireProviderCatalogServingReadinessUsing(
		w, family, enabled, defaultServingCapabilityEvaluator().RequireProviderConfigEnabled,
	)
}

func requireProviderCatalogServingReadinessUsing(w http.ResponseWriter, family string, enabled bool, require func(string) error) bool {
	if !enabled && isKnownProviderCatalogProtocol(family) {
		return true
	}
	if require == nil {
		writeError(w, http.StatusServiceUnavailable, "provider_readiness_unavailable", "provider readiness checker is unavailable")
		return false
	}
	err := require(family)
	if err == nil {
		return true
	}
	var readinessErr *servingcapability.ReadinessError
	if errors.As(err, &readinessErr) {
		reason := strings.TrimSpace(readinessErr.Result.Reason)
		if reason == "" {
			reason = "closure_incomplete"
		}
		writeError(w, http.StatusUnprocessableEntity, "provider_serving_not_ready", reason)
		return false
	}
	writeError(w, http.StatusServiceUnavailable, "provider_readiness_unavailable", err.Error())
	return false
}

func decodeProviderCatalogMutationJSON(w http.ResponseWriter, r *http.Request, dst any, required bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "body_read_error", err.Error())
		return false
	}
	if strings.TrimSpace(string(body)) == "" {
		if required {
			writeError(w, http.StatusBadRequest, "invalid_json", "request body is required")
			return false
		}
		return true
	}
	if err := json.Unmarshal(body, dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return false
	}
	return true
}

func buildProviderCatalogAuditParams(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity, tenantID int64, action, reason string, payload map[string]any) (admindb.InsertAdminAuditEventParams, bool) {
	raw, err := json.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "audit_payload_failed", err.Error())
		return admindb.InsertAdminAuditEventParams{}, false
	}
	actorRole := ident.Role
	if actorRole == "" {
		actorRole = admin.RoleTenantOperator
	}
	reqID := middleware.GetReqID(r.Context())
	var reqIDArg *string
	if reqID != "" {
		reqIDArg = &reqID
	}
	reason = strings.TrimSpace(reason)
	var reasonArg *string
	if reason != "" {
		reasonArg = &reason
	}
	return admindb.InsertAdminAuditEventParams{
		TenantID: &tenantID, ActorID: ident.AuditActor(), ActorRole: actorRole,
		Action: action, TargetType: "provider", RequestID: reqIDArg,
		Reason: reasonArg, Payload: raw,
	}, true
}

func writeProviderCatalogMutationError(w http.ResponseWriter, err error, fallbackCode string) {
	switch {
	case errors.Is(err, errProviderCatalogCodeConflict):
		writeError(w, http.StatusConflict, "provider_code_conflict", "provider code already exists")
	case errors.Is(err, errProviderCatalogActiveAccounts):
		writeError(w, http.StatusConflict, "provider_has_active_accounts", "provider has active provider accounts")
	case errors.Is(err, errProviderCatalogNotFound), errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "provider_not_found", "provider not found")
	default:
		writeError(w, http.StatusServiceUnavailable, fallbackCode, err.Error())
	}
}

func normalizeProviderCatalogDBError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "uq_providers_tenant_code" {
		return errProviderCatalogCodeConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errProviderCatalogNotFound
	}
	return err
}

func providerCatalogItemFromInsertRow(row admindb.InsertProviderRow) providerCatalogItem {
	return providerCatalogItem{
		ID: row.ID, Code: row.Code, DisplayName: row.DisplayName,
		UpstreamProtocol: row.UpstreamProtocol, Enabled: row.Enabled,
		CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func providerCatalogItemFromUpdateRow(row admindb.UpdateProviderRow) providerCatalogItem {
	return providerCatalogItem{
		ID: row.ID, Code: row.Code, DisplayName: row.DisplayName,
		UpstreamProtocol: row.UpstreamProtocol, Enabled: row.Enabled,
		CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func providerCatalogItemFromSoftDeleteRow(row admindb.SoftDeleteProviderRow) providerCatalogItem {
	return providerCatalogItem{
		ID: row.ID, Code: row.Code, DisplayName: row.DisplayName,
		UpstreamProtocol: row.UpstreamProtocol, Enabled: row.Enabled,
		CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func isKnownProviderCatalogProtocol(protocol string) bool {
	return registrydefault.IsSupportedProtocolFamily(protocol)
}

func isCanonicalProviderCatalogMutationProtocol(protocol string) bool {
	if protocol == "" || strings.TrimSpace(protocol) != protocol {
		return false
	}
	if isKnownProviderCatalogProtocol(protocol) {
		return true
	}
	contract, ok := servingcapability.DefaultContractRegistry().Lookup(protocol)
	return ok && contract.Family == protocol
}
