package gatewayhttp

import (
	"context"
	"errors"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/thinkingnorm"
)

// resolveModelWithEffortSuffix resolves the requested model, folding in
// registry-aware effort-suffix normalization. It first resolves the FULL
// requested name (the existing routing/pricing resolution). ONLY when that name
// is unknown does it consider a reasoning/thinking effort suffix
// (e.g. "gpt-5-thinking-high"): it strips to the base, and if the base resolves
// as a reasoning-capable model, normalizes ex.req.Model + ex.body in place and
// re-resolves the base. A real shipped model that merely ends in an
// effort-looking token (e.g. "yi-medium") resolves as the full name on the first
// call, so it is never touched and no extra registry lookup is made.
func (ex *chatExecution) resolveModelWithEffortSuffix() (registry.Resolved, error) {
	resolved, err := ex.d.Registry.ResolveModel(ex.ctx, ex.req.Model, ex.ident.TenantID)
	if errors.Is(err, registry.ErrUnknownModel) && ex.applyEffortSuffixNormalization() {
		return ex.d.Registry.ResolveModel(ex.ctx, ex.req.Model, ex.ident.TenantID)
	}
	return resolved, err
}

// applyEffortSuffixNormalization is the registry-aware effort-suffix hook, run
// from prepareRoute ONLY after the full requested model name has already failed
// to resolve (registry.ErrUnknownModel). It strips a recognized reasoning/
// thinking effort suffix off ex.req.Model, and — only when the BASE name
// resolves as a reasoning-capable model — rewrites ex.req.Model to the base and
// ex.body so the canonical thinking/reasoning parameter (selected by the INGRESS
// protocol, not the model family) carries that effort upstream. It returns true
// iff it mutated the request, so the caller re-resolves with the base.
//
// Cost shape: the cheap pre-check (HasEffortSuffix) short-circuits with ZERO
// registry calls when the name has no effort token. A registry lookup for the
// base happens only on the suffix-bearing, full-unresolved path; the resolved
// common path and the no-suffix path make NO extra registry calls.
func (ex *chatExecution) applyEffortSuffixNormalization() bool {
	if !thinkingnorm.HasEffortSuffix(ex.req.Model) {
		return false
	}
	resolver := effortSuffixResolver{
		ctx:      ex.ctx,
		registry: ex.d.Registry,
		tenantID: ex.ident.TenantID,
	}
	outcome, normalizedBody := thinkingnorm.NormalizeEffortSuffix(
		ex.req.Model, ex.body, ingressProtocolForEffort(ex.clientProtocol), resolver)
	if !outcome.Normalized {
		return false
	}
	ex.req.Model = outcome.BaseModel
	ex.body = normalizedBody
	return true
}

// ingressProtocolForEffort maps the gateway's ingress client protocol to the
// effort-parameter shape the downstream canonical parser for that ingress reads.
// The emitted parameter is keyed off the INGRESS PATH's protocol (which selects
// the request parser), NOT the model-name family: the openai chat-completions
// ingress parser reads a top-level reasoning_effort string; the anthropic
// messages ingress parser reads a top-level thinking object.
//
// The OpenAI Responses ingress is deliberately left unmodeled (IngressOther): its
// canonical parser consumes a NESTED reasoning object, not a top-level
// reasoning_effort, so emitting the chat shape there would be silently dropped
// during canonicalization. Leaving it unmodeled means a Responses request with an
// effort suffix is not rewritten and behaves exactly as before (no silent effort
// loss). Native Responses effort wiring is a follow-up. Any other ingress is also
// left unmodeled so its request is never rewritten.
func ingressProtocolForEffort(p proto.ClientProtocol) thinkingnorm.IngressProtocol {
	switch p {
	case proto.ClientProtocolOpenAIChat:
		return thinkingnorm.IngressOpenAIChat
	case proto.ClientProtocolAnthropicMessages:
		return thinkingnorm.IngressAnthropic
	default:
		return thinkingnorm.IngressOther
	}
}

// effortSuffixResolver adapts the model registry to thinkingnorm.ModelResolver.
// It answers "does this name resolve, and is it reasoning/thinking capable?" by
// running the same ResolveModel the route preparation already uses, so a base
// name is judged by exactly the routing/pricing registry view. Resolution errors
// (unknown / disabled / no-access / transient) all mean "not a usable base", so
// the effort suffix is left in place and the request 404s as before.
type effortSuffixResolver struct {
	ctx      context.Context
	registry registry.Registry
	tenantID int64
}

func (r effortSuffixResolver) Resolve(name string) (resolves bool, reasoningCapable bool) {
	if r.registry == nil {
		return false, false
	}
	resolved, err := r.registry.ResolveModel(r.ctx, name, r.tenantID)
	if err != nil {
		return false, false
	}
	return true, hasReasoningCapability(resolved.Capabilities)
}

// hasReasoningCapability reports whether a resolved model's capability list
// marks it reasoning/thinking capable. Both registry capability vocabularies are
// honored: "reasoning" (public discovery descriptor) and "thinking" (HCSF
// capability family).
func hasReasoningCapability(capabilities []string) bool {
	for _, c := range capabilities {
		if c == "reasoning" || c == "thinking" {
			return true
		}
	}
	return false
}
