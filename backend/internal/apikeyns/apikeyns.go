// 包 apikeyns 是 API key 命名空间前缀的【唯一真相源】。客户 live/test key 的前缀
// base 可由运维 env HUAKAI_API_KEY_PREFIX 覆盖(默认 hk),签发(admin/keygen)与
// 入站校验(auth/api_key_resolver)都从这里取,避免两处各自硬编码而漂移——一旦
// 签发用 A 前缀、校验认 B 前缀,签出的 key 自己就登录不过。
//
// admin token 前缀 hk_admin_ 【刻意不可配】:它是 operator 权限边界的判别依据
// (admin/operator_auth 按此前缀区分管理凭据与客户 key),配置化会引入越权混淆面。
package apikeyns

import (
	"fmt"
	"os"
	"strings"
)

// AdminPrefix 是 operator admin token 的固定前缀,不可配(权限边界)。
const AdminPrefix = "hk_admin_"

// defaultBase 是客户 key 前缀 base 的默认值;保持默认即不触发任何 default-flip。
const defaultBase = "hk"

// Base 返回客户 API key 前缀 base。HUAKAI_API_KEY_PREFIX 非法/空一律回落默认,
// 永不破坏签发/校验(回落是"功能可见"失败而非安全失败:运维若设了非法值,
// 由 ConfiguredBaseError 在启动期 fail-loud,不会走到静默回落)。
func Base() string {
	return sanitizeBase(os.Getenv("HUAKAI_API_KEY_PREFIX"))
}

func sanitizeBase(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if !validBase(raw) {
		return defaultBase
	}
	return raw
}

// validBase:仅小写字母+数字、1-6 字符。上限 6 是为保持 key_prefix(16 字符)的
// 索引选择性——base 越长,命名空间(<base>_live_)越长,16 字符索引里留给随机后缀
// 的位越少,resolver 按 prefix 精确查的候选桶越拥挤(超 LIMIT 5 会让合法 key 查不到
// 误判 401)。6 给 "<=6>_live_" ≤12,索引内仍 ≥4 随机 base32 字符(约 1.05M 桶);
// 默认 hk(2)更是留 8 随机字符(万亿桶)。越短越安全,长 base 仅短前缀场景够用。
func validBase(s string) bool {
	if len(s) < 1 || len(s) > 6 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// LivePrefix / TestPrefix 是客户 live/test key 的命名空间前缀(含尾下划线)。
func LivePrefix() string { return Base() + "_live_" }
func TestPrefix() string { return Base() + "_test_" }

// ValidCustomerFormat 廉价判定 token 是否带合法客户前缀,用于 DB 查询前快速拒掉
// 明显异源的 token(如 sk-...)。注意:这只是过滤优化,真正的鉴权是入库 bcrypt。
func ValidCustomerFormat(token string) bool {
	return strings.HasPrefix(token, LivePrefix()) || strings.HasPrefix(token, TestPrefix())
}

// ConfiguredBaseError 报告 HUAKAI_API_KEY_PREFIX 是否被设成了非法值(非空但不符
// [a-z0-9]{1,8})。供 config.Load 启动期 fail-loud,让运维当场发现 typo,而不是
// 静默回落默认后所有客户端被拒还查不出原因。空值=用默认,返回 nil。
func ConfiguredBaseError() error {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("HUAKAI_API_KEY_PREFIX")))
	if raw == "" {
		return nil
	}
	if !validBase(raw) {
		return fmt.Errorf("HUAKAI_API_KEY_PREFIX %q 非法:仅允许小写字母+数字、1-8 字符", raw)
	}
	return nil
}
