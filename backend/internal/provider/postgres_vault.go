// 包 provider 提供凭据仓库的 PostgreSQL 后端实现。
// 本文件实现 PostgresCredentialVault，负责从 provider_accounts 表中读取
// 上游凭据，并将数据库行映射为 Credential + AccountInfo。
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrAccountDisabled 表示 provider_accounts.enabled = false。
// 调用方应将此错误映射到 HTTP 403 或适当的拒绝响应，不同于 404 Not Found。
var ErrAccountDisabled = errors.New("provider credential account is disabled")

// ErrCredentialFormat 表示 credentials JSONB 字段无法按预期格式反序列化。
// 始终包装底层错误，可用 errors.Unwrap 获取原始原因。
var ErrCredentialFormat = errors.New("provider credential format invalid")

// PostgresCredentialVault 是基于 PostgreSQL 的 CredentialVault 实现。
// 从 provider_accounts JOIN providers 表中读取凭据。
// 使用 REPEATABLE READ + READ ONLY 事务，与 postgres_registry.go 保持一致。
type PostgresCredentialVault struct {
	pool *pgxpool.Pool
}

// 编译期接口合规断言。
var _ CredentialVault = (*PostgresCredentialVault)(nil)

// NewPostgresCredentialVault 用给定的连接池创建 PostgresCredentialVault。
// pool 不应为 nil；调用方负责池的生命周期管理。
func NewPostgresCredentialVault(pool *pgxpool.Pool) *PostgresCredentialVault {
	return &PostgresCredentialVault{pool: pool}
}

// providerAccountRow 是查询结果的内部映射结构。
type providerAccountRow struct {
	id              int64
	tenantID        int64
	providerID      int64
	name            string
	accountType     string
	enabled         bool
	credentialState string
	credentials     []byte  // 原始 JSONB 字节
	platform        string  // providers.code via JOIN
}

// Resolve 按 accountID 查询 provider_accounts 和关联的 providers 表，
// 返回 Credential 和 AccountInfo。
//
// 错误语义：
//   - 行不存在            → ErrAccountNotFound
//   - enabled = false     → ErrAccountDisabled
//   - JSONB 解析失败      → ErrCredentialFormat（包装底层错误）
//   - 数据库基础设施故障  → 包装底层 pgx 错误
func (v *PostgresCredentialVault) Resolve(ctx context.Context, accountID int64) (Credential, AccountInfo, error) {
	// 开启 REPEATABLE READ + READ ONLY 事务，与 postgres_registry.go 保持一致，
	// 确保 JOIN 读取在同一快照下完成，避免 mid-read 凭据状态撕裂。
	tx, err := v.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Credential{}, AccountInfo{}, fmt.Errorf("provider vault: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row, err := queryProviderAccount(ctx, tx, accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Credential{}, AccountInfo{}, fmt.Errorf("account %d: %w", accountID, ErrAccountNotFound)
		}
		return Credential{}, AccountInfo{}, fmt.Errorf("provider vault: query: %w", err)
	}

	// 账号已禁用时提前返回，不解析凭据。
	if !row.enabled {
		return Credential{}, AccountInfo{}, fmt.Errorf("account %d: %w", accountID, ErrAccountDisabled)
	}

	cred, err := mapCredential(row.accountType, row.credentials)
	if err != nil {
		return Credential{}, AccountInfo{}, err
	}

	// 提交只读事务（提交一个只读事务无副作用，与 postgres_registry.go 一致）。
	if err := tx.Commit(ctx); err != nil {
		return Credential{}, AccountInfo{}, fmt.Errorf("provider vault: commit: %w", err)
	}

	info := AccountInfo{
		AccountID:   row.id,
		Platform:    row.platform,
		AccountType: row.accountType,
	}

	return cred, info, nil
}

