package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// TransportErrorClass 是 Go dispatcher 与未来 Rust transport sidecar 共用的
// attempt 级传输错误分类。空值表示没有传输层分类。
type TransportErrorClass string

const (
	TransportErrorNone                    TransportErrorClass = ""
	TransportErrorConnectTimeout          TransportErrorClass = "connect_timeout"
	TransportErrorNetworkTimeout          TransportErrorClass = "network_timeout"
	TransportErrorTLSHandshakeFailed      TransportErrorClass = "tls_handshake_failed"
	TransportErrorUpstreamHeaderTimeout   TransportErrorClass = "upstream_header_timeout"
	TransportErrorUpstreamBodyIdleTimeout TransportErrorClass = "upstream_body_idle_timeout"
	TransportErrorLocalDispatch           TransportErrorClass = "local_dispatch_error"
)

// CredentialRefreshIntent 表示 attempt 失败后是否应触发凭据热刷新路径。
type CredentialRefreshIntent string

const (
	RefreshNone         CredentialRefreshIntent = ""
	RefreshOAuthHotPath CredentialRefreshIntent = "oauth_hot_path"
)

// AttemptRetryDecision 是 handler attempt loop 的稳定输入。PR2 只产出决策，
// PR3 才消费它执行 retry/failover。
type AttemptRetryDecision struct {
	RetryableBeforeDelivery bool
	SwitchAccount           bool
	SwitchPool              bool
	RefreshIntent           CredentialRefreshIntent
	ClientStatus            int
	AbortReason             string

	// TransportClass 保留传输层原始分类，便于 PR3 写审计与 channelhealth 信号。
	TransportClass TransportErrorClass

	// CountsAgainstAuthFailoverBudget 标记综合稿 override-1 的 401 子预算。
	// 该预算独立于普通 attempt budget，整个请求最多消费一次。
	CountsAgainstAuthFailoverBudget bool
}

// ClassifyAttemptHTTPError 将上游 HTTP 错误先交给既有 Classify 归一化，
// 再收敛成 attempt loop 可消费的 retry decision。
func ClassifyAttemptHTTPError(httpStatus int, headers http.Header, body []byte, provider string) (AttemptRetryDecision, Classification, error) {
	classification, err := Classify(httpStatus, headers, body, provider)
	if err != nil {
		return AttemptRetryDecision{}, Classification{}, err
	}
	return decisionFromHTTPClassification(httpStatus, classification), classification, nil
}

// ClassifyAttemptDispatchError 将本地出站错误映射为 transport class，再复用
// 与 Rust sidecar 相同的 decision 表。
func ClassifyAttemptDispatchError(err error) AttemptRetryDecision {
	class := TransportErrorClassFromError(err)
	return ClassifyAttemptTransportError(class)
}

// TransportErrorClassFromError 尽量从 Go 标准库错误链中提取稳定传输分类。
// 无法证明是可重试传输错误时，保守归入 local_dispatch_error。
func TransportErrorClassFromError(err error) TransportErrorClass {
	if err == nil {
		return TransportErrorNone
	}

	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "awaiting response headers"),
		strings.Contains(lower, "response header timeout"),
		strings.Contains(lower, "header timeout"):
		return TransportErrorUpstreamHeaderTimeout
	case strings.Contains(lower, "body idle timeout"),
		strings.Contains(lower, "idle timeout"):
		return TransportErrorUpstreamBodyIdleTimeout
	}

	if isTLSError(err, lower) {
		return TransportErrorTLSHandshakeFailed
	}
	if strings.Contains(lower, "connect timeout") || strings.Contains(lower, "connection timeout") {
		return TransportErrorConnectTimeout
	}

	var netOpErr *net.OpError
	if errors.As(err, &netOpErr) && strings.EqualFold(netOpErr.Op, "dial") && isTimeoutError(err) {
		return TransportErrorConnectTimeout
	}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		op := strings.ToLower(urlErr.Op)
		if op == "dial" && isTimeoutError(err) {
			return TransportErrorConnectTimeout
		}
		if isTimeoutError(err) {
			return TransportErrorNetworkTimeout
		}
		return TransportErrorLocalDispatch
	}
	if isTimeoutError(err) {
		return TransportErrorNetworkTimeout
	}
	return TransportErrorLocalDispatch
}

