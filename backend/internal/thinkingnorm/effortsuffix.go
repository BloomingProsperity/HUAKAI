package thinkingnorm

// Reasoning/thinking effort-suffix normalization at chat ingress.
//
// A caller may request a model whose alias carries a reasoning/thinking effort
// suffix, e.g. "gpt-5-thinking-high". The suffix is a per-request knob for how
// hard the model should think; it is NOT part of the routable/priceable model
// identity. When a genuine "<reasoning-model>-<effort>" name is detected, this
// package returns the BASE model name (so routing + pricing observe the base)
// and rewrites the request body so the canonical thinking/reasoning parameter
// carries the requested effort upstream.
//
// REGISTRY-AWARE strip (the load-bearing guarantee): an effort suffix is only
// stripped when the FULL requested name does NOT resolve in the model registry
// AND the base name DOES resolve as a reasoning-capable model. A real shipped
// model whose name merely ends in an effort-looking token (e.g. "yi-medium")
// resolves on its own, so it is never touched. Resolution is supplied by the
// caller via ModelResolver, so this package never reaches the registry itself;
// the no-suffix common path performs ZERO resolver calls.
//
// EFFORT PARAM BY INGRESS PROTOCOL: HUAKAI selects the request parser by the
// ingress path (openai-chat vs anthropic), not by the model-name family. So the
// emitted parameter is keyed off the ingress client protocol, NOT the model
// name: an openai-chat ingress emits a top-level reasoning_effort string (which
// the openai-chat canonical parser reads); an anthropic ingress emits a
// top-level thinking object (which the anthropic canonical parser reads). This
// is what lets "claude-...-high" sent to the openai-chat endpoint survive
// canonicalization instead of being silently dropped.
//
// This composes with the existing thinking representation (capability_thinking /
// NormalizeThinkingValidity); it only fills the request-level parameter the
// downstream canonical parser already consumes.

import (
	"encoding/json"
	"strings"
)

// IngressProtocol identifies the client protocol of the ingress request, which
// determines WHICH request parser will read the body downstream and therefore
// which body parameter the effort must be written into. It is a thin local
// mirror of the gateway's client-protocol enum so this package carries no
// dependency on the proto/gateway layers.
type IngressProtocol int

const (
	// IngressOpenAIChat covers the OpenAI chat-completions and responses
	// ingress paths, whose canonical parser reads a top-level reasoning_effort
	// string and drops a top-level thinking object.
	IngressOpenAIChat IngressProtocol = iota
	// IngressAnthropic covers the Anthropic messages ingress path, whose
	// canonical parser reads a top-level thinking object and drops a top-level
	// reasoning_effort string.
	IngressAnthropic
	// IngressOther is any ingress whose effort parameter shape is not modeled
	// here; such a request is left unchanged (no suffix is stripped) so it can
	// 404/resolve exactly as before.
	IngressOther
)

// effortLevel is a canonical, lower-cased thinking effort level.
type effortLevel string

const (
	effortMinimal effortLevel = "minimal"
	effortLow     effortLevel = "low"
	effortMedium  effortLevel = "medium"
	effortHigh    effortLevel = "high"
	effortMax     effortLevel = "max"
	effortNone    effortLevel = "none"
)

// suffixEntry binds a hyphenated wire suffix to its canonical effort level.
type suffixEntry struct {
	suffix string
	level  effortLevel
}

// effortSuffixes are the recognized effort tokens, ordered most-specific-first
// so a longer token ("-minimal") is matched before a shorter one could
// ambiguously win. The set is deliberately small and explicit: a token NOT in
// this set (e.g. a "...-turbo" / "...-latest" / "...-preview" alias) never even
// enters the resolve-aware path, so it can never be touched. Because the strip
// is gated on registry resolution (full-unresolved + base-resolves-reasoning),
// the set may safely include "-max" / "-none": a real "...-max" model resolves
// on its own and is left intact regardless of the token.
var effortSuffixes = []suffixEntry{
	{"-minimal", effortMinimal},
	{"-medium", effortMedium},
	{"-high", effortHigh},
	{"-low", effortLow},
	{"-none", effortNone},
	{"-max", effortMax},
}

