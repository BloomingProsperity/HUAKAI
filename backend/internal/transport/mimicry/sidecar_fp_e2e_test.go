//go:build sidecar_fp_e2e

// 端到端出口指纹保真验证(需真起 Rust sidecar 二进制 + 外网)。
//
// 经 sidecar 的真实 IPC 路径,对四家 mimicry profile 各拨一次 https://tls.peet.ws/api/all,
// 让独立第三方观测我们实际出站的 ClientHello,把观测到的指纹与 profile 存储值对拍。
// 校验的是「部署态 sidecar 二进制经真实拨号,是否为每家产出字节正确且互异的 ClientHello」——
// 这是真号实测前不触账号即可做的最强出口保真验证(TLS 握手在 HTTP 鉴权之前完成)。
//
// 跑法:
//
//	HUAKAI_TLS_SIDECAR_SOCKET=/path/to/sc.sock \
//	  go test -tags sidecar_fp_e2e ./internal/transport/mimicry/ -run TestSidecarFingerprintE2E -v -count=1
//
// 断言取舍(经实测校准,避开第三方观测器的两处特性,以免假阴性):
//   - JA3 主体(密码 + 扩展类型 + 组 + 点格式)逐字节 == profile 存储 expected_ja3 的对应字段。
//     这是 ClientHello 结构的完整字节等价证明。忽略 JA3 首字段(TLS 版本号):peet.ws 用
//     legacy_version(771),我们 profile 存储用协商版本变体(772),属工具差异非 wire 差异。
//   - ja4_b(密码集合哈希,与目标/padding 无关)== 存储 ja4_b,独立复核密码集。
//   - 不硬断 ja4_a 完整串:peet.ws 对个位数计数不补零、无 ALPN 时省略 token(非 FoxIO 规范),
//     与我们规范值格式不同(内容一致)。
//   - 不硬断 ja4_c:FoxIO 从 ja4_c 排除 padding(0x0015)扩展,而 padding 长度依赖 SNI 长度,
//     故带 padding 的 profile(anthropic)连不同 host 时 ja4_c 会变;仅记录供人工核对。
package mimicry

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type fpE2ECase struct {
	mode        TransportMode
	profileID   string
	expectedJA3 string // 与 profile.rs 存储一致;比较时忽略首字段(版本号)
	wantJA4B    string // JA4 中段(密码集合哈希)
}

func TestSidecarFingerprintE2E(t *testing.T) {
	socket := os.Getenv("HUAKAI_TLS_SIDECAR_SOCKET")
	if socket == "" {
		t.Skip("需设 HUAKAI_TLS_SIDECAR_SOCKET 指向已运行的 sidecar socket")
	}

	cases := []fpE2ECase{
		{ModeMimicryClaudeCode, "anthropic-cli-mimicry-v1",
			"771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49161-49171-49162-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-21,29-23-24,0", "5b57614c22b0"},
		{ModeMimicryChatGPT, "openai-codex-cli-v1",
			"772,4866-4867-4865-49196-49200-159-52393-52392-52394-49195-49199-158-49188-49192-107-49187-49191-103-49162-49172-57-49161-49171-51-157-156-61-60-53-47,65281-0-11-10-35-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0-1-2", "1d37bd780c83"},
		{ModeMimicryGeminiAdvanced, "gemini-cli-v1",
			"772,4866-4867-4865-49199-49195-49200-49196-158-49191-103-49192-107-163-159-52393-52392-52394-49325-49311-49245-49249-49239-49235-162-49324-49310-49244-49248-49238-49234-49188-106-49187-64-49162-49172-57-56-49161-49171-51-50-157-49309-49233-156-49308-49232-61-60-53-47,65281-0-11-10-35-16-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0-1-2", "b262b3658495"},
		{ModeMimicryKiro, "kiro-cli-v1",
			"772,4866-4865-4867-49196-49195-52393-49200-49199-52392,10-43-51-0-45-11-5-35-23-13,4588-29-23-24,0", "f91f431d341e"},
	}

	// ja3Meat 去掉首字段(版本号),返回 密码,扩展,组,点格式 —— ClientHello 的结构指纹。
	ja3Meat := func(ja3 string) string {
		parts := strings.SplitN(ja3, ",", 2)
		if len(parts) != 2 {
			return ja3
		}
		return parts[1]
	}

	seen := map[string]TransportMode{}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			rt, err := NewSidecarRoundTripperForMode(socket, tc.mode)
			if err != nil {
				t.Fatalf("构造 sidecar roundtripper(mode=%s): %v", tc.mode, err)
			}
			client := &http.Client{Transport: rt, Timeout: 30 * time.Second}
			resp, err := client.Get("https://tls.peet.ws/api/all")
			if err != nil {
				t.Fatalf("经 sidecar GET peet.ws(profile=%s)失败: %v", tc.profileID, err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("读响应体(profile=%s): %v", tc.profileID, err)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("peet.ws 非 200(profile=%s): %d body=%.200s", tc.profileID, resp.StatusCode, body)
			}

			var parsed struct {
				TLS struct {
					JA3  string `json:"ja3"`
					JA4  string `json:"ja4"`
					JA4R string `json:"ja4_r"`
				} `json:"tls"`
			}
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatalf("解析 peet.ws JSON(profile=%s): %v", tc.profileID, err)
			}

			// 握手成功 + 拿到指纹 = 该 profile 经真实 IPC 加载可用(硬门)。
			if parsed.TLS.JA4 == "" || parsed.TLS.JA3 == "" {
				t.Fatalf("profile=%s 未观测到指纹(握手/上报异常)", tc.profileID)
			}
			t.Logf("profile=%s\n  JA3 实测=%s\n  JA4 实测=%s\n  JA4_r=%s", tc.profileID, parsed.TLS.JA3, parsed.TLS.JA4, parsed.TLS.JA4R)

			// 四家 JA4 必须互异——防 mode→profile 映射错乱或 profile 未真正加载而全塌成一家(硬门)。
			if prev, dup := seen[parsed.TLS.JA4]; dup {
				t.Errorf("JA4 撞车:mode=%s 与 mode=%s 产出同一 JA4=%s(profile 未按家区分)", tc.mode, prev, parsed.TLS.JA4)
			}
			seen[parsed.TLS.JA4] = tc.mode

			// 字节级对拍(核心):观测 JA3 主体 == 存储 expected_ja3 主体(密码/扩展/组/格式全等)。
			gotMeat, wantMeat := ja3Meat(parsed.TLS.JA3), ja3Meat(tc.expectedJA3)
			if gotMeat != wantMeat {
				t.Errorf("JA3 主体不符(profile=%s):\n  期望 %s\n  实测 %s", tc.profileID, wantMeat, gotMeat)
			}

			// ja4_b(密码集合哈希)独立复核 —— 与 padding/目标无关。
			gotJA4B := ""
			if segs := strings.Split(parsed.TLS.JA4, "_"); len(segs) == 3 {
				gotJA4B = segs[1]
			}
			if gotJA4B != tc.wantJA4B {
				t.Errorf("ja4_b 不符(profile=%s):期望 %s 实测 %s", tc.profileID, tc.wantJA4B, gotJA4B)
			}
		})
	}
}
