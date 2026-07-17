package codexagent

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

func NewPostgresRuntimeService(pool *pgxpool.Pool, keys credentialstore.KeyProvider, credentials *credentialstore.Store, proxyResolver provider.ProxyResolver) *Service {
	return NewService(newTaskStore(pool, keys), credentials, newRegistrationClient(http.DefaultClient, proxyResolver))
}
