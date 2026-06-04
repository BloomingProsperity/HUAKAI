package budget

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

func EncodeScope(scope Scope) (string, error) {
	if scope.TenantID <= 0 {
		return "", fmt.Errorf("budget: tenant_id must be positive")
	}
	id := strings.TrimSpace(scope.ID)
	if id == "" {
		return "", fmt.Errorf("budget: scope id required")
	}
	tenant := intString(scope.TenantID)
	safeID := safeSegment(id)
	model := strings.TrimSpace(scope.Model)
	switch scope.Kind {
	case ScopeUser:
		if model != "" {
			return "um:" + tenant + ":" + safeID + ":" + safeSegment(model), nil
		}
		return "u:" + tenant + ":" + safeID, nil
	case ScopeAPIKey:
		if model != "" {
			return "km:" + tenant + ":" + safeID + ":" + safeSegment(model), nil
		}
		return "k:" + tenant + ":" + safeID, nil
	case ScopePoolGroup:
		return "g:" + tenant + ":" + safeID, nil
	default:
		return "", fmt.Errorf("budget: unsupported scope kind %q", scope.Kind)
	}
}

func RedisCounterKey(scope Scope, counter Counter, minute int64) string {
	encoded, err := EncodeScope(scope)
	if err != nil {
		encoded = "invalid"
	}
	suffix := "r"
	if counter == CounterTPM {
		suffix = "t"
	}
	return fmt.Sprintf("bgt:{%s}:%s:%d", encoded, suffix, minute)
}

func redisScopePrefix(scope Scope) (string, error) {
	encoded, err := EncodeScope(scope)
	if err != nil {
		return "", err
	}
	return "bgt:{" + encoded + "}:", nil
}

func safeSegment(value string) string {
	if value == "*" {
		return "*"
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil && n >= 0 {
		return intString(n)
	}
	return "b64_" + base64.RawURLEncoding.EncodeToString([]byte(value))
}
