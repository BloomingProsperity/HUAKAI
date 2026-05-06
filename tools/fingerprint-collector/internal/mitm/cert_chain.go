// 包 mitm 提供 TLS 证书链的完整性检测功能，
// 用于识别 TLS 拦截代理（MITM），防止捕获代理的指纹而非真实客户端指纹。
//
// 严格策略（Owner 2026-05-06 directive：选 C）：
// 信任决策完全依赖系统证书池 x509.SystemCertPool()。该池不可用或任何
// 验证步骤失败时一律 fail-closed —— 不接受"内置 fallback 列表"，因为
// 假数据反而是攻击面（被假 hash 误判为信任 = 蒙混 MITM）。
package mitm

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"time"
)

// ErrNoSystemCertPool 表示当前 OS 上无法加载系统信任库；调用方应当 fail-closed
// 退出，并提示运维安装 ca-certificates 或对应平台等价物。
var ErrNoSystemCertPool = errors.New("mitm: 系统证书池不可用，无法做信任决策")

// CheckResult 保存证书链检查的结果。
type CheckResult struct {
	// OK 为 true 表示证书链通过验证，未检测到 MITM
	OK bool `json:"ok"`
	// LeafCN 是叶证书的 Common Name
	LeafCN string `json:"leaf_cn"`
	// LeafSANs 是叶证书的 Subject Alternative Names
	LeafSANs []string `json:"leaf_sans"`
	// RootCN 是根证书的 Common Name（仅 OK=true 时填充）
	RootCN string `json:"root_cn"`
	// Warning 在检测到潜在 MITM 时包含警告说明
	Warning string `json:"warning,omitempty"`
	// CertChainLen 是完整证书链的长度
	CertChainLen int `json:"cert_chain_len"`
}

// CheckHost 通过直接 TLS 握手检查目标主机的证书链是否完整可信。
// 此方法主动发起连接，不依赖 pcap，适合工具启动时的预检。
// timeout 建议设置为 10 秒。
func CheckHost(host string, timeout time.Duration) (*CheckResult, error) {
	addr := net.JoinHostPort(host, "443")

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: false,
	})
	if err != nil {
		// 连接失败不等同于 MITM；可能是网络不可达
		return &CheckResult{
			OK:      false,
			Warning: fmt.Sprintf("TLS 握手失败，无法验证证书链: %v", err),
		}, nil
	}
	defer conn.Close()

	state := conn.ConnectionState()
	return CheckCertChain(host, state.PeerCertificates)
}

// CheckCertChain 检查已解析的证书链是否可信且与预期主机名匹配。
// certs[0] 应为叶证书，certs[len-1] 应为根或中间 CA。
//
// 返回 (CheckResult, error)。当系统证书池不可用时返回 ErrNoSystemCertPool —
// 调用方必须 fail-closed，不可继续运行（运维需安装 ca-certificates 或在
// Windows 上确保 schannel 信任库可访问后重试）。
func CheckCertChain(expectedHost string, certs []*x509.Certificate) (*CheckResult, error) {
	if len(certs) == 0 {
		return &CheckResult{
			OK:      false,
			Warning: "证书链为空，无法验证",
		}, nil
	}

	leaf := certs[0]
	result := &CheckResult{
		LeafCN:       leaf.Subject.CommonName,
		LeafSANs:     leaf.DNSNames,
		CertChainLen: len(certs),
	}

	// 第一步：叶证书 SAN/CN 必须与预期主机名匹配
	if err := leaf.VerifyHostname(expectedHost); err != nil {
		result.OK = false
		result.Warning = fmt.Sprintf(
			"证书主机名不匹配，预期 %q，叶证书 CN=%q SAN=%v。可能存在 MITM 代理。错误: %v",
			expectedHost, leaf.Subject.CommonName, leaf.DNSNames, err,
		)
		return result, nil
	}

	// 第二步：必须能拿到系统证书池作为唯一信任来源。
	// 拿不到时硬失败；不接受 fallback。
	systemPool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoSystemCertPool, err)
	}
	if systemPool == nil {
		return nil, ErrNoSystemCertPool
	}

	// 第三步：构建验证选项 — 用系统池作为唯一信任根
	opts := x509.VerifyOptions{
		DNSName: expectedHost,
		Roots:   systemPool,
	}

	// 添加中间 CA（如有）
	if len(certs) > 2 {
		intermediates := x509.NewCertPool()
		for _, cert := range certs[1 : len(certs)-1] {
			intermediates.AddCert(cert)
		}
		opts.Intermediates = intermediates
	}

	// 第四步：基于系统池验证完整链
	chains, err := leaf.Verify(opts)
	if err != nil {
		result.OK = false
		if len(certs) > 0 {
			result.RootCN = certs[len(certs)-1].Subject.CommonName
		}
		result.Warning = fmt.Sprintf(
			"证书链验证失败，根 CA 不在系统信任库中（呈现的根 CN=%q）。"+
				"可能存在企业 MITM 代理（如 Zscaler / BlueCoat / FortiGate / 杀毒软件 TLS 拦截）。"+
				"若确信这是预期环境，可使用 -disable-mitm-detection 跳过此检查。原始错误: %v",
			result.RootCN, err,
		)
		return result, nil
	}

	// 验证通过：从系统池构建的链中取根 CA 名
	if len(chains) > 0 && len(chains[0]) > 0 {
		root := chains[0][len(chains[0])-1]
		result.RootCN = root.Subject.CommonName
	}
	result.OK = true
	return result, nil
}
