package gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const dynamicCredentialErrorProbeLimit = 64 << 10

type prefixedResponseBody struct {
	io.Reader
	closer io.Closer
}

func (b *prefixedResponseBody) Close() error {
	if b == nil || b.closer == nil {
		return nil
	}
	return b.closer.Close()
}

func (d *UpstreamDispatcher) doWithDynamicCredentialRecovery(
	ctx context.Context,
	account provider.AccountInfo,
	credential provider.Credential,
	client HTTPDoer,
	request *http.Request,
	rebuild func(provider.Credential) (*http.Request, error),
) (*http.Response, error) {
	response, err := client.Do(request)
	if err != nil || response == nil || response.Body == nil || d.DynamicCredentialRecoverer == nil ||
		response.StatusCode != http.StatusUnauthorized {
		return response, err
	}
	prefix, readErr := io.ReadAll(io.LimitReader(response.Body, dynamicCredentialErrorProbeLimit+1))
	response.Body = &prefixedResponseBody{Reader: io.MultiReader(bytes.NewReader(prefix), response.Body), closer: response.Body}
	if readErr != nil {
		return response, nil
	}
	if !d.DynamicCredentialRecoverer.ShouldRecoverDynamicCredential(account, response.StatusCode, prefix) {
		return response, nil
	}
	recovered, applicable, recoverErr := d.DynamicCredentialRecoverer.RecoverDynamicCredential(ctx, account, credential)
	if recoverErr != nil || !applicable {
		return response, nil
	}
	_ = response.Body.Close()
	retryRequest, err := rebuild(recovered)
	if err != nil {
		return nil, err
	}
	return client.Do(retryRequest)
}
