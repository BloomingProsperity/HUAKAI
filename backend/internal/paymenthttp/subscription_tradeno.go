// HUAKAI · iKun

package paymenthttp

// ExternalTradeNoForTenant 生成一条 tenant-routable 外部交易号 (rech_t<tenant>_<random>),
// 供订阅自助购买在 internal/subscriptionhttp 建订阅订单时复用 —— 与充值开单同格式,
// 保证 provider webhook 能用 tenantIDFromExternalTradeNo 反查回正确租户后履约。
// 单一所有权: 外部交易号格式只在本包定义, 调用方不得自拼前缀。
func ExternalTradeNoForTenant(tenantID int64) (string, error) {
	suffix, err := randomExternalTradeSuffix()
	if err != nil {
		return "", err
	}
	return externalTradeNoForTenant(tenantID, suffix), nil
}
