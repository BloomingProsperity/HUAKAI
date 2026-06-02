// Package tlsfpadmin provides the validation + error-mapping service layer for
// admin CRUD of TLS fingerprint profiles (F-FP-POOL Phase 1.3). The sqlc/querier
// layer in internal/db/admin already implements the SQL; this package adds input
// validation, typed sentinel errors, and not-found detection for the `:exec`
// SetStatus/SoftDelete queries (which return nil on zero rows) via a pre-flight
// existence check. It imports no router/auth/gateway packages (CMB-1).
package tlsfpadmin

import (
	"errors"
	"time"
)

// Sentinel errors. The HTTP layer maps these to status codes; callers use
// errors.Is. ErrBackend wraps any unexpected querier/DB failure.
var (
	ErrInvalidInput  = errors.New("tlsfpadmin: invalid input")
	ErrInvalidStatus = errors.New("tlsfpadmin: invalid status")
	ErrNotFound      = errors.New("tlsfpadmin: profile not found")
	ErrDuplicateName = errors.New("tlsfpadmin: duplicate profile name")
	ErrBackend       = errors.New("tlsfpadmin: backend failure")
)

// adminSettableStatuses are the status values a platform_admin may set via the
// status endpoint. "drift_detected" is intentionally excluded — only the
// drift-detection worker (Phase 3) sets it, writing through the sqlc layer
// directly. Setting "active" on a drift_detected profile is the intentional
// admin-override "clear drift" path (the SQL refreshes last_validated_at).
var adminSettableStatuses = map[string]bool{"active": true, "disabled": true}

// Profile is the admin-facing DTO. The drift metadata (ExpectedJA3Hash,
// LastValidatedAt) is exposed read-only; it is managed by the drift worker.
type Profile struct {
	ID                   int64      `json:"id"`
	TenantID             int64      `json:"tenant_id"`
	Name                 string     `json:"name"`
	Description          *string    `json:"description,omitempty"`
	GreaseEnabled        bool       `json:"grease_enabled"`
	CipherSuites         []int32    `json:"cipher_suites"`
	SupportedCurves      []int32    `json:"supported_curves"`
	EcPointFormats       []int32    `json:"ec_point_formats"`
	SignatureAlgorithms  []int32    `json:"signature_algorithms"`
	AlpnProtocols        []string   `json:"alpn_protocols"`
	TLSSupportedVersions []int32    `json:"tls_supported_versions"`
	KeyShareGroups       []int32    `json:"key_share_groups"`
	PskModes             []int32    `json:"psk_modes"`
	ExtensionsOrder      []int32    `json:"extensions_order"`
	ExpectedJA3Hash      string     `json:"expected_ja3_hash"`
	Status               string     `json:"status"`
	LastValidatedAt      *time.Time `json:"last_validated_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// CreateInput holds the fields for creating a profile (status defaults to
// 'active' at the DB layer; it is not settable on create).
type CreateInput struct {
	TenantID             int64
	Name                 string
	Description          *string
	GreaseEnabled        bool
	CipherSuites         []int32
	SupportedCurves      []int32
	EcPointFormats       []int32
	SignatureAlgorithms  []int32
	AlpnProtocols        []string
	TLSSupportedVersions []int32
	KeyShareGroups       []int32
	PskModes             []int32
	ExtensionsOrder      []int32
	ExpectedJA3Hash      string
}

// UpdateInput is a full-field content update. Status is intentionally absent —
// status changes travel through SetStatus only.
type UpdateInput struct {
	TenantID             int64
	ID                   int64
	Name                 string
	Description          *string
	GreaseEnabled        bool
	CipherSuites         []int32
	SupportedCurves      []int32
	EcPointFormats       []int32
	SignatureAlgorithms  []int32
	AlpnProtocols        []string
	TLSSupportedVersions []int32
	KeyShareGroups       []int32
	PskModes             []int32
	ExtensionsOrder      []int32
	ExpectedJA3Hash      string
}

// SetStatusInput holds a status transition request.
type SetStatusInput struct {
	TenantID int64
	ID       int64
	Status   string
}
