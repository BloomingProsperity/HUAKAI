// R7.5 测试：覆盖 metadata.user_id 双格式解析、组件替换、Fallback 注入、
// preserve/force 模式、版本检测等核心路径。
package gateway

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	testHex64Original  = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	testHex64Override  = "1111111111111111111111111111111111111111111111111111111111111111"
	testUUID36Original = "11111111-2222-3333-4444-555555555555"
	testUUID36Override = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testAcctOriginal   = "00000000-1111-2222-3333-444444444444"
	testAcctOverride   = "ffffffff-1111-2222-3333-aaaaaaaaaaaa"
)

func legacyUserID(device, acct, session string) string {
	return "user_" + device + "_account_" + acct + "_session_" + session
}

func jsonUserID(device, acct, session string) string {
	b, _ := json.Marshal(map[string]string{
		"device_id":    device,
		"account_uuid": acct,
		"session_id":   session,
	})
	return string(b)
}

func TestParseMetadataUserID(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantNil   bool
		wantNew   bool
		wantDev   string
		wantAcct  string
		wantSess  string
	}{
		{name: "空串", raw: "", wantNil: true},
		{name: "纯空白", raw: "   ", wantNil: true},
		{
			name:     "legacy 标准形态",
			raw:      legacyUserID(testHex64Original, testAcctOriginal, testUUID36Original),
			wantDev:  testHex64Original,
			wantAcct: testAcctOriginal,
			wantSess: testUUID36Original,
		},
		{
			name:     "legacy account 为空",
			raw:      legacyUserID(testHex64Original, "", testUUID36Original),
			wantDev:  testHex64Original,
			wantAcct: "",
			wantSess: testUUID36Original,
		},
		{
			name:    "legacy hex 长度错误",
			raw:     "user_abc_account_x_session_" + testUUID36Original,
			wantNil: true,
		},
		{
			name:     "JSON 标准形态",
			raw:      jsonUserID(testHex64Original, testAcctOriginal, testUUID36Original),
			wantNew:  true,
			wantDev:  testHex64Original,
			wantAcct: testAcctOriginal,
			wantSess: testUUID36Original,
		},
		{
			name:    "JSON 缺 device_id",
			raw:     `{"account_uuid":"x","session_id":"y"}`,
			wantNil: true,
		},
		{
			name:    "JSON 不合法",
			raw:     `{"device_id":`,
			wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMetadataUserID(tc.raw)
			if (got == nil) != tc.wantNil {
				t.Fatalf("got=%v wantNil=%v", got, tc.wantNil)
			}
			if got == nil {
				return
			}
			if got.IsNewFormat != tc.wantNew {
				t.Errorf("IsNewFormat=%v want %v", got.IsNewFormat, tc.wantNew)
			}
			if got.DeviceID != tc.wantDev {
				t.Errorf("DeviceID=%q want %q", got.DeviceID, tc.wantDev)
			}
			if got.AccountUUID != tc.wantAcct {
				t.Errorf("AccountUUID=%q want %q", got.AccountUUID, tc.wantAcct)
			}
			if got.SessionID != tc.wantSess {
				t.Errorf("SessionID=%q want %q", got.SessionID, tc.wantSess)
			}
		})
	}
}

func TestFormatMetadataUserID(t *testing.T) {
	t.Run("legacy 形态", func(t *testing.T) {
		got := FormatMetadataUserID("dev", "acct", "sess", false)
		want := "user_dev_account_acct_session_sess"
		if got != want {
			t.Errorf("got=%q want=%q", got, want)
		}
	})
	t.Run("JSON 形态", func(t *testing.T) {
		got := FormatMetadataUserID("dev", "acct", "sess", true)
		var parsed map[string]string
		if err := json.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("不是合法 JSON: %v", err)
		}
		if parsed["device_id"] != "dev" || parsed["account_uuid"] != "acct" || parsed["session_id"] != "sess" {
			t.Errorf("JSON 字段不对: %v", parsed)
		}
	})
}

func TestIsNewMetadataFormatVersion(t *testing.T) {
	cases := []struct {
		ver  string
		want bool
	}{
		{"", false},
		{"2.1.77", false},
		{"2.1.78", true},
		{"2.1.79", true},
		{"2.2.0", true},
		{"3.0.0", true},
		{"1.99.99", false},
	}
	for _, c := range cases {
		t.Run(c.ver, func(t *testing.T) {
			if got := IsNewMetadataFormatVersion(c.ver); got != c.want {
				t.Errorf("ver=%s got=%v want=%v", c.ver, got, c.want)
			}
		})
	}
}

