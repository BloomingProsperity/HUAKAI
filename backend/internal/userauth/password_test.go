package userauth

import (
	"errors"
	"strings"
	"testing"
)

// Owner 2026-05-27 抓出 P2: parsePasswordHash 把 `p=` 等参数 ParseUint 到
// uint64, 再直接 uint8/uint32 截断, p=256 会 wrap 成 0 后被
// normalizePasswordPolicy 静默拉回 default, 攻击者可借此让 hash 校验绕过
// hash header 中声明的 parallelism。同时 m / t 缺上限可 DoS。
// 判别 mutation: 删 p > 255 / m > 2^20 / t > 100 任一 bound check → 对应
// sub-test 立即 PASS 接受恶意值 而 happy path test 仍 PASS, 区分。
func TestParsePasswordHashRejectsOutOfRangeParams(t *testing.T) {
	// 合法 argon2id hash header (默认 m=64MB t=3 p=1) + 空 salt/key:
	// $argon2id$v=19$m=65536,t=3,p=1$<salt>$<key>
	// salt/key base64 段任意, 我们只测 header 段解析。
	const validSalt = "MDAwMDAwMDAwMDAwMDAwMA" // 16 bytes 0x30, raw std b64
	const validKey = "MDAwMDAwMDAwMDAwMDAwMA"

	cases := []struct {
		name, encoded string
		wantErr       bool
	}{
		{name: "happy_path_defaults", encoded: "$argon2id$v=19$m=65536,t=3,p=1$" + validSalt + "$" + validKey, wantErr: false},
		{name: "parallelism_wraps_p_256", encoded: "$argon2id$v=19$m=65536,t=3,p=256$" + validSalt + "$" + validKey, wantErr: true},
		{name: "parallelism_huge_p_65535", encoded: "$argon2id$v=19$m=65536,t=3,p=65535$" + validSalt + "$" + validKey, wantErr: true},
		{name: "memory_dos_above_1gib", encoded: "$argon2id$v=19$m=1048577,t=3,p=1$" + validSalt + "$" + validKey, wantErr: true},
		{name: "iteration_dos_above_100", encoded: "$argon2id$v=19$m=65536,t=101,p=1$" + validSalt + "$" + validKey, wantErr: true},
		// Owner 2026-05-27 二次抓: 0 值原本被 normalizePasswordPolicy 兜底成
		// default, 攻击者可写 m=0/t=0/p=0 让 hash 校验静默走 default。修复
		// 把 0 视为非法。
		{name: "memory_zero_must_reject", encoded: "$argon2id$v=19$m=0,t=3,p=1$" + validSalt + "$" + validKey, wantErr: true},
		{name: "iteration_zero_must_reject", encoded: "$argon2id$v=19$m=65536,t=0,p=1$" + validSalt + "$" + validKey, wantErr: true},
		{name: "parallelism_zero_must_reject", encoded: "$argon2id$v=19$m=65536,t=3,p=0$" + validSalt + "$" + validKey, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := parsePasswordHash(tc.encoded)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidCredentials) {
					t.Fatalf("err=%v want ErrInvalidCredentials for out-of-range param", err)
				}
				return
			}
			if err != nil {
				if strings.Contains(err.Error(), "invalid") {
					t.Fatalf("happy-path 不应被错误拒绝, err=%v", err)
				}
				t.Fatalf("err=%v", err)
			}
		})
	}
}
