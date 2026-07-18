// 包 tlsfpresolve 把账号绑定或轮换池选中的数据库 TLS profile 规范化为 Rust
// sidecar 的 inline profile。它只负责读取、选择和校验，不创建网络 transport。
package tlsfpresolve

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// accountState 是账号的 TLS 指纹解析输入:
//   - rotate=false:bound 为绑定的 active profile(nil=无)。
//   - rotate=true :pool 为该 tenant 的 active profile 列表(供确定性轮换)。
type accountState struct {
	rotate bool
	bound  *mimicry.ProfileFields
	pool   []mimicry.ProfileFields
}

// fetcher 读账号的 TLS 指纹状态。抽象出来让 Resolver 选择逻辑可脱离 DB 单测。
type fetcher interface {
	fetch(ctx context.Context, accountID int64) (accountState, error)
}

// Resolver 返回账号应使用的动态 profile；nil 表示使用 mode 对应的内置 profile。
type Resolver struct {
	f fetcher
}

func newResolver(f fetcher) *Resolver { return &Resolver{f: f} }

// NewPostgresResolver 用连接池构造生产 Resolver。
func NewPostgresResolver(pool *pgxpool.Pool) *Resolver {
	return &Resolver{f: &pgxFetcher{pool: pool}}
}

// ResolveProfile 仅在账号明确绑定 active profile，或开启轮换且池非空时返回动态
// profile。显式选中的坏 profile 和数据库故障都向上传播，防止静默换用错误指纹。
func (r *Resolver) ResolveProfile(ctx context.Context, accountID int64) (*mimicry.InlineTLSProfile, error) {
	if r == nil || r.f == nil || accountID == 0 {
		return nil, nil
	}
	st, err := r.f.fetch(ctx, accountID)
	if err != nil {
		return nil, err
	}
	chosen := selectProfile(accountID, st)
	if chosen == nil {
		return nil, nil
	}
	profile, err := mimicry.InlineTLSProfileFromFields(*chosen)
	if err != nil {
		return nil, fmt.Errorf("tlsfpresolve: account_id=%d 的动态 profile 无法执行: %w", accountID, err)
	}
	return profile, nil
}

// selectProfile 按 state 选出该账号应使用的 profile fields(nil=builtin)。
// rotate 时按 pickIndex 在 pool 里确定性选一个;否则用绑定 profile。纯函数,可单测。
func selectProfile(accountID int64, st accountState) *mimicry.ProfileFields {
	if st.rotate {
		if len(st.pool) == 0 {
			return nil
		}
		p := st.pool[pickIndex(accountID, len(st.pool))]
		return &p
	}
	return st.bound
}

// pickIndex 在 [0,n) 内按 accountID 确定性选一个:不同账号散开、同账号永远粘同一个
// (anti-clustering 但每账号指纹稳定)。
func pickIndex(accountID int64, n int) int {
	if n <= 0 {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(strconv.FormatInt(accountID, 10)))
	return int(h.Sum32() % uint32(n))
}

// pgxFetcher 用 raw pgx 读账号 TLS 指纹状态(仿 proxy_resolver,不依赖 admindb/sqlc)。
type pgxFetcher struct {
	pool *pgxpool.Pool
}

func (p *pgxFetcher) fetch(ctx context.Context, accountID int64) (accountState, error) {
	if p == nil || p.pool == nil {
		return accountState{}, nil
	}
	const q1 = `
		SELECT pa.tenant_id, COALESCE(pa.tls_fingerprint_rotate, false), COALESCE(pr.id,0),
		       COALESCE(pr.status,''), COALESCE(pr.name,''), COALESCE(pr.grease_enabled,false),
		       pr.cipher_suites, pr.supported_curves, pr.ec_point_formats, pr.signature_algorithms,
		       pr.alpn_protocols, pr.tls_supported_versions, pr.key_share_groups, pr.psk_modes,
		       pr.extensions_order, COALESCE(pr.expected_ja3_hash,'')
		FROM provider_accounts pa
		LEFT JOIN tls_fingerprint_profiles pr
		       ON pa.tls_fingerprint_profile_id = pr.id AND pr.deleted_at IS NULL
		WHERE pa.id = $1`
	var tenantID, profileID int64
	var rotate, grease bool
	var status, name, ja3 string
	var ciphers, curves, points, sigs, versions, keyShares, pskModes, exts []int32
	var alpn []string
	err := p.pool.QueryRow(ctx, q1, accountID).Scan(
		&tenantID, &rotate, &profileID, &status, &name, &grease, &ciphers, &curves, &points, &sigs,
		&alpn, &versions, &keyShares, &pskModes, &exts, &ja3,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return accountState{}, nil
		}
		return accountState{}, err
	}
	if rotate {
		pool, lerr := p.listActive(ctx, tenantID)
		if lerr != nil {
			return accountState{}, lerr
		}
		return accountState{rotate: true, pool: pool}, nil
	}
	if status == "active" {
		f := mkFields(profileID, name, grease, ciphers, curves, points, sigs, alpn, versions, keyShares, pskModes, exts, ja3)
		return accountState{bound: &f}, nil
	}
	return accountState{}, nil
}

func (p *pgxFetcher) listActive(ctx context.Context, tenantID int64) ([]mimicry.ProfileFields, error) {
	const q2 = `
		SELECT id, name, grease_enabled, cipher_suites, supported_curves, ec_point_formats,
		       signature_algorithms, alpn_protocols, tls_supported_versions, key_share_groups,
		       psk_modes, extensions_order, expected_ja3_hash
		FROM tls_fingerprint_profiles
		WHERE tenant_id = $1 AND status = 'active' AND deleted_at IS NULL
		ORDER BY id`
	rows, err := p.pool.Query(ctx, q2, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mimicry.ProfileFields
	for rows.Next() {
		var profileID int64
		var grease bool
		var name, ja3 string
		var ciphers, curves, points, sigs, versions, keyShares, pskModes, exts []int32
		var alpn []string
		if err := rows.Scan(&profileID, &name, &grease, &ciphers, &curves, &points, &sigs, &alpn,
			&versions, &keyShares, &pskModes, &exts, &ja3); err != nil {
			return nil, err
		}
		out = append(out, mkFields(profileID, name, grease, ciphers, curves, points, sigs, alpn, versions, keyShares, pskModes, exts, ja3))
	}
	return out, rows.Err()
}

func mkFields(id int64, name string, grease bool, ciphers, curves, points, sigs []int32, alpn []string, versions, keyShares, pskModes, exts []int32, ja3 string) mimicry.ProfileFields {
	return mimicry.ProfileFields{
		ID:                   id,
		Name:                 name,
		GreaseEnabled:        grease,
		CipherSuites:         i32s(ciphers),
		SupportedCurves:      i32s(curves),
		EcPointFormats:       i32s(points),
		SignatureAlgorithms:  i32s(sigs),
		AlpnProtocols:        alpn,
		TLSSupportedVersions: i32s(versions),
		KeyShareGroups:       i32s(keyShares),
		PskModes:             i32s(pskModes),
		ExtensionsOrder:      i32s(exts),
		ExpectedJA3Hash:      ja3,
	}
}

func i32s(in []int32) []int {
	if in == nil {
		return nil
	}
	out := make([]int, len(in))
	for i, v := range in {
		out[i] = int(v)
	}
	return out
}
