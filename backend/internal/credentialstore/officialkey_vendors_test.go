// HUAKAI · iKun
package credentialstore

import (
	"errors"
	"testing"
)

// officialKeyVendors:2026-07-02 Owner 指派接入的官 key 厂商全集(与 0169 迁移白名单同步)。
var officialKeyVendors = []string{
	VendorGrok, VendorDeepSeek, VendorKimi,
	VendorQwen, VendorGLM, VendorYi, VendorBaichuan,
	VendorDoubao, VendorMiniMax, VendorErnie, VendorHunyuan, VendorStep,
}

// TestOfficialKeyAPIKeyHandlers 守护:12 家官 key 厂商都注册了 api_key handlerSpec,
// 且物化行为正确(Kind=api_key、Value=原样 key、缺 key fail-closed)。
// 变异验证:删掉 defaultHandlers 里任一新 handlerSpec 行 → 对应子测试 MustLookup 红。
func TestOfficialKeyAPIKeyHandlers(t *testing.T) {
	reg := DefaultHandlerRegistry()
	for _, vendor := range officialKeyVendors {
		t.Run(vendor, func(t *testing.T) {
			h, err := reg.MustLookup(vendor, AuthModeAPIKey)
			if err != nil {
				t.Fatalf("%s/api_key 应已注册 handlerSpec:%v", vendor, err)
			}
			if h.RuntimeKind() != RuntimeAPIKey {
				t.Fatalf("%s/api_key runtimeKind=%q want %q", vendor, h.RuntimeKind(), RuntimeAPIKey)
			}
			if h.Refreshable() {
				t.Fatalf("%s/api_key 是静态官 key,不应参与刷新调度", vendor)
			}
			// 判别性 fixture:每家用不同 key 值,断言逐字节原样透出(而非只断非空)。
			key := "sk-live-" + vendor + "-0001"
			m, err := h.RuntimeMaterial([]byte(`{"api_key":"` + key + `"}`))
			if err != nil {
				t.Fatalf("%s/api_key 物化失败:%v", vendor, err)
			}
			if m.Kind != RuntimeAPIKey || m.Value != key {
				t.Fatalf("%s/api_key 物化结果 kind=%q value=%q want kind=%q value=%q", vendor, m.Kind, m.Value, RuntimeAPIKey, key)
			}
			// fail-closed:缺 api_key 字段必须拒绝(校验与物化两层都要拒)。
			if err := h.ValidatePayload([]byte(`{"note":"missing key"}`)); !errors.Is(err, ErrInvalidPayload) {
				t.Fatalf("%s/api_key 缺 key 校验应报 ErrInvalidPayload,得 %v", vendor, err)
			}
			if _, err := h.RuntimeMaterial([]byte(`{}`)); err == nil {
				t.Fatalf("%s/api_key 空 payload 物化应失败", vendor)
			}
		})
	}
}

// TestHostedCloudVendorsStayUnregistered 边界锁:全球推理托管云(Owner 明确不接)
// 不得拥有 api_key handlerSpec——防止「顺手全量放开」把不接的厂商一并放进来
// (存储层 0169 CHECK 同样未放行,这里锁代码层)。
func TestHostedCloudVendorsStayUnregistered(t *testing.T) {
	reg := DefaultHandlerRegistry()
	for _, vendor := range []string{
		VendorOpenRouter, VendorMistral, VendorGroqCloud,
		VendorTogether, VendorPerplexity, VendorFireworks, VendorCursor,
	} {
		if _, err := reg.MustLookup(vendor, AuthModeAPIKey); !errors.Is(err, ErrUnknownMode) {
			t.Fatalf("%s/api_key 不应注册(Owner:托管云不接),got err=%v", vendor, err)
		}
	}
}
