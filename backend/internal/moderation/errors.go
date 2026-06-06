package moderation

import "errors"

var (
	ErrModerationDisabled = errors.New("moderation: disabled")
	ErrScreenerBackend    = errors.New("moderation: screener backend")
	ErrKeywordExists      = errors.New("moderation: keyword exists")
	ErrHashExists         = errors.New("moderation: hash exists")
	ErrNotFound           = errors.New("moderation: not found")
	ErrBulkImportTooLarge = errors.New("moderation: bulk import too large")
)
