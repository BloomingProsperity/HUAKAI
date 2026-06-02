// HUAKAI · iKun

package payment

const maxOutTradeNoLen = 128

// validateOutTradeNo 校验 caller 提供的外部订单号: 必须稳定 (用于幂等), 长度受限, 仅 [A-Za-z0-9_-]。
// P1 不再 server 端自动生成 — caller 必须给稳定值, 否则重试会建出第二张可入账订单 (双账)。
func validateOutTradeNo(s string) error {
	if s == "" || len(s) > maxOutTradeNoLen {
		return ErrInvalidInput
	}
	for _, r := range s {
		switch {
		case r == '-' || r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		default:
			return ErrInvalidInput
		}
	}
	return nil
}
