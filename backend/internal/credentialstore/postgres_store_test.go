package credentialstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestRefreshFailureStateOperatorConfigRequiresAttention(t *testing.T) {
	if got := refreshFailureState("operator_config_required"); got != StateOperatorAttention {
		t.Fatalf("state=%q, want %q", got, StateOperatorAttention)
	}
	if got := refreshFailureState("temporary"); got != StateTempUnschedulable {
		t.Fatalf("temporary state=%q, want %q", got, StateTempUnschedulable)
	}
}

func TestResolveActiveRejectsAmbiguousActiveCredentialModes(t *testing.T) {
	calls := 0
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, args ...interface{}) pgx.Row {
			calls++
			for _, required := range []string{
				"COUNT(*) OVER () AS active_mode_count",
				"active_mode_count",
				"WHERE ac.provider_account_id = $1",
				"AND ac.tenant_id = $2",
			} {
				if !strings.Contains(sql, required) {
					t.Fatalf("ResolveActive SQL missing %q:\n%s", required, sql)
				}
			}
			if len(args) != 2 || args[0] != int64(42) || args[1] != int64(7) {
				t.Fatalf("ResolveActive args=%#v, want account=42 tenant=7", args)
			}
			return credentialStoreRowValuesStub{values: resolveActiveRecordValues(2)}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.ResolveActive(context.Background(), 7, 42)
	if !errors.Is(err, ErrCredentialAmbiguous) {
		t.Fatalf("ResolveActive err=%v, want ambiguous active credential modes", err)
	}
	if calls != 1 {
		t.Fatalf("ResolveActive QueryRow calls=%d, want 1 atomic ambiguity-aware query", calls)
	}
}

func TestLoadForProviderAccountTestRejectsAmbiguousCredentialModes(t *testing.T) {
	// 判别 provider-account test 不能随便 LIMIT 1:多个可测试 credential mode
	// 与运行时 resolver 一样是歧义,否则操作员可能测到并非实际可安全使用的凭据。
	calls := 0
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, args ...interface{}) pgx.Row {
			calls++
			for _, required := range []string{
				"COUNT(*) OVER () AS test_mode_count",
				"test_mode_count",
				"WHERE ac.provider_account_id = $1",
				"AND ac.tenant_id = $2",
				"AND ac.state IN ('active', 'refreshing_with_grace', 'temp_unschedulable', 'operator_attention')",
			} {
				if !strings.Contains(sql, required) {
					t.Fatalf("LoadForProviderAccountTest SQL missing %q:\n%s", required, sql)
				}
			}
			if len(args) != 2 || args[0] != int64(42) || args[1] != int64(7) {
				t.Fatalf("LoadForProviderAccountTest args=%#v, want account=42 tenant=7", args)
			}
			return credentialStoreRowValuesStub{values: providerAccountTestRecordValues(2)}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.LoadForProviderAccountTest(context.Background(), 7, 42)
	if !errors.Is(err, ErrCredentialAmbiguous) {
		t.Fatalf("LoadForProviderAccountTest err=%v, want ambiguous credential modes", err)
	}
	if calls != 1 {
		t.Fatalf("LoadForProviderAccountTest QueryRow calls=%d, want 1 atomic ambiguity-aware query", calls)
	}
}

func TestResolveActiveRejectsCrossTenantCredentialJoin(t *testing.T) {
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, _ ...interface{}) pgx.Row {
			if !strings.Contains(sql, "pa.tenant_id = ac.tenant_id") {
				t.Fatalf("ResolveActive SQL missing tenant equality join:\n%s", sql)
			}
			return credentialStoreRowStub{err: pgx.ErrNoRows}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.ResolveActive(context.Background(), 7, 42)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("ResolveActive err=%v want %v", err, ErrCredentialNotFound)
	}
}

