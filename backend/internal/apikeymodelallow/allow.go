package apikeymodelallow

import "strings"

func Normalize(entries []string) []string {
	out := make([]string, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, raw := range entries {
		normalized, ok := normalizeModel(raw)
		if !ok {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func NormalizeCSV(raw string) []string {
	return Normalize(strings.Split(raw, ","))
}

func StorageText(entries []string) *string {
	normalized := Normalize(entries)
	if len(normalized) == 0 {
		return nil
	}
	value := strings.Join(normalized, ",")
	return &value
}

func AllowsCSV(raw *string, requestedModel string) bool {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return true
	}
	entries := NormalizeCSV(*raw)
	if len(entries) == 0 {
		return true
	}
	model, ok := normalizeModel(requestedModel)
	if !ok {
		return false
	}
	for _, entry := range entries {
		if entry == model {
			return true
		}
	}
	return false
}

func normalizeModel(raw string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", false
	}
	return s, true
}
