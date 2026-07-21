package upstreamfeedback

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/channelhealth"
	"github.com/BloomingProsperity/HUAKAI/internal/clienterr"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func healthKey(attempt Attempt) (channelhealth.ChannelKey, bool) {
	key := channelhealth.ChannelKey{
		TenantID:            attempt.TenantID,
		Vendor:              strings.TrimSpace(attempt.Account.Platform),
		ProviderAccountID:   attempt.Account.AccountID,
		AccountCredentialID: attempt.Account.AccountCredentialID,
		CredentialVersion:   attempt.Account.CredentialVersion,
	}
	if err := key.Validate(); err != nil {
		return channelhealth.ChannelKey{}, false
	}
	key.ChannelID = key.StableChannelID()
	return key, true
}

func dispatchSignalClass(err error, decision gateway.AttemptRetryDecision, classification gateway.Classification) channelhealth.SignalClass {
	if err == nil {
		return ""
	}
	switch transport.TransportErrorClassOf(err) {
	case transport.TransportErrorClassSidecarUnavailable,
		transport.TransportErrorClassSidecarProfileUnavailable:
		return ""
	}
	switch decision.TransportClass {
	case gateway.TransportErrorCredentialExpired:
		return channelhealth.SignalAuthChallenge
	case gateway.TransportErrorConnectTimeout,
		gateway.TransportErrorNetworkTimeout,
		gateway.TransportErrorUpstreamHeaderTimeout,
		gateway.TransportErrorUpstreamBodyIdleTimeout:
		return channelhealth.SignalTimeout
	case gateway.TransportErrorTLSHandshakeFailed,
		gateway.TransportErrorConnectionRefused,
		gateway.TransportErrorDNSFailure,
		gateway.TransportErrorNetworkUnreachable,
		gateway.TransportErrorProxyFailure:
		return channelhealth.SignalChannelError
	case gateway.TransportErrorLocalDispatch:
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return channelhealth.SignalTimeout
	}
	return gateway.SignalFromClassification(0, classification)
}

func rateLimitReset(classification gateway.Classification, now time.Time) *time.Time {
	if classification.RetryAfterMs <= 0 {
		return nil
	}
	reset := now.Add(time.Duration(classification.RetryAfterMs) * time.Millisecond)
	return &reset
}

func accountCooldownCandidate(statusCode int) bool {
	switch statusCode {
	case http.StatusTooManyRequests, 529,
		http.StatusRequestTimeout,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func classificationProvider(attempt Attempt) string {
	if strings.EqualFold(strings.TrimSpace(attempt.ProtocolFamily), "bedrock_invoke") {
		return "bedrock"
	}
	if strings.EqualFold(strings.TrimSpace(attempt.ProtocolFamily), "antigravity_session") ||
		(strings.EqualFold(strings.TrimSpace(attempt.Account.Platform), credentialstore.VendorGemini) &&
			strings.EqualFold(strings.TrimSpace(attempt.Account.AccountType), credentialstore.AuthModeAntigravity)) {
		return credentialstore.VendorAntigravity
	}
	return strings.TrimSpace(attempt.Account.Platform)
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func logInternal(ctx context.Context, requestID, code string, err error) {
	clienterr.LogInternal(ctx, requestID, code, err)
}
