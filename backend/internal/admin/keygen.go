// Bearer token generation for admin issuance. Mirrors the customer
// resolver's prefix length (16) so reviewers don't learn a new constant.
//
// Format: <namespace>_<24-char-base32>
//   - hk_live_  customer key for production env
//   - hk_test_  customer key for test/staging
//   - hk_admin_ admin token (operator credential)
//
// Base32 (RFC 4648 lowercase, dash-separator stripped) chosen over base64
// because operators paste these by hand from the issuance response —
// dropping `0`/`O`/`1`/`I` ambiguity reduces support load.
//
// Entropy: 24 base32 chars = 5 bits each = 120 bits well above the
// 80-bit safe baseline.

package admin

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/apikeyns"
)

// PrefixLen mirrors auth.APIKeyPrefixLen so the inbound resolver's
// indexed prefix lookup works identically for issued keys.
const PrefixLen = 16

// Environment marks whether a customer key is for production or test.
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

// GenerateBearer returns (plaintextBearer, prefix, error). The plaintext
// is intended to be returned to the operator ONCE in the issuance
// response; callers MUST NOT log or persist it. Prefix is the first
// PrefixLen chars of plaintext, indexed for hot-path lookup.
func GenerateBearer(env Environment) (string, string, error) {
	ns := env.namespace()
	if ns == "" {
		return "", "", fmt.Errorf("%w: invalid environment %q", ErrAdminBadRequest, env)
	}
	const randBytes = 15 // 15 bytes -> 24 base32 chars
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