func TestResolveActiveReturnsNotActiveWhenCredentialRowsExistButNoneServing(t *testing.T) {
	calls := 0
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, args ...interface{}) pgx.Row {
			calls++
			for _, required := range []string{
				"credential_row_count",
				"ac.deleted_at IS NULL",
				"pa.tenant_id = ac.tenant_id",
				"WHERE ac.provider_account_id = $1",
				"AND ac.tenant_id = $2",
			} {
				if !strings.Contains(sql, required) {
					t.Fatalf("ResolveActive SQL missing %q:\n%s", required, sql)
				}
			}
			if len(args) != 2 || args[0] != int64(42) || args[1] != int64(7) {
				t.Fatalf("ResolveActive args=%#v, want account=42 tenant=7", args)
			}
			return credentialStoreRowValuesStub{values: resolveInactiveRecordValues(1)}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.ResolveActive(context.Background(), 7, 42)
	if !errors.Is(err, ErrCredentialNotActive) {
		t.Fatalf("ResolveActive err=%v, want %v", err, ErrCredentialNotActive)
	}
	if calls != 1 {
		t.Fatalf("ResolveActive QueryRow calls=%d, want 1 atomic query", calls)
	}
}

func TestResolveActiveReturnsNotFoundWhenNoCredentialRowsExist(t *testing.T) {
	calls := 0
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, _ ...interface{}) pgx.Row {
			calls++
			if !strings.Contains(sql, "credential_row_count") {
				t.Fatalf("ResolveActive SQL missing credential_row_count:\n%s", sql)
			}
			return credentialStoreRowValuesStub{values: resolveInactiveRecordValues(0)}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.ResolveActive(context.Background(), 7, 42)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("ResolveActive err=%v, want %v", err, ErrCredentialNotFound)
	}
	if calls != 1 {
		t.Fatalf("ResolveActive QueryRow calls=%d, want 1 atomic query", calls)
	}
}

func TestLoadForRefreshQueryFiltersUnsafeProviderAccountHealth(t *testing.T) {
	// 加锁重读守护:LoadForRefresh 在 adapter 工作之前会被调用,在 refresh 事务内
	// 又会再次被调用。它的 SQL 必须拒绝 revoked 以及仍在 cooling 的 provider 账号,
	// 否则一行在扫描后被 revoked 的记录仍可能抵达上游 refresh adapter。变异检查:
	// 从 LoadForRefresh 删除 provider-account health 谓词,即使本地跳过真实 PG 测试,
	// 这个 SQL 形状守护也会变红。
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, _ ...interface{}) pgx.Row {
			for _, required := range []string{
				"pa.enabled",
				"pa.health_state = 'healthy'",
				"pa.health_state IN ('throttled', 'cooldown')",
				"pa.health_state_until <= NOW()",
				"pa.health_state <> 'revoked'",
			} {
				if !strings.Contains(sql, required) {
					t.Fatalf("LoadForRefresh SQL missing %q:\n%s", required, sql)
				}
			}
			return credentialStoreRowStub{err: pgx.ErrNoRows}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.LoadForRefresh(context.Background(), 77)
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("LoadForRefresh err=%v want %v", err, ErrCredentialNotFound)
	}
}

