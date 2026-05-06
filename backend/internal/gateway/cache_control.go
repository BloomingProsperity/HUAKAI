// R7.1: Anthropic Messages API cache_control state analyzer.
// Spec: docs/specs/upstream-credential-management.md §F-AUTH-005 Phase H /
// docs/specs/protocol-translation.md (Anthropic Messages shape).
//
// Read-only inspector + breakpoint planner. Pure JSON traversal — no IO,
// no network, no credential contact, no body mutation. R7.2 (mutator) is
// the next atomic piece.
//
// Anthropic documents a hard cap of 4 cache_control breakpoints per request.
// This module surfaces that cap to callers and helps choose where to place
// new breakpoints when room remains.
//
// Synthesis of two parallel-draft lanes (CLAUDE.md #10 + 2026-05-04 directive).
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
)

// CacheControlMaxAllowed is the Anthropic-documented per-request breakpoint cap.
const CacheControlMaxAllowed = 4

// CacheControlLocation identifies where a cache_control field was found.
type CacheControlLocation struct {
	Path  string // "system" | "messages" | "tools"
	Index int    // array index; -1 means top-level (e.g. system as a single object)
	Type  string // cache_control type (e.g. "ephemeral")
}

// CacheControlSnapshot summarizes all cache_control occurrences in a request.
type CacheControlSnapshot struct {
	Count      int
	Locations  []CacheControlLocation
	MaxAllowed int
}

// BreakpointSuggestion describes which positions to add cache_control to and
// which positions were skipped due to the MaxAllowed cap. Both fields are
// human-actionable plans only — body is never mutated.
type BreakpointSuggestion struct {
	Add     []CacheControlLocation
	Skipped []string
}

// InspectCacheControl parses an Anthropic Messages API request body and
// returns the cache_control snapshot. Returns an error on invalid JSON or
// schema (e.g. missing messages, wrong role/content types).
func InspectCacheControl(body []byte) (CacheControlSnapshot, error) {
	root, err := decodeMessagesRequest(body)
	if err != nil {
		return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, err
	}
	return inspectCacheControlRoot(root)
}

// SuggestBreakpoints recommends where to add cache_control given the current
// snapshot. Priority order: last system block → last tool definition →
// last user message. Body is never mutated. Already-occupied positions are
// skipped. Candidates beyond MaxAllowed go to Skipped.
func SuggestBreakpoints(body []byte, snapshot CacheControlSnapshot) (BreakpointSuggestion, error) {
	root, err := decodeMessagesRequest(body)
	if err != nil {
		return BreakpointSuggestion{}, err
	}
	if _, err := inspectCacheControlRoot(root); err != nil {
		return BreakpointSuggestion{}, err
	}

	candidates, err := breakpointCandidates(root)
	if err != nil {
		return BreakpointSuggestion{}, err
	}

	maxAllowed := snapshot.MaxAllowed
	if maxAllowed <= 0 {
		maxAllowed = CacheControlMaxAllowed
	}
	remaining := maxAllowed - snapshot.Count
	if remaining < 0 {
		remaining = 0
	}

	var suggestion BreakpointSuggestion
	for _, candidate := range candidates {
		if remaining > 0 {
			suggestion.Add = append(suggestion.Add, candidate)
			remaining--
			continue
		}
		suggestion.Skipped = append(suggestion.Skipped, formatSkipped(candidate))
	}
	return suggestion, nil
}

func decodeMessagesRequest(body []byte) (map[string]interface{}, error) {
	if len(body) == 0 {
		return nil, errors.New("cache_control: request body is empty")
	}
	var root map[string]interface{}
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, fmt.Errorf("cache_control: invalid JSON: %w", err)
	}
	if root == nil {
		return nil, errors.New("cache_control: request body must be a JSON object")
	}
	return root, nil
}

func inspectCacheControlRoot(root map[string]interface{}) (CacheControlSnapshot, error) {
	snapshot := CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}

	if system, ok := root["system"]; ok {
		if err := inspectSystem(system, &snapshot); err != nil {
			return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, err
		}
	}

	messages, ok := root["messages"]
	if !ok {
		return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, errors.New("cache_control: messages must be present")
	}
	if err := inspectMessages(messages, &snapshot); err != nil {
		return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, err
	}

	if tools, ok := root["tools"]; ok {
		if err := inspectTools(tools, &snapshot); err != nil {
			return CacheControlSnapshot{MaxAllowed: CacheControlMaxAllowed}, err
		}
	}

	snapshot.Count = len(snapshot.Locations)
	return snapshot, nil
}

