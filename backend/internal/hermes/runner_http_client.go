package hermes

import (
	"net"
	"net/http"
	"time"
)

// Bounds for runner egress. These deliberately do NOT include a total
// Client.Timeout: RunnerClient.Chat returns a long-lived Server-Sent-Events
// stream that the chat bridge reads incrementally, and a blanket deadline would
// truncate a healthy stream mid-token. Instead we bound only the phases a sick
// or unreachable runner can stall on — connecting, the TLS handshake, and the
// wait for response headers — so the streaming body itself stays unbounded
// (governed by the inbound request context).
const (
	// runnerConnectTimeout caps dial time. A DOWN runner (refused/black-holed)
	// fails here in seconds instead of hanging until the inbound deadline.
	runnerConnectTimeout = 5 * time.Second
	// runnerTLSHandshakeTimeout caps the TLS handshake to a hung TLS peer.
	runnerTLSHandshakeTimeout = 5 * time.Second
	// runnerResponseHeaderTimeout caps the wait between finishing the request
	// write and receiving the response headers. An SSE runner writes its 200 +
	// text/event-stream headers before generating tokens, so this bounds a hung
	// runner WITHOUT cutting a slow-but-working stream's body. Kept under the
	// 60s control-plane deadline so it is the tighter bound on the bad path.
	runnerResponseHeaderTimeout = 50 * time.Second
	runnerExpectContinueTimeout = 1 * time.Second
	runnerIdleConnTimeout       = 90 * time.Second
	runnerMaxIdleConns          = 64
	runnerMaxIdleConnsPerHost   = 16
)

// defaultRunnerHTTPClient returns the bounded HTTP client used for runner egress
// in production. Without it the client falls back to http.DefaultClient, which
// has no connect/TLS/response-header bounds: a burst of admin chats against a
// dead or hung runner would each hold a goroutine (and the SSE read) until the
// inbound 60s deadline, piling up on resources shared with the core data plane.
// The bounded client makes a bad runner fail fast instead.
func defaultRunnerHTTPClient() *http.Client {
	return &http.Client{
		// No Timeout: streaming responses must not be truncated. See the const block.
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: runnerConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   runnerTLSHandshakeTimeout,
			ResponseHeaderTimeout: runnerResponseHeaderTimeout,
			ExpectContinueTimeout: runnerExpectContinueTimeout,
			IdleConnTimeout:       runnerIdleConnTimeout,
			MaxIdleConns:          runnerMaxIdleConns,
			MaxIdleConnsPerHost:   runnerMaxIdleConnsPerHost,
			ForceAttemptHTTP2:     true,
		},
	}
}
