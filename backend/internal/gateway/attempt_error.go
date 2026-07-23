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
	"syscall"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
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
	TransportErrorCredentialExpired       TransportErrorClass = "local_credential_expired"
	TransportErrorLocalDispatch           TransportErrorClass = "local_dispatch_error"

	// DM-06 持久型传输错误:重试同一账号几乎必然再失败(端点拒连/域名
	// 解析失败/路由不可达/代理握手失败),必须立刻 failover 换号并计入
	// channelhealth,而非归 local_dispatch_error 直接 500 终结。
	TransportErrorConnectionRefused  TransportErrorClass = "connection_refused"
	TransportErrorDNSFailure         TransportErrorClass = "dns_failure"
	TransportErrorNetworkUnreachable TransportErrorClass = "network_unreachable"
	TransportErrorProxyFailure       TransportErrorClass = "proxy_failure"
	TransportErrorUpstreamConnect    TransportErrorClass = "upstream_connect_failed"
	TransportErrorTLSProfileInvalid  TransportErrorClass = "tls_profile_invalid"
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
	// ClientCode/ClientMessage/ClientRuleID 是账号显式规则命中后的可选客户端投影。
	// 它们不参与重试、换号、鉴权刷新、健康分类或计费终态。
	ClientCode    string
	ClientMessage string
	ClientRuleID  string
	AbortReason   string

	// TransportClass 保留传输层原始分类，便于 PR3 写审计与 channelhealth 信号。
	TransportClass TransportErrorClass

	// CountsAgainstAuthFailoverBudget 标记综合稿 override-1 的 401 子预算。
	// 该预算独立于普通 attempt budget，整个请求最多消费一次。
	CountsAgainstAuthFailoverBudget bool
}

