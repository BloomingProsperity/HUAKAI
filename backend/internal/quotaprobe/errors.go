package quotaprobe

import (
	"errors"
	"fmt"
	"net/http"
)

const (
	ErrorClassDependencyNotConfigured      = "dependency_not_configured"
	ErrorClassCoordinationDependencyFailed = "coordination_dependency_unavailable"
	ErrorClassCoordinationContractInvalid  = "coordination_contract_invalid"
	ErrorClassDatabaseReadFailed           = "database_read_failed"
	ErrorClassDatabaseWriteFailed          = "database_write_failed"
	ErrorClassCredentialResolutionFailed   = "credential_resolution_failed"
	ErrorClassConfigurationInvalid         = "configuration_invalid"
	ErrorClassCredentialUnavailable        = "credential_unavailable"
	ErrorClassCredentialScopeMissing       = "credential_scope_missing"
	ErrorClassProxyResolutionFailed        = "proxy_resolution_failed"
	ErrorClassUpstreamUnreachable          = "upstream_unreachable"
	ErrorClassUpstreamAuthorization        = "upstream_authorization_failed"
	ErrorClassUpstreamRateLimited          = "upstream_rate_limited"
	ErrorClassUpstreamUnavailable          = "upstream_unavailable"
	ErrorClassUpstreamRejected             = "upstream_rejected"
	ErrorClassUpstreamResponseInvalid      = "upstream_response_invalid"
	ErrorClassUpstreamResponseIncomplete   = "upstream_response_incomplete"
	ErrorClassUpstreamPartialResponse      = "upstream_partial_response"
)

type classifiedError struct {
	class string
	err   error
}

func (e classifiedError) Error() string {
	return e.err.Error()
}

func (e classifiedError) Unwrap() error {
	return e.err
}

func withErrorClass(class string, err error) error {
	if err == nil {
		return nil
	}
	return classifiedError{class: class, err: err}
}

func usageErrorClass(err error) string {
	var classified classifiedError
	if errors.As(err, &classified) && classified.class != "" {
		return classified.class
	}
	return ErrorClassUpstreamUnavailable
}

func statusErrorClass(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrorClassUpstreamAuthorization
	case http.StatusTooManyRequests:
		return ErrorClassUpstreamRateLimited
	default:
		if statusCode >= 500 {
			return ErrorClassUpstreamUnavailable
		}
		return ErrorClassUpstreamRejected
	}
}

func upstreamStatusError(statusCode int) error {
	return withErrorClass(statusErrorClass(statusCode), fmt.Errorf("quota probe usage endpoint status %d", statusCode))
}