func TestRewriteMetadataUserID_Table(t *testing.T) {
	// 公共原始 user_id —— legacy 形态。
	originalLegacy := legacyUserID(testHex64Original, testAcctOriginal, testUUID36Original)

	tests := []struct {
		name        string
		input       string
		plan        MetadataUserIDPlan
		wantApplied bool
		wantReason  string
		wantErr     bool
		assertBody  func(t *testing.T, res MetadataUserIDResult)
	}{
		{
			name:       "空 body 返回 invalid_body",
			input:      "",
			plan:       MetadataUserIDPlan{FallbackUserID: "x"},
			wantReason: reasonMetaInvalidBody,
			wantErr:    true,
		},
		{
			name:       "plan 完全空返回 empty_plan",
			input:      `{"metadata":{"user_id":"` + originalLegacy + `"}}`,
			plan:       MetadataUserIDPlan{},
			wantReason: reasonMetaEmptyPlan,
		},
		{
			name: "rewrite 模式按组件替换 legacy 形态",
			input: `{"metadata":{"user_id":"` + originalLegacy + `"}}`,
			plan: MetadataUserIDPlan{
				Mode:        MetadataInjectRewrite,
				DeviceID:    testHex64Override,
				AccountUUID: testAcctOverride,
				SessionID:   testUUID36Override,
			},
			wantApplied: true,
			wantReason:  reasonMetaRewrote,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				want := legacyUserID(testHex64Override, testAcctOverride, testUUID36Override)
				if res.FinalUserID != want {
					t.Errorf("FinalUserID=%q want %q", res.FinalUserID, want)
				}
				if res.ParsedBefore == nil || res.ParsedBefore.DeviceID != testHex64Original {
					t.Errorf("ParsedBefore 缺失 / 不对: %+v", res.ParsedBefore)
				}
				assertMetadataUserID(t, res.Body, want)
			},
		},
		{
			name:  "rewrite 仅替换部分组件",
			input: `{"metadata":{"user_id":"` + originalLegacy + `"}}`,
			plan: MetadataUserIDPlan{
				Mode:     MetadataInjectRewrite,
				DeviceID: testHex64Override,
				// AccountUUID 与 SessionID 留空 → 沿用原值
			},
			wantApplied: true,
			wantReason:  reasonMetaRewrote,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				want := legacyUserID(testHex64Override, testAcctOriginal, testUUID36Original)
				if res.FinalUserID != want {
					t.Errorf("FinalUserID=%q want %q", res.FinalUserID, want)
				}
			},
		},
		{
			name:  "rewrite 时切换到 JSON 形态输出",
			input: `{"metadata":{"user_id":"` + originalLegacy + `"}}`,
			plan: MetadataUserIDPlan{
				Mode:         MetadataInjectRewrite,
				DeviceID:     testHex64Override,
				UseNewFormat: true,
			},
			wantApplied: true,
			wantReason:  reasonMetaRewrote,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				if !strings.HasPrefix(res.FinalUserID, "{") {
					t.Errorf("FinalUserID 应是 JSON: %q", res.FinalUserID)
				}
				p := ParseMetadataUserID(res.FinalUserID)
				if p == nil || !p.IsNewFormat {
					t.Errorf("FinalUserID 不可被解析为 JSON: %q", res.FinalUserID)
				}
			},
		},
		{
			name:  "rewrite 解析 JSON 形态输入并替换组件",
			input: `{"metadata":{"user_id":` + jsonStringEscape(jsonUserID(testHex64Original, testAcctOriginal, testUUID36Original)) + `}}`,
			plan: MetadataUserIDPlan{
				Mode:         MetadataInjectRewrite,
				SessionID:    testUUID36Override,
				UseNewFormat: true,
			},
			wantApplied: true,
			wantReason:  reasonMetaRewrote,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				p := ParseMetadataUserID(res.FinalUserID)
				if p == nil {
					t.Fatalf("FinalUserID 不可解析: %q", res.FinalUserID)
				}
				if p.SessionID != testUUID36Override {
					t.Errorf("SessionID=%q want %q", p.SessionID, testUUID36Override)
				}
				if p.DeviceID != testHex64Original {
					t.Errorf("DeviceID 沿用应为 original，实际 %q", p.DeviceID)
				}
			},
		},
		{
			name:  "metadata 字段缺失时按 fallback 注入",
			input: `{"model":"claude-3-5"}`,
			plan: MetadataUserIDPlan{
				Mode:           MetadataInjectRewrite,
				FallbackUserID: "user_FALLBACK",
			},
			wantApplied: true,
			wantReason:  reasonMetaInjected,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				assertMetadataUserID(t, res.Body, "user_FALLBACK")
			},
		},
		{
			name:  "user_id 不可解析且有 fallback → unparseable_used_fallback",
			input: `{"metadata":{"user_id":"random_garbage"}}`,
			plan: MetadataUserIDPlan{
				Mode:           MetadataInjectRewrite,
				FallbackUserID: "user_FB",
			},
			wantApplied: true,
			wantReason:  reasonMetaUnparseableAndFallback,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				assertMetadataUserID(t, res.Body, "user_FB")
			},
		},
		{
			name:  "user_id 不可解析且无 fallback → unparseable_no_fallback",
			input: `{"metadata":{"user_id":"random_garbage"}}`,
			plan: MetadataUserIDPlan{
				Mode:     MetadataInjectRewrite,
				DeviceID: "x",
			},
			wantReason: reasonMetaUnparseableNoFallback,
		},
		{
			name:  "preserve 模式：合法 user_id 保持不动",
			input: `{"metadata":{"user_id":"` + originalLegacy + `"}}`,
			plan: MetadataUserIDPlan{
				Mode:           MetadataInjectPreserveExisting,
				FallbackUserID: "user_should_not_be_used",
			},
			wantReason: reasonMetaPreserved,
		},
		{
			name:  "preserve 模式：user_id 缺失时注入 fallback",
			input: `{"metadata":{}}`,
			plan: MetadataUserIDPlan{
				Mode:           MetadataInjectPreserveExisting,
				FallbackUserID: "user_FB",
			},
			wantApplied: true,
			wantReason:  reasonMetaInjected,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				assertMetadataUserID(t, res.Body, "user_FB")
			},
		},
		{
			name:  "preserve 模式：user_id 不可解析时按 fallback 注入",
			input: `{"metadata":{"user_id":"garbage"}}`,
			plan: MetadataUserIDPlan{
				Mode:           MetadataInjectPreserveExisting,
				FallbackUserID: "user_FB",
			},
			wantApplied: true,
			wantReason:  reasonMetaInjected,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				assertMetadataUserID(t, res.Body, "user_FB")
			},
		},
		{
			name:  "force_fallback 模式无条件覆盖",
			input: `{"metadata":{"user_id":"` + originalLegacy + `"}}`,
			plan: MetadataUserIDPlan{
				Mode:           MetadataInjectForceFallback,
				FallbackUserID: "user_FORCE",
			},
			wantApplied: true,
			wantReason:  reasonMetaRewrote,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				assertMetadataUserID(t, res.Body, "user_FORCE")
			},
		},
		{
			name:       "metadata 字段非对象 → unsupported_metadata_shape",
			input:      `{"metadata":"not an object"}`,
			plan:       MetadataUserIDPlan{Mode: MetadataInjectRewrite, FallbackUserID: "x"},
			wantReason: reasonMetaUnsupportedMetadataShape,
		},
		{
			name:        "metadata 已有其它字段时保留",
			input:       `{"metadata":{"trace_id":"abc","user_id":"` + originalLegacy + `"}}`,
			plan:        MetadataUserIDPlan{Mode: MetadataInjectRewrite, DeviceID: testHex64Override},
			wantApplied: true,
			wantReason:  reasonMetaRewrote,
			assertBody: func(t *testing.T, res MetadataUserIDResult) {
				var root map[string]json.RawMessage
				if err := json.Unmarshal(res.Body, &root); err != nil {
					t.Fatal(err)
				}
				var meta map[string]json.RawMessage
				if err := json.Unmarshal(root["metadata"], &meta); err != nil {
					t.Fatal(err)
				}
				if _, ok := meta["trace_id"]; !ok {
					t.Errorf("trace_id 字段在改写后丢失")
				}
			},
		},
		{
			name:       "metadata=null 等同缺失",
			input:      `{"metadata":null}`,
			plan:       MetadataUserIDPlan{Mode: MetadataInjectRewrite, FallbackUserID: "user_FB"},
			wantApplied: true,
			wantReason: reasonMetaInjected,
		},
		{
			name:       "user_id 字段不是字符串 → 视为不可解析",
			input:      `{"metadata":{"user_id":123}}`,
			plan:       MetadataUserIDPlan{Mode: MetadataInjectRewrite, FallbackUserID: "user_FB"},
			wantApplied: true,
			wantReason: reasonMetaInjected, // user_id 不算字符串 → 视为缺失 → injected
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			res, err := RewriteMetadataUserID([]byte(tt.input), tt.plan)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if res.Reason != tt.wantReason {
				t.Errorf("Reason=%q want %q", res.Reason, tt.wantReason)
			}
			if res.Applied != tt.wantApplied {
				t.Errorf("Applied=%v want %v", res.Applied, tt.wantApplied)
			}
			if tt.assertBody != nil {
				tt.assertBody(t, res)
			}
		})
	}
}

// assertMetadataUserID 断言 body 中 metadata.user_id == want。
func assertMetadataUserID(t *testing.T, body []byte, want string) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatalf("解析 body：%v", err)
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(root["metadata"], &meta); err != nil {
		t.Fatalf("解析 metadata：%v", err)
	}
	var got string
	if err := json.Unmarshal(meta["user_id"], &got); err != nil {
		t.Fatalf("user_id 不是字符串：%v", err)
	}
	if got != want {
		t.Errorf("user_id=%q want %q", got, want)
	}
}

// jsonStringEscape 把字符串编码为合法 JSON 字符串字面量（带引号）。
func jsonStringEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
