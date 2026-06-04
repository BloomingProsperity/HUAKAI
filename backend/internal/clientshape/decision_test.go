// decision_test.go — U6-D-2 测试: ClientShape decision 选择器决策树。
//
// 覆盖优先级（synthesis §"选择优先级"）:
//   1. explicit path 强 contract
//   2. route_config 显式声明
//   3. identity 仅当 IdentityAware=true + confidence ≥ 0.7 才填空白
//   4. default fallback OpenAIChat
//
// 验证 conflict 标记 + 不互越界（identity 不覆盖 path/route）。
package clientshape

import (
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/clientid"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
)

func TestSelect_ExplicitPathWins(t *testing.T) {
	cases := []struct {
		path string
		want proto.ClientProtocol
	}{
		{"/v1/chat/completions", proto.ClientProtocolOpenAIChat},
		{"/v1/chat/completions?stream=true", proto.ClientProtocolOpenAIChat},
		{"/v1/responses", proto.ClientProtocolOpenAIResponses},
		{"/v1/messages", proto.ClientProtocolAnthropicMessages},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			d := Select(Inputs{Path: tc.path})
			if d.ClientProtocol != tc.want {
				t.Errorf("ClientProtocol=%q want %q", d.ClientProtocol, tc.want)
			}
			if d.Source != SourceExplicitPath {
				t.Errorf("Source=%q want %q", d.Source, SourceExplicitPath)
			}
			if d.Confidence != 1.0 {
				t.Errorf("Confidence=%.2f want 1.0", d.Confidence)
			}
		})
	}
}

func TestSelect_PathBeatsRouteConfig_OnWellKnownPath(t *testing.T) {
	// well-known path /v1/chat/completions 强 wire-contract，应优先于 route_config
	d := Select(Inputs{
		Path:              "/v1/chat/completions", // path 推断 OpenAIChat
		RouteConfigClient: proto.ClientProtocolAnthropicMessages,
	})
	if d.ClientProtocol != proto.ClientProtocolOpenAIChat {
		t.Errorf("well-known path 应优先 RouteConfig，得 %q", d.ClientProtocol)
	}
	if d.Source != SourceExplicitPath {
		t.Errorf("Source=%q want %q", d.Source, SourceExplicitPath)
	}
}

func TestSelect_RouteConfigUsedWhenPathNotWellKnown(t *testing.T) {
	// path 非 well-known → route_config 显式声明使用
	d := Select(Inputs{
		Path:              "/custom/gateway/route",
		RouteConfigClient: proto.ClientProtocolAnthropicMessages,
	})
	if d.ClientProtocol != proto.ClientProtocolAnthropicMessages {
		t.Errorf("path 非已知时 RouteConfig 应胜出，得 %q", d.ClientProtocol)
	}
	if d.Source != SourceRouteConfig {
		t.Errorf("Source=%q want %q", d.Source, SourceRouteConfig)
	}
}

func TestSelect_IdentityFillsAmbiguousPath_OnlyWhenIdentityAware(t *testing.T) {
	// path 不命中已知，IdentityAware=true 且 confidence≥0.7 → identity 填
	d := Select(Inputs{
		Path:          "/custom/generic/route", // 未知 path
		Identity:      clientid.IdentityCursor,
		IdentityConf:  0.9,
		IdentityAware: true,
	})
	if d.ClientProtocol != proto.ClientProtocolOpenAIChat {
		t.Errorf("Cursor identity 应推 OpenAIChat，得 %q", d.ClientProtocol)
	}
	if d.Source != SourceIdentity {
		t.Errorf("Source=%q want %q", d.Source, SourceIdentity)
	}
}

func TestSelect_IdentityIgnoredWhenIdentityAwareFalse(t *testing.T) {
	// IdentityAware=false → identity 仅观测，决策走 default
	d := Select(Inputs{
		Path:          "/custom/generic/route",
		Identity:      clientid.IdentityCursor,
		IdentityConf:  0.95,
		IdentityAware: false,
	})
	if d.Source != SourceDefault {
		t.Errorf("IdentityAware=false 应走 default，得 %q", d.Source)
	}
	if d.ClientProtocol != proto.ClientProtocolOpenAIChat {
		t.Errorf("default 应 OpenAIChat，得 %q", d.ClientProtocol)
	}
}

func TestSelect_IdentityIgnoredBelowConfidenceThreshold(t *testing.T) {
	// confidence < MinIdentityConfidence → 不信
	d := Select(Inputs{
		Path:          "/custom/generic/route",
		Identity:      clientid.IdentityClaudeCode,
		IdentityConf:  0.5, // < 0.7
		IdentityAware: true,
	})
	if d.Source != SourceDefault {
		t.Errorf("低 confidence 应 default，得 %q", d.Source)
	}
}