func inspectSystem(value interface{}, snapshot *CacheControlSnapshot) error {
	switch system := value.(type) {
	case string:
		// Plain-string system: no cache_control possible.
		return nil
	case map[string]interface{}:
		// Single-object system: top-level (Index=-1) cache_control if present.
		return appendCacheControl(snapshot, system, CacheControlLocation{Path: "system", Index: -1}, "system")
	case []interface{}:
		for i, item := range system {
			block, err := objectAt(item, fmt.Sprintf("system[%d]", i))
			if err != nil {
				return err
			}
			if err := appendCacheControl(snapshot, block, CacheControlLocation{Path: "system", Index: i}, fmt.Sprintf("system[%d]", i)); err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("cache_control: system must be a string, object, or array")
	}
}

func inspectMessages(value interface{}, snapshot *CacheControlSnapshot) error {
	messages, ok := value.([]interface{})
	if !ok {
		return errors.New("cache_control: messages must be an array")
	}
	for i, item := range messages {
		message, err := objectAt(item, fmt.Sprintf("messages[%d]", i))
		if err != nil {
			return err
		}
		role, ok := message["role"].(string)
		if !ok || role == "" {
			return fmt.Errorf("cache_control: messages[%d].role must be a non-empty string", i)
		}
		content, ok := message["content"]
		if !ok {
			return fmt.Errorf("cache_control: messages[%d].content must be present", i)
		}
		if err := inspectMessageContent(content, i, snapshot); err != nil {
			return err
		}
	}
	return nil
}

func inspectMessageContent(value interface{}, messageIndex int, snapshot *CacheControlSnapshot) error {
	switch content := value.(type) {
	case string:
		return nil
	case []interface{}:
		for i, item := range content {
			block, err := objectAt(item, fmt.Sprintf("messages[%d].content[%d]", messageIndex, i))
			if err != nil {
				return err
			}
			location := CacheControlLocation{Path: "messages", Index: messageIndex}
			if err := appendCacheControl(snapshot, block, location, fmt.Sprintf("messages[%d].content[%d]", messageIndex, i)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("cache_control: messages[%d].content must be a string or array", messageIndex)
	}
}

func inspectTools(value interface{}, snapshot *CacheControlSnapshot) error {
	tools, ok := value.([]interface{})
	if !ok {
		return errors.New("cache_control: tools must be an array")
	}
	for i, item := range tools {
		tool, err := objectAt(item, fmt.Sprintf("tools[%d]", i))
		if err != nil {
			return err
		}
		if err := appendCacheControl(snapshot, tool, CacheControlLocation{Path: "tools", Index: i}, fmt.Sprintf("tools[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

func appendCacheControl(snapshot *CacheControlSnapshot, block map[string]interface{}, location CacheControlLocation, where string) error {
	raw, ok := block["cache_control"]
	if !ok {
		return nil
	}
	cacheType, err := cacheControlType(raw, where)
	if err != nil {
		return err
	}
	location.Type = cacheType
	snapshot.Locations = append(snapshot.Locations, location)
	return nil
}

func cacheControlType(value interface{}, where string) (string, error) {
	control, ok := value.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("cache_control: %s.cache_control must be an object", where)
	}
	cacheType, ok := control["type"].(string)
	if !ok || cacheType == "" {
		return "", fmt.Errorf("cache_control: %s.cache_control.type must be a non-empty string", where)
	}
	return cacheType, nil
}

// breakpointCandidates returns ordered candidates: at each round, system →
// tools → messages, walking each list backward (last block has highest priority).
func breakpointCandidates(root map[string]interface{}) ([]CacheControlLocation, error) {
	system, err := systemCandidates(root)
	if err != nil {
		return nil, err
	}
	tools, err := toolCandidates(root)
	if err != nil {
		return nil, err
	}
	messages, err := userMessageCandidates(root)
	if err != nil {
		return nil, err
	}
	maxLen := len(system)
	if len(tools) > maxLen {
		maxLen = len(tools)
	}
	if len(messages) > maxLen {
		maxLen = len(messages)
	}
	candidates := make([]CacheControlLocation, 0, len(system)+len(tools)+len(messages))
	for i := 0; i < maxLen; i++ {
		if i < len(system) {
			candidates = append(candidates, system[i])
		}
		if i < len(tools) {
			candidates = append(candidates, tools[i])
		}
		if i < len(messages) {
			candidates = append(candidates, messages[i])
		}
	}
	return candidates, nil
}

func systemCandidates(root map[string]interface{}) ([]CacheControlLocation, error) {
	value, ok := root["system"]
	if !ok {
		return nil, nil
	}
	switch system := value.(type) {
	case string:
		return nil, nil
	case map[string]interface{}:
		if hasCacheControl(system) {
			return nil, nil
		}
		return []CacheControlLocation{{Path: "system", Index: -1, Type: "ephemeral"}}, nil
	case []interface{}:
		var candidates []CacheControlLocation
		for i := len(system) - 1; i >= 0; i-- {
			block, err := objectAt(system[i], fmt.Sprintf("system[%d]", i))
			if err != nil {
				return nil, err
			}
			if !hasCacheControl(block) {
				candidates = append(candidates, CacheControlLocation{Path: "system", Index: i, Type: "ephemeral"})
			}
		}
		return candidates, nil
	default:
		return nil, errors.New("cache_control: system must be a string, object, or array")
	}
}

func toolCandidates(root map[string]interface{}) ([]CacheControlLocation, error) {
	value, ok := root["tools"]
	if !ok {
		return nil, nil
	}
	tools, ok := value.([]interface{})
	if !ok {
		return nil, errors.New("cache_control: tools must be an array")
	}
	var candidates []CacheControlLocation
	for i := len(tools) - 1; i >= 0; i-- {
		tool, err := objectAt(tools[i], fmt.Sprintf("tools[%d]", i))
		if err != nil {
			return nil, err
		}
		if !hasCacheControl(tool) {
			candidates = append(candidates, CacheControlLocation{Path: "tools", Index: i, Type: "ephemeral"})
		}
	}
	return candidates, nil
}

func userMessageCandidates(root map[string]interface{}) ([]CacheControlLocation, error) {
	value, ok := root["messages"]
	if !ok {
		return nil, errors.New("cache_control: messages must be present")
	}
	messages, ok := value.([]interface{})
	if !ok {
		return nil, errors.New("cache_control: messages must be an array")
	}
	var candidates []CacheControlLocation
	for i := len(messages) - 1; i >= 0; i-- {
		message, err := objectAt(messages[i], fmt.Sprintf("messages[%d]", i))
		if err != nil {
			return nil, err
		}
		role, ok := message["role"].(string)
		if !ok || role == "" {
			return nil, fmt.Errorf("cache_control: messages[%d].role must be a non-empty string", i)
		}
		if role != "user" {
			continue
		}
		hasControl, err := messageContentHasCacheControl(message, i)
		if err != nil {
			return nil, err
		}
		if !hasControl {
			candidates = append(candidates, CacheControlLocation{Path: "messages", Index: i, Type: "ephemeral"})
		}
	}
	return candidates, nil
}

func messageContentHasCacheControl(message map[string]interface{}, messageIndex int) (bool, error) {
	content, ok := message["content"]
	if !ok {
		return false, fmt.Errorf("cache_control: messages[%d].content must be present", messageIndex)
	}
	switch blocks := content.(type) {
	case string:
		return false, nil
	case []interface{}:
		for i, item := range blocks {
			block, err := objectAt(item, fmt.Sprintf("messages[%d].content[%d]", messageIndex, i))
			if err != nil {
				return false, err
			}
			if hasCacheControl(block) {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("cache_control: messages[%d].content must be a string or array", messageIndex)
	}
}

func hasCacheControl(block map[string]interface{}) bool {
	_, ok := block["cache_control"]
	return ok
}

func objectAt(value interface{}, where string) (map[string]interface{}, error) {
	object, ok := value.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("cache_control: %s must be an object", where)
	}
	return object, nil
}

func formatSkipped(location CacheControlLocation) string {
	if location.Index < 0 {
		return fmt.Sprintf("%s[top-level] skipped: cache_control max reached", location.Path)
	}
	return fmt.Sprintf("%s[%d] skipped: cache_control max reached", location.Path, location.Index)
}
