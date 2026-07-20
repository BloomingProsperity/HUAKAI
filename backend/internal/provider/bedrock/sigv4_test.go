// 包 bedrock — SigV4 签名实现单元测试。
//
// 主要验证点：
//  1. AWS 官方公开测试向量（IAM 服务，2015-08-30）——签名必须与预期完全一致。
//  2. 签名密钥推导链正确性。
//  3. 规范 header 构造（小写、空白折叠、排序）。
//  4. query string 规范化（排序、编码）。
package bedrock

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// AWS 官方公开测试向量
// 来源：https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html
// ─────────────────────────────────────────────────────────────────────────────

// TestSignStringAWSPublicVector 验证 buildStringToSign 对官方 canonical request
// hash 生成的签名字符串哈希与官方预期签名一致。
//
// 官方向量：
//
//	AccessKey:  AKIDEXAMPLE
//	SecretKey:  wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY
//	Region:     us-east-1
//	Service:    iam
//	AmzDate:    20150830T123600Z
//	CanonicalRequest hash: f536975d06c0309214f805bb90ccff089219ecd68b2577efef23edd43b7e1a59
//	ExpectedSignature: 5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7
func TestSignStringAWSPublicVector(t *testing.T) {
	const (
		accessKey   = "AKIDEXAMPLE"
		secretKey   = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		region      = "us-east-1"
		service     = "iam"
		amzDate     = "20150830T123600Z"
		dateStamp   = "20150830"
		canonHash   = "f536975d06c0309214f805bb90ccff089219ecd68b2577efef23edd43b7e1a59"
		expectedSig = "5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"
	)

	// 构造 credential scope
	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"

	// 构造 string to sign（canonical request 已是 hash 形式）
	// buildStringToSign 内部会对 canonicalRequest 字节再做一次 SHA-256，
	// 所以这里需要传入原始 canonical request 字符串使其 hash == canonHash。
	// 官方向量直接给出了 canonical request hash，因此我们反向验证：
	// 手动构造 string to sign 再签名，与官方预期签名比对。
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + credentialScope + "\n" + canonHash

	// 推导签名密钥
	signingKey := buildSigningKey(secretKey, dateStamp, region, service)

	// 计算签名
	sigBytes := hmacSHA256(signingKey, []byte(stringToSign))

	// 转十六进制
	got := ""
	for _, b := range sigBytes {
		got += string([]byte{hexChar(b >> 4), hexChar(b & 0x0f)})
	}

	if got != expectedSig {
		t.Errorf("签名不匹配\n  got:  %s\n  want: %s", got, expectedSig)
	}
}

// hexChar 将 0-15 映射到十六进制小写字符。
func hexChar(b byte) byte {
	if b < 10 {
		return '0' + b
	}
	return 'a' + b - 10
}

// TestBuildSigningKey 验证签名密钥推导链（官方向量）。
func TestBuildSigningKey(t *testing.T) {
	// 官方向量中 signing key 的最终十六进制值（来自 AWS 文档示例）
	// https://docs.aws.amazon.com/general/latest/gr/sigv4-calculate-signature.html
	const (
		secretKey = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"
		dateStamp = "20150830"
		region    = "us-east-1"
		service   = "iam"
		// 由本实现推导得到的 signing key hex（iam 服务，20150830）。
		// 已通过 TestSignStringAWSPublicVector（官方完整向量）验证签名链正确。
		expectedKeyHex = "c4afb1cc5771d871763a393e44b703571b55cc28424d1a5e86da6ed3c154a4b9"
	)

	key := buildSigningKey(secretKey, dateStamp, region, service)
	got := ""
	for _, b := range key {
		got += string([]byte{hexChar(b >> 4), hexChar(b & 0x0f)})
	}

	if got != expectedKeyHex {
		t.Errorf("signing key 不匹配\n  got:  %s\n  want: %s", got, expectedKeyHex)
	}
}

// TestCanonicalizeHeaders 验证 header 规范化：小写、trim、内部空白折叠、排序。
func TestCanonicalizeHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("X-Amz-Date", "20150830T123600Z")
	headers.Set("Host", "iam.amazonaws.com")

	canonical, signedHdrs := canonicalizeHeaders(headers)

	// 所有 header 名必须小写
	for _, name := range []string{"content-type", "host", "x-amz-date"} {
		found := false
		for _, line := range splitLines(canonical) {
			if len(line) > len(name) && line[:len(name)+1] == name+":" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("规范 header 中未找到 %q，canonical:\n%s", name, canonical)
		}
	}

	// signed headers 必须包含所有名称，以分号分隔
	for _, name := range []string{"content-type", "host", "x-amz-date"} {
		found := false
		for _, h := range splitSemicolon(signedHdrs) {
			if h == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("signedHeaders 中未找到 %q，got: %s", name, signedHdrs)
		}
	}
}

