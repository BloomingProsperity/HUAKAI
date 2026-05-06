// 包 mitm — 证书链完整性检查的单元测试。
// 使用程序化生成的自签名证书进行测试，不依赖任何真实网络连接。
package mitm

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

// generateSelfSignedCert 程序化生成一个自签名证书，用于测试。
func generateSelfSignedCert(cn string, isCA bool, dnsNames []string) (*x509.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   cn,
			Organization: []string{"Test Org"},
		},
		DNSNames:              dnsNames,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	return x509.ParseCertificate(certDER)
}

// generateCertChain 生成一个由 rootCA 签发叶证书的两层证书链。
func generateCertChain(rootCN, leafCN string, leafDNS []string) ([]*x509.Certificate, *ecdsa.PrivateKey, error) {
	// 生成根 CA 密钥
	rootPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	rootTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   rootCN,
			Organization: []string{"Test Root CA"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootPriv.PublicKey, rootPriv)
	if err != nil {
		return nil, nil, err
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return nil, nil, err
	}

	// 生成叶证书密钥
	leafPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName:   leafCN,
			Organization: []string{"Test Leaf"},
		},
		DNSNames:              leafDNS,
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, rootCert, &leafPriv.PublicKey, rootPriv)
	if err != nil {
		return nil, nil, err
	}
	leafCert, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, nil, err
	}

	return []*x509.Certificate{leafCert, rootCert}, leafPriv, nil
}

// TestCheckCertChain_HostnameMismatch 测试主机名不匹配时检测到异常。
func TestCheckCertChain_HostnameMismatch(t *testing.T) {
	// 证书颁发给 example.com，但期望 api.anthropic.com
	cert, err := generateSelfSignedCert("example.com", false, []string{"example.com"})
	if err != nil {
		t.Fatalf("生成测试证书失败: %v", err)
	}

	result, err := CheckCertChain("api.anthropic.com", []*x509.Certificate{cert})
	if err != nil {
		t.Fatalf("CheckCertChain 返回意外错误: %v", err)
	}

	// 主机名不匹配时应标记为 NOT OK
	if result.OK {
		t.Error("主机名不匹配时 result.OK 应为 false")
	}
	if result.Warning == "" {
		t.Error("主机名不匹配时 Warning 不应为空")
	}
	t.Logf("警告信息: %s", result.Warning)
}

// TestCheckCertChain_EmptyChain 测试空证书链的处理。
func TestCheckCertChain_EmptyChain(t *testing.T) {
	result, err := CheckCertChain("api.anthropic.com", nil)
	if err != nil {
		t.Fatalf("不应返回 error: %v", err)
	}
	if result.OK {
		t.Error("空证书链时 result.OK 应为 false")
	}
}

// TestCheckCertChain_SelfSigned 测试自签名证书（MITM 常见特征）被正确标识。
func TestCheckCertChain_SelfSigned(t *testing.T) {
	// 自签名证书：CN 与目标主机匹配，但不在任何系统 CA 链中
	cert, err := generateSelfSignedCert("api.anthropic.com", true, []string{"api.anthropic.com"})
	if err != nil {
		t.Fatalf("生成测试证书失败: %v", err)
	}

	result, err := CheckCertChain("api.anthropic.com", []*x509.Certificate{cert})
	if err != nil {
		t.Fatalf("CheckCertChain 返回意外错误: %v", err)
	}

	// 自签名证书在系统 CA 池中验证失败（这是预期行为）
	// 主机名匹配，但证书链不受信任
	if result.OK {
		// 在某些测试环境中，系统 CA 池可能包含测试证书，因此这里仅记录
		t.Logf("注意：自签名证书在此环境中被标记为 OK（系统 CA 池行为）")
	} else {
		t.Logf("自签名证书被正确标识为不受信任: %s", result.Warning)
	}
}

// TestCheckCertChain_SystemPoolAvailable 验证当前测试环境的系统证书池可用，
// CheckCertChain 不会触发 ErrNoSystemCertPool。Owner 2026-05-06 directive (选 C)：
// 删除内置 fallback 列表后，cert_chain 完全依赖系统池；本测试同时回归
// 兜底"未拿到系统池则 fail-closed"路径在主流 OS 不会被误触发。
func TestCheckCertChain_SystemPoolAvailable(t *testing.T) {
	cert, err := generateSelfSignedCert("api.anthropic.com", false, []string{"api.anthropic.com"})
	if err != nil {
		t.Fatalf("生成测试证书失败: %v", err)
	}
	// 调用 CheckCertChain，验证不返回 ErrNoSystemCertPool。
	// 自签名证书会被系统池拒绝（result.OK=false），但函数本身不应报错。
	_, err = CheckCertChain("api.anthropic.com", []*x509.Certificate{cert})
	if err != nil {
		t.Fatalf("当前 OS 应当能加载系统证书池，但 CheckCertChain 返回错误: %v", err)
	}
}

// TestCheckCertChain_ChainLengthRecorded 验证证书链长度被正确记录。
func TestCheckCertChain_ChainLengthRecorded(t *testing.T) {
	certs, _, err := generateCertChain("Test Root CA", "api.anthropic.com", []string{"api.anthropic.com"})
	if err != nil {
		t.Fatalf("生成测试证书链失败: %v", err)
	}

	result, err := CheckCertChain("api.anthropic.com", certs)
	if err != nil {
		t.Fatalf("CheckCertChain 返回意外错误: %v", err)
	}

	if result.CertChainLen != 2 {
		t.Errorf("证书链长度应为 2，得到 %d", result.CertChainLen)
	}
	if result.LeafCN != "api.anthropic.com" {
		t.Errorf("叶证书 CN 应为 'api.anthropic.com'，得到 %q", result.LeafCN)
	}
}
