// 包 tlsfpresolve — UTLS-03/04:把账号绑定(或轮换池)的 DB TLS-fingerprint
// profile 解析成出站 uTLS RoundTripper,让"写入即生效"。未绑定 / 非 active /
// profile 非法 / 空池一律回落 builtin per-mode 指纹(永不让坏 profile 把账号打黑)。
package tlsfpresolve

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"net/http"
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

// Resolver 由账号绑定/轮换的 DB profile 构造 per-account uTLS RoundTripper;返回
// nil 表示保持 builtin。实现 gateway 消费的结构化接口(返回 http.RoundTripper,
// gateway 无需反向 import mimicry)。
type Resolver struct {
	f fetcher
}

func newResolver(f fetcher) *Resolver { return &Resolver{f: f} }

// NewPostgresResolver 用连接池构造生产 Resolver。
func NewPostgresResolver(pool *pgxpool.Pool) *Resolver {
	return &Resolver{f: &pgxFetcher{pool: pool}}
}

// ResolveRoundTripper 返回账号 TLS profile 驱动的 uTLS RoundTripper,或 (nil,nil)
// 回落 builtin。永不因坏/缺/空 profile 让 dispatch 失败:转换错误 -> builtin(记
// 日志);infra 错误向上传播,由 dispatcher 记录并回落。
func (r *Resolver) ResolveRoundTripper(ctx context.Context, accountID int64) (http.RoundTripper, error) {
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
	tmpl, cerr := mimicry.TemplateFromProfileFields(*chosen)
	if cerr != nil {
		slog.Warn("tlsfpresolve: 选中的 TLS profile 非法, 回落 builtin", "account_id", accountID, "err", cerr)
		return nil, nil
	}
	return mimicry.NewRoundTripper(tmpl), nil
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
		SELECT pa.tenant_id, COALESCE(pa.tls_fingerprint_rotate, false),
		       COALESCE(pr.status,''), COALESCE(pr.name,''), COALESCE(pr.grease_enabled,false),
		       pr.cipher_suites, pr.supported_curves, pr.ec_point_formats, pr.signature_algorithms,
		       pr.alpn_protocols, pr.tls_supported_versions, pr.key_share_groups, pr.psk_modes,
		       pr.extensions_order, COALESCE(pr.expected_ja3_hash,'')
		FROM provider_accounts pa
		LEFT JOIN tls_fingerprint_profiles pr
		       ON pa.tls_fingerprint_profile_id = pr.id AND pr.deleted_at IS NULL
		WHERE pa.id = $1`
	var tenantID int64
	var rotate, grease bool
	var status, name, ja3 string
	var ciphers, curves, points, sigs, versions, keyShares, pskModes, exts []int32
	var alpn []string
	err := p.pool.QueryRow(ctx, q1, accountID).Scan(
		&tenantID, &rotate, &status, &name, &grease, &ciphers, &curves, &points, &sigs,
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
		f := mkFields(name, grease, ciphers, curves, points, sigs, alpn, versions, keyShares, pskModes, exts, ja3)
		return accountState{bound: &f}, nil
	}
	return accountState{}, nil
}

func (p *pgxFetcher) listActive(ctx context.Context, tenantID int64) ([]mimicry.ProfileFields, error) {
	const q2 = `
		SELECT name, grease_enabled, cipher_suites, supported_curves, ec_point_formats,
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
		var grease bool
		var name, ja3 string
		var ciphers, curves, points, sigs, versions, keyShares, pskModes, exts []int32
		var alpn []string
		if err := rows.Scan(&name, &grease, &ciphers, &curves, &points, &sigs, &alpn,
			&versions, &keyShares, &pskModes, &exts, &ja3); err != nil {
			return nil, err
		}
		out = append(out, mkFields(name, grease, ciphers, curves, points, sigs, alpn, versions, keyShares, pskModes, exts, ja3))
	}
	return out, rows.Err()
}

func mkFields(name string, grease bool, ciphers, curves, points, sigs []int32, alpn []string, versions, keyShares, pskModes, exts []int32, ja3 string) mimicry.ProfileFields {
	return mimicry.ProfileFields{
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
