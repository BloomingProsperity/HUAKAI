// Package registry: alias normalization (D14).
//
// Public model aliases are operator identifiers, not case-sensitive
// secrets. Normalize for unique-index lookup; preserve as-seeded casing
// in `public_alias_display` for audit/`/models` endpoint output.

package registry

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// AliasNormalize maps a public alias string to its lookup form.
//
// Steps:
//  1. Trim leading/trailing whitespace (operators paste from docs).
//  2. NFC-normalize so accent-composition variants collapse.
//  3. Lower-case.
//
// The result is what `model_aliases.public_alias_normalized` stores and
// what the lookup query matches against. Original casing is preserved by
// the caller and stored in `public_alias_display`.
func AliasNormalize(alias string) string {
	if alias == "" {
		return ""
	}
	trimmed := strings.TrimFunc(alias, unicode.IsSpace)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(norm.NFC.String(trimmed))
}
