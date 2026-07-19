package mimicry

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sidecarTestSocket = "/run/huakai/tls-sidecar.sock"

func TestSidecarClientDialTLSWritesVersionedControlAndReturnsPlaintextConn(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, &shortWriteConn{Conn: clientConn, max: 3})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		req := readSidecarTestRequest(t, serverConn)
		if req.Version != SidecarProtocolVersion || req.Operation != sidecarOperationConnect {
			t.Errorf("协议头 = version:%d operation:%q", req.Version, req.Operation)
			return
		}
		if req.TargetHost != "api.anthropic.com" || req.Port != 443 || req.ProfileID != SidecarProfileAnthropicCLIMimicryV1 {
			t.Errorf("控制请求 = %+v", req)
			return
		}
		if req.InlineProfile != nil {
			t.Errorf("内置 profile 请求不应携带 inline_profile: %+v", req.InlineProfile)
			return
		}
		if req.ForceH1 != nil {
			t.Errorf("默认请求不应携带 force_h1: %v", *req.ForceH1)
			return
		}
		writeSidecarTestAck(t, serverConn, sidecarControlAck{Version: SidecarProtocolVersion, OK: true})
		if _, err := serverConn.Write([]byte("plaintext")); err != nil {
			t.Errorf("write plaintext: %v", err)
		}
	}()

	client := NewSidecarClient(sidecarTestSocket)
	conn, err := client.DialTLS(context.Background(), "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1, nil, nil)
	if err != nil {
		t.Fatalf("DialTLS: %v", err)
	}
	defer conn.Close()
	buf := make([]byte, len("plaintext"))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read plaintext: %v", err)
	}
	if string(buf) != "plaintext" {
		t.Fatalf("plaintext = %q", buf)
	}
	<-serverDone
}

func TestSidecarClientForceH1CrossesTheVersionedWire(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		req := readSidecarTestRequest(t, serverConn)
		if req.ForceH1 == nil || !*req.ForceH1 {
			t.Errorf("显式 ForceH1 必须进入控制帧，request=%+v", req)
			return
		}
		writeSidecarTestAck(t, serverConn, sidecarControlAck{Version: SidecarProtocolVersion, OK: true})
	}()

	client := NewSidecarClient(sidecarTestSocket)
	conn, err := client.dialTLS(context.Background(), "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1, nil, true, nil)
	if err != nil {
		t.Fatalf("dialTLS: %v", err)
	}
	conn.Close()
	<-serverDone
}

func TestPinnedSidecarRoundTripperBindsResolvedAddressesIntoControlFrame(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		req := readSidecarTestRequest(t, serverConn)
		if !reflect.DeepEqual(req.PinnedTargetIPs, []string{"8.8.8.8", "2001:4860:4860::8888"}) {
			t.Errorf("目标地址绑定=%v", req.PinnedTargetIPs)
			return
		}
		writeSidecarTestAck(t, serverConn, sidecarControlAck{Version: SidecarProtocolVersion, OK: true})
	}()

	roundTripper, err := NewPinnedSidecarRoundTripper(
		NewSidecarClient(sidecarTestSocket),
		SidecarProfileOperatorSourceSafeV1,
		func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("8.8.8.8"),
				netip.MustParseAddr("::ffff:8.8.8.8"),
				netip.MustParseAddr("2001:4860:4860::8888"),
			}, nil
		},
	)
	if err != nil {
		t.Fatalf("NewPinnedSidecarRoundTripper: %v", err)
	}
	transport := roundTripper.(*sidecarTransport)
	conn, err := transport.boundRT.DialTLSContext(context.Background(), "tcp", "operator.example:443")
	if err != nil {
		t.Fatalf("DialTLSContext: %v", err)
	}
	conn.Close()
	<-serverDone
}

func TestPinnedSidecarRoundTripperRejectsEmptyResolutionBeforeSidecarDial(t *testing.T) {
	oldDial := sidecarDialContext
	var dialed bool
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		dialed = true
		return nil, errors.New("unexpected")
	}
	t.Cleanup(func() { sidecarDialContext = oldDial })
	roundTripper, err := NewPinnedSidecarRoundTripper(
		NewSidecarClient(sidecarTestSocket),
		SidecarProfileOperatorSourceSafeV1,
		func(context.Context, string) ([]netip.Addr, error) { return nil, nil },
	)
	if err != nil {
		t.Fatalf("NewPinnedSidecarRoundTripper: %v", err)
	}
	transport := roundTripper.(*sidecarTransport)
	if _, err := transport.boundRT.DialTLSContext(context.Background(), "tcp", "operator.example:443"); err == nil {
		t.Fatal("空解析结果必须失败")
	}
	if dialed {
		t.Fatal("地址校验失败后不得连接 sidecar")
	}
}