// levelToBudget maps a canonical effort level to a thinking budget in tokens,
// used for the anthropic-ingress thinking object which wants a numeric budget.
// "none" => 0 means "disable thinking". These are conservative mid-points, not
// provider maxima; final clamping to the request's own output budget happens in
// applyAnthropicThinkingBudget.
var levelToBudget = map[effortLevel]int{
	effortMinimal: 512,
	effortLow:     1024,
	effortMedium:  8192,
	effortHigh:    24576,
	effortMax:     32768,
	effortNone:    0,
}

// openAIEffortLevels are the discrete reasoning_effort string values the
// OpenAI-chat canonical parser understands. "max" is folded to "high" so an
// OpenAI-invalid level can never be emitted; "none" removes the field.
var openAIEffortLevels = map[effortLevel]bool{
	effortMinimal: true,
	effortLow:     true,
	effortMedium:  true,
	effortHigh:    true,
}

// ModelResolver answers, for a candidate model name, whether it resolves in the
// model registry and (if so) whether it is reasoning/thinking capable. The
// caller wires this to the gateway's registry; this package never resolves
// models itself. Implementations should be cheap to call but are still only
// called on the suffix-bearing, full-unresolved path.
type ModelResolver interface {
	// Resolve reports whether name is a known model (resolves) and, when it
	// resolves, whether it is reasoning/thinking capable.
	Resolve(name string) (resolves bool, reasoningCapable bool)
}

// EffortSuffixOutcome reports the result of a normalization pass.
type EffortSuffixOutcome struct {
	// Normalized is true only when an effort suffix was stripped and the body
	// rewritten. When false, BaseModel == input model and Body is byte-identical
	// to the input.
	Normalized bool
	// BaseModel is the model name to route/price with: the stripped base when
	// Normalized, otherwise the unchanged input.
	BaseModel string
	// Level is the canonical effort level parsed from the suffix (empty when not
	// normalized). Useful for logging/accounting.
	Level string
}

// HasEffortSuffix reports whether model ends in a recognized effort token. It is
// the CHEAP PRE-CHECK: a pure string-shape test with no allocation beyond a
// lower-case fold and ZERO resolver/registry calls. The 99% common path (no
// effort suffix) returns false here and the caller short-circuits, leaving the
// request byte-identical. This MUST be the first gate the caller consults.
func HasEffortSuffix(model string) bool {
	_, _, ok := parseEffortSuffix(model)
	return ok
}

// NormalizeEffortSuffix applies registry-aware effort-suffix normalization.
//
// Contract / order of checks (each gate is cheaper than the next):
//
//  1. CHEAP PRE-CHECK: if model has no recognized effort token, return
//     Normalized=false, BaseModel==model, body byte-identical. ZERO resolver
//     calls. (The caller is expected to gate on HasEffortSuffix first, but this
//     function re-checks so it is safe to call directly.)
//  2. If model ends in an effort token, the caller has already learned the FULL
//     name does not resolve (fullResolves=false is the precondition for calling
//     the strip path). Strip to base and ask the resolver about the BASE.
//  3. Only if the base RESOLVES and is REASONING-CAPABLE do we strip + rewrite.
//     Otherwise return UNCHANGED so the request 404s / routes exactly as before.
//
// A real model whose name ends in an effort-looking token (e.g. "yi-medium")
// resolves as the FULL name, so the caller never enters this function's strip
// branch for it; even if it did, the base ("yi") failing to resolve would leave
// it unchanged. Either way "yi-medium" is never mutated.
//
// The body is rewritten by INGRESS PROTOCOL, not model family: openai-chat
// ingress -> top-level reasoning_effort string; anthropic ingress -> top-level
// thinking object. A body that is not a JSON object is left untouched (the base
// model is still returned) so the ingress path can never crash on a malformed
// body. A recognized suffix is authoritative and OVERWRITES any explicit
// body-level reasoning/thinking parameter the client also set (the suffix is an
// explicit per-call model choice the caller typed).
func NormalizeEffortSuffix(model string, body []byte, ingress IngressProtocol, resolver ModelResolver) (EffortSuffixOutcome, []byte) {
	base, level, ok := parseEffortSuffix(model)
	if !ok {
		return EffortSuffixOutcome{BaseModel: model}, body
	}
	if ingress == IngressOther {
		return EffortSuffixOutcome{BaseModel: model}, body
	}
	if resolver == nil {
		return EffortSuffixOutcome{BaseModel: model}, body
	}
	resolves, reasoning := resolver.Resolve(base)
	if !resolves || !reasoning {
		// Base is not a known reasoning-capable model: do NOT strip. Let the
		// request route/404 exactly as it would have without this feature.
		return EffortSuffixOutcome{BaseModel: model}, body
	}

	outcome := EffortSuffixOutcome{Normalized: true, BaseModel: base, Level: string(level)}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil || obj == nil {
		// Body is not a JSON object we can edit; still strip the suffix so
		// routing/pricing observe the base model, but leave the body untouched.
		return outcome, body
	}

	switch ingress {
	case IngressAnthropic:
		applyAnthropicThinkingBudget(obj, level)
	default: // IngressOpenAIChat
		applyOpenAIReasoningEffort(obj, level)
	}

	out, err := json.Marshal(obj)
	if err != nil {
		return outcome, body
	}
	return outcome, out
}

