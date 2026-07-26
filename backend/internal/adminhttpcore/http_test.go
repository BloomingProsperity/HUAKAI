package adminhttpcore

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONStrictContract(t *testing.T) {
	type request struct {
		Name string `json:"name"`
	}

	t.Run("接受唯一且字段已声明的对象", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"primary"}`))
		recorder := httptest.NewRecorder()
		var got request
		if !DecodeJSON(recorder, req, &got) || got.Name != "primary" {
			t.Fatalf("解析结果=%+v，状态=%d，响应=%s", got, recorder.Code, recorder.Body.String())
		}
	})

	t.Run("拒绝未知字段", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"primary","ignored":true}`))
		recorder := httptest.NewRecorder()
		var got request
		if DecodeJSON(recorder, req, &got) {
			t.Fatal("未知字段被静默接受")
		}
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("状态=%d，期望=%d", recorder.Code, http.StatusBadRequest)
		}
		var body map[string]map[string]string
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析错误响应：%v", err)
		}
		if body["error"]["code"] != "invalid_json" {
			t.Fatalf("错误码=%q，期望 invalid_json", body["error"]["code"])
		}
	})

	t.Run("拒绝尾随第二个JSON值", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"primary"} {"name":"shadow"}`))
		recorder := httptest.NewRecorder()
		var got request
		if DecodeJSON(recorder, req, &got) {
			t.Fatal("第二个 JSON 值被静默忽略")
		}
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("状态=%d，期望=%d", recorder.Code, http.StatusBadRequest)
		}
	})
}

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