// ClassifyAttemptTransportError 是未来 Rust transport class 的接入口。
func ClassifyAttemptTransportError(class TransportErrorClass) AttemptRetryDecision {
	switch class {
	case TransportErrorConnectTimeout:
		return retryableTransportDecision(class, http.StatusServiceUnavailable, "transport_connect_timeout")
	case TransportErrorNetworkTimeout:
		return retryableTransportDecision(class, http.StatusServiceUnavailable, "transport_network_timeout")
	case TransportErrorTLSHandshakeFailed:
		return retryableTransportDecision(class, http.StatusBadGateway, "transport_tls_handshake_failed")
	case TransportErrorUpstreamHeaderTimeout:
		return retryableTransportDecision(class, http.StatusServiceUnavailable, "transport_upstream_header_timeout")
	case TransportErrorUpstreamBodyIdleTimeout:
		return retryableTransportDecision(class, http.StatusServiceUnavailable, "transport_upstream_body_idle_timeout")
	case TransportErrorNone:
		return AttemptRetryDecision{}
	default:
		return AttemptRetryDecision{
			ClientStatus:   http.StatusInternalServerError,
			AbortReason:    "local_dispatch_error",
			TransportClass: class,
		}
	}
}

func decisionFromHTTPClassification(httpStatus int, c Classification) AttemptRetryDecision {
	switch c.Class {
	case ErrorClassServerError:
		return retryableHTTPDecision(http.StatusBadGateway, "upstream_5xx")
	case ErrorClassOverloaded:
		return retryableHTTPDecision(http.StatusServiceUnavailable, "upstream_overloaded")
	case ErrorClassUpstreamTimeout:
		return retryableHTTPDecision(http.StatusServiceUnavailable, "upstream_timeout")
	case ErrorClassRateLimited:
		return retryableHTTPDecision(http.StatusServiceUnavailable, "upstream_rate_limited")
	case ErrorClassOAuthInvalidGrant, ErrorClassTokenRevoked:
		// 见 docs/process/plans/2026-05-21-phase1-design-synthesis.md §3
		// override-1: 401 可交付前换一次号，但只消费 auth 子预算，
		// 不把它当普通 channelhealth degraded 信号。upstream_auth_failure
		// 故意不进 RoutePlan.RetryableEndClasses；executor 必须用
		// RetryableEndClasses 或 CountsAgainstAuthFailoverBudget 双通道判定重试。
		return AttemptRetryDecision{
			RetryableBeforeDelivery:         true,
			SwitchAccount:                   true,
			RefreshIntent:                   RefreshOAuthHotPath,
			ClientStatus:                    http.StatusUnauthorized,
			AbortReason:                     "upstream_auth_failure",
			CountsAgainstAuthFailoverBudget: true,
		}
	case ErrorClassPlatformPolicy:
		return AttemptRetryDecision{
			ClientStatus: http.StatusForbidden,
			AbortReason:  "upstream_forbidden",
		}
	case ErrorClassRequestTooLarge:
		return AttemptRetryDecision{
			ClientStatus: http.StatusBadRequest,
			AbortReason:  "request_too_large",
		}
	case ErrorClassNetworkTimeout:
		return retryableHTTPDecision(http.StatusServiceUnavailable, "transport_network_timeout")
	}

	if httpStatus >= 400 && httpStatus <= 499 {
		return AttemptRetryDecision{
			ClientStatus: httpStatus,
			AbortReason:  "upstream_client_4xx",
		}
	}
	return AttemptRetryDecision{
		ClientStatus: http.StatusBadGateway,
		AbortReason:  "unknown_upstream",
	}
}

func retryableHTTPDecision(clientStatus int, abortReason string) AttemptRetryDecision {
	return AttemptRetryDecision{
		RetryableBeforeDelivery: true,
		SwitchAccount:           true,
		SwitchPool:              true,
		ClientStatus:            clientStatus,
		AbortReason:             abortReason,
	}
}

func retryableTransportDecision(class TransportErrorClass, clientStatus int, abortReason string) AttemptRetryDecision {
	return AttemptRetryDecision{
		RetryableBeforeDelivery: true,
		SwitchAccount:           true,
		SwitchPool:              true,
		ClientStatus:            clientStatus,
		AbortReason:             abortReason,
		TransportClass:          class,
	}
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func isTLSError(err error, lower string) bool {
	var certVerifyErr *tls.CertificateVerificationError
	var recordHeaderErr tls.RecordHeaderError
	var hostnameErr x509.HostnameError
	var unknownAuthorityErr x509.UnknownAuthorityError
	var certInvalidErr x509.CertificateInvalidError
	var systemRootsErr x509.SystemRootsError
	if errors.As(err, &certVerifyErr) ||
		errors.As(err, &recordHeaderErr) ||
		errors.As(err, &hostnameErr) ||
		errors.As(err, &unknownAuthorityErr) ||
		errors.As(err, &certInvalidErr) ||
		errors.As(err, &systemRootsErr) {
		return true
	}
	return strings.Contains(lower, "tls handshake") ||
		strings.Contains(lower, "tls:") ||
		strings.Contains(lower, "x509:")
}