// CredentialProtocolIncompatibleDecision 返回发网前账号身份不匹配时的统一换号决策。
// 该失败尚未产生上游副作用，因此允许排除当前账号并使用至多一次的鉴权换号预算。
func CredentialProtocolIncompatibleDecision() AttemptRetryDecision {
	return AttemptRetryDecision{
		RetryableBeforeDelivery:         true,
		SwitchAccount:                   true,
		ClientStatus:                    http.StatusServiceUnavailable,
		AbortReason:                     "credential_protocol_incompatible",
		CountsAgainstAuthFailoverBudget: true,
	}
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
	if errors.Is(err, credentialstore.ErrCredentialExpired) {
		return TransportErrorCredentialExpired
	}
	var coded interface{ TransportErrorCode() string }
	if errors.As(err, &coded) {
		return transportErrorClassFromStableCode(coded.TransportErrorCode())
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
	if class := persistentTransportErrorClass(err, lower); class != TransportErrorNone {
		return class
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

func transportErrorClassFromStableCode(code string) TransportErrorClass {
	switch code {
	case mimicry.SidecarErrorUpstreamDNS:
		return TransportErrorDNSFailure
	case mimicry.SidecarErrorConnectionRefused:
		return TransportErrorConnectionRefused
	case mimicry.SidecarErrorNetworkUnreachable:
		return TransportErrorNetworkUnreachable
	case mimicry.SidecarErrorProxyInvalid, mimicry.SidecarErrorProxyConnect:
		return TransportErrorProxyFailure
	case mimicry.SidecarErrorUpstreamTimeout:
		return TransportErrorConnectTimeout
	case mimicry.SidecarErrorTLSHandshake:
		return TransportErrorTLSHandshakeFailed
	case mimicry.SidecarErrorUpstreamConnect:
		return TransportErrorUpstreamConnect
	case mimicry.SidecarErrorProfileInvalid:
		return TransportErrorTLSProfileInvalid
	default:
		return TransportErrorLocalDispatch
	}
}

// persistentTransportErrorClass 识别持久型传输错误(DM-06)。proxyconnect
// 判定必须最先:Go transport 的代理 CONNECT 失败串里常内嵌 connection
// refused,故障应归代理而非目标端点。DNS 超时仍归 network_timeout
// (transient),只有解析失败才算持久 DNS 故障。
func persistentTransportErrorClass(err error, lower string) TransportErrorClass {
	if strings.Contains(lower, "proxyconnect") || strings.Contains(lower, "proxy authentication required") {
		return TransportErrorProxyFailure
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsTimeout {
			return TransportErrorNetworkTimeout
		}
		return TransportErrorDNSFailure
	}
	if errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(lower, "connection refused") {
		return TransportErrorConnectionRefused
	}
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) ||
		strings.Contains(lower, "host is unreachable") || strings.Contains(lower, "network is unreachable") {
		return TransportErrorNetworkUnreachable
	}
	if strings.Contains(lower, "no such host") {
		return TransportErrorDNSFailure
	}
	return TransportErrorNone
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
	case TransportErrorConnectionRefused:
		return retryableTransportDecision(class, http.StatusBadGateway, "transport_connection_refused")
	case TransportErrorDNSFailure:
		return retryableTransportDecision(class, http.StatusBadGateway, "transport_dns_failure")
	case TransportErrorNetworkUnreachable:
		return retryableTransportDecision(class, http.StatusBadGateway, "transport_network_unreachable")
	case TransportErrorProxyFailure:
		return retryableTransportDecision(class, http.StatusBadGateway, "transport_proxy_failure")
	case TransportErrorUpstreamConnect:
		return retryableTransportDecision(class, http.StatusBadGateway, "transport_upstream_connect_failed")
	case TransportErrorTLSProfileInvalid:
		return retryableTransportDecision(class, http.StatusServiceUnavailable, "transport_tls_profile_invalid")
	case TransportErrorCredentialExpired:
		return AttemptRetryDecision{
			RetryableBeforeDelivery:         true,
			SwitchAccount:                   true,
			RefreshIntent:                   RefreshOAuthHotPath,
			ClientStatus:                    http.StatusServiceUnavailable,
			AbortReason:                     "local_credential_expired",
			TransportClass:                  class,
			CountsAgainstAuthFailoverBudget: true,
		}
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

// EndClassFromAttempt 把一次交付前失败的分类与传输决策收敛成 Router 可判定的稳定终态。
// 非流式与流式 executor 必须共用这张映射，避免同一种上游错误在不同协议上出现不同重试语义。
func EndClassFromAttempt(classification Classification, decision AttemptRetryDecision) StreamEndClass {
	var draft UsageRecordDraft
	ApplyClassificationToDraft(&draft, classification)
	if draft.EndClass != "" && draft.EndClass != UnknownTermination {
		return draft.EndClass
	}
	switch decision.TransportClass {
	case TransportErrorConnectTimeout,
		TransportErrorNetworkTimeout,
		TransportErrorUpstreamHeaderTimeout,
		TransportErrorUpstreamBodyIdleTimeout:
		return InterEventTimeout
	case TransportErrorTLSHandshakeFailed,
		TransportErrorCredentialExpired,
		TransportErrorConnectionRefused,
		TransportErrorDNSFailure,
		TransportErrorNetworkUnreachable,
		TransportErrorProxyFailure,
		TransportErrorUpstreamConnect,
		TransportErrorTLSProfileInvalid:
		return UpstreamError5xx
	}
	switch decision.AbortReason {
	case "upstream_5xx", "upstream_overloaded", "pool_no_capacity",
		"pool_select_error", "pool_select_no_account", "credential_resolve_error",
		"upstream_dispatch_error", "upstream_empty_response",
		"local_credential_expired",
		"transport_connection_refused", "transport_dns_failure",
		"transport_network_unreachable", "transport_proxy_failure",
		"transport_upstream_connect_failed", "transport_tls_profile_invalid":
		return UpstreamError5xx
	case "upstream_rate_limited", "queue_wait":
		return UpstreamRateLimit
	case "upstream_timeout", "transport_connect_timeout", "transport_network_timeout",
		"transport_upstream_header_timeout", "transport_upstream_body_idle_timeout":
		return InterEventTimeout
	}
	return UnknownTermination
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
	case ErrorClassProjectContextRejected:
		return AttemptRetryDecision{
			RetryableBeforeDelivery: true,
			SwitchAccount:           true,
			SwitchPool:              true,
			RefreshIntent:           RefreshOAuthHotPath,
			ClientStatus:            http.StatusServiceUnavailable,
			AbortReason:             "project_context_rejected",
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
