// Package tlsfphttp exposes admin HTTP CRUD for TLS fingerprint profiles
// (F-FP-POOL Phase 1.3). It is NOT in the frozen gatewayhttp package; it is
// mounted by cmd/gateway/routes.go under /admin/v1/tls-fingerprint-profiles.
// Only platform_admin may access. tenant_id for by-id operations is taken from
// the ?tenant_id query parameter (consistent with routeadminhttp); create takes
// tenant_id from the body. status changes go through POST /{id}/status only —
// PUT rejects a status field via DisallowUnknownFields.
package tlsfphttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/tlsfpadmin"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// AdminAuth resolves inbound admin credentials.
type AdminAuth interface {
	Resolve(context.Context, *http.Request) (admin.AdminIdentity, error)
}

// Service is the tlsfpadmin capability subset the handlers depend on
// (implemented by *tlsfpadmin.Service). Declared as an interface for testability.
type Service interface {
	List(context.Context, int64) ([]tlsfpadmin.Profile, error)
	Get(context.Context, int64, int64) (tlsfpadmin.Profile, error)
	Create(context.Context, tlsfpadmin.CreateInput) (tlsfpadmin.Profile, error)
	Update(context.Context, tlsfpadmin.UpdateInput) (tlsfpadmin.Profile, error)
	SetStatus(context.Context, tlsfpadmin.SetStatusInput) (tlsfpadmin.Profile, error)
	Delete(context.Context, int64, int64) error
}

// AdminDeps holds handler dependencies.
type AdminDeps struct {
	Auth    AdminAuth
	Service Service
}

type createRequest struct {
	TenantID             int64    `json:"tenant_id"`
	Name                 string   `json:"name"`
	Description          *string  `json:"description"`
	GreaseEnabled        bool     `json:"grease_enabled"`
	CipherSuites         []int32  `json:"cipher_suites"`
	SupportedCurves      []int32  `json:"supported_curves"`
	EcPointFormats       []int32  `json:"ec_point_formats"`
	SignatureAlgorithms  []int32  `json:"signature_algorithms"`
	AlpnProtocols        []string `json:"alpn_protocols"`
	TLSSupportedVersions []int32  `json:"tls_supported_versions"`
	KeyShareGroups       []int32  `json:"key_share_groups"`
	PskModes             []int32  `json:"psk_modes"`
	ExtensionsOrder      []int32  `json:"extensions_order"`
	ExpectedJA3Hash      string   `json:"expected_ja3_hash"`
}

// updateRequest deliberately omits tenant_id (from query), id (from path), and
// status (POST /{id}/status only). DisallowUnknownFields rejects a smuggled status.
type updateRequest struct {
	Name                 string   `json:"name"`
	Description          *string  `json:"description"`
	GreaseEnabled        bool     `json:"grease_enabled"`
	CipherSuites         []int32  `json:"cipher_suites"`
	SupportedCurves      []int32  `json:"supported_curves"`
	EcPointFormats       []int32  `json:"ec_point_formats"`
	SignatureAlgorithms  []int32  `json:"signature_algorithms"`
	AlpnProtocols        []string `json:"alpn_protocols"`
	TLSSupportedVersions []int32  `json:"tls_supported_versions"`
	KeyShareGroups       []int32  `json:"key_share_groups"`
	PskModes             []int32  `json:"psk_modes"`
	ExtensionsOrder      []int32  `json:"extensions_order"`
	ExpectedJA3Hash      string   `json:"expected_ja3_hash"`
}

type setStatusRequest struct {
	Status string `json:"status"`
}

// MountTLSFPAdminRoutes mounts the six CRUD endpoints on r.
func MountTLSFPAdminRoutes(r chi.Router, d AdminDeps) {
	r.Get("/", listHandler(d))
	r.Post("/", createHandler(d))
	r.Get("/{id}", getHandler(d))
	r.Put("/{id}", updateHandler(d))
	r.Post("/{id}/status", setStatusHandler(d))
	r.Delete("/{id}", deleteHandler(d))
}

func listHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		profiles, err := d.Service.List(r.Context(), tenantID)
		if err != nil {
			writeTLSFPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "tls_fingerprint_profiles_list", "items": profiles})
	}
}

func getHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		profile, err := d.Service.Get(r.Context(), tenantID, id)
		if err != nil {
			writeTLSFPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
	}
}

func createHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		var req createRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		profile, err := d.Service.Create(r.Context(), tlsfpadmin.CreateInput{
			TenantID: req.TenantID, Name: req.Name, Description: req.Description, GreaseEnabled: req.GreaseEnabled,
			CipherSuites: req.CipherSuites, SupportedCurves: req.SupportedCurves, EcPointFormats: req.EcPointFormats,
			SignatureAlgorithms: req.SignatureAlgorithms, AlpnProtocols: req.AlpnProtocols, TLSSupportedVersions: req.TLSSupportedVersions,
			KeyShareGroups: req.KeyShareGroups, PskModes: req.PskModes, ExtensionsOrder: req.ExtensionsOrder,
			ExpectedJA3Hash: req.ExpectedJA3Hash,
		})
		if err != nil {
			writeTLSFPError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"profile": profile})
	}
}

func updateHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req updateRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		profile, err := d.Service.Update(r.Context(), tlsfpadmin.UpdateInput{
			TenantID: tenantID, ID: id, Name: req.Name, Description: req.Description, GreaseEnabled: req.GreaseEnabled,
			CipherSuites: req.CipherSuites, SupportedCurves: req.SupportedCurves, EcPointFormats: req.EcPointFormats,
			SignatureAlgorithms: req.SignatureAlgorithms, AlpnProtocols: req.AlpnProtocols, TLSSupportedVersions: req.TLSSupportedVersions,
			KeyShareGroups: req.KeyShareGroups, PskModes: req.PskModes, ExtensionsOrder: req.ExtensionsOrder,
			ExpectedJA3Hash: req.ExpectedJA3Hash,
		})
		if err != nil {
			writeTLSFPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
	}
}

func setStatusHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		var req setStatusRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		profile, err := d.Service.SetStatus(r.Context(), tlsfpadmin.SetStatusInput{TenantID: tenantID, ID: id, Status: req.Status})
		if err != nil {
			writeTLSFPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": profile})
	}
}

func deleteHandler(d AdminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolveAdmin(w, r, d); !ok {
			return
		}
		tenantID, ok := parsePositiveQuery(w, r, "tenant_id")
		if !ok {
			return
		}
		id, ok := parsePathID(w, r)
		if !ok {
			return
		}
		if err := d.Service.Delete(r.Context(), tenantID, id); err != nil {
			writeTLSFPError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
	}
}

func resolveAdmin(w http.ResponseWriter, r *http.Request, d AdminDeps) (admin.AdminIdentity, bool) {
	if d.Auth == nil || d.Service == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "gateway_not_configured", "tls fingerprint admin dependency unset")
		return admin.AdminIdentity{}, false
	}
	ident, err := d.Auth.Resolve(r.Context(), r)
	if err != nil {
		if errors.Is(err, admin.ErrAdminBackend) {
			writeJSONError(w, http.StatusServiceUnavailable, "admin_backend_error", "admin auth backend transient failure")
		} else {
			writeJSONError(w, http.StatusUnauthorized, "admin_unauthorized", "missing or invalid admin credential")
		}
		return admin.AdminIdentity{}, false
	}
	if ident.Role != admin.RolePlatformAdmin {
		writeJSONError(w, http.StatusForbidden, "admin_forbidden", "platform_admin role required")
		return admin.AdminIdentity{}, false
	}
	return ident, true
}

func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSONError(w, http.StatusBadRequest, "invalid_profile_id", "profile id must be a positive int64")
		return 0, false
	}
	return id, true
}

func parsePositiveQuery(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		writeJSONError(w, http.StatusBadRequest, name+"_required", name+" query parameter must be positive")
		return 0, false
	}
	return n, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "request body is not valid JSON or contains unknown fields")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeJSONError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// writeTLSFPError maps tlsfpadmin sentinels to HTTP status codes. It never echoes
// the raw error (CMB-5): ErrBackend yields a fixed generic message.
func writeTLSFPError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tlsfpadmin.ErrInvalidInput):
		writeJSONError(w, http.StatusBadRequest, "invalid_request", "profile request is invalid")
	case errors.Is(err, tlsfpadmin.ErrInvalidStatus):
		writeJSONError(w, http.StatusBadRequest, "invalid_status", "status must be one of: active, disabled")
	case errors.Is(err, tlsfpadmin.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, "profile_not_found", "tls fingerprint profile not found for tenant")
	case errors.Is(err, tlsfpadmin.ErrDuplicateName):
		writeJSONError(w, http.StatusConflict, "profile_name_conflict", "profile name already exists for tenant")
	default:
		writeJSONError(w, http.StatusServiceUnavailable, "tls_fp_admin_backend_error", "tls fingerprint admin service unavailable")
	}
}
