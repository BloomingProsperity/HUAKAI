package adminhttpcore

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRequiredPositiveQueryInt64(t *testing.T) {
	t.Run("接受正整数", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/?tenant_id=42", nil)
		recorder := httptest.NewRecorder()

		got, ok := ParseRequiredPositiveQueryInt64(recorder, req, "tenant_id")
		if !ok || got != 42 {
			t.Fatalf("解析结果=(%d,%t)，期望=(42,true)", got, ok)
		}
		if recorder.Code != http.StatusOK {
			t.Fatalf("成功解析不应写响应，状态=%d", recorder.Code)
		}
	})

	for _, raw := range []string{"", "0", "-1", "not-a-number"} {
		t.Run("拒绝_"+raw, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?tenant_id="+raw, nil)
			recorder := httptest.NewRecorder()

			got, ok := ParseRequiredPositiveQueryInt64(recorder, req, "tenant_id")
			if ok || got != 0 {
				t.Fatalf("解析结果=(%d,%t)，期望拒绝", got, ok)
			}
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("状态=%d，期望=%d，响应=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
		})
	}
}
