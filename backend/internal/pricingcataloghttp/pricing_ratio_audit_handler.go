package pricingcataloghttp

import (
	"net/http"

	"github.com/BloomingProsperity/HUAKAI/internal/admin"
)

// ratioAuditVerifyResponse 刻意保持最小化：只暴露通过/失败的结论、
// 第一条出问题的记录 id，以及一句简短的人类可读原因。
// 密钥材料、签名、prev/entry 哈希以及底层倍率值都绝不回传，
// 这样运维人员做完整性证明时也不会泄露审计机密。
type ratioAuditVerifyResponse struct {
	Object string `json:"object"`
	OK     bool   `json:"ok"`
	RowID  int64  `json:"row_id,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// newRatioAuditVerifyHandler 对已签名的定价倍率审计哈希链做完整性证明。
// 它复用与倍率写入处理器相同的平台管理员门槛：只有被解析为
// RolePlatformAdmin 的调用方才能运行这次链证明。
func newRatioAuditVerifyHandler(d AdminPricingRatioDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Auth == nil || d.Store == nil {
			writeError(w, http.StatusServiceUnavailable, "gateway_not_configured", "pricing ratio dependency unset")
			return
		}
		ident, err := d.Auth.Resolve(r.Context(), r)
		if err != nil {
			writeAdminAuthError(w, err)
			return
		}
		if ident.Role != admin.RolePlatformAdmin {
			writeAdminError(w, admin.ErrAdminForbidden)
			return
		}
		result, err := d.Store.VerifyChain(r.Context())
		if err != nil {
			writeRatioStoreError(w, "pricing_ratio_audit_verify_failed", err)
			return
		}
		writeJSON(w, http.StatusOK, ratioAuditVerifyResponse{
			Object: "pricing_ratio_audit_verification",
			OK:     result.OK,
			RowID:  result.RowID,
			Reason: result.Reason,
		})
	}
}
