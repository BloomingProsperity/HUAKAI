package gateway

import (
	"context"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

type tlsProfileMarkerRT struct{}

func (tlsProfileMarkerRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

type fakeTLSProfileResolver struct {
	rt  http.RoundTripper
	err error
}

func (f fakeTLSProfileResolver) ResolveRoundTripper(context.Context, int64) (http.RoundTripper, error) {
	return f.rt, f.err
}

// UTLS-03 dispatcher wiring: applyTLSProfile must swap in the per-account DB
// profile RoundTripper for mimicry-mode accounts, and keep the builtin (orig rt)
// for standard mode / account 0 / nil resolver / resolver error.
func TestApplyTLSProfile_Wiring(t *testing.T) {
	ctx := context.Background()
	marker := tlsProfileMarkerRT{}
	orig := tlsProfileMarkerRT{}
	var origRT http.RoundTripper = orig

	d := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{rt: marker}}

	// MUTATION GUARD: making applyTLSProfile always return rt (ignore resolver)
	// makes this assertion red -> the per-account DB fingerprint never engages.
	if got := d.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 7); got != http.RoundTripper(marker) {
		t.Fatalf("mimicry mode + bound profile: expected profile RT to replace builtin")
	}
	// standard mode must never get a fingerprint (Owner carve-out for the plain path)
	if got := d.applyTLSProfile(ctx, origRT, transport.TransportModeStandard, 7); got != origRT {
		t.Fatalf("standard mode must keep builtin RT")
	}
	// no account id -> builtin
	if got := d.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 0); got != origRT {
		t.Fatalf("account 0 must keep builtin RT")
	}
	// resolver error -> builtin (never fail the dispatch)
	dErr := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{err: context.DeadlineExceeded}}
	if got := dErr.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 7); got != origRT {
		t.Fatalf("resolver error must fall back to builtin RT")
	}
	// nil resolver -> builtin
	dNil := &UpstreamDispatcher{}
	if got := dNil.applyTLSProfile(ctx, origRT, transport.TransportModeMimicryClaudeCode, 7); got != origRT {
		t.Fatalf("nil resolver must keep builtin RT")
	}
}