func TestCreateRejectsProviderAccountTenantMismatchBeforeInsert(t *testing.T) {
	insertSeen := false
	db := &credentialStoreDBStub{
		queryRow: func(_ context.Context, sql string, _ ...interface{}) pgx.Row {
			if strings.Contains(sql, "INSERT INTO account_credentials") {
				insertSeen = true
			}
			return credentialStoreRowStub{err: pgx.ErrNoRows}
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.Create(context.Background(), CreateCredentialInput{
		TenantID:          7,
		ProviderAccountID: 99,
		Vendor:            VendorOpenAI,
		AuthMode:          AuthModeAPIKey,
		Payload:           []byte(`{"api_key":"sk-test"}`),
	})
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("Create err=%v want %v", err, ErrCredentialNotFound)
	}
	if insertSeen {
		t.Fatal("Create attempted account_credentials insert after provider account tenant mismatch")
	}
}

func TestListRenewStatusPlaintextFreeTenantCursorQuery(t *testing.T) {
	tenantID := int64(7)
	cursorUpdatedAt := time.Date(2026, 5, 20, 10, 0, 0, 0, time.UTC)
	rowUpdatedAt := time.Date(2026, 5, 20, 9, 30, 0, 0, time.UTC)
	db := &credentialStoreDBStub{
		query: func(_ context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
			for _, forbidden := range []string{
				"encrypted_payload",
				"nonce",
				"aad_hash",
				"key_id",
				"refresh_token_fingerprint",
				"payload_fingerprint",
				"provider_accounts.credentials",
				"pa.credentials",
			} {
				if strings.Contains(sql, forbidden) {
					t.Fatalf("ListRenewStatus SQL selected plaintext or fingerprint column %q:\n%s", forbidden, sql)
				}
			}
			for _, required := range []string{
				"INNER JOIN provider_accounts pa",
				"INNER JOIN tenants t",
				"WHERE ac.deleted_at IS NULL",
				"($1::bigint IS NULL OR ac.tenant_id = $1::bigint)",
				"($2::timestamptz IS NULL OR (ac.updated_at, ac.id) < ($2::timestamptz, $3::bigint))",
				"ORDER BY ac.updated_at DESC, ac.id DESC",
			} {
				if !strings.Contains(sql, required) {
					t.Fatalf("ListRenewStatus SQL missing %q:\n%s", required, sql)
				}
			}
			if len(args) != 4 {
				t.Fatalf("ListRenewStatus args len=%d want 4", len(args))
			}
			gotTenant, ok := args[0].(*int64)
			if !ok || gotTenant == nil || *gotTenant != tenantID {
				t.Fatalf("tenant arg=%#v want pointer to %d", args[0], tenantID)
			}
			if got, ok := args[1].(time.Time); !ok || !got.Equal(cursorUpdatedAt) {
				t.Fatalf("cursor updated_at arg=%#v want %s", args[1], cursorUpdatedAt.Format(time.RFC3339))
			}
			if got, ok := args[2].(int64); !ok || got != 201 {
				t.Fatalf("cursor id arg=%#v want 201", args[2])
			}
			if got, ok := args[3].(int32); !ok || got != 2 {
				t.Fatalf("limit arg=%#v want 2", args[3])
			}
			return &credentialStoreRowsStub{rows: []renewStatusRow{{
				CredentialID: 301, TenantID: tenantID, TenantName: "tenant-a",
				AccountID: 77, AccountName: "acct-a", Vendor: VendorOpenAI, AuthMode: AuthModeAPIKey,
				State: StateActive, CredentialVersion: 3, FailureCount: 0,
				UpdatedAt: pgtype.Timestamptz{Time: rowUpdatedAt, Valid: true},
			}}}, nil
		},
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	rows, err := store.ListRenewStatus(context.Background(), ListRenewStatusParams{
		TenantID: &tenantID, CursorUpdatedAt: cursorUpdatedAt, CursorID: 201, Limit: 2,
	})
	if err != nil {
		t.Fatalf("ListRenewStatus err=%v", err)
	}
	if len(rows) != 1 || rows[0].TenantID != tenantID || rows[0].TenantName != "tenant-a" ||
		rows[0].AccountID != 77 || rows[0].CredentialID != 301 || !rows[0].UpdatedAt.Equal(rowUpdatedAt) {
		t.Fatalf("ListRenewStatus rows mismatch: %+v", rows)
	}
}

func mustTestKeyProvider(t *testing.T) KeyProvider {
	t.Helper()
	provider, err := NewStaticKeyProvider("test-key", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key provider: %v", err)
	}
	return provider
}

type credentialStoreDBStub struct {
	queryRow func(context.Context, string, ...interface{}) pgx.Row
	query    func(context.Context, string, ...interface{}) (pgx.Rows, error)
}

func (s *credentialStoreDBStub) Exec(context.Context, string, ...interface{}) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected Exec")
}

func (s *credentialStoreDBStub) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	if s.query == nil {
		return nil, errors.New("unexpected Query")
	}
	return s.query(ctx, sql, args...)
}

func (s *credentialStoreDBStub) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	if s.queryRow == nil {
		return credentialStoreRowStub{err: pgx.ErrNoRows}
	}
	return s.queryRow(ctx, sql, args...)
}

type credentialStoreRowStub struct {
	err error
}

func (r credentialStoreRowStub) Scan(...interface{}) error {
	if r.err != nil {
		return r.err
	}
	return nil
}

type credentialStoreRowValuesStub struct {
	values []any
}

