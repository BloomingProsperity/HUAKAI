package userkeycontrols

import "errors"

var (
	ErrInvalidQuota          = errors.New("userkeycontrols: quota invalid")
	ErrInvalidGroup          = errors.New("userkeycontrols: group invalid")
	ErrInvalidIPAllowlist    = errors.New("userkeycontrols: ip allowlist invalid")
	ErrInvalidModelAllowlist = errors.New("userkeycontrols: model allowlist invalid")
	ErrKeyNotFound           = errors.New("userkeycontrols: api_key not found for owner")
	ErrQuotaPolicyNotFound   = errors.New("userkeycontrols: api_key quota policy not found")
	ErrGroupNotFound         = errors.New("userkeycontrols: api_key group not found")
	ErrServiceMisconfig      = errors.New("userkeycontrols: service not configured")
	ErrBackend               = errors.New("userkeycontrols: backend datastore error")
)
