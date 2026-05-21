package gateway

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "dial tcp: i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

func TestClassifyAttemptHTTPError_TaxonomyTable(t *testing.T) {
	t.Parallel()

	retryAfter := http.Header{}
	retryAfter.Set("Retry-After", "2")

	tests := []struct {
		name           string
		status         int
		headers        http.Header
		body           []byte
		provider       string
		wantRetry      bool
		wantAccount    bool
		wantPool       bool
		wantRefresh    CredentialRefreshIntent
		wantAuthBudget bool
		wantClient     int
		wantAbort      string
		wantClass      ErrorClass
	}{
		{
			name:        "upstream 5xx retries before delivery and can switch account and pool",
			status:      http.StatusInternalServerError,
			provider:    "openai",
			wantRetry:   true,
			wantAccount: true,
			wantPool:    true,
			wantClient:  http.StatusBadGateway,
			wantAbort:   "upstream_5xx",
			wantClass:   ErrorClassServerError,
		},
		{
			name:        "upstream overload retries before delivery and can switch account and pool",
			status:      529,
			provider:    "openai",
			wantRetry:   true,
			wantAccount: true,
			wantPool:    true,
			wantClient:  http.StatusServiceUnavailable,
			wantAbort:   "upstream_overloaded",
			wantClass:   ErrorClassOverloaded,
		},
		{
			name:        "upstream timeout retries before delivery and can switch account and pool",
			status:      http.StatusGatewayTimeout,
			provider:    "anthropic",
			wantRetry:   true,
			wantAccount: true,
			wantPool:    true,
			wantClient:  http.StatusServiceUnavailable,
			wantAbort:   "upstream_timeout",
			wantClass:   ErrorClassUpstreamTimeout,
		},
		{
			name:        "429 retries before delivery and preserves retry after in classification",
			status:      http.StatusTooManyRequests,
			headers:     retryAfter,
			provider:    "openai",
			wantRetry:   true,
			wantAccount: true,
			wantPool:    true,
			wantClient:  http.StatusServiceUnavailable,
			wantAbort:   "upstream_rate_limited",
			wantClass:   ErrorClassRateLimited,
		},
		{
			name:           "401 follows synthesis override and consumes auth failover budget",
			status:         http.StatusUnauthorized,
			body:           []byte(`{"error":"invalid_grant"}`),
			provider:       "openai",
			wantRetry:      true,
			wantAccount:    true,
			wantPool:       false,
			wantRefresh:    RefreshOAuthHotPath,
			wantAuthBudget: true,
			wantClient:     http.StatusUnauthorized,
			wantAbort:      "upstream_auth_failure",
			wantClass:      ErrorClassOAuthInvalidGrant,
		},
		{
			name:       "403 does not retry or fail over",
			status:     http.StatusForbidden,
			provider:   "openai",
			wantClient: http.StatusForbidden,
			wantAbort:  "upstream_forbidden",
			wantClass:  ErrorClassPlatformPolicy,
		},
		{
			name:       "client 400 does not retry",
			status:     http.StatusBadRequest,
			body:       []byte(`{"error":"invalid request"}`),
			provider:   "openai",
			wantClient: http.StatusBadRequest,
			wantAbort:  "upstream_client_4xx",
			wantClass:  ErrorClassUnknown,
		},
		{
			name:       "client 404 does not retry",
			status:     http.StatusNotFound,
			provider:   "openai",
			wantClient: http.StatusNotFound,
			wantAbort:  "upstream_client_4xx",
			wantClass:  ErrorClassUnknown,
		},
		{
			name:       "413 request too large does not retry",
			status:     http.StatusRequestEntityTooLarge,
			provider:   "anthropic",
			wantClient: http.StatusBadRequest,
			wantAbort:  "request_too_large",
			wantClass:  ErrorClassRequestTooLarge,
		},
		{
			name:       "unknown upstream stays terminal by default",
			status:     0,
			body:       []byte("mystery upstream failure"),
			provider:   "openai",
			wantClient: http.StatusBadGateway,
			wantAbort:  "unknown_upstream",
			wantClass:  ErrorClassUnknown,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision, classification, err := ClassifyAttemptHTTPError(tt.status, tt.headers, tt.body, tt.provider)
			if err != nil {
				t.Fatalf("ClassifyAttemptHTTPError: %v", err)
			}
			assertDecision(t, decision, AttemptRetryDecision{
				RetryableBeforeDelivery:         tt.wantRetry,
				SwitchAccount:                   tt.wantAccount,
				SwitchPool:                      tt.wantPool,
				RefreshIntent:                   tt.wantRefresh,
				ClientStatus:                    tt.wantClient,
				AbortReason:                     tt.wantAbort,
				CountsAgainstAuthFailoverBudget: tt.wantAuthBudget,
			})
			if classification.Class != tt.wantClass {
				t.Fatalf("classification.Class=%q want %q", classification.Class, tt.wantClass)
			}
			if tt.status == http.StatusTooManyRequests && classification.RetryAfterMs != 2000 {
				t.Fatalf("RetryAfterMs=%d want 2000", classification.RetryAfterMs)
			}
		})
	}
}

