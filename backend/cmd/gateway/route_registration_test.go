package main

import (
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type routeRegistrationLedger struct {
	seen       map[string]int
	duplicates []string
}

func newRouteRegistrationLedger() *routeRegistrationLedger {
	return &routeRegistrationLedger{seen: make(map[string]int)}
}

var routeParamPattern = regexp.MustCompile(`\{([^}:]+)(:[^}]+)?\}`)

func normalizeRegisteredPath(path string) string {
	return routeParamPattern.ReplaceAllStringFunc(path, func(segment string) string {
		if colon := strings.IndexByte(segment, ':'); colon >= 0 {
			return "{" + segment[colon:]
		}
		return "{}"
	})
}

func (l *routeRegistrationLedger) record(method, path string) {
	method = strings.ToUpper(strings.TrimSpace(method))
	path = normalizeRegisteredPath(path)
	key := method + " " + path
	allKey := "* " + path
	if l.seen[key] > 0 || (method != "*" && l.seen[allKey] > 0) {
		l.duplicates = append(l.duplicates, key)
	}
	if method == "*" {
		for existing, count := range l.seen {
			if count > 0 && strings.HasSuffix(existing, " "+path) {
				l.duplicates = append(l.duplicates, key)
				break
			}
		}
	}
	l.seen[key]++
}

type recordingRouter struct {
	chi.Router
	prefix string
	ledger *routeRegistrationLedger
}

func newRecordingRouter() *recordingRouter {
	return &recordingRouter{Router: chi.NewRouter(), ledger: newRouteRegistrationLedger()}
}

func joinRoutePath(prefix, pattern string) string {
	if prefix == "" {
		return pattern
	}
	if pattern == "" {
		return prefix
	}
	if pattern == "/" {
		return strings.TrimSuffix(prefix, "/") + "/"
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(pattern, "/")
}

func (r *recordingRouter) record(method, pattern string) {
	r.ledger.record(method, joinRoutePath(r.prefix, pattern))
}

func (r *recordingRouter) With(middlewares ...func(http.Handler) http.Handler) chi.Router {
	return &recordingRouter{Router: r.Router.With(middlewares...), prefix: r.prefix, ledger: r.ledger}
}

func (r *recordingRouter) Group(fn func(chi.Router)) chi.Router {
	var child *recordingRouter
	r.Router.Group(func(router chi.Router) {
		child = &recordingRouter{Router: router, prefix: r.prefix, ledger: r.ledger}
		fn(child)
	})
	return child
}

func (r *recordingRouter) Route(pattern string, fn func(chi.Router)) chi.Router {
	var child *recordingRouter
	r.Router.Route(pattern, func(router chi.Router) {
		child = &recordingRouter{Router: router, prefix: joinRoutePath(r.prefix, pattern), ledger: r.ledger}
		fn(child)
	})
	return child
}

func (r *recordingRouter) Mount(pattern string, handler http.Handler) {
	if routes, ok := handler.(chi.Routes); ok {
		_ = chi.Walk(routes, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			r.ledger.record(method, joinRoutePath(joinRoutePath(r.prefix, pattern), route))
			return nil
		})
	}
	r.Router.Mount(pattern, handler)
}

func (r *recordingRouter) Handle(pattern string, handler http.Handler) {
	method, path := splitHandlePattern(pattern)
	r.record(method, path)
	r.Router.Handle(pattern, handler)
}

func (r *recordingRouter) HandleFunc(pattern string, handler http.HandlerFunc) {
	method, path := splitHandlePattern(pattern)
	r.record(method, path)
	r.Router.HandleFunc(pattern, handler)
}

func splitHandlePattern(pattern string) (string, string) {
	parts := strings.SplitN(pattern, " ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "*", pattern
}

func (r *recordingRouter) Method(method, pattern string, handler http.Handler) {
	r.record(method, pattern)
	r.Router.Method(method, pattern, handler)
}

func (r *recordingRouter) MethodFunc(method, pattern string, handler http.HandlerFunc) {
	r.record(method, pattern)
	r.Router.MethodFunc(method, pattern, handler)
}

func (r *recordingRouter) Connect(pattern string, handler http.HandlerFunc) {
	r.record(http.MethodConnect, pattern)
	r.Router.Connect(pattern, handler)
}

func (r *recordingRouter) Delete(pattern string, handler http.HandlerFunc) {
	r.record(http.MethodDelete, pattern)
	r.Router.Delete(pattern, handler)
}

func (r *recordingRouter) Get(pattern string, handler http.HandlerFunc) {
	r.record(http.MethodGet, pattern)
	r.Router.Get(pattern, handler)
}

func (r *recordingRouter) Head(pattern string, handler http.HandlerFunc) {
	r.record(http.MethodHead, pattern)
	r.Router.Head(pattern, handler)
}

func (r *recordingRouter) Options(pattern string, handler http.HandlerFunc) {
	r.record(http.MethodOptions, pattern)
	r.Router.Options(pattern, handler)
}

func (r *recordingRouter) Patch(pattern string, handler http.HandlerFunc) {
	r.record(http.MethodPatch, pattern)
	r.Router.Patch(pattern, handler)
}

func (r *recordingRouter) Post(pattern string, handler http.HandlerFunc) {
	r.record(http.MethodPost, pattern)
	r.Router.Post(pattern, handler)
}

func (r *recordingRouter) Put(pattern string, handler http.HandlerFunc) {
	r.record(http.MethodPut, pattern)
	r.Router.Put(pattern, handler)
}

func (r *recordingRouter) Trace(pattern string, handler http.HandlerFunc) {
	r.record(http.MethodTrace, pattern)
	r.Router.Trace(pattern, handler)
}

func TestGatewayRouteRegistrationsHaveNoSilentOverwrite(t *testing.T) {
	router := newRecordingRouter()
	mountTestRoutes(t, router)
	if len(router.ledger.seen) < 100 {
		t.Fatalf("只记录到 %d 条路由，记录器未覆盖真实网关路由树", len(router.ledger.seen))
	}
	if len(router.ledger.duplicates) == 0 {
		return
	}
	sort.Strings(router.ledger.duplicates)
	t.Fatalf("发现会被 chi 静默覆盖的重复 method/path 注册:\n%s",
		strings.Join(router.ledger.duplicates, "\n"))
}

func TestRecordingRouterDetectsEquivalentParameterNames(t *testing.T) {
	router := newRecordingRouter()
	router.Get("/items/{id}", func(http.ResponseWriter, *http.Request) {})
	router.Get("/items/{item_id}", func(http.ResponseWriter, *http.Request) {})
	if len(router.ledger.duplicates) != 1 {
		t.Fatalf("duplicates=%v，参数名不同但匹配形状相同的路由必须判冲突", router.ledger.duplicates)
	}
	if got := router.ledger.duplicates[0]; got != fmt.Sprintf("%s /items/{}", http.MethodGet) {
		t.Fatalf("duplicate=%q", got)
	}
}
