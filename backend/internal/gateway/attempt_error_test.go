package gateway

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
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

// TestClassifyAttemptDispatchErrorCredentialExpiredUsesAuthBudget 咬住本地过期
// 不得落回通用 local_dispatch_error：必须在交付前 abort、换号并触发一次热刷新，
// 且只消费独立 auth 子预算，不切换 pool。
func TestClassifyAttemptDispatchErrorCredentialExpiredUsesAuthBudget(t *testing.T) {
	err := errors.Join(errors.New("adapter build failed"), credentialstore.ErrCredentialExpired)
	decision := ClassifyAttemptDispatchError(err)
	assertDecision(t, decision, AttemptRetryDecision{
		RetryableBeforeDelivery:         true,
		SwitchAccount:                   true,
		RefreshIntent:                   RefreshOAuthHotPath,
		ClientStatus:                    http.StatusServiceUnavailable,
		AbortReason:                     "local_credential_expired",
		TransportClass:                  TransportErrorCredentialExpired,
		CountsAgainstAuthFailoverBudget: true,
	})
}

func TestCredentialProtocolIncompatibleDecision(t *testing.T) {
	assertDecision(t, CredentialProtocolIncompatibleDecision(), AttemptRetryDecision{
		RetryableBeforeDelivery:         true,
		SwitchAccount:                   true,
		ClientStatus:                    http.StatusServiceUnavailable,
		AbortReason:                     "credential_protocol_incompatible",
		CountsAgainstAuthFailoverBudget: true,
	})
}

