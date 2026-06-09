package auth

// NewAPIKeyResolverWithFakeQueries constructs a resolver backed by an arbitrary
// apiKeyQueries implementation. Used by external test packages (auth_test) that
// need to inject fake query results without a real database.
// A nil clientIPResolver is valid and falls back to RemoteAddr-only resolution.
func NewAPIKeyResolverWithFakeQueries(q apiKeyQueries) *APIKeyResolver {
	return &APIKeyResolver{q: q, clientIPResolver: nil}
}
