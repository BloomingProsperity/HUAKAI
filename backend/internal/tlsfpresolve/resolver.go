// 包 tlsfpresolve — UTLS-03:把账号绑定的 DB TLS-fingerprint profile 解析成
// 出站 uTLS RoundTripper,让"写入即生效"。未绑定 / 非 active / profile 非法
// 一律回落到 builtin per-mode 指纹(永不让坏 profile 把账号打黑)。
package tlsfpresolve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// boundProfile 是账号绑定的 active TLS profile。active=false 表示"无可用 profile"
// -> 走 builtin。
type boundProfile struct {
	active bool
	fields mimicry.ProfileFields
}

// fetcher 读账号绑定的 active TLS profile。抽象出来让 Resolver 逻辑可脱离 DB 单测。
type fetcher interface {
	fetch(ctx context.Context, accountID int64) (boundProfile, error)
}

// Resolver 由账号绑定的 DB profile 构造 per-account uTLS RoundTripper;返回 nil
// 表示保持 builtin per-mode 指纹。实现 gateway 消费的结构化接口(返回
// http.RoundTripper,gateway 无需反向 import mimicry)。
type Resolver struct {
	f fetcher
}

func newResolver(f fetcher) *Resolver { return &Resolver{f: f} }

// NewPostgresResolver 用连接池构造生产 Resolver。
func NewPostgresResolver(pool *pgxpool.Pool) *Resolver {
	return &Resolver{f: &pgxFetcher{pool: pool}}
}

// ResolveRoundTripper 返回由账号绑定 DB profile 驱动的 uTLS RoundTripper,或
// (nil,nil) 回落 builtin。永不因坏/缺 profile 让 dispatch 失败:转换错误 ->
// builtin(记日志)。infra 错误向上传播,由调用方(dispatcher)记录并回落。
func (r *Resolver) ResolveRoundTripper(ctx context.Context, accountID int64) (http.RoundTripper, error) {
	if r == nil || r.f == nil || accountID == 0 {
		return nil, nil
	}
	bp, err := r.f.fetch(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if !bp.active {
		return nil, nil
	}
	tmpl, cerr := mimicry.TemplateFromProfileFields(bp.fields)
	if cerr != nil {
		slog.Warn("tlsfpresolve: 绑定的 TLS profile 非法, 回落 builtin", "account_id", accountID, "err", cerr)
		return nil, nil
	}
	return mimicry.NewRoundTripper(tmpl), nil
}

// pgxFetcher 用单条 LEFT JOIN 读账号绑定的 active profile(仿 proxy_resolver,
// 不依赖 admindb / sqlc)。
type pgxFetcher struct {
	pool *pgxpool.Pool
}

func (p *pgxFetcher) fetch(ctx context.Context, accountID int64) (boundProfile, error) {
	if p == nil || p.pool == nil {
		return boundProfile{}, nil
	}
	const q = `
		SELECT COALESCE(pr.status,''), COALESCE(pr.name,''), COALESCE(pr.grease_enabled,false),
		       pr.cipher_suites, pr.supported_curves, pr.ec_point_formats, pr.signature_algorithms,
		       pr.alpn_protocols, pr.tls_supported_versions, pr.key_share_groups, pr.psk_modes,
		       pr.extensions_order, COALESCE(pr.expected_ja3_hash,'')
		FROM provider_accounts pa
		LEFT JOIN tls_fingerprint_profiles pr
		       ON pa.tls_fingerprint_profile_id = pr.id AND pr.deleted_at IS NULL
		WHERE pa.id = $1`
	var status, name, ja3 string
	var grease bool
	var ciphers, curves, points, sigs, versions, keyShares, pskModes, exts []int32
	var alpn []string
	err := p.pool.QueryRow(ctx, q, accountID).Scan(
		&status, &name, &grease, &ciphers, &curves, &points, &sigs,
		&alpn, &versions, &keyShares, &pskModes, &exts, &ja3,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return boundProfile{}, nil
		}
		return boundProfile{}, err
	}
	if status != "active" {
		return boundProfile{}, nil
	}
	return boundProfile{active: true, fields: mimicry.ProfileFields{
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
	}}, nil
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
