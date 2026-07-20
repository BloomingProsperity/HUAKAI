// 包 bedrock — AWS SigV4 签名实现。
//
// 严格按照 AWS 公开规范实现：
// https://docs.aws.amazon.com/general/latest/gr/sigv4_signing.html
//
// 仅使用标准库：crypto/sha256、crypto/hmac、encoding/hex、sort、strings、time。
// 不依赖任何 aws-sdk-go 或第三方签名库。
package bedrock

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// sigV4Service 是本 adapter 固定使用的 AWS 服务名。
const sigV4Service = "bedrock"

// sigV4Credentials 包含一次签名所需的所有 AWS 凭据。
type sigV4Credentials struct {
	// AccessKeyID AWS 访问密钥 ID（如 AKIDEXAMPLE）。
	AccessKeyID string
	// SecretKey AWS 访问密钥（明文，不含 "AWS4" 前缀）。
	SecretKey string
	// SessionToken 临时会话令牌（STS AssumeRole 等场景；可为空）。
	SessionToken string
}

// sigV4Signer 对一个 *http.Request 执行 AWS SigV4 签名，直接向 req.Header
// 写入 Authorization、x-amz-date、x-amz-content-sha256，以及可选的
// x-amz-security-token。
//
// 调用前 req.URL、req.Method、req.Body（body 字节通过 bodyBytes 传入）
// 必须已设置完毕。
type sigV4Signer struct {
	// region AWS 区域（如 "us-east-1"）。
	region string
	// service AWS 服务名（如 "bedrock"）。
	service string
	// creds 签名所需凭据。
	creds sigV4Credentials
	// now 签名时间戳（测试时可注入固定时间）。
	now time.Time
}

// Sign 对 req 执行 SigV4 签名，向 req.Header 写入所有必要 header。
// bodyBytes 为请求体原始字节（body reader 已 reset 到此处供 RoundTripper 使用，
// 这里独立接受字节以避免重复读取）。
func (s *sigV4Signer) Sign(req *http.Request, bodyBytes []byte) error {
	if s.creds.AccessKeyID == "" {
		return fmt.Errorf("sigv4: AccessKeyID 不能为空")
	}
	if s.creds.SecretKey == "" {
		return fmt.Errorf("sigv4: SecretKey 不能为空")
	}

	// 时间格式：ISO 8601 基础格式（UTC）
	amzDate := s.now.UTC().Format("20060102T150405Z")
	dateStamp := s.now.UTC().Format("20060102")

	// 计算 payload hash（十六进制 SHA-256）
	payloadHash := hashSHA256Hex(bodyBytes)

	// 注入必要 header（签名前写入，canonical headers 会包含它们）
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if s.creds.SessionToken != "" {
		req.Header.Set("x-amz-security-token", s.creds.SessionToken)
	}
	// host header（SigV4 必须包含）
	host := req.URL.Host
	if host == "" {
		host = req.Host
	}
	req.Header.Set("Host", host)

	// 步骤一：构造 canonical request
	canonicalReq, signedHeaders := buildCanonicalRequest(req, payloadHash)

	// 步骤二：构造 string to sign
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", dateStamp, s.region, s.service)
	stringToSign := buildStringToSign(amzDate, credentialScope, canonicalReq)

	// 步骤三：计算签名密钥并签名
	signingKey := buildSigningKey(s.creds.SecretKey, dateStamp, s.region, s.service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// 步骤四：注入 Authorization header
	authHeader := fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.creds.AccessKeyID, credentialScope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authHeader)

	return nil
}

// buildCanonicalRequest 构造规范请求字符串并返回签名 header 列表。
//
// 规范请求格式：
//
//	METHOD\n
//	URI\n
//	QUERY_STRING\n
//	CANONICAL_HEADERS\n
//	SIGNED_HEADERS\n
//	PAYLOAD_HASH
func buildCanonicalRequest(req *http.Request, payloadHash string) (canonicalReq string, signedHeaders string) {
	// 规范 URI：对路径中每段做 URI 编码（保留 /）
	canonicalURI := canonicalizeURI(req.URL)

	// 规范 query string：按 key 排序，key 和 value 均做 URI 编码
	canonicalQuery := canonicalizeQueryString(req.URL.RawQuery)

	// 规范 headers：名称小写，值 trim 内部连续空白后折叠，按名称排序
	canonicalHeaders, signedHdrs := canonicalizeHeaders(req.Header)

	canonicalReq = strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQuery,
		canonicalHeaders, // 每行已以 \n 结尾
		signedHdrs,
		payloadHash,
	}, "\n")

	return canonicalReq, signedHdrs
}