func TestClassifyAttemptHTTPErrorReadsBodyForIronCladSignals(t *testing.T) {
	t.Parallel()

	// GW-02 回归守卫:UpstreamHTTPError.Error() 去掉 body 摘要后,分类必须仍由
	// body 字段(而非 status 单独)驱动 iron-clad 信号检测。
	// 每条用例都自证:带 iron-clad 关键字 body 得到的 class,必须与「相同
	// status + 空 body」基线不同 —— 否则该 fixture 无法证明 body 被读取。
	// (class→decision 的映射由 TestClassifyAttemptHTTPError 表测试覆盖。)
	tests := []struct {
		name      string
		status    int
		body      []byte
		provider  string
		wantClass ErrorClass
	}{
		{
			// 裸 401 → oauth_invalid_grant(R-009);带 token_revoked 关键字
			// → token_revoked(R-004,priority 10 压过 R-009)。body 翻转 class。
			name:      "401 token_revoked body overrides status-only invalid_grant",
			status:    http.StatusUnauthorized,
			body:      []byte(`{"error":"token_revoked","detail":"SENSITIVE_UPSTREAM_MARKER"}`),
			provider:  "openai",
			wantClass: ErrorClassTokenRevoked,
		},
		{
			// 裸 400 不会归为 org_disabled;带 org_disabled 关键字
			// → org_disabled(R-003)。body 翻转 class。
			name:      "400 org_disabled body overrides status-only baseline",
			status:    http.StatusBadRequest,
			body:      []byte(`{"error":"org_disabled","detail":"SENSITIVE_UPSTREAM_MARKER"}`),
			provider:  "openai",
			wantClass: ErrorClassOrgDisabled,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, withBody, err := ClassifyAttemptHTTPError(tt.status, nil, tt.body, tt.provider)
			if err != nil {
				t.Fatalf("ClassifyAttemptHTTPError(with body): %v", err)
			}
			if withBody.Class != tt.wantClass {
				t.Fatalf("with body: class=%q want %q", withBody.Class, tt.wantClass)
			}

			_, statusOnly, err := ClassifyAttemptHTTPError(tt.status, nil, nil, tt.provider)
			if err != nil {
				t.Fatalf("ClassifyAttemptHTTPError(status only): %v", err)
			}
			// 关键断言:body 必须改变分类结果。若两者相等,说明该 fixture
			// 无法证明 body 被读取 —— 这正是
			if withBody.Class == statusOnly.Class {
				t.Fatalf("body did not change classification: with-body and status-only both produced %q; fixture cannot prove body-driven classification", withBody.Class)
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
		{class: TransportErrorUpstreamConnect, wantRetry: true, wantClient: http.StatusBadGateway, wantAbort: "transport_upstream_connect_failed"},
		{class: TransportErrorTLSProfileInvalid, wantRetry: true, wantClient: http.StatusServiceUnavailable, wantAbort: "transport_tls_profile_invalid"},
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

func TestClassifyAttemptDispatchErrorUsesStableSidecarCodes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		code string
		want TransportErrorClass
	}{
		{code: mimicry.SidecarErrorUpstreamDNS, want: TransportErrorDNSFailure},
		{code: mimicry.SidecarErrorConnectionRefused, want: TransportErrorConnectionRefused},
		{code: mimicry.SidecarErrorNetworkUnreachable, want: TransportErrorNetworkUnreachable},
		{code: mimicry.SidecarErrorProxyConnect, want: TransportErrorProxyFailure},
		{code: mimicry.SidecarErrorUpstreamTimeout, want: TransportErrorConnectTimeout},
		{code: mimicry.SidecarErrorTLSHandshake, want: TransportErrorTLSHandshakeFailed},
		{code: mimicry.SidecarErrorUpstreamConnect, want: TransportErrorUpstreamConnect},
		{code: mimicry.SidecarErrorProfileInvalid, want: TransportErrorTLSProfileInvalid},
		{code: mimicry.SidecarErrorProfileUnknown, want: TransportErrorLocalDispatch},
		{code: mimicry.SidecarErrorInternal, want: TransportErrorLocalDispatch},
	} {
		test := test
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()
			error := &mimicry.SidecarError{Code: test.code, Message: "不应参与分类的文本"}
			if got := TransportErrorClassFromError(error); got != test.want {
				t.Fatalf("class=%q want=%q", got, test.want)
			}
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

// 变异: persistentTransportErrorClass 退化返回 TransportErrorNone,或
// ClassifyAttemptTransportError 漏新 case → 子断言红(DM-06:持久传输错必须
// failover 换号,不得归 local_dispatch_error 直接 500 终结)。
func TestClassifyAttemptDispatchError_PersistentTransportFailures(t *testing.T) {
	t.Parallel()

	refused := &url.Error{Op: "Post", URL: "https://api.example.com/v1/messages",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}}
	dns := &url.Error{Op: "Post", URL: "https://api.example.com/v1/messages",
		Err: &net.OpError{Op: "dial", Net: "tcp",
			Err: &net.DNSError{Err: "no such host", Name: "api.example.com", IsNotFound: true}}}
	unreachable := &url.Error{Op: "Post", URL: "https://api.example.com/v1/messages",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)}}
	proxy := &url.Error{Op: "Post", URL: "https://api.example.com/v1/messages",
		Err: errors.New("proxyconnect tcp: dial tcp 10.0.0.9:8080: connect: connection refused")}

	tests := []struct {
		name      string
		err       error
		wantClass TransportErrorClass
		wantAbort string
	}{
		{"connection refused fails over", refused, TransportErrorConnectionRefused, "transport_connection_refused"},
		{"dns not found fails over", dns, TransportErrorDNSFailure, "transport_dns_failure"},
		{"host unreachable fails over", unreachable, TransportErrorNetworkUnreachable, "transport_network_unreachable"},
		{"proxyconnect wins over embedded connection refused", proxy, TransportErrorProxyFailure, "transport_proxy_failure"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			decision := ClassifyAttemptDispatchError(tt.err)
			assertDecision(t, decision, AttemptRetryDecision{
				RetryableBeforeDelivery: true,
				SwitchAccount:           true,
				SwitchPool:              true,
				ClientStatus:            http.StatusBadGateway,
				AbortReason:             tt.wantAbort,
				TransportClass:          tt.wantClass,
			})
		})
	}

	// 回归:无法证明持久/可重试的本地错误仍保守归 local_dispatch_error。
	plain := ClassifyAttemptDispatchError(errors.New("boom"))
	if plain.TransportClass != TransportErrorLocalDispatch || plain.RetryableBeforeDelivery {
		t.Fatalf("plain error 应保守 local_dispatch: %+v", plain)
	}

	// DNS 超时仍是 transient network_timeout,不得误归持久 DNS 故障。
	dnsTimeout := ClassifyAttemptDispatchError(&url.Error{Op: "Post", URL: "https://x",
		Err: &net.DNSError{Err: "i/o timeout", Name: "x", IsTimeout: true}})
	if dnsTimeout.TransportClass != TransportErrorNetworkTimeout {
		t.Fatalf("dns timeout 应 network_timeout: %+v", dnsTimeout)
	}
}
