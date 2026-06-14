package hermes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newBoundedTestRunner(t *testing.T, url string, httpClient *http.Client) *RunnerClient {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	c, err := NewRunnerClient(RunnerConfig{
		RunnerURL: url, JWTPrivateKey: priv, JWTKID: "kid-test", HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("NewRunnerClient: %v", err)
	}
	return c
}

// TestRunnerClient_DefaultEgressIsBounded proves the production fallback is the
// bounded client, NOT http.DefaultClient: a Transport with connect/TLS/
// response-header timeouts and crucially NO total Client.Timeout (which would
// truncate the SSE stream). MUTATION: revert the NewRunnerClient fallback to
// http.DefaultClient -> Transport is nil / Timeout assertions fail -> RED.
func TestRunnerClient_DefaultEgressIsBounded(t *testing.T) {
	c := newBoundedTestRunner(t, "http://runner.local", nil) // no injected client -> fallback

	if c.httpClient == http.DefaultClient {
		t.Fatal("runner egress must not fall back to the unbounded http.DefaultClient")
	}
	if c.httpClient.Timeout != 0 {
		t.Fatalf("runner client must NOT set a total timeout (would truncate SSE); got %v", c.httpClient.Timeout)
	}
	tr, ok := c.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("runner client transport must be a bounded *http.Transport, got %T", c.httpClient.Transport)
	}
	if tr.ResponseHeaderTimeout != runnerResponseHeaderTimeout || tr.ResponseHeaderTimeout <= 0 {
		t.Fatalf("ResponseHeaderTimeout must be bounded (%v), got %v", runnerResponseHeaderTimeout, tr.ResponseHeaderTimeout)
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Fatalf("TLSHandshakeTimeout must be bounded, got %v", tr.TLSHandshakeTimeout)
	}
}

// TestRunnerClient_ResponseHeaderTimeoutFiresOnHungRunner is the self-proving
// behavioral test: against a runner that accepts the connection but stalls
// before sending response headers, the BOUNDED client must fail fast (at its
// response-header timeout) while the UNBOUNDED http.DefaultClient would wait for
// the full stall. Proves the timeout actually protects the shared resources.
// MUTATION: drop ResponseHeaderTimeout (use DefaultClient) -> the bounded branch
// also waits the full stall -> the "fast" assertion fails -> RED.
func TestRunnerClient_ResponseHeaderTimeoutFiresOnHungRunner(t *testing.T) {
	const stall = 600 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(stall) // hang before writing any response header
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Bounded client with a tiny response-header timeout (proves the mechanism
	// deterministically without waiting the real 50s production bound).
	bounded := &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 50 * time.Millisecond}}
	c := newBoundedTestRunner(t, srv.URL, bounded)

	start := time.Now()
	resp, err := c.Chat(context.Background(), 1, 1, []byte(`{}`))
	elapsed := time.Since(start)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("bounded client must error when the runner stalls past the response-header timeout")
	}
	if !errors.Is(err, ErrRunnerFailure) {
		t.Fatalf("error must wrap ErrRunnerFailure, got %v", err)
	}
	if elapsed >= stall {
		t.Fatalf("bounded client must fail FAST (<%v), took %v", stall, elapsed)
	}

	// Control (self-proving): the unbounded DefaultClient would NOT fail fast —
	// it waits out the stall and gets a 200. This is exactly the brown-out
	// behavior the bounded client removes.
	ctrl := newBoundedTestRunner(t, srv.URL, http.DefaultClient)
	cstart := time.Now()
	cresp, cerr := ctrl.Chat(context.Background(), 1, 1, []byte(`{}`))
	cElapsed := time.Since(cstart)
	if cresp != nil {
		cresp.Body.Close()
	}
	if cerr != nil {
		t.Fatalf("control DefaultClient should reach the server (no header bound), got %v", cerr)
	}
	if cElapsed < stall {
		t.Fatalf("control must have waited out the stall (>=%v) — the test server isn't stalling; got %v", stall, cElapsed)
	}
}
