// admin 签发用的 bearer token 生成。沿用客户 resolver 的前缀长度(16),
// 这样审阅者无需再学一个新常量。
//
// 格式:<namespace>_<24 字符 base32>
//   - hk_live_  生产环境的客户 key
//   - hk_test_  测试/预发的客户 key
//   - hk_admin_ admin token(运维凭证)
//
// 选用 Base32(RFC 4648 小写、去掉分隔横杠)而非 base64,是因为运维会
// 从签发响应里手工粘贴这些值 —— 去除 `0`/`O`/`1`/`I` 的歧义可降低
// 支持负担。
//
// 熵:24 个 base32 字符 = 每个 5 bit = 120 bit,远高于 80-bit 的安全基线。

package admin

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeyns"
)

// PrefixLen 与 auth.APIKeyPrefixLen 对应,这样入站 resolver 基于索引的
// 前缀查找对已签发的 key 同样有效。
const PrefixLen = 16

// Environment 标记一个客户 key 是用于生产还是测试。
type Environment string

const (
	EnvLive  Environment = "live"
	EnvTest  Environment = "test"
	EnvAdmin Environment = "admin"
)

func (e Environment) namespace() string {
	switch e {
	case EnvLive:
		// 客户 live/test 前缀 base 可由运维 env 覆盖(默认 hk),与入站校验同源。
		return apikeyns.LivePrefix()
	case EnvTest:
		return apikeyns.TestPrefix()
	case EnvAdmin:
		// admin 前缀不可配(operator 权限边界)。
		return apikeyns.AdminPrefix
	default:
		return ""
	}
}

// GenerateBearer 返回 (plaintextBearer, prefix, error)。该明文意在于签发
// 响应中【一次性】返回给运维;调用方【绝不可】记日志或持久化它。
// prefix 是明文的前 PrefixLen 个字符,已建索引以供热路径查找。
func GenerateBearer(env Environment) (string, string, error) {
	ns := env.namespace()
	if ns == "" {
		return "", "", fmt.Errorf("%w: invalid environment %q", ErrAdminBadRequest, env)
	}
	const randBytes = 15 // 15 字节 -> 24 个 base32 字符
	buf := make([]byte, randBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("%w: rand: %v", ErrAdminBackend, err)
	}
	suffix := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf))
	bearer := ns + suffix
	prefix := bearer
	if len(prefix) > PrefixLen {
		prefix = prefix[:PrefixLen]
	}
	return bearer, prefix, nil
}
