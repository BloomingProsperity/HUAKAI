package credentialacq

import (
	"encoding/csv"
	"encoding/json"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func ParseCLIImportContent(input string) ([]CredentialCandidate, error) {
	return ParseImportContent(input, credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth)
}

func ParseImportContent(input, defaultVendor, defaultAuthMode string) ([]CredentialCandidate, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, ErrInvalidImportBody
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
		return importCandidatesFromDecoded(decoded, defaultVendor, defaultAuthMode)
	}
	lines := strings.Split(trimmed, "\n")
	out := make([]CredentialCandidate, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var one any
		if err := json.Unmarshal([]byte(line), &one); err == nil {
			got, err := importCandidatesFromDecoded(one, defaultVendor, defaultAuthMode)
			if err != nil {
				return nil, err
			}
			out = append(out, got...)
			continue
		}
		out = append(out, importTokenCandidate(line, defaultVendor, defaultAuthMode))
	}
	if len(out) == 0 {
		return nil, ErrInvalidImportBody
	}
	return out, nil
}

func ParseCSVImportContent(input, defaultVendor, defaultAuthMode string) ([]CredentialCandidate, error) {
	reader := csv.NewReader(strings.NewReader(input))
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil || len(rows) == 0 {
		return nil, ErrInvalidImportBody
	}
	header := rows[0]
	out := make([]CredentialCandidate, 0, len(rows)-1)
	for _, row := range rows[1:] {
		fields := map[string]any{}
		for i, name := range header {
			if i >= len(row) {
				continue
			}
			name = strings.TrimSpace(name)
			if name != "" {
				fields[name] = strings.TrimSpace(row[i])
			}
		}
		if len(fields) == 0 {
			continue
		}
		out = append(out, importCandidateFromMap(fields, defaultVendor, defaultAuthMode))
	}
	if len(out) == 0 {
		return nil, ErrInvalidImportBody
	}
	return out, nil
}

func importCandidatesFromDecoded(decoded any, defaultVendor, defaultAuthMode string) ([]CredentialCandidate, error) {
	switch v := decoded.(type) {
	case map[string]any:
		return []CredentialCandidate{importCandidateFromMap(v, defaultVendor, defaultAuthMode)}, nil
	case []any:
		out := make([]CredentialCandidate, 0, len(v))
		for _, item := range v {
			switch typed := item.(type) {
			case map[string]any:
				out = append(out, importCandidateFromMap(typed, defaultVendor, defaultAuthMode))
			case string:
				out = append(out, importTokenCandidate(typed, defaultVendor, defaultAuthMode))
			default:
				return nil, ErrInvalidImportBody
			}
		}
		return out, nil
	case string:
		return []CredentialCandidate{importTokenCandidate(v, defaultVendor, defaultAuthMode)}, nil
	default:
		return nil, ErrInvalidImportBody
	}
}

func importCandidateFromMap(fields map[string]any, defaultVendor, defaultAuthMode string) CredentialCandidate {
	vendor := importStringField(fields, "vendor", defaultVendor)
	mode := importStringField(fields, "auth_mode", defaultAuthMode)
	payload, _ := json.Marshal(fields)
	return CredentialCandidate{
		Vendor: credentialstore.Normalize(vendor), AuthMode: credentialstore.Normalize(mode), Payload: payload,
		RedactedContext: map[string]any{"shape": "json_object"},
	}
}

func importTokenCandidate(token, defaultVendor, defaultAuthMode string) CredentialCandidate {
	payload, _ := json.Marshal(map[string]string{"session_token": strings.TrimSpace(token)})
	return CredentialCandidate{
		Vendor: credentialstore.Normalize(defaultVendor), AuthMode: credentialstore.Normalize(defaultAuthMode), Payload: payload,
		RedactedContext: map[string]any{"shape": "single_token"},
	}
}

func importStringField(fields map[string]any, key, fallback string) string {
	switch value := fields[key].(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return fallback
}
