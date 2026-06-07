// Package registry: error classes (D4 + D6 mapping).
//
// HTTP mapping (handled in chat handler):
//
//	ErrUnknownModel     -> 404 model_not_available
//	ErrModelDisabled    -> 404 model_not_available  (uniform per D4 anti-enum)
//	ErrTenantNoAccess   -> 404 model_not_available
//	ErrRegistryBackend  -> 503 registry_backend_error
//
// The four classes exist so audit logs and structured fields can record
// the precise internal reason while the public response stays uniform —
// per docs/process/plans/2026-04-30-n5-model-registry.md D4.

package registry

import "errors"

// ErrUnknownModel is returned when no matching alias row exists for the
// (tenant_id, alias_normalized) pair AND either the tenant has no
// inherit_global_catalog policy or the global lookup also misses.
var ErrUnknownModel = errors.New("registry: unknown model alias")

// ErrModelDisabled is returned when an alias resolves but the alias row
// or the canonical model row has status != 'active'. A tenant-scoped
// disabled alias ALSO blocks global fallback (D3 explicit deny) and
// surfaces as ErrModelDisabled — never as ErrUnknownModel.
var ErrModelDisabled = errors.New("registry: model disabled")

// ErrTenantNoAccess is returned when an alias resolves and the model is
// active, but no enabled pool binding survives the effective_from/until
// filter. From the operator POV the model is "configured but unrouted".
var ErrTenantNoAccess = errors.New("registry: model has no eligible pool binding")

// ErrRegistryBackend wraps any datastore failure during resolve. The
// handler maps this to HTTP 503 — NOT 404 — so legitimate clients are
// not told their valid alias does not exist during an infra outage.
// Mirrors auth.ErrAuthBackend.
var ErrRegistryBackend = errors.New("registry: backend datastore error")

// ErrInvalidModelCapability is returned by admin writers before touching the
// datastore when a model-capability binding uses a value outside HUAKAI's
// known model capability vocabulary.
var ErrInvalidModelCapability = errors.New("registry: invalid model capability")
