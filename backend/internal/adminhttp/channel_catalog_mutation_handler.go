package adminhttp

// Admin channel catalog mutation handlers (create / update / delete). Mirrors
// provider_catalog_mutation_handler.go: each mutation runs in a pgx tx that
// writes the channels row and an admin audit event atomically, behind the same
// adminCatalogAuth tenant/role gate. Channels are keyed by id within a tenant
// (no "code"); the (tenant_id, pool_group_id, name) uniqueness is enforced by
// uq_channels_tenant_pool_name and surfaced as 409.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
)

var (
	errChannelCatalogNameConflict = errors.New("channel name already exists in pool group")
	errChannelCatalogPoolNotFound = errors.New("pool group not found for tenant")
	errChannelCatalogNotFound     = errors.New("channel not found")
	errChannelCatalogTxPoolUnset  = errors.New("channel catalog transaction pool unset")
)

// defaultFailoverStatusCodes mirrors the channels.failover_status_codes column
// default; applied when the caller omits the list.
var defaultFailoverStatusCodes = []int32{401, 403, 429, 529}

type channelCatalogCreateParams struct {
	TenantID            int64
	PoolGroupID         int64
	Name                string
	FailoverStatusCodes []int32
	Enabled             bool
}

type channelCatalogUpdateParams struct {
	TenantID            int64
	ID                  int64
	PoolGroupID         int64
	Name                string
	FailoverStatusCodes []int32
	Enabled             bool
}

type channelCatalogDeleteParams struct {
	TenantID int64
	ID       int64
}

type channelCatalogMutationRequest struct {
	PoolGroupID         *int64  `json:"pool_group_id,omitempty"`
	Name                string  `json:"name,omitempty"`
	FailoverStatusCodes []int32 `json:"failover_status_codes,omitempty"`
	Enabled             *bool   `json:"enabled,omitempty"`
	Reason              string  `json:"reason,omitempty"`
}

type channelCatalogDeleteResponse struct {
	Object  string `json:"object"`
	ID      int64  `json:"id"`
	Deleted bool   `json:"deleted"`
}

type adminChannelCatalogStore interface {
	adminChannelCatalogQueries
	CreateChannelCatalogWithAudit(context.Context, channelCatalogCreateParams, admindb.InsertAdminAuditEventParams) (channelCatalogItem, error)
	UpdateChannelCatalogWithAudit(context.Context, channelCatalogUpdateParams, admindb.InsertAdminAuditEventParams) (channelCatalogItem, error)
	DeleteChannelCatalogWithAudit(context.Context, channelCatalogDeleteParams, admindb.InsertAdminAuditEventParams) (channelCatalogItem, error)
}

type channelCatalogDB interface {
	adminChannelCatalogQueries
	CreateChannel(context.Context, admindb.CreateChannelParams) (admindb.CreateChannelRow, error)
	UpdateChannel(context.Context, admindb.UpdateChannelParams) (admindb.UpdateChannelRow, error)
	SoftDeleteChannel(context.Context, admindb.SoftDeleteChannelParams) (admindb.SoftDeleteChannelRow, error)
	InsertAdminAuditEvent(context.Context, admindb.InsertAdminAuditEventParams) (admindb.InsertAdminAuditEventRow, error)
}

type channelCatalogStoreAdapter struct {
	base channelCatalogDB
	pool *pgxpool.Pool
}

// NewChannelCatalogStoreAdapter wires the sqlc queries + tx pool into the
// mutation store used by the create/update/delete handlers.
func NewChannelCatalogStoreAdapter(base channelCatalogDB, pool *pgxpool.Pool) adminChannelCatalogStore {
	return channelCatalogStoreAdapter{base: base, pool: pool}
}

func NewChannelCatalogCreateHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return newCreateChannelCatalogHandler(d)
}

func NewChannelCatalogUpdateHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return newUpdateChannelCatalogHandler(d)
}

func NewChannelCatalogDeleteHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return newDeleteChannelCatalogHandler(d)
}

func (s channelCatalogStoreAdapter) ListAdminChannelsByTenant(ctx context.Context, arg admindb.ListAdminChannelsByTenantParams) ([]admindb.ListAdminChannelsByTenantRow, error) {
	if s.base == nil {
		return nil, errChannelCatalogTxPoolUnset
	}
	return s.base.ListAdminChannelsByTenant(ctx, arg)
}

