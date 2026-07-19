package bedrock

import (
	"net/url"
	"strings"
	"testing"
)

// TestCanonicalizeURI_MatchesWireEncodedPath_S1 证明审计 S1.1:
//
// Bedrock 的 model id 含冒号(例 anthropic.claude-3-5-sonnet-20241022-v2:0)。
// 出站 URL 由 buildEndpoint→awsURIEncode 构造,awsURIEncode 会把 ':' 编码成 '%3A'
// (isUnreserved(':')=false),所以【线缆实发的 path】= /model/...v2%3A0/invoke。
//
// 但签名侧 canonicalizeURI 对每段先 url.PathUnescape(把 %3A 还原成 ':')再 url.PathEscape
// (PathEscape 不编码 ':'),得到 /model/...v2:0/invoke —— 与线缆 path 不一致。
// AWS SigV4 要求 canonical URI 必须等于请求行里实际发送的、已编码的 path,否则
// SignatureDoesNotMatch(403)。
//
// 判别:此测试断言「签名用的 canonical path」== 「线缆实际编码的 path」。
// 当前有缺陷的实现会让两者不等 → 测试 RED,实证 bug 真实存在。修复后应 GREEN。
func TestCanonicalizeURI_MatchesWireEncodedPath_S1(t *testing.T) {
	const modelID = "anthropic.claude-3-5-sonnet-20241022-v2:0"

	// 线缆实际编码:与 buildEndpoint 用的同一个 awsURIEncode。
	wirePath := "/model/" + awsURIEncode(modelID) + "/invoke"

	// 构造一个 *url.URL,其 EscapedPath 就是线缆 path(模拟已发出的请求)。
	u, err := url.Parse("https://bedrock-runtime.us-east-1.amazonaws.com" + wirePath)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	// 前置校验:线缆 path 确实含 %3A(冒号被编码),这是 AWS 的要求。
	if !strings.Contains(u.EscapedPath(), "%3A") {
		t.Fatalf("前置假设失败:线缆 path 应含 %%3A,实得 %q", u.EscapedPath())
	}

	got := canonicalizeURI(u)

	if got != u.EscapedPath() {
		t.Fatalf("签名 canonical URI 与线缆 path 不一致 → SigV4 SignatureDoesNotMatch(403)\n"+
			"  线缆实发 path : %q\n"+
			"  签名 canonical: %q\n"+
			"  差异根因: canonicalizeURI 对段做 PathUnescape+PathEscape, PathEscape 不编码 ':',"+
			"把 %%3A 变回 ':',与 awsURIEncode 的 %%3A 不符。", u.EscapedPath(), got)
	}
}
