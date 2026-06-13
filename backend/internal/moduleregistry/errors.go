package moduleregistry

import "errors"

// ErrEmptyID is returned by Register when a descriptor has an empty ID. An
// unaddressable module is a programming error (it can never be Get-ed or shown
// to an operator), so it is rejected rather than silently stored.
var ErrEmptyID = errors.New("moduleregistry: descriptor ID must not be empty")