func TestSidecarClientDialTLSBadSocketFailsClosedWithoutTargetFallback(t *testing.T) {
	dialCalls := make([]string, 0, 1)
	oldDial := sidecarDialContext
	sidecarDialContext = func(_ context.Context, network, address string) (net.Conn, error) {
		dialCalls = append(dialCalls, network+" "+address)
		return nil, errors.New("missing sidecar socket")
	}
	t.Cleanup(func() { sidecarDialContext = oldDial })
	client := NewSidecarClient("/run/huakai/missing-sidecar.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	conn, err := client.DialTLS(ctx, "127.0.0.1", 443, SidecarProfileAnthropicCLIMimicryV1, nil, nil)

	if err == nil {
		conn.Close()
		t.Fatal("sidecar socket 缺失时必须 fail-closed")
	}
	if !strings.Contains(err.Error(), "sidecar") {
		t.Fatalf("错误应标识 sidecar 失败，实际 %v", err)
	}
	if !reflect.DeepEqual(dialCalls, []string{"unix /run/huakai/missing-sidecar.sock"}) {
		t.Fatalf("不得回退拨目标，拨号记录=%v", dialCalls)
	}
}

func TestSidecarClientDialTLSNoAckTimesOutFailClosed(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	requestRead := make(chan struct{})
	releaseServer := make(chan struct{})
	go func() {
		_ = readSidecarTestRequest(t, serverConn)
		close(requestRead)
		<-releaseServer
	}()
	client := NewSidecarClient(sidecarTestSocket)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	conn, err := client.DialTLS(ctx, "api.anthropic.com", 443, SidecarProfileAnthropicCLIMimicryV1, nil, nil)

	close(releaseServer)
	if err == nil {
		conn.Close()
		t.Fatal("sidecar 不响应时必须 fail-closed")
	}
	<-requestRead
	if !strings.Contains(err.Error(), "read ack frame") {
		t.Fatalf("错误应定位 ACK 阶段，实际 %v", err)
	}
}

func TestSidecarClientDialTLSCancellationWithoutDeadlineInterruptsAckWait(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	requestRead := make(chan struct{})
	go func() {
		_ = readSidecarTestRequest(t, serverConn)
		close(requestRead)
	}()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := NewSidecarClient(sidecarTestSocket).DialTLS(
			ctx,
			"api.anthropic.com",
			443,
			SidecarProfileAnthropicCLIMimicryV1,
			nil,
			nil,
		)
		result <- err
	}()
	<-requestRead

	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消必须返回 context.Canceled，实际 %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("无 deadline 的取消必须立即打断 ACK 等待")
	}
}

func TestSidecarInspectUsesReadyAndReturnsCapabilities(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	go func() {
		req := readSidecarTestRequest(t, serverConn)
		if req.Version != SidecarProtocolVersion || req.Operation != sidecarOperationReady {
			t.Errorf("ready 请求 = %+v", req)
			return
		}
		if req.TargetHost != "" || req.Port != 0 || req.ProfileID != "" || req.InlineProfile != nil {
			t.Errorf("ready 不应伪造上游请求: %+v", req)
			return
		}
		writeSidecarTestAck(t, serverConn, sidecarControlAck{
			Version:      SidecarProtocolVersion,
			OK:           true,
			Capabilities: []string{SidecarCapabilityBuiltinProfile, SidecarCapabilityInlineProfile},
			ProfileIDs:   []string{SidecarProfileAnthropicCLIMimicryV1},
		})
	}()

	status, err := NewSidecarClient(sidecarTestSocket).Inspect(context.Background())
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if status.Version != SidecarProtocolVersion || !containsString(status.Capabilities, SidecarCapabilityInlineProfile) {
		t.Fatalf("status = %+v", status)
	}
	if !reflect.DeepEqual(status.ProfileIDs, []string{SidecarProfileAnthropicCLIMimicryV1}) {
		t.Fatalf("profile_ids = %v", status.ProfileIDs)
	}
}

func TestProbeSidecarForModeRejectsMissingProfileFromReady(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	go func() {
		_ = readSidecarTestRequest(t, serverConn)
		writeSidecarTestAck(t, serverConn, sidecarControlAck{
			Version:      SidecarProtocolVersion,
			OK:           true,
			Capabilities: []string{SidecarCapabilityBuiltinProfile},
			ProfileIDs:   []string{SidecarProfileGeminiCLIV1},
		})
	}()

	err := ProbeSidecarForMode(context.Background(), sidecarTestSocket, ModeMimicryClaudeCode)

	if !errors.Is(err, ErrSidecarProfileUnavailable) {
		t.Fatalf("缺少 profile 必须归类为 profile unavailable，实际 %v", err)
	}
}