func TestSelect_PathWinsOverIdentity_NoOverride(t *testing.T) {
	// path 命中 + identity 推断不同 → path 优先 + 标 Conflict
	d := Select(Inputs{
		Path:          "/v1/chat/completions", // → OpenAIChat
		Identity:      clientid.IdentityClaudeCode, // → AnthropicMessages
		IdentityConf:  0.9,
		IdentityAware: true,
	})
	if d.ClientProtocol != proto.ClientProtocolOpenAIChat {
		t.Errorf("path 应优先于 identity，得 %q", d.ClientProtocol)
	}
	if d.Source != SourceExplicitPath {
		t.Errorf("Source=%q want %q", d.Source, SourceExplicitPath)
	}
	if !d.Conflict {
		t.Error("应标 Conflict (claude_code identity vs OpenAIChat path)")
	}
	if d.ConflictReason == "" {
		t.Error("ConflictReason 应非空")
	}
}

func TestSelect_NoConflictWhenIdentityAgreesWithPath(t *testing.T) {
	d := Select(Inputs{
		Path:          "/v1/chat/completions",
		Identity:      clientid.IdentityCursor, // 推 OpenAIChat 与 path 一致
		IdentityConf:  0.9,
		IdentityAware: true,
	})
	if d.Conflict {
		t.Errorf("identity 与 path 一致不应 Conflict")
	}
}

func TestSelect_NoConflictWhenIdentityIsAmbiguous(t *testing.T) {
	// Cody 不映射到具体 ClientProtocol（双协议） → 不报 conflict
	d := Select(Inputs{
		Path:          "/v1/chat/completions",
		Identity:      clientid.IdentityCody,
		IdentityConf:  0.9,
		IdentityAware: true,
	})
	if d.Conflict {
		t.Errorf("Cody identity ambiguous 不应 Conflict")
	}
}

func TestSelect_UnknownIdentityIgnored(t *testing.T) {
	// IdentityUnknown 不影响决策
	d := Select(Inputs{
		Path:          "/v1/chat/completions",
		Identity:      clientid.IdentityUnknown,
		IdentityConf:  0.5,
		IdentityAware: true,
	})
	if d.Conflict {
		t.Error("Unknown identity 不应 Conflict")
	}
}

func TestSelect_EmptyPathFallsBackToDefault(t *testing.T) {
	d := Select(Inputs{Path: ""})
	if d.Source != SourceDefault {
		t.Errorf("空 path 应 default，得 %q", d.Source)
	}
	if d.ClientProtocol != proto.ClientProtocolOpenAIChat {
		t.Errorf("default 应 OpenAIChat")
	}
}

func TestSelect_IdentityCanFillWhenPathMissesButIdentityAgreesWithRouteConfig(t *testing.T) {
	// route_config 显式 + identity 一致 → no conflict, source=route_config
	d := Select(Inputs{
		Path:              "/custom/path",
		RouteConfigClient: proto.ClientProtocolOpenAIChat,
		Identity:          clientid.IdentityCursor,
		IdentityConf:      0.9,
		IdentityAware:     true,
	})
	if d.Source != SourceRouteConfig {
		t.Errorf("Source=%q want route_config", d.Source)
	}
	if d.Conflict {
		t.Error("RouteConfig + 一致 identity 不应 Conflict")
	}
}

func TestSelect_PathPrefixMatching(t *testing.T) {
	// /v1/chat/completions/foo 也应匹配 (HasPrefix)
	d := Select(Inputs{Path: "/v1/chat/completions/v2/extra"})
	if d.ClientProtocol != proto.ClientProtocolOpenAIChat {
		t.Errorf("/v1/chat/completions/* 应识别 OpenAIChat")
	}
}

func TestSelect_QueryStringStripped(t *testing.T) {
	d := Select(Inputs{Path: "/v1/messages?stream=true&model=claude"})
	if d.ClientProtocol != proto.ClientProtocolAnthropicMessages {
		t.Errorf("query string 应被剥离, 得 %q", d.ClientProtocol)
	}
}

// TestSelect_IdentityAware_UnknownIdentity_FallsThroughToDefault:
// IdentityAware=true + path 非已知 + Identity=Unknown
// → clientFromIdentity 返回 false → 落到 default
func TestSelect_IdentityAware_UnknownIdentity_FallsThroughToDefault(t *testing.T) {
	d := Select(Inputs{
		Path:          "/custom/path", // 非 well-known
		Identity:      clientid.IdentityUnknown,
		IdentityConf:  0.9, // 高 conf 但 identity=Unknown 不映射
		IdentityAware: true,
	})
	if d.Source != SourceDefault {
		t.Errorf("Identity=Unknown 应 fall through 到 default，得 %q", d.Source)
	}
	if d.ClientProtocol != proto.ClientProtocolOpenAIChat {
		t.Errorf("default 应 OpenAIChat，得 %q", d.ClientProtocol)
	}
}