// parseEffortSuffix returns (baseModel, level, true) when model ends in a
// recognized effort token. The comparison is case-insensitive on the token; the
// returned base preserves the original casing of the prefix. Ordered
// most-specific-first.
func parseEffortSuffix(model string) (string, effortLevel, bool) {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return model, "", false
	}
	lower := strings.ToLower(trimmed)
	for _, ent := range effortSuffixes {
		if strings.HasSuffix(lower, ent.suffix) && len(trimmed) > len(ent.suffix) {
			return trimmed[:len(trimmed)-len(ent.suffix)], ent.level, true
		}
	}
	return model, "", false
}

// applyOpenAIReasoningEffort sets the top-level reasoning_effort string consumed
// by the openai-chat canonical parser. "none" removes the field (no reasoning);
// "max" folds to "high" so an OpenAI-invalid level is never emitted; the rest
// emit as-is.
func applyOpenAIReasoningEffort(obj map[string]json.RawMessage, level effortLevel) {
	switch {
	case level == effortNone:
		delete(obj, "reasoning_effort")
		return
	case openAIEffortLevels[level]:
		// emit as-is
	case level == effortMax:
		level = effortHigh
	default:
		return
	}
	if raw, err := json.Marshal(string(level)); err == nil {
		obj["reasoning_effort"] = raw
	}
}

// applyAnthropicThinkingBudget writes the top-level thinking object consumed by
// the anthropic canonical parser. "none" disables thinking; any other level
// enables it with a budget from the level<->budget table, clamped down under the
// request's own max-output budget when that is smaller (the thinking budget can
// never exceed the answer budget).
func applyAnthropicThinkingBudget(obj map[string]json.RawMessage, level effortLevel) {
	if level == effortNone {
		thinking, _ := json.Marshal(map[string]any{"type": "disabled", "budget_tokens": 0})
		obj["thinking"] = thinking
		return
	}
	budget := budgetForLevel(level)
	if maxOut := requestMaxOutputBudget(obj); maxOut > 0 && budget > maxOut {
		budget = maxOut
	}
	thinking, _ := json.Marshal(map[string]any{"type": "enabled", "budget_tokens": budget})
	obj["thinking"] = thinking
}

// budgetForLevel resolves a level to a token budget, defaulting to the medium
// budget for any unexpected level so a future level never yields a zero
// (thinking-off) budget by accident.
func budgetForLevel(level effortLevel) int {
	if b, ok := levelToBudget[level]; ok {
		return b
	}
	return levelToBudget[effortMedium]
}

// requestMaxOutputBudget reads the request's max-output token budget from the
// first present of max_completion_tokens / max_tokens / max_output_tokens.
// Returns 0 when none is a usable positive integer.
func requestMaxOutputBudget(obj map[string]json.RawMessage) int {
	for _, key := range []string{"max_completion_tokens", "max_tokens", "max_output_tokens"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var n int
		if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
			return n
		}
	}
	return 0
}
