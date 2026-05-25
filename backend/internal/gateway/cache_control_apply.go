// R7.2: Anthropic Messages API cache_control mutator.
// Sister function to R7.1 inspector (cache_control.go).
// Spec: docs/specs/upstream-credential-management.md §F-AUTH-005 Phase H /
// docs/reference_delta/2026-05-06/vendor-drift-audit.md (D5 TTL constraints).
//
// Pure JSON mutation — no IO, no network, no credential contact.
// Applies cache_control breakpoints as planned by SuggestBreakpoints (R7.1).
// Invariant: InspectCacheControl(result.Body).Count ==
//
//	InspectCacheControl(originalBody).Count + len(result.Applied)
//
// Parallel-draft lane: implementer-claude (CLAUDE.md #10 + 2026-05-04).
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// BreakpointApplyResult is the output of ApplyBreakpoints and
// ApplyBreakpointsWithTTLOrdering. Body is always a fresh allocation;
// the original input slice is never mutated.
type BreakpointApplyResult struct {
	Body    []byte
	Applied []CacheControlLocation
	Skipped []SkipReason
}

// SkipReason pairs a location with a human-readable explanation of why
// ApplyBreakpoints declined to insert cache_control there.
type SkipReason struct {
	Location CacheControlLocation
	Reason   string
}

// skipReasonAlreadyHas is returned when the target block already carries
// a cache_control field.
const skipReasonAlreadyHas = "already has cache_control"

// skipReasonNotFound is returned when the requested path/index does not
// exist in the body.
const skipReasonNotFound = "location not found in body"

// skipReasonExceedsCap is returned when applying the breakpoint would push
// the total count past CacheControlMaxAllowed.
const skipReasonExceedsCap = "would exceed cap"

// ApplyBreakpoints applies the cache_control breakpoints described by plan
// to a copy of body. It never mutates the caller's slice.
//
// For each location in plan.Add:
//   - If the block already has cache_control → Skipped ("already has cache_control").
//   - If the path/index does not resolve → Skipped ("location not found in body").
//   - If applying would exceed CacheControlMaxAllowed → Skipped ("would exceed cap").
//   - Otherwise the cache_control object is inserted and the location is
//     recorded in Applied.
//
// TTL="" produces {"type":"ephemeral"}; TTL="1h" produces
// {"type":"ephemeral","ttl":"1h"}.
//
// The returned Body is re-serialized from the parsed representation; key
// ordering within objects may change relative to the input.
func ApplyBreakpoints(body []byte, plan BreakpointSuggestion) (BreakpointApplyResult, error) {
	root, err := decodeMessagesRequest(body)
	if err != nil {
		return BreakpointApplyResult{}, err
	}

	// Count existing breakpoints so we know how much cap remains.
	snapshot, err := inspectCacheControlRoot(root)
	if err != nil {
		return BreakpointApplyResult{}, err
	}

	var result BreakpointApplyResult
	currentCount := snapshot.Count

	for _, loc := range plan.Add {
		// Cap guard.
		if currentCount >= CacheControlMaxAllowed {
			result.Skipped = append(result.Skipped, SkipReason{Location: loc, Reason: skipReasonExceedsCap})
			continue
		}

		block, notFound, err := resolveBlock(root, loc)
		if err != nil {
			return BreakpointApplyResult{}, err
		}
		if notFound {
			result.Skipped = append(result.Skipped, SkipReason{Location: loc, Reason: skipReasonNotFound})
			continue
		}

		// Already occupied guard.
		if hasCacheControl(block) {
			result.Skipped = append(result.Skipped, SkipReason{Location: loc, Reason: skipReasonAlreadyHas})
			continue
		}

		// Insert cache_control.
		block["cache_control"] = buildCacheControlObject(loc.TTL)
		currentCount++
		result.Applied = append(result.Applied, loc)
	}

	// Re-serialize.
	out, err := json.Marshal(root)
	if err != nil {
		return BreakpointApplyResult{}, fmt.Errorf("cache_control apply: re-serialization failed: %w", err)
	}
	result.Body = out
	return result, nil
}

// ApplyBreakpointsWithTTLOrdering behaves identically to ApplyBreakpoints
// but first sorts plan.Add so that longer-TTL entries ("1h") precede
// shorter-TTL entries ("") before applying. This satisfies the Anthropic
// requirement that longer-TTL breakpoints appear earlier in the request.
//
// If the sorted plan still cannot produce a valid ordering (e.g. the
// pre-existing breakpoints already violate ordering), an error is returned.
func ApplyBreakpointsWithTTLOrdering(body []byte, plan BreakpointSuggestion) (BreakpointApplyResult, error) {
	if len(plan.Add) == 0 {
		return ApplyBreakpoints(body, plan)
	}

	// Sort: "1h" (long) before "" (short). Stable sort preserves relative
	// order within each TTL tier.
	sorted := make([]CacheControlLocation, len(plan.Add))
	copy(sorted, plan.Add)
	sort.SliceStable(sorted, func(i, j int) bool {
		return ttlRank(sorted[i].TTL) > ttlRank(sorted[j].TTL)
	})

	sortedPlan := BreakpointSuggestion{
		Add:     sorted,
		Skipped: plan.Skipped,
	}

	result, err := ApplyBreakpoints(body, sortedPlan)
	if err != nil {
		return BreakpointApplyResult{}, err
	}

	// Validate that the resulting body respects TTL ordering.
	if len(result.Body) > 0 {
		finalSnap, err := InspectCacheControl(result.Body)
		if err != nil {
			return BreakpointApplyResult{}, fmt.Errorf("cache_control apply: post-apply inspect failed: %w", err)
		}
		if err := ValidateTTLOrdering(finalSnap); err != nil {
			return BreakpointApplyResult{}, fmt.Errorf("cache_control apply: TTL ordering cannot be satisfied: %w", err)
		}
	}

	return result, nil
}