func (s channelCatalogStoreAdapter) CreateChannelCatalogWithAudit(ctx context.Context, arg channelCatalogCreateParams, audit admindb.InsertAdminAuditEventParams) (channelCatalogItem, error) {
	if s.pool == nil {
		return channelCatalogItem{}, errChannelCatalogTxPoolUnset
	}
	var item channelCatalogItem
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, err := q.CreateChannel(ctx, admindb.CreateChannelParams{
			TenantID: arg.TenantID, PoolGroupID: arg.PoolGroupID, Name: arg.Name,
			FailoverStatusCodes: arg.FailoverStatusCodes, Enabled: arg.Enabled,
		})
		if err != nil {
			// EXISTS pool-group guard miss returns no row.
			if errors.Is(err, pgx.ErrNoRows) {
				return errChannelCatalogPoolNotFound
			}
			return normalizeChannelCatalogDBError(err)
		}
		audit.TargetID = &row.ID
		if _, err := q.InsertAdminAuditEvent(ctx, audit); err != nil {
			return err
		}
		item = channelCatalogItemFromCreateRow(row)
		return nil
	})
	return item, err
}

func (s channelCatalogStoreAdapter) UpdateChannelCatalogWithAudit(ctx context.Context, arg channelCatalogUpdateParams, audit admindb.InsertAdminAuditEventParams) (channelCatalogItem, error) {
	if s.pool == nil {
		return channelCatalogItem{}, errChannelCatalogTxPoolUnset
	}
	var item channelCatalogItem
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, err := q.UpdateChannel(ctx, admindb.UpdateChannelParams{
			TenantID: arg.TenantID, ID: arg.ID, PoolGroupID: arg.PoolGroupID,
			Name: arg.Name, FailoverStatusCodes: arg.FailoverStatusCodes, Enabled: arg.Enabled,
		})
		if err != nil {
			// No row means either the channel is gone for this tenant or the
			// pool-group guard failed; surface not-found (the conflict path is
			// the 23505 below).
			if errors.Is(err, pgx.ErrNoRows) {
				return errChannelCatalogNotFound
			}
			return normalizeChannelCatalogDBError(err)
		}
		audit.TargetID = &row.ID
		if _, err := q.InsertAdminAuditEvent(ctx, audit); err != nil {
			return err
		}
		item = channelCatalogItemFromUpdateRow(row)
		return nil
	})
	return item, err
}

func (s channelCatalogStoreAdapter) DeleteChannelCatalogWithAudit(ctx context.Context, arg channelCatalogDeleteParams, audit admindb.InsertAdminAuditEventParams) (channelCatalogItem, error) {
	if s.pool == nil {
		return channelCatalogItem{}, errChannelCatalogTxPoolUnset
	}
	var item channelCatalogItem
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := admindb.New(tx)
		row, err := q.SoftDeleteChannel(ctx, admindb.SoftDeleteChannelParams{
			TenantID: arg.TenantID, ID: arg.ID,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errChannelCatalogNotFound
			}
			return normalizeChannelCatalogDBError(err)
		}
		audit.TargetID = &row.ID
		if _, err := q.InsertAdminAuditEvent(ctx, audit); err != nil {
			return err
		}
		item = channelCatalogItemFromSoftDeleteRow(row)
		return nil
	})
	return item, err
}

// MountChannelCatalogRoutes registers the full channel catalog CRUD surface.
func MountChannelCatalogRoutes(r chi.Router, d AdminChannelCatalogDeps) {
	r.Get("/", NewChannelCatalogListHandler(d))
	r.Post("/", newCreateChannelCatalogHandler(d))
	r.Put("/{id}", newUpdateChannelCatalogHandler(d))
	r.Delete("/{id}", newDeleteChannelCatalogHandler(d))
}

func newCreateChannelCatalogHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := channelCatalogStore(d)
		ident, tenantID, ok := resolveChannelCatalogMutationAdmin(w, r, d, store)
		if !ok {
			return
		}
		var req channelCatalogMutationRequest
		if !decodeChannelCatalogMutationJSON(w, r, &req, true) {
			return
		}
		arg, ok := validateChannelCatalogCreateRequest(w, tenantID, req)
		if !ok {
			return
		}
		audit, ok := buildChannelCatalogAuditParams(w, r, ident, tenantID, "create_channel", req.Reason, map[string]any{
			"tenant_id": tenantID, "pool_group_id": arg.PoolGroupID, "name": arg.Name,
			"failover_status_codes": arg.FailoverStatusCodes, "enabled": arg.Enabled,
		})
		if !ok {
			return
		}
		item, err := store.CreateChannelCatalogWithAudit(r.Context(), arg, audit)
		if err != nil {
			writeChannelCatalogMutationError(w, err, "channel_create_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusCreated, item)
	}
}

func newUpdateChannelCatalogHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := channelCatalogStore(d)
		ident, tenantID, ok := resolveChannelCatalogMutationAdmin(w, r, d, store)
		if !ok {
			return
		}
		id, ok := parseChannelCatalogID(w, r)
		if !ok {
			return
		}
		var req channelCatalogMutationRequest
		if !decodeChannelCatalogMutationJSON(w, r, &req, true) {
			return
		}
		arg, ok := validateChannelCatalogUpdateRequest(w, tenantID, id, req)
		if !ok {
			return
		}
		audit, ok := buildChannelCatalogAuditParams(w, r, ident, tenantID, "update_channel", req.Reason, map[string]any{
			"tenant_id": tenantID, "id": arg.ID, "pool_group_id": arg.PoolGroupID,
			"name": arg.Name, "failover_status_codes": arg.FailoverStatusCodes, "enabled": arg.Enabled,
		})
		if !ok {
			return
		}
		item, err := store.UpdateChannelCatalogWithAudit(r.Context(), arg, audit)
		if err != nil {
			writeChannelCatalogMutationError(w, err, "channel_update_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusOK, item)
	}
}

func newDeleteChannelCatalogHandler(d AdminChannelCatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := channelCatalogStore(d)
		ident, tenantID, ok := resolveChannelCatalogMutationAdmin(w, r, d, store)
		if !ok {
			return
		}
		id, ok := parseChannelCatalogID(w, r)
		if !ok {
			return
		}
		var req channelCatalogMutationRequest
		if !decodeChannelCatalogMutationJSON(w, r, &req, false) {
			return
		}
		arg := channelCatalogDeleteParams{TenantID: tenantID, ID: id}
		audit, ok := buildChannelCatalogAuditParams(w, r, ident, tenantID, "delete_channel", req.Reason, map[string]any{
			"tenant_id": tenantID, "id": id, "deleted": true,
		})
		if !ok {
			return
		}
		item, err := store.DeleteChannelCatalogWithAudit(r.Context(), arg, audit)
		if err != nil {
			writeChannelCatalogMutationError(w, err, "channel_delete_failed")
			return
		}
		writeAdminCatalogJSON(w, http.StatusOK, channelCatalogDeleteResponse{
			Object:  "admin_channel_deleted",
			ID:      item.ID,
			Deleted: true,
		})
	}
}

func channelCatalogStore(d AdminChannelCatalogDeps) adminChannelCatalogStore {
	if d.Store != nil {
		return d.Store
	}
	if store, ok := d.Queries.(adminChannelCatalogStore); ok {
		return store
	}
	return nil
}

func resolveChannelCatalogMutationAdmin(w http.ResponseWriter, r *http.Request, d AdminChannelCatalogDeps, store adminChannelCatalogStore) (admin.AdminIdentity, int64, bool) {
	if d.Auth == nil || store == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_not_configured",
			"admin channel catalog mutation dependency unset")
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

func parseChannelCatalogID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(chi.URLParam(r, "id"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "channel_id_required", "channel id is required")
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_channel_id", "channel id must be a positive integer")
		return 0, false
	}
	return id, true
}

func validateChannelCatalogCreateRequest(w http.ResponseWriter, tenantID int64, req channelCatalogMutationRequest) (channelCatalogCreateParams, bool) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "channel_name_required", "channel name is required")
		return channelCatalogCreateParams{}, false
	}
	if req.PoolGroupID == nil || *req.PoolGroupID <= 0 {
		writeError(w, http.StatusBadRequest, "channel_pool_group_required", "channel pool_group_id is required")
		return channelCatalogCreateParams{}, false
	}
	enabled, ok := validateChannelCatalogEnabled(w, req.Enabled)
	if !ok {
		return channelCatalogCreateParams{}, false
	}
	codes, ok := validateChannelCatalogFailoverCodes(w, req.FailoverStatusCodes, true)
	if !ok {
		return channelCatalogCreateParams{}, false
	}
	return channelCatalogCreateParams{
		TenantID: tenantID, PoolGroupID: *req.PoolGroupID, Name: name,
		FailoverStatusCodes: codes, Enabled: enabled,
	}, true
}