// queryProviderAccount 执行 provider_accounts JOIN providers 查询。
// 在调用方已建立的事务 tx 内运行，保持快照一致性。
func queryProviderAccount(ctx context.Context, tx pgx.Tx, accountID int64) (providerAccountRow, error) {
	const q = `
SELECT
    pa.id,
    pa.tenant_id,
    pa.provider_id,
    pa.name,
    pa.account_type,
    pa.enabled,
    pa.credential_state,
    pa.credentials,
    p.code AS platform
FROM provider_accounts pa
JOIN providers p ON p.id = pa.provider_id
WHERE pa.id = $1
  AND pa.deleted_at IS NULL
LIMIT 1`

	var r providerAccountRow
	err := tx.QueryRow(ctx, q, accountID).Scan(
		&r.id,
		&r.tenantID,
		&r.providerID,
		&r.name,
		&r.accountType,
		&r.enabled,
		&r.credentialState,
		&r.credentials,
		&r.platform,
	)
	if err != nil {
		return providerAccountRow{}, err
	}
	return r, nil
}

// ---- JSONB 凭据形态映射 -------------------------------------------------------

// rawAPIKey 对应 account_type='api_key' 的 credentials JSONB 结构。
type rawAPIKey struct {
	APIKey string            `json:"api_key"`
	Extra  map[string]string `json:"extra,omitempty"`
}

// rawOAuth 对应 account_type='oauth' 的 credentials JSONB 结构。
type rawOAuth struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

// rawServiceAccount 对应 account_type='service_account'（Google SA）的 credentials JSONB 结构。
type rawServiceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri,omitempty"`
}

// rawUpstreamStatic 对应 account_type='upstream_static' 的 credentials JSONB 结构。
type rawUpstreamStatic struct {
	BaseURL         string `json:"base_url"`
	AuthHeaderValue string `json:"auth_header_value"`
}

