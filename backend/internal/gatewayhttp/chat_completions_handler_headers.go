package gatewayhttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

const (
	headerHUAKAIModelRequested = "X-HUAKAI-Model-Requested"
	headerHUAKAIModelDelivered = "X-HUAKAI-Model-Delivered"
)

func setAccountingModelRequested(env *proto.HCSF, requested string) {
	if env == nil || requested == "" {
		return
	}
	ensureAccountingModelChain(env).Requested = requested
}

func setAccountingModelRouteDecided(env *proto.HCSF, routeDecided string) {
	if env == nil || routeDecided == "" {
		return
	}
	ensureAccountingModelChain(env).RouteDecided = routeDecided
}

func setHUAKAIModelHeaders(h http.Header, requested string, env *proto.HCSF) {
	if h == nil {
		return
	}
	if requested != "" {
		h.Set(headerHUAKAIModelRequested, requested)
	}
	if delivered := deliveredModel(env); delivered != "" {
		h.Set(headerHUAKAIModelDelivered, delivered)
	}
}

func ensureAccountingModelChain(env *proto.HCSF) *proto.ModelChain {
	if env.Accounting.ModelChain == nil {
		env.Accounting.ModelChain = &proto.ModelChain{}
	}
	return env.Accounting.ModelChain
}

func deliveredModel(env *proto.HCSF) string {
	if env == nil {
		return ""
	}
	if env.BufferedResponse != nil && env.BufferedResponse.Model != "" {
		return env.BufferedResponse.Model
	}
	if env.Accounting.ModelChain == nil {
		return ""
	}
	if env.Accounting.ModelChain.UpstreamReported != "" {
		return env.Accounting.ModelChain.UpstreamReported
	}
	return env.Accounting.ModelChain.RouteDecided
}
