package tlsfpresolve

import (
	"context"
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

func validFields() mimicry.ProfileFields {
	return mimicry.ProfileFields{
		Name:                 "tenant-chrome",
		GreaseEnabled:        true,
		CipherSuites:         []int{0x1301, 0x1302},
		SupportedCurves:      []int{29, 23},
		EcPointFormats:       []int{0},
		SignatureAlgorithms:  []int{0x0403},
		AlpnProtocols:        []string{"h2"},
		TLSSupportedVersions: []int{0x0304},
		KeyShareGroups:       []int{29},
		PskModes:             []int{1},
		ExtensionsOrder:      []int{0, 23},
		ExpectedJA3Hash:      "abc",
	}
}

type fakeFetcher struct {
	bp  boundProfile
	err error
}

func (f fakeFetcher) fetch(context.Context, int64) (boundProfile, error) { return f.bp, f.err }

func TestResolver_ActiveProfile_BuildsRT(t *testing.T) {
	r := newResolver(fakeFetcher{bp: boundProfile{active: true, fields: validFields()}})
	rt, err := r.ResolveRoundTripper(context.Background(), 7)
	if err != nil || rt == nil {
		t.Fatalf("active profile should build a uTLS RT, got rt=%v err=%v", rt, err)
	}
}

// MUTATION GUARD: making ResolveRoundTripper ignore !bp.active (always build)
// would emit a custom fingerprint for accounts that opted OUT -> this expects a
// nil (builtin) RT and goes red.
func TestResolver_NoBoundProfile_Builtin(t *testing.T) {
	r := newResolver(fakeFetcher{bp: boundProfile{active: false}})
	rt, err := r.ResolveRoundTripper(context.Background(), 7)
	if err != nil || rt != nil {
		t.Fatalf("no bound active profile must fall back to builtin (nil RT), got rt=%v err=%v", rt, err)
	}
}

// A bound-but-invalid profile must NOT break the account: fall back to builtin.
func TestResolver_InvalidProfile_FallsBackBuiltin(t *testing.T) {
	bad := validFields()
	bad.CipherSuites = []int{0x10000} // out of uint16 range -> converter errors
	r := newResolver(fakeFetcher{bp: boundProfile{active: true, fields: bad}})
	rt, err := r.ResolveRoundTripper(context.Background(), 7)
	if err != nil || rt != nil {
		t.Fatalf("invalid profile must fall back to builtin (nil RT, no error), got rt=%v err=%v", rt, err)
	}
}

func TestResolver_FetchError_Propagates(t *testing.T) {
	sentinel := errors.New("db down")
	r := newResolver(fakeFetcher{err: sentinel})
	_, err := r.ResolveRoundTripper(context.Background(), 7)
	if !errors.Is(err, sentinel) {
		t.Fatalf("infra fetch error should propagate, got %v", err)
	}
}

func TestResolver_ZeroAccount_Builtin(t *testing.T) {
	r := newResolver(fakeFetcher{bp: boundProfile{active: true, fields: validFields()}})
	rt, err := r.ResolveRoundTripper(context.Background(), 0)
	if err != nil || rt != nil {
		t.Fatalf("accountID 0 -> builtin (nil RT), got rt=%v err=%v", rt, err)
	}
}
