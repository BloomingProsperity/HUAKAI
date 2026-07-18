package gateway

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/transport"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

type tlsProfileMarkerRT struct{ name string }

func (*tlsProfileMarkerRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }

type fakeTLSProfileResolver struct {
	profile *mimicry.InlineTLSProfile
	err     error
}

func (f fakeTLSProfileResolver) ResolveProfile(context.Context, int64) (*mimicry.InlineTLSProfile, error) {
	return f.profile, f.err
}

func TestApplyTLSProfileBindsDynamicProfileToSidecar(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "true")
	profile := validGatewayInlineProfile()
	base := mimicry.NewSidecarRoundTripper(
		mimicry.NewSidecarClient("/run/huakai/tls-sidecar.sock"),
		mimicry.SidecarProfileAnthropicCLIMimicryV1,
	)
	d := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{profile: profile}}

	got, err := d.applyTLSProfile(context.Background(), base, transport.TransportModeMimicryClaudeCode, 7)
	if err != nil {
		t.Fatalf("applyTLSProfile: %v", err)
	}
	marked, ok := got.(interface{ SidecarProfileID() string })
	if !ok {
		t.Fatalf("动态 profile 返回值没有 sidecar 标记，got=%T", got)
	}
	if marked.SidecarProfileID() != "inline:"+profile.ID {
		t.Fatalf("动态 profile 未绑定到 sidecar，id=%q", marked.SidecarProfileID())
	}
	if got == base {
		t.Fatal("绑定动态 profile 必须派生独立 transport，不能污染共享连接池")
	}
}

func TestApplyTLSProfileKeepsBuiltinWhenNoDynamicSelection(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "true")
	orig := &tlsProfileMarkerRT{name: "builtin"}
	d := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{}}

	got, err := d.applyTLSProfile(context.Background(), orig, transport.TransportModeMimicryClaudeCode, 7)
	if err != nil || got != http.RoundTripper(orig) {
		t.Fatalf("无动态 profile 应保持 builtin，got=%#v err=%v", got, err)
	}
}

func TestApplyTLSProfileSkipsNonMimicryContexts(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "true")
	orig := &tlsProfileMarkerRT{name: "builtin"}
	d := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{profile: validGatewayInlineProfile()}}

	for name, tc := range map[string]struct {
		mode      transport.TransportMode
		accountID int64
	}{
		"standard":  {mode: transport.TransportModeStandard, accountID: 7},
		"account-0": {mode: transport.TransportModeMimicryClaudeCode},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := d.applyTLSProfile(context.Background(), orig, tc.mode, tc.accountID)
			if err != nil || got != http.RoundTripper(orig) {
				t.Fatalf("应保留原 transport，got=%#v err=%v", got, err)
			}
		})
	}
}

func TestApplyTLSProfileFailsClosedOnResolverOrTransportMismatch(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "true")
	orig := &tlsProfileMarkerRT{name: "not-sidecar"}
	sentinel := errors.New("db down")

	dResolver := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{err: sentinel}}
	if got, err := dResolver.applyTLSProfile(context.Background(), orig, transport.TransportModeMimicryClaudeCode, 7); got != nil || !errors.Is(err, sentinel) {
		t.Fatalf("resolver 故障不得回落其他指纹，got=%#v err=%v", got, err)
	}

	dMismatch := &UpstreamDispatcher{TLSProfileResolver: fakeTLSProfileResolver{profile: validGatewayInlineProfile()}}
	if got, err := dMismatch.applyTLSProfile(context.Background(), orig, transport.TransportModeMimicryClaudeCode, 7); got != nil || err == nil {
		t.Fatalf("动态 profile 配到非 sidecar transport 必须失败，got=%#v err=%v", got, err)
	}
}

func validGatewayInlineProfile() *mimicry.InlineTLSProfile {
	return &mimicry.InlineTLSProfile{
		ID:                   "db-profile-77",
		CipherSuites:         []uint16{4865, 49195},
		SupportedGroups:      []uint16{29, 23},
		ECPointFormats:       []uint8{0},
		SignatureAlgorithms:  []uint16{1027, 2052},
		ALPNProtocols:        []string{"http/1.1"},
		TLSSupportedVersions: []uint16{772, 771},
		KeyShareGroups:       []uint16{29},
		PSKModes:             []uint8{1},
		ExtensionsOrder:      []uint16{0, 10, 11, 13, 16, 43, 45, 51},
	}
}