func validateChannelCatalogUpdateRequest(w http.ResponseWriter, tenantID, id int64, req channelCatalogMutationRequest) (channelCatalogUpdateParams, bool) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "channel_name_required", "channel name is required")
		return channelCatalogUpdateParams{}, false
	}
	if req.PoolGroupID == nil || *req.PoolGroupID <= 0 {
		writeError(w, http.StatusBadRequest, "channel_pool_group_required", "channel pool_group_id is required")
		return channelCatalogUpdateParams{}, false
	}
	enabled, ok := validateChannelCatalogEnabled(w, req.Enabled)
	if !ok {
		return channelCatalogUpdateParams{}, false
	}
	codes, ok := validateChannelCatalogFailoverCodes(w, req.FailoverStatusCodes, false)
	if !ok {
		return channelCatalogUpdateParams{}, false
	}
	return channelCatalogUpdateParams{
		TenantID: tenantID, ID: id, PoolGroupID: *req.PoolGroupID, Name: name,
		FailoverStatusCodes: codes, Enabled: enabled,
	}, true
}

func validateChannelCatalogEnabled(w http.ResponseWriter, enabled *bool) (bool, bool) {
	if enabled == nil {
		writeError(w, http.StatusBadRequest, "channel_enabled_required", "channel enabled is required")
		return false, false
	}
	return *enabled, true
}

// validateChannelCatalogFailoverCodes validates HTTP status codes and applies
// the column default on create when the list is omitted. Empty-on-update is an
// explicit "clear to default" too, keeping a row from ever holding no codes.
func validateChannelCatalogFailoverCodes(w http.ResponseWriter, codes []int32, _ bool) ([]int32, bool) {
	if len(codes) == 0 {
		out := make([]int32, len(defaultFailoverStatusCodes))
		copy(out, defaultFailoverStatusCodes)
		return out, true
	}
	for _, c := range codes {
		if c < 100 || c > 599 {
			writeError(w, http.StatusBadRequest, "invalid_failover_status_code",
				fmt.Sprintf("failover status code %d is out of range", c))
			return nil, false
		}
	}
	return codes, true
}

func decodeChannelCatalogMutationJSON(w http.ResponseWriter, r *http.Request, dst any, required bool) bool {
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

func buildChannelCatalogAuditParams(w http.ResponseWriter, r *http.Request, ident admin.AdminIdentity, tenantID int64, action, reason string, payload map[string]any) (admindb.InsertAdminAuditEventParams, bool) {
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
		TenantID: &tenantID, ActorID: fmt.Sprintf("%d", ident.TokenID), ActorRole: actorRole,
		Action: action, TargetType: "channel", RequestID: reqIDArg,
		Reason: reasonArg, Payload: raw,
	}, true
}

func writeChannelCatalogMutationError(w http.ResponseWriter, err error, fallbackCode string) {
	switch {
	case errors.Is(err, errChannelCatalogNameConflict):
		writeError(w, http.StatusConflict, "channel_name_conflict", "channel name already exists in pool group")
	case errors.Is(err, errChannelCatalogPoolNotFound):
		writeError(w, http.StatusBadRequest, "channel_pool_group_not_found", "pool group not found for tenant")
	case errors.Is(err, errChannelCatalogNotFound), errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "channel_not_found", "channel not found")
	default:
		writeError(w, http.StatusServiceUnavailable, fallbackCode, err.Error())
	}
}

func normalizeChannelCatalogDBError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) &&
		pgErr.Code == "23505" &&
		pgErr.ConstraintName == "uq_channels_tenant_pool_name" {
		return errChannelCatalogNameConflict
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errChannelCatalogNotFound
	}
	return err
}

func channelCatalogItemFromCreateRow(row admindb.CreateChannelRow) channelCatalogItem {
	return channelCatalogItem{
		ID: row.ID, PoolGroupID: row.PoolGroupID, Name: row.Name,
		FailoverStatusCodes: row.FailoverStatusCodes, Enabled: row.Enabled,
		CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func channelCatalogItemFromUpdateRow(row admindb.UpdateChannelRow) channelCatalogItem {
	return channelCatalogItem{
		ID: row.ID, PoolGroupID: row.PoolGroupID, Name: row.Name,
		FailoverStatusCodes: row.FailoverStatusCodes, Enabled: row.Enabled,
		CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}

func channelCatalogItemFromSoftDeleteRow(row admindb.SoftDeleteChannelRow) channelCatalogItem {
	return channelCatalogItem{
		ID: row.ID, PoolGroupID: row.PoolGroupID, Name: row.Name,
		FailoverStatusCodes: row.FailoverStatusCodes, Enabled: row.Enabled,
		CreatedAt: formatCatalogTime(row.CreatedAt),
	}
}
