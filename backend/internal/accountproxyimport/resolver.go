// Package accountproxyimport 把受控账号来源携带的代理与账号写入合并到同一事务。
package accountproxyimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/gatewayhttp/accountintake"
	"github.com/BloomingProsperity/HUAKAI/internal/proxyadmin"
	"github.com/BloomingProsperity/HUAKAI/internal/proxysecret"
)

var sourceRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Resolver struct {
	keys credentialstore.KeyProvider
}

func New(keys credentialstore.KeyProvider) *Resolver {
	return &Resolver{keys: keys}
}

func (r *Resolver) ResolveTx(ctx context.Context, tx pgx.Tx, in accountintake.ProxyResolveInput) (int64, error) {
	if r == nil || r.keys == nil || tx == nil || in.TenantID <= 0 {
		return 0, accountintake.ErrNotConfigured
	}
	material := normalize(in.Material)
	if !sourceRefPattern.MatchString(material.SourceRef) {
		return 0, accountintake.ErrInvalidInput
	}
	if (material.AuthUsername == "") != (material.AuthSecret == "") {
		return 0, accountintake.ErrInvalidInput
	}
	name := deterministicName(material)
	username := optional(material.AuthUsername)
	secret, err := encryptedSecret(ctx, r.keys, in.TenantID, material.AuthSecret)
	if err != nil {
		return 0, err
	}
	if err := proxyadmin.ValidateCreateInput(proxyadmin.CreateInput{
		TenantID: in.TenantID, Name: name, Protocol: material.Protocol,
		Host: material.Host, Port: material.Port, AuthUsername: username,
		AuthSecret: optional(material.AuthSecret), Status: "active",
	}); err != nil {
		return 0, err
	}
	lockKey := fmt.Sprintf("account-proxy-import:%d:%s", in.TenantID, name)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
		return 0, err
	}

	var existing struct {
		id       int64
		protocol string
		host     string
		port     int32
	}
	err = tx.QueryRow(ctx, `
SELECT id, protocol, host, port
FROM proxies
WHERE tenant_id=$1 AND name=$2 AND deleted_at IS NULL
FOR UPDATE`, in.TenantID, name).Scan(&existing.id, &existing.protocol, &existing.host, &existing.port)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	q := admindb.New(tx)
	if errors.Is(err, pgx.ErrNoRows) {
		row, createErr := q.CreateProxy(ctx, admindb.CreateProxyParams{
			TenantID: in.TenantID, Name: name, Protocol: material.Protocol, Host: material.Host,
			Port: material.Port, AuthUsername: username, AuthSecret: secret, Status: "active",
		})
		if createErr != nil {
			return 0, createErr
		}
		return row.ID, nil
	}
	if existing.protocol != material.Protocol || existing.host != material.Host || existing.port != material.Port {
		return 0, fmt.Errorf("%w: imported proxy identity collision", accountintake.ErrExecutionStale)
	}
	row, err := q.UpdateProxy(ctx, admindb.UpdateProxyParams{
		TenantID: in.TenantID, ID: existing.id, Name: name, Protocol: material.Protocol,
		Host: material.Host, Port: material.Port, AuthUsername: username, AuthSecret: secret,
	})
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}

func normalize(in accountintake.ProxyMaterial) accountintake.ProxyMaterial {
	in.Name = strings.TrimSpace(in.Name)
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	in.Host = strings.ToLower(strings.TrimSpace(in.Host))
	in.AuthUsername = strings.TrimSpace(in.AuthUsername)
	in.SourceRef = strings.ToLower(strings.TrimSpace(in.SourceRef))
	return in
}

func deterministicName(in accountintake.ProxyMaterial) string {
	endpoint := strings.Join([]string{in.Protocol, in.Host, fmt.Sprint(in.Port), in.AuthUsername}, "\x00")
	sum := sha256.Sum256([]byte(endpoint))
	return "source-" + in.SourceRef + "-" + hex.EncodeToString(sum[:6])
}

func encryptedSecret(ctx context.Context, keys credentialstore.KeyProvider, tenantID int64, value string) (*string, error) {
	if value == "" {
		return nil, nil
	}
	encoded, err := proxysecret.Encode(ctx, keys, tenantID, value)
	if err != nil {
		return nil, err
	}
	return &encoded, nil
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}