// TestCollapseWhitespace 验证内部连续空白折叠。
func TestCollapseWhitespace(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello  world", "hello world"},
		{"a   b   c", "a b c"},
		{"no-spaces", "no-spaces"},
		{"  leading", " leading"},   // trim 由调用方负责
		{"trailing  ", "trailing "}, // trim 由调用方负责
	}
	for _, tc := range cases {
		got := collapseWhitespace(tc.in)
		if got != tc.want {
			t.Errorf("collapseWhitespace(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestHashSHA256Hex 验证 SHA-256 十六进制输出（空串官方已知值）。
func TestHashSHA256Hex(t *testing.T) {
	// echo -n "" | sha256sum => e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	got := hashSHA256Hex([]byte{})
	if got != emptyHash {
		t.Errorf("hashSHA256Hex(\"\") = %q, want %q", got, emptyHash)
	}
}

// TestAWSURIEncode 验证 AWS percent-encoding（保留 unreserved 字符，编码其他）。
func TestAWSURIEncode(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"abc123", "abc123"},
		{"-._~", "-._~"},
		{"hello world", "hello%20world"},
		{"a+b", "a%2Bb"},
		{"a/b", "a%2Fb"},
		{"a:b", "a%3Ab"},
	}
	for _, tc := range cases {
		got := awsURIEncode(tc.in)
		if got != tc.want {
			t.Errorf("awsURIEncode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCanonicalizeURIUsesSameEncodingAsEndpoint(t *testing.T) {
	u, err := url.Parse("https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-v2%3A0/invoke")
	if err != nil {
		t.Fatal(err)
	}
	want := "/model/anthropic.claude-v2%3A0/invoke"
	if got := canonicalizeURI(u); got != want {
		t.Fatalf("canonical URI = %q，期望与实际 endpoint 编码一致 %q", got, want)
	}
}

// TestCanonicalizeQueryString 验证 query string 规范化：排序、编码。
func TestCanonicalizeQueryString(t *testing.T) {
	// 多参数乱序
	raw := "z=last&a=first&m=middle"
	got := canonicalizeQueryString(raw)
	// 应按 key 升序
	want := "a=first&m=middle&z=last"
	if got != want {
		t.Errorf("canonicalizeQueryString(%q) = %q, want %q", raw, got, want)
	}

	// 空 query
	if got := canonicalizeQueryString(""); got != "" {
		t.Errorf("empty query should return empty, got %q", got)
	}
}

// TestSignerInjectsXAmzSecurityToken 验证 session token 时注入 x-amz-security-token header。
func TestSignerInjectsXAmzSecurityToken(t *testing.T) {
	fixedTime, _ := time.Parse("20060102T150405Z", "20150830T123600Z")

	signer := &sigV4Signer{
		region:  "us-east-1",
		service: sigV4Service,
		creds: sigV4Credentials{
			AccessKeyID:  "AKIDEXAMPLE",
			SecretKey:    "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
			SessionToken: "mysessiontoken",
		},
		now: fixedTime,
	}

	u, _ := url.Parse("https://bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-3/invoke")
	req, _ := http.NewRequest(http.MethodPost, u.String(), nil)

	if err := signer.Sign(req, []byte(`{"prompt":"hello"}`)); err != nil {
		t.Fatalf("Sign 失败: %v", err)
	}

	if got := req.Header.Get("X-Amz-Security-Token"); got != "mysessiontoken" {
		t.Errorf("x-amz-security-token = %q, want %q", got, "mysessiontoken")
	}
}

// TestSignerNoSessionToken 验证无 session token 时不注入 x-amz-security-token。
func TestSignerNoSessionToken(t *testing.T) {
	fixedTime, _ := time.Parse("20060102T150405Z", "20150830T123600Z")

	signer := &sigV4Signer{
		region:  "us-east-1",
		service: sigV4Service,
		creds: sigV4Credentials{
			AccessKeyID: "AKIDEXAMPLE",
			SecretKey:   "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		},
		now: fixedTime,
	}

	u, _ := url.Parse("https://bedrock-runtime.us-east-1.amazonaws.com/model/test/invoke")
	req, _ := http.NewRequest(http.MethodPost, u.String(), nil)

	if err := signer.Sign(req, []byte(`{}`)); err != nil {
		t.Fatalf("Sign 失败: %v", err)
	}

	if got := req.Header.Get("X-Amz-Security-Token"); got != "" {
		t.Errorf("不应设置 x-amz-security-token，got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 辅助函数
// ─────────────────────────────────────────────────────────────────────────────

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func splitSemicolon(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
