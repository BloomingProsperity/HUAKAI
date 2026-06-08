package tlsfphealth

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/transport/mimicry"
)

// pgxStore 用 raw pgx 实现 Lister + DriftMarker(不依赖 admindb/sqlc;全租户扫)。
type pgxStore struct {
	pool *pgxpool.Pool
}

func NewPostgresLister(pool *pgxpool.Pool) Lister           { return &pgxStore{pool: pool} }
func NewPostgresDriftMarker(pool *pgxpool.Pool) DriftMarker { return &pgxStore{pool: pool} }

func (p *pgxStore) ListActive(ctx context.Context) ([]ProfileRecord, error) {
	const q = `
		SELECT id, tenant_id, COALESCE(name,''), COALESCE(grease_enabled,false),
		       cipher_suites, supported_curves, ec_point_formats, signature_algorithms,
		       alpn_protocols, tls_supported_versions, key_share_groups, psk_modes,
		       extensions_order, COALESCE(expected_ja3_hash,'')
		FROM tls_fingerprint_profiles
		WHERE status = 'active' AND deleted_at IS NULL
		ORDER BY id
		LIMIT $1`
	rows, err := p.pool.Query(ctx, q, maxPerTick)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProfileRecord
	for rows.Next() {
		var rec ProfileRecord
		var grease bool
		var ja3 string
		var ciphers, curves, points, sigs, versions, keyShares, pskModes, exts []int32
		var alpn []string
		if err := rows.Scan(&rec.ID, &rec.TenantID, &rec.Name, &grease,
			&ciphers, &curves, &points, &sigs, &alpn, &versions, &keyShares, &pskModes, &exts, &ja3); err != nil {
			return nil, err
		}
		rec.Fields = mimicry.ProfileFields{
			Name:                 rec.Name,
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
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (p *pgxStore) MarkDrift(ctx context.Context, tenantID, id int64) error {
	_, err := p.pool.Exec(ctx,
		`UPDATE tls_fingerprint_profiles SET status = 'drift_detected', last_validated_at = NOW()
		 WHERE id = $1 AND tenant_id = $2 AND status = 'active'`,
		id, tenantID)
	return err
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