func TestClassifyAttemptDispatchError_TransportTaxonomy(t *testing.T) {
	t.Parallel()

	hostnameErr := x509.HostnameError{Certificate: &x509.Certificate{DNSNames: []string{"api.invalid"}}, Host: "wrong.invalid"}

	tests := []struct {
		name        string
		err         error
		wantClass   TransportErrorClass
		wantRetry   bool
		wantAccount bool
		wantPool    bool
		wantClient  int
		wantAbort   string
	}{
		{
			name:        "context deadline exceeded maps to network timeout",
			err:         context.DeadlineExceeded,
			wantClass:   TransportErrorNetworkTimeout,
			wantRetry:   true,
			wantAccount: true,
			wantPool:    true,
			wantClient:  http.StatusServiceUnavailable,
			wantAbort:   "transport_network_timeout",
		},
		{
			name:        "net timeout maps to network timeout",
			err:         timeoutNetError{},
			wantClass:   TransportErrorNetworkTimeout,
			wantRetry:   true,
			wantAccount: true,
			wantPool:    true,
			wantClient:  http.StatusServiceUnavailable,
			wantAbort:   "transport_network_timeout",
		},
		{
			name: "dial timeout maps to connect timeout",
			err: &url.Error{
				Op:  "Get",
				URL: "https://provider.invalid",
				Err: &net.OpError{Op: "dial", Net: "tcp", Err: timeoutNetError{}},
			},
			wantClass:   TransportErrorConnectTimeout,
			wantRetry:   true,
			wantAccount: true,
			wantPool:    true,
			wantClient:  http.StatusServiceUnavailable,
			wantAbort:   "transport_connect_timeout",
		},
		{
			name:        "header timeout maps to upstream header timeout",
			err:         &url.Error{Op: "Get", URL: "https://provider.invalid", Err: errors.New("net/http: timeout awaiting response headers")},
			wantClass:   TransportErrorUpstreamHeaderTimeout,
			wantRetry:   true,
			wantAccount: true,
			wantPool:    true,
			wantClient:  http.StatusServiceUnavailable,
			wantAbort:   "transport_upstream_header_timeout",
		},
		{
			name:        "TLS x509 error maps to TLS handshake failure",
			err:         hostnameErr,
			wantClass:   TransportErrorTLSHandshakeFailed,
			wantRetry:   true,
			wantAccount: true,
			wantPool:    true,
			wantClient:  http.StatusBadGateway,
			wantAbort:   "transport_tls_handshake_failed",
		},
		{
			name:       "local URL configuration error stays terminal",
			err:        &url.Error{Op: "Post", URL: "://bad-url", Err: errors.New("unsupported protocol scheme")},
			wantClass:  TransportErrorLocalDispatch,
			wantClient: http.StatusInternalServerError,
			wantAbort:  "local_dispatch_error",
		},
		{
			name:       "adapter missing error stays terminal",
			err:        errors.New("protocol adapter missing for provider"),
			wantClass:  TransportErrorLocalDispatch,
			wantClient: http.StatusInternalServerError,
			wantAbort:  "local_dispatch_error",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			decision := ClassifyAttemptDispatchError(tt.err)
			assertDecision(t, decision, AttemptRetryDecision{
				RetryableBeforeDelivery: tt.wantRetry,
				SwitchAccount:           tt.wantAccount,
				SwitchPool:              tt.wantPool,
				ClientStatus:            tt.wantClient,
				AbortReason:             tt.wantAbort,
				TransportClass:          tt.wantClass,
			})
		})
	}
}

func TestClassifyAttemptTransportError_FutureRustClasses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		class      TransportErrorClass
		wantRetry  bool
		wantClient int
		wantAbort  string
	}{
		{class: TransportErrorConnectTimeout, wantRetry: true, wantClient: http.StatusServiceUnavailable, wantAbort: "transport_connect_timeout"},
		{class: TransportErrorNetworkTimeout, wantRetry: true, wantClient: http.StatusServiceUnavailable, wantAbort: "transport_network_timeout"},
		{class: TransportErrorTLSHandshakeFailed, wantRetry: true, wantClient: http.StatusBadGateway, wantAbort: "transport_tls_handshake_failed"},
		{class: TransportErrorUpstreamHeaderTimeout, wantRetry: true, wantClient: http.StatusServiceUnavailable, wantAbort: "transport_upstream_header_timeout"},
		{class: TransportErrorUpstreamBodyIdleTimeout, wantRetry: true, wantClient: http.StatusServiceUnavailable, wantAbort: "transport_upstream_body_idle_timeout"},
		{class: TransportErrorLocalDispatch, wantClient: http.StatusInternalServerError, wantAbort: "local_dispatch_error"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(string(tt.class), func(t *testing.T) {
			t.Parallel()

			decision := ClassifyAttemptTransportError(tt.class)
			assertDecision(t, decision, AttemptRetryDecision{
				RetryableBeforeDelivery: tt.wantRetry,
				SwitchAccount:           tt.wantRetry,
				SwitchPool:              tt.wantRetry,
				ClientStatus:            tt.wantClient,
				AbortReason:             tt.wantAbort,
				TransportClass:          tt.class,
			})
		})
	}
}

func assertDecision(t *testing.T, got, want AttemptRetryDecision) {
	t.Helper()
	if got.RetryableBeforeDelivery != want.RetryableBeforeDelivery ||
		got.SwitchAccount != want.SwitchAccount ||
		got.SwitchPool != want.SwitchPool ||
		got.RefreshIntent != want.RefreshIntent ||
		got.ClientStatus != want.ClientStatus ||
		got.AbortReason != want.AbortReason ||
		got.TransportClass != want.TransportClass ||
		got.CountsAgainstAuthFailoverBudget != want.CountsAgainstAuthFailoverBudget {
		t.Fatalf("decision mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

var _ net.Error = timeoutNetError{}