// buildStringToSign 构造 AWS SigV4 签名字符串。
func buildStringToSign(amzDate, credentialScope, canonicalRequest string) string {
	return strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hashSHA256Hex([]byte(canonicalRequest)),
	}, "\n")
}

// buildSigningKey 按 AWS 规范推导签名密钥：
//
//	HMAC("AWS4"+secret, date) → HMAC(., region) → HMAC(., service) → HMAC(., "aws4_request")
func buildSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

// canonicalizeURI 对请求路径做 AWS 规范化：每个路径段做 percent-encode，
// 保留路径分隔符 /。空路径返回 "/"。
func canonicalizeURI(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	// 分段重新编码（AWS 规范：路径段内 unreserved 字符不编码，其余编码）
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		// 先解码已有转义，再按签名规范逐字节编码，确保实际请求路径与
		// canonical URI 对冒号等保留字符使用同一编码规则。
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			decoded = seg
		}
		segments[i] = awsURIEncode(decoded)
	}
	return strings.Join(segments, "/")
}

// canonicalizeQueryString 把 raw query string 规范化为 AWS 要求的形态：
// key 和 value 分别 percent-encode（RFC 3986 unreserved 字符集），按 key 升序排列。
func canonicalizeQueryString(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	params, err := url.ParseQuery(rawQuery)
	if err != nil {
		// 解析失败则原样返回（降级）
		return rawQuery
	}

	// 收集所有 key-value 对（同 key 多值分别排序）
	type kv struct{ k, v string }
	var pairs []kv
	for k, vals := range params {
		ek := awsURIEncode(k)
		for _, v := range vals {
			pairs = append(pairs, kv{ek, awsURIEncode(v)})
		}
	}
	// 先按 key 排序，key 相同时按 value 排序
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})

	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = p.k + "=" + p.v
	}
	return strings.Join(parts, "&")
}

// canonicalizeHeaders 构造规范 header 字符串和 signed headers 列表。
//
// 规则：
//   - header 名称全部小写
//   - header 值 trim leading/trailing 空白，内部连续空白折叠为单个空格
//   - 按名称升序排列
//   - 每行格式 "name:value\n"（注意每行末尾有换行）
func canonicalizeHeaders(headers http.Header) (canonical string, signedHeaders string) {
	type hdr struct{ name, value string }
	var hdrs []hdr

	for name, values := range headers {
		lname := strings.ToLower(name)
		// 多值合并（逗号分隔，AWS 规范允许）
		combined := strings.Join(values, ",")
		// trim 两端空白，折叠内部连续空白
		trimmed := collapseWhitespace(strings.TrimSpace(combined))
		hdrs = append(hdrs, hdr{lname, trimmed})
	}

	// 按名称升序排列
	sort.Slice(hdrs, func(i, j int) bool {
		return hdrs[i].name < hdrs[j].name
	})

	var sb strings.Builder
	names := make([]string, len(hdrs))
	for i, h := range hdrs {
		sb.WriteString(h.name)
		sb.WriteByte(':')
		sb.WriteString(h.value)
		sb.WriteByte('\n')
		names[i] = h.name
	}

	return sb.String(), strings.Join(names, ";")
}

// collapseWhitespace 将字符串中所有连续的空白字符折叠为单个空格。
func collapseWhitespace(s string) string {
	var sb strings.Builder
	prevSpace := false
	for _, r := range s {
		isSpace := r == ' ' || r == '\t'
		if isSpace {
			if !prevSpace {
				sb.WriteByte(' ')
			}
			prevSpace = true
		} else {
			sb.WriteRune(r)
			prevSpace = false
		}
	}
	return sb.String()
}

// awsURIEncode 对字符串做 AWS SigV4 要求的 percent-encoding：
// RFC 3986 unreserved 字符（A-Z a-z 0-9 - _ . ~）不编码，其余全部编码。
func awsURIEncode(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isUnreserved(c) {
			sb.WriteByte(c)
		} else {
			fmt.Fprintf(&sb, "%%%02X", c)
		}
	}
	return sb.String()
}

// isUnreserved 判断字节是否属于 RFC 3986 unreserved 字符集。
func isUnreserved(c byte) bool {
	return (c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.' || c == '~'
}

// hmacSHA256 计算 HMAC-SHA256。
func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// hashSHA256Hex 计算 SHA-256 并返回小写十六进制字符串。
func hashSHA256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
