package userauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

type PasswordPolicy struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltBytes   uint32
	KeyBytes    uint32
}

func DefaultPasswordPolicy() PasswordPolicy {
	return PasswordPolicy{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 1,
		SaltBytes:   16,
		KeyBytes:    32,
	}
}

func HashPassword(password string, policy PasswordPolicy) (string, error) {
	if strings.TrimSpace(password) == "" {
		return "", ErrInvalidInput
	}
	policy = normalizePasswordPolicy(policy)
	salt := make([]byte, policy.SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, policy.Iterations, policy.MemoryKiB, policy.Parallelism, policy.KeyBytes)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		policy.MemoryKiB,
		policy.Iterations,
		policy.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func VerifyPassword(encoded, password string) (bool, error) {
	params, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func normalizePasswordPolicy(policy PasswordPolicy) PasswordPolicy {
	defaults := DefaultPasswordPolicy()
	if policy.MemoryKiB == 0 {
		policy.MemoryKiB = defaults.MemoryKiB
	}
	if policy.Iterations == 0 {
		policy.Iterations = defaults.Iterations
	}
	if policy.Parallelism == 0 {
		policy.Parallelism = defaults.Parallelism
	}
	if policy.SaltBytes == 0 {
		policy.SaltBytes = defaults.SaltBytes
	}
	if policy.KeyBytes == 0 {
		policy.KeyBytes = defaults.KeyBytes
	}
	return policy
}

func parsePasswordHash(encoded string) (PasswordPolicy, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return PasswordPolicy{}, nil, nil, ErrInvalidCredentials
	}
	var policy PasswordPolicy
	for _, item := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(item, "=")
		if !ok {
			return PasswordPolicy{}, nil, nil, ErrInvalidCredentials
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return PasswordPolicy{}, nil, nil, ErrInvalidCredentials
		}
		// parsePasswordHash 必须强制 hash header 里
		// m/t/p 是显式合法值, 不允许靠 normalizePasswordPolicy 把 0 兜底成
		// default — 否则攻击者写恶意 hash header `m=0,t=0,p=0` 仍能让校验
		// 静默走默认 params 而非 hash 实际声明值, hash 不变量被破坏。
		// 上限取业界保守: memory ≤ 1 GiB, iterations ≤ 100, parallelism ≤ 255。
		// 下限: 必须 > 0, 不能靠 normalize 兜底。
		switch k {
		case "m":
			if n == 0 || n > 1<<20 { // 1 GiB KiB
				return PasswordPolicy{}, nil, nil, ErrInvalidCredentials
			}
			policy.MemoryKiB = uint32(n)
		case "t":
			if n == 0 || n > 100 {
				return PasswordPolicy{}, nil, nil, ErrInvalidCredentials
			}
			policy.Iterations = uint32(n)
		case "p":
			if n == 0 || n > 255 {
				return PasswordPolicy{}, nil, nil, ErrInvalidCredentials
			}
			policy.Parallelism = uint8(n)
		default:
			return PasswordPolicy{}, nil, nil, ErrInvalidCredentials
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return PasswordPolicy{}, nil, nil, ErrInvalidCredentials
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return PasswordPolicy{}, nil, nil, ErrInvalidCredentials
	}
	return normalizePasswordPolicy(policy), salt, key, nil
}
