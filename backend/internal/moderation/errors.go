package moderation

import "errors"

var (
	ErrModerationDisabled = errors.New("moderation: disabled")
	ErrScreenerBackend    = errors.New("moderation: screener backend")
	ErrKeywordExists      = errors.New("moderation: keyword exists")
	ErrNotFound           = errors.New("moderation: not found")
)