func (r credentialStoreRowValuesStub) Scan(dest ...interface{}) error {
	if len(dest) != len(r.values) {
		return errors.New("scan destination count mismatch")
	}
	for i := range dest {
		if err := setScanValue(dest[i], r.values[i]); err != nil {
			return err
		}
	}
	return nil
}

func resolveActiveRecordValues(activeModeCount int64) []any {
	values := credentialRecordBaseValues()
	// external_account_id 列(迁移 0141)在 resolveActiveQuery 末段位于 deleted_at
	// 之后、active_mode_count 之前;mock 行须对齐此顺序,否则 Scan 计数不符。
	values = append(values, (*string)(nil))
	return append(values, activeModeCount, int64(1), false)
}

func credentialRecordBaseValues() []any {
	return []any{
		int64(301), int64(7), int64(42), VendorGemini, AuthModeAIStudioAPIKey, StateActive,
		int32(1), []byte("ciphertext"), "aes-256-gcm", "test-key",
		[]byte("nonce"), "aad-hash", (*string)(nil), (*string)(nil),
		pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{},
		pgtype.Timestamptz{}, (*string)(nil), (*string)(nil), int32(0),
		pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{},
	}
}

func resolveInactiveRecordValues(credentialRowCount int64) []any {
	return []any{
		int64(0), int64(7), int64(42), "", "", "",
		int32(0), []byte{}, "", "",
		[]byte{}, "", (*string)(nil), (*string)(nil),
		pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{},
		pgtype.Timestamptz{}, (*string)(nil), (*string)(nil), int32(0),
		pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{}, pgtype.Timestamptz{},
		(*string)(nil), // external_account_id 占位(no_serving_credential 分支返 NULL)
		int64(0), credentialRowCount, true,
	}
}

func providerAccountTestRecordValues(testModeCount int64) []any {
	return append(credentialRecordBaseValues(), testModeCount)
}

func setScanValue(dest any, value any) error {
	target := reflect.ValueOf(dest)
	if target.Kind() != reflect.Ptr || target.IsNil() {
		return errors.New("scan destination is not a non-nil pointer")
	}
	elem := target.Elem()
	if value == nil {
		elem.Set(reflect.Zero(elem.Type()))
		return nil
	}
	source := reflect.ValueOf(value)
	if source.Type().AssignableTo(elem.Type()) {
		elem.Set(source)
		return nil
	}
	if source.Type().ConvertibleTo(elem.Type()) {
		elem.Set(source.Convert(elem.Type()))
		return nil
	}
	return errors.New("scan destination type mismatch")
}

type credentialStoreRowsStub struct {
	rows []renewStatusRow
	idx  int
	err  error
}

func (r *credentialStoreRowsStub) Close() {}

func (r *credentialStoreRowsStub) Err() error {
	return r.err
}

func (r *credentialStoreRowsStub) CommandTag() pgconn.CommandTag {
	return pgconn.CommandTag{}
}

func (r *credentialStoreRowsStub) FieldDescriptions() []pgconn.FieldDescription {
	return nil
}

func (r *credentialStoreRowsStub) Next() bool {
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return true
}

func (r *credentialStoreRowsStub) Scan(dest ...any) error {
	if r.idx == 0 || r.idx > len(r.rows) {
		return errors.New("Scan without current row")
	}
	row := r.rows[r.idx-1]
	values := []any{
		row.CredentialID, row.TenantID, row.TenantName, row.AccountID, row.AccountName,
		row.Vendor, row.AuthMode, row.State, row.CredentialVersion,
		row.AccessExpiresAt, row.RefreshBeforeAt, row.LastRefreshAt,
		row.LastRefreshOutcome, row.FailureClass, row.FailureCount, row.UpdatedAt,
	}
	if len(dest) != len(values) {
		return errors.New("scan destination count mismatch")
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(values[i]))
	}
	return nil
}

func (r *credentialStoreRowsStub) Values() ([]any, error) {
	return nil, errors.New("unexpected Values")
}

func (r *credentialStoreRowsStub) RawValues() [][]byte {
	return nil
}

func (r *credentialStoreRowsStub) Conn() *pgx.Conn {
	return nil
}