func TestProbeSidecarForModeRejectsMissingForceH1Capability(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	go func() {
		_ = readSidecarTestRequest(t, serverConn)
		writeSidecarTestAck(t, serverConn, sidecarControlAck{
			Version:      SidecarProtocolVersion,
			OK:           true,
			Capabilities: []string{SidecarCapabilityBuiltinProfile},
			ProfileIDs:   []string{SidecarProfileAnthropicCLIMimicryV1},
		})
	}()

	err := ProbeSidecarForMode(context.Background(), sidecarTestSocket, ModeMimicryClaudeCode)

	if !errors.Is(err, ErrSidecarUnavailable) || !strings.Contains(err.Error(), SidecarCapabilityForceH1) {
		t.Fatalf("缺少 force_h1 capability 必须 fail-closed，实际 %v", err)
	}
}

func TestProbeSidecarReadinessRequiresAllCapabilitiesAndProfiles(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	go func() {
		_ = readSidecarTestRequest(t, serverConn)
		writeSidecarTestAck(t, serverConn, sidecarControlAck{
			Version:      SidecarProtocolVersion,
			OK:           true,
			Capabilities: append([]string(nil), requiredSidecarCapabilities...),
			ProfileIDs:   append([]string(nil), requiredSidecarProfiles...),
		})
	}()

	if err := ProbeSidecarReadiness(context.Background(), sidecarTestSocket); err != nil {
		t.Fatalf("完整 sidecar 合同应通过 readiness：%v", err)
	}
}

func TestProbeSidecarReadinessRejectsIncompleteRuntime(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	go func() {
		_ = readSidecarTestRequest(t, serverConn)
		writeSidecarTestAck(t, serverConn, sidecarControlAck{
			Version:      SidecarProtocolVersion,
			OK:           true,
			Capabilities: append([]string(nil), requiredSidecarCapabilities...),
			ProfileIDs:   []string{SidecarProfileAnthropicCLIMimicryV1},
		})
	}()

	err := ProbeSidecarReadiness(context.Background(), sidecarTestSocket)
	if !errors.Is(err, ErrSidecarProfileUnavailable) || !strings.Contains(err.Error(), SidecarProfileOpenAICodexCLIV1) {
		t.Fatalf("缺少内置 profile 必须失败并点名缺项：%v", err)
	}
}

func TestSidecarInspectRejectsWrongAckVersion(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	go func() {
		_ = readSidecarTestRequest(t, serverConn)
		writeSidecarTestAck(t, serverConn, sidecarControlAck{Version: SidecarProtocolVersion - 1, OK: true})
	}()

	_, err := NewSidecarClient(sidecarTestSocket).Inspect(context.Background())

	if !errors.Is(err, ErrSidecarUnavailable) {
		t.Fatalf("协议版本错必须归类为 sidecar unavailable，实际 %v", err)
	}
	var sidecarErr *SidecarError
	if !errors.As(err, &sidecarErr) || sidecarErr.Code != SidecarErrorProtocolUnsupported {
		t.Fatalf("错误未保留稳定协议码: %v", err)
	}
}

func TestSidecarInspectRejectsUnknownAckField(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	go func() {
		_ = readSidecarTestRequest(t, serverConn)
		writeSidecarTestFrame(t, serverConn, []byte(`{"version":3,"ok":true,"future_semantics":true}`))
	}()

	_, err := NewSidecarClient(sidecarTestSocket).Inspect(context.Background())

	if !errors.Is(err, ErrSidecarUnavailable) || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("未知 ACK 字段必须 fail-closed，实际 %v", err)
	}
}

func TestSidecarClientPreservesStructuredProfileError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	go func() {
		_ = readSidecarTestRequest(t, serverConn)
		writeSidecarTestAck(t, serverConn, sidecarControlAck{
			Version: SidecarProtocolVersion,
			OK:      false,
			Error: &sidecarControlError{
				Code:    SidecarErrorProfileUnknown,
				Message: "profile 不存在",
			},
		})
	}()

	_, err := NewSidecarClient(sidecarTestSocket).DialTLS(
		context.Background(), "api.example.com", 443, "missing-profile", nil, nil,
	)

	if !errors.Is(err, ErrSidecarProfileUnavailable) {
		t.Fatalf("profile 错误分类不正确: %v", err)
	}
	var sidecarErr *SidecarError
	if !errors.As(err, &sidecarErr) || sidecarErr.Code != SidecarErrorProfileUnknown || sidecarErr.Message != "profile 不存在" {
		t.Fatalf("结构化错误丢失: %#v", sidecarErr)
	}
}

