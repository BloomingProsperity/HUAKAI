package auth

// NewAPIKeyResolverWithFakeQueries 用任意 apiKeyQueries 实现构造一个 resolver。
// 供需要在无真实数据库的情况下注入 fake 查询结果的外部测试包 (auth_test) 使用。
// clientIPResolver 为 nil 是合法的, 此时回退到仅用 RemoteAddr 解析。
func NewAPIKeyResolverWithFakeQueries(q apiKeyQueries) *APIKeyResolver {
	return &APIKeyResolver{q: q, clientIPResolver: nil}
}