// ttlRank maps TTL strings to a sortable integer so that longer-TTL values
// sort higher. Unknown TTL strings sort lower than "".
func ttlRank(ttl string) int {
	switch ttl {
	case "1h":
		return 2
	case "":
		return 1
	default:
		return 0
	}
}

// buildCacheControlObject constructs the cache_control map to insert.
// TTL="" → {"type":"ephemeral"}; TTL="1h" → {"type":"ephemeral","ttl":"1h"}.
func buildCacheControlObject(ttl string) map[string]interface{} {
	obj := map[string]interface{}{"type": "ephemeral"}
	if ttl != "" {
		obj["ttl"] = ttl
	}
	return obj
}

// resolveBlock navigates root to find the map[string]interface{} targeted by
// loc. Returns (block, notFound=true, nil) when the path/index is absent.
// Returns (nil, false, err) on structural errors (e.g. wrong JSON types).
//
// Supported paths:
//   - "system" with Index=-1  → system as a single object
//   - "system" with Index>=0  → system as array element
//   - "messages" with Index   → messages[Index] content's last block (or the
//     message itself when content is a string)
//   - "tools"   with Index    → tools[Index]
func resolveBlock(root map[string]interface{}, loc CacheControlLocation) (block map[string]interface{}, notFound bool, err error) {
	switch loc.Path {
	case "system":
		return resolveSystemBlock(root, loc.Index)
	case "messages":
		return resolveMessageBlock(root, loc.Index)
	case "tools":
		return resolveToolBlock(root, loc.Index)
	default:
		return nil, true, nil // unknown path → treat as not found
	}
}

func resolveSystemBlock(root map[string]interface{}, index int) (map[string]interface{}, bool, error) {
	value, ok := root["system"]
	if !ok {
		return nil, true, nil
	}
	switch system := value.(type) {
	case string:
		return nil, true, nil
	case map[string]interface{}:
		if index != -1 {
			return nil, true, nil
		}
		return system, false, nil
	case []interface{}:
		if index < 0 || index >= len(system) {
			return nil, true, nil
		}
		block, err := objectAt(system[index], fmt.Sprintf("system[%d]", index))
		if err != nil {
			return nil, false, err
		}
		return block, false, nil
	default:
		return nil, false, errors.New("cache_control apply: system must be a string, object, or array")
	}
}

func resolveMessageBlock(root map[string]interface{}, index int) (map[string]interface{}, bool, error) {
	value, ok := root["messages"]
	if !ok {
		return nil, true, nil
	}
	messages, ok := value.([]interface{})
	if !ok {
		return nil, false, errors.New("cache_control apply: messages must be an array")
	}
	if index < 0 || index >= len(messages) {
		return nil, true, nil
	}
	message, err := objectAt(messages[index], fmt.Sprintf("messages[%d]", index))
	if err != nil {
		return nil, false, err
	}

	// The cache_control belongs on the last content block (array case) or
	// directly on the message object (string content case). For the mutator
	// we need to return the block that should receive cache_control. When
	// content is an array we target the last element; when content is a
	// string we cannot attach cache_control — return notFound.
	content, contentOK := message["content"]
	if !contentOK {
		return nil, true, nil
	}
	switch blocks := content.(type) {
	case string:
		// String content: cache_control cannot be placed here.
		return nil, true, nil
	case []interface{}:
		if len(blocks) == 0 {
			return nil, true, nil
		}
		// Target the last content block.
		last := blocks[len(blocks)-1]
		block, err := objectAt(last, fmt.Sprintf("messages[%d].content[%d]", index, len(blocks)-1))
		if err != nil {
			return nil, false, err
		}
		return block, false, nil
	default:
		return nil, false, fmt.Errorf("cache_control apply: messages[%d].content must be a string or array", index)
	}
}

func resolveToolBlock(root map[string]interface{}, index int) (map[string]interface{}, bool, error) {
	value, ok := root["tools"]
	if !ok {
		return nil, true, nil
	}
	tools, ok := value.([]interface{})
	if !ok {
		return nil, false, errors.New("cache_control apply: tools must be an array")
	}
	if index < 0 || index >= len(tools) {
		return nil, true, nil
	}
	block, err := objectAt(tools[index], fmt.Sprintf("tools[%d]", index))
	if err != nil {
		return nil, false, err
	}
	return block, false, nil
}
