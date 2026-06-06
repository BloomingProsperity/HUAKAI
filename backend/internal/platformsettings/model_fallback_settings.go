package platformsettings

import (
	"encoding/json"
	"fmt"
	"strings"
)

const modelFallbackMaxDepth = 10

type modelFallbackChainsConfig struct {
	Enabled       bool                `json:"enabled"`
	MaxDepth      int                 `json:"max_depth"`
	General       map[string][]string `json:"general"`
	ContextWindow map[string][]string `json:"context_window"`
	ContentPolicy map[string][]string `json:"content_policy"`
}

var allowedModelFallbackBuckets = map[string]struct{}{
	"enabled":        {},
	"max_depth":      {},
	"general":        {},
	"context_window": {},
	"content_policy": {},
}

func validateModelFallbackChainsValue(key SettingKey, value string) (string, error) {
	if value == "" {
		return "", nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &top); err != nil || top == nil {
		return "", fmt.Errorf("%w: %s must be a JSON object", ErrInvalidValue, key)
	}
	for bucket := range top {
		if _, ok := allowedModelFallbackBuckets[bucket]; !ok {
			return "", fmt.Errorf("%w: %s contains unknown model fallback bucket %q", ErrInvalidValue, key, bucket)
		}
	}
	if err := validateModelFallbackBoolField(key, top, "enabled"); err != nil {
		return "", err
	}
	if err := validateModelFallbackMaxDepth(key, top); err != nil {
		return "", err
	}
	var cfg modelFallbackChainsConfig
	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		return "", fmt.Errorf("%w: %s must match model fallback config schema", ErrInvalidValue, key)
	}
	for _, bucket := range []struct {
		name   string
		chains map[string][]string
	}{
		{name: "general", chains: cfg.General},
		{name: "context_window", chains: cfg.ContextWindow},
		{name: "content_policy", chains: cfg.ContentPolicy},
	} {
		if err := validateModelFallbackBucket(key, bucket.name, bucket.chains, top); err != nil {
			return "", err
		}
	}
	return value, nil
}

func validateModelFallbackBoolField(key SettingKey, top map[string]json.RawMessage, field string) error {
	raw, ok := top[field]
	if !ok {
		return nil
	}
	var parsed *bool
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed == nil {
		return fmt.Errorf("%w: %s %s must be boolean", ErrInvalidValue, key, field)
	}
	return nil
}

func validateModelFallbackMaxDepth(key SettingKey, top map[string]json.RawMessage) error {
	raw, ok := top["max_depth"]
	if !ok {
		return nil
	}
	var maxDepth *int
	if err := json.Unmarshal(raw, &maxDepth); err != nil || maxDepth == nil {
		return fmt.Errorf("%w: %s max_depth must be integer between 1 and %d", ErrInvalidValue, key, modelFallbackMaxDepth)
	}
	if *maxDepth < 1 || *maxDepth > modelFallbackMaxDepth {
		return fmt.Errorf("%w: %s max_depth must be between 1 and %d", ErrInvalidValue, key, modelFallbackMaxDepth)
	}
	return nil
}

func validateModelFallbackBucket(key SettingKey, name string, chains map[string][]string, top map[string]json.RawMessage) error {
	raw, ok := top[name]
	if !ok {
		return nil
	}
	var rawObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawObject); err != nil || rawObject == nil {
		return fmt.Errorf("%w: %s %s must be a JSON object of fallback chains", ErrInvalidValue, key, name)
	}
	normalized := make(map[string][]string, len(chains))
	for model, chain := range chains {
		source := strings.TrimSpace(model)
		if source == "" {
			return fmt.Errorf("%w: %s %s contains empty model name", ErrInvalidValue, key, name)
		}
		if len(chain) == 0 {
			return fmt.Errorf("%w: %s %s chain for %q must be a non-empty string array", ErrInvalidValue, key, name, source)
		}
		for _, candidate := range chain {
			target := strings.TrimSpace(candidate)
			if target == "" {
				return fmt.Errorf("%w: %s %s chain for %q contains empty model name", ErrInvalidValue, key, name, source)
			}
			normalized[source] = append(normalized[source], target)
		}
	}
	return validateModelFallbackAcyclic(key, name, normalized)
}

func validateModelFallbackAcyclic(key SettingKey, bucket string, chains map[string][]string) error {
	for source, targets := range chains {
		for _, target := range targets {
			if source == target {
				return fmt.Errorf("%w: %s %s chain for %q references itself", ErrInvalidValue, key, bucket, source)
			}
			for _, backTarget := range chains[target] {
				if backTarget == source {
					return fmt.Errorf("%w: %s %s chains contain cycle %q -> %q -> %q", ErrInvalidValue, key, bucket, source, target, source)
				}
			}
		}
	}
	return nil
}