// rawSession 对应 account_type='session' 的 credentials JSONB 结构。
// session 类型用于订阅反转（cursor / copilot / gemini_advanced 等），
// session_token 为主值，extra 透传 vendor-specific 元数据
// （cookie / oai_device_id / codeium_extension_version 等）。
type rawSession struct {
	SessionToken string            `json:"session_token"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// mapCredential 根据 accountType 将原始 JSONB 字节映射为 Credential。
// 解析失败或必填字段为空时返回包装了 ErrCredentialFormat 的错误。
func mapCredential(accountType string, raw []byte) (Credential, error) {
	switch accountType {
	case "api_key":
		return mapAPIKey(raw)
	case "oauth":
		return mapOAuth(raw)
	case "service_account":
		return mapServiceAccount(raw)
	case "upstream_static":
		return mapUpstreamStatic(raw)
	case "session":
		return mapSession(raw)
	default:
		// 未知 account_type：包装为格式错误，避免静默返回零值凭据。
		return Credential{}, fmt.Errorf("%w: unknown account_type %q", ErrCredentialFormat, accountType)
	}
}

// mapSession 解析 session 类型凭据。
// session_token 为主值；extra 字段可选，透传 vendor 元数据。
func mapSession(raw []byte) (Credential, error) {
	var r rawSession
	if err := json.Unmarshal(raw, &r); err != nil {
		return Credential{}, fmt.Errorf("%w: session unmarshal: %v", ErrCredentialFormat, err)
	}
	if r.SessionToken == "" {
		return Credential{}, fmt.Errorf("%w: session_token field is empty", ErrCredentialFormat)
	}
	cred := Credential{
		Type:  CredentialTypeSessionToken,
		Value: r.SessionToken,
	}
	if len(r.Extra) > 0 {
		cred.Extra = make(map[string]string, len(r.Extra))
		for k, v := range r.Extra {
			cred.Extra[k] = v
		}
	}
	return cred, nil
}

// mapAPIKey 解析 api_key 类型凭据。
// credentials.api_key 必须非空；extra 字段可选。
func mapAPIKey(raw []byte) (Credential, error) {
	var r rawAPIKey
	if err := json.Unmarshal(raw, &r); err != nil {
		return Credential{}, fmt.Errorf("%w: api_key unmarshal: %v", ErrCredentialFormat, err)
	}
	if r.APIKey == "" {
		return Credential{}, fmt.Errorf("%w: api_key field is empty", ErrCredentialFormat)
	}
	cred := Credential{
		Type:  CredentialTypeAPIKey,
		Value: r.APIKey,
	}
	// 仅当 extra 非空时才分配 map，避免不必要的内存分配。
	if len(r.Extra) > 0 {
		cred.Extra = make(map[string]string, len(r.Extra))
		for k, val := range r.Extra {
			cred.Extra[k] = val
		}
	}
	return cred, nil
}

// mapOAuth 解析 oauth 类型凭据。
// access_token 为主值；refresh_token / expires_at 及其他字段进入 Extra。
func mapOAuth(raw []byte) (Credential, error) {
	var r rawOAuth
	if err := json.Unmarshal(raw, &r); err != nil {
		return Credential{}, fmt.Errorf("%w: oauth unmarshal: %v", ErrCredentialFormat, err)
	}
	if r.AccessToken == "" {
		return Credential{}, fmt.Errorf("%w: oauth access_token field is empty", ErrCredentialFormat)
	}
	// 将所有 JSON 字段解码到通用 map，以捕获 vendor 扩展字段。
	var allFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &allFields); err != nil {
		return Credential{}, fmt.Errorf("%w: oauth fields unmarshal: %v", ErrCredentialFormat, err)
	}
	extra := make(map[string]string)
	for k, v := range allFields {
		if k == "access_token" {
			continue // access_token 已提升为 Value，不重复放入 Extra
		}
		// 仅处理 JSON 字符串类型的扩展字段；非字符串类型跳过以保持稳定性。
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			extra[k] = s
		}
	}
	cred := Credential{
		Type:  CredentialTypeOAuthAccessToken,
		Value: r.AccessToken,
	}
	if len(extra) > 0 {
		cred.Extra = extra
	}
	return cred, nil
}

// mapServiceAccount 解析 service_account 类型凭据（Google SA）。
// 映射为 CredentialTypeOAuthAccessToken，并在 Extra["auth_kind"]="google_sa" 标记。
// client_email / private_key / token_uri 一并放入 Extra，供下游 adapter 使用。
func mapServiceAccount(raw []byte) (Credential, error) {
	var r rawServiceAccount
	if err := json.Unmarshal(raw, &r); err != nil {
		return Credential{}, fmt.Errorf("%w: service_account unmarshal: %v", ErrCredentialFormat, err)
	}
	if r.ClientEmail == "" {
		return Credential{}, fmt.Errorf("%w: service_account client_email field is empty", ErrCredentialFormat)
	}
	if r.PrivateKey == "" {
		return Credential{}, fmt.Errorf("%w: service_account private_key field is empty", ErrCredentialFormat)
	}
	extra := map[string]string{
		"auth_kind":    "google_sa",
		"client_email": r.ClientEmail,
		"private_key":  r.PrivateKey,
	}
	if r.TokenURI != "" {
		extra["token_uri"] = r.TokenURI
	}
	// service_account 在获取访问令牌前主值留空；适配器凭 auth_kind 决策令牌交换流程。
	return Credential{
		Type:  CredentialTypeOAuthAccessToken,
		Value: "",
		Extra: extra,
	}, nil
}

// mapUpstreamStatic 解析 upstream_static 类型凭据。
// auth_header_value 为主值；base_url 放入 Extra["base_url"]。
func mapUpstreamStatic(raw []byte) (Credential, error) {
	var r rawUpstreamStatic
	if err := json.Unmarshal(raw, &r); err != nil {
		return Credential{}, fmt.Errorf("%w: upstream_static unmarshal: %v", ErrCredentialFormat, err)
	}
	if r.AuthHeaderValue == "" {
		return Credential{}, fmt.Errorf("%w: upstream_static auth_header_value field is empty", ErrCredentialFormat)
	}
	extra := map[string]string{}
	if r.BaseURL != "" {
		extra["base_url"] = r.BaseURL
	}
	cred := Credential{
		Type:  CredentialTypeUpstreamPassthrough,
		Value: r.AuthHeaderValue,
	}
	if len(extra) > 0 {
		cred.Extra = extra
	}
	return cred, nil
}
