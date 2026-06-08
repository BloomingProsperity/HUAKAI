package affinityrules

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

const (
	KeySourceRequestHeader = "request_header"
	KeySourceGJSON         = "gjson"
)

type KeySource struct {
	Type string
	Key  string
	Path string
}

type AffinityRule struct {
	Name             string
	ModelRegex       []string
	PathRegex        []string
	UserAgentInclude []string
	KeySources       []KeySource
	ValueRegex       string
	IncludeRuleName  bool
}

type AffinityRuleSet []AffinityRule

type MatchRequest struct {
	Model     string
	Path      string
	UserAgent string
	Header    func(string) string
	Body      []byte
}

func (s AffinityRuleSet) Match(req MatchRequest) (ruleName string, affinityKey string, matched bool) {
	for _, rule := range s {
		if !rule.matches(req) {
			continue
		}
		key := rule.extractKey(req)
		if key == "" {
			continue
		}
		name := strings.TrimSpace(rule.Name)
		if rule.IncludeRuleName && name != "" {
			key = name + ":" + key
		}
		return rule.Name, key, true
	}
	return "", "", false
}

func (r AffinityRule) matches(req MatchRequest) bool {
	if !regexListMatches(r.ModelRegex, req.Model) {
		return false
	}
	if !regexListMatches(r.PathRegex, req.Path) {
		return false
	}
	return userAgentIncludes(req.UserAgent, r.UserAgentInclude)
}

func regexListMatches(patterns []string, value string) bool {
	hasPattern := false
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		hasPattern = true
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false
		}
		if re.MatchString(value) {
			return true
		}
	}
	return !hasPattern
}

func userAgentIncludes(userAgent string, includes []string) bool {
	for _, include := range includes {
		include = strings.TrimSpace(include)
		if include == "" {
			continue
		}
		if !strings.Contains(userAgent, include) {
			return false
		}
	}
	return true
}

func (r AffinityRule) extractKey(req MatchRequest) string {
	for _, source := range r.KeySources {
		value := extractSourceValue(source, req)
		if value == "" {
			continue
		}
		value = applyValueRegex(value, r.ValueRegex)
		if value != "" {
			return value
		}
	}
	return ""
}

func extractSourceValue(source KeySource, req MatchRequest) string {
	switch strings.ToLower(strings.TrimSpace(source.Type)) {
	case KeySourceRequestHeader:
		if req.Header == nil || strings.TrimSpace(source.Key) == "" {
			return ""
		}
		return strings.TrimSpace(req.Header(source.Key))
	case KeySourceGJSON:
		return jsonPathString(req.Body, source.Path)
	default:
		return ""
	}
}

func applyValueRegex(value, pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return strings.TrimSpace(value)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return ""
	}
	matches := re.FindStringSubmatch(value)
	if matches == nil {
		return ""
	}
	if len(matches) == 1 {
		return strings.TrimSpace(matches[0])
	}
	for _, match := range matches[1:] {
		match = strings.TrimSpace(match)
		if match != "" {
			return match
		}
	}
	return ""
}

func jsonPathString(body []byte, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || len(body) == 0 {
		return ""
	}
	raw := json.RawMessage(body)
	for _, segment := range strings.Split(path, ".") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return ""
		}
		next, ok := jsonPathChild(raw, segment)
		if !ok {
			return ""
		}
		raw = next
	}
	return jsonScalarString(raw)
}

func jsonPathChild(raw json.RawMessage, segment string) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false
	}
	switch trimmed[0] {
	case '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &obj); err != nil {
			return nil, false
		}
		child, ok := obj[segment]
		return child, ok
	case '[':
		index, err := strconv.Atoi(segment)
		if err != nil || index < 0 {
			return nil, false
		}
		var arr []json.RawMessage
		if err := json.Unmarshal(trimmed, &arr); err != nil || index >= len(arr) {
			return nil, false
		}
		return arr[index], true
	default:
		return nil, false
	}
}

func jsonScalarString(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return ""
		}
		return strings.TrimSpace(s)
	case '{', '[':
		return ""
	default:
		if !json.Valid(trimmed) {
			return ""
		}
		return string(trimmed)
	}
}
