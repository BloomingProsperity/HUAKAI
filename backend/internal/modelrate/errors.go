package modelrate

import "errors"

var (
	ErrNotFound           = errors.New("modelrate: override not found")
	ErrBackend            = errors.New("modelrate: backend unavailable")
	ErrInvalidInput       = errors.New("modelrate: invalid input")
	ErrOutOfRange         = errors.New("modelrate: rate out of range")
	ErrAuditSignerMissing = errors.New("modelrate: audit signer missing")
	ErrAuditTxMissing     = errors.New("modelrate: audit transaction missing")
	ErrStoreNotConfigured = errors.New("modelrate: store not configured")
)