func TestSidecarRoundTripperThreadsInlineProfileWithoutBuiltinID(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	restoreSidecarDial(t, clientConn)
	profile := validInlineTLSProfile()
	want := profile.clone()
	captured := make(chan sidecarControlRequest, 1)
	go func() {
		req := readSidecarTestRequest(t, serverConn)
		captured <- req
		writeSidecarTestAck(t, serverConn, sidecarControlAck{Version: SidecarProtocolVersion, OK: true})
	}()

	base := NewSidecarRoundTripper(NewSidecarClient(sidecarTestSocket), SidecarProfileAnthropicCLIMimicryV1)
	transport := base.(*sidecarTransport)
	bound, err := transport.WithInlineTLSProfile(profile)
	if err != nil {
		t.Fatalf("WithInlineTLSProfile: %v", err)
	}
	profile.ID = "mutated-after-bind"
	profile.CipherSuites[0] = 999
	conn, err := bound.(*sidecarTransport).boundRT.DialTLSContext(context.Background(), "tcp", "api.example.com:443")
	if err != nil {
		t.Fatalf("DialTLSContext: %v", err)
	}
	conn.Close()

	req := <-captured
	if req.ProfileID != "" || req.InlineProfile == nil {
		t.Fatalf("动态请求必须只带 inline_profile: %+v", req)
	}
	if !reflect.DeepEqual(req.InlineProfile, want) {
		t.Fatalf("inline profile 在线路中丢字段或被绑定后修改\ngot=%+v\nwant=%+v", req.InlineProfile, want)
	}
}

func TestSidecarClientRejectsAmbiguousOrInvalidProfileBeforeDial(t *testing.T) {
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("无效 profile 不得拨 sidecar")
		return nil, nil
	}
	t.Cleanup(func() { sidecarDialContext = oldDial })
	client := NewSidecarClient(sidecarTestSocket)

	for name, tc := range map[string]struct {
		profileID string
		inline    *InlineTLSProfile
	}{
		"两者都空":   {},
		"两者都有":   {profileID: SidecarProfileAnthropicCLIMimicryV1, inline: validInlineTLSProfile()},
		"动态字段非法": {inline: &InlineTLSProfile{ID: "bad"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.DialTLS(context.Background(), "api.example.com", 443, tc.profileID, tc.inline, nil)
			if err == nil {
				t.Fatal("无效 profile 必须被拒绝")
			}
		})
	}
}

func validInlineTLSProfile() *InlineTLSProfile {
	return &InlineTLSProfile{
		ID:                   "tenant-profile-v1",
		CipherSuites:         []uint16{4865, 4866, 49195},
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

func restoreSidecarDial(t *testing.T, conn net.Conn) {
	t.Helper()
	oldDial := sidecarDialContext
	sidecarDialContext = func(context.Context, string, string) (net.Conn, error) { return conn, nil }
	t.Cleanup(func() { sidecarDialContext = oldDial })
}

type shortWriteConn struct {
	net.Conn
	max int
}

func (c *shortWriteConn) Write(p []byte) (int, error) {
	if len(p) > c.max {
		p = p[:c.max]
	}
	return c.Conn.Write(p)
}

func readSidecarTestRequest(t *testing.T, conn net.Conn) sidecarControlRequest {
	t.Helper()
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		t.Errorf("read prefix: %v", err)
		return sidecarControlRequest{}
	}
	n := binary.LittleEndian.Uint32(prefix[:])
	if n == binary.BigEndian.Uint32(prefix[:]) {
		t.Errorf("fixture 必须能区分小端和大端帧长")
		return sidecarControlRequest{}
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Errorf("read body: %v", err)
		return sidecarControlRequest{}
	}
	var req sidecarControlRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Errorf("decode request: %v", err)
	}
	return req
}

func writeSidecarTestAck(t *testing.T, conn net.Conn, ack sidecarControlAck) {
	t.Helper()
	body, err := json.Marshal(ack)
	if err != nil {
		t.Fatal(err)
	}
	writeSidecarTestFrame(t, conn, body)
}

func writeSidecarTestFrame(t *testing.T, conn net.Conn, body []byte) {
	t.Helper()
	var prefix [4]byte
	binary.LittleEndian.PutUint32(prefix[:], uint32(len(body)))
	if _, err := conn.Write(prefix[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(body); err != nil {
		t.Fatal(err)
	}
}
