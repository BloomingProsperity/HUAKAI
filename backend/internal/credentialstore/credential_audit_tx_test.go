package credentialstore

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestCreateAuditFailureRollsBackCredentialWithTransaction(t *testing.T) {
	db := newCredentialAuditTxFakeDB()
	db.failAuditEvent = CredentialEventCreated
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.Create(context.Background(), CreateCredentialInput{
		TenantID:          db.tenantID,
		ProviderAccountID: db.providerAccountID,
		Vendor:            VendorOpenAI,
		AuthMode:          AuthModeAPIKey,
		Payload:           []byte(`{"api_key":"sk-create-rollback"}`),
		ActorID:           "owner",
	})
	if !errors.Is(err, ErrCredentialAuditWriteFailed) {
		t.Fatalf("Create err=%v want ErrCredentialAuditWriteFailed", err)
	}
	if db.credential != nil {
		t.Fatalf("Create audit failure persisted credential: %+v", db.credential)
	}
	if db.rollbackCount != 1 || db.commitCount != 0 {
		t.Fatalf("tx counts rollback=%d commit=%d want rollback=1 commit=0", db.rollbackCount, db.commitCount)
	}
}

func TestSubscriptionProjectionRollsBackWithCredentialAndAudit(t *testing.T) {
	db := newCredentialAuditTxFakeDB()
	db.failAuditEvent = CredentialEventCreated
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.Create(context.Background(), CreateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID,
		Vendor: VendorAntigravity, AuthMode: AuthModeOAuth,
		Payload: []byte(`{"access_token":"access","refresh_token":"refresh","project_id":"project","subscription_tier_raw":"g1-pro-tier"}`),
		ActorID: "owner",
	})
	if !errors.Is(err, ErrCredentialAuditWriteFailed) {
		t.Fatalf("Create err=%v，期望 ErrCredentialAuditWriteFailed", err)
	}
	if db.credential != nil || db.subscription != nil {
		t.Fatalf("日志失败后凭据和套餐投影必须一起回滚：credential=%+v subscription=%+v", db.credential, db.subscription)
	}
	if db.rollbackCount != 1 || db.commitCount != 0 {
		t.Fatalf("事务计数 rollback=%d commit=%d，期望 rollback=1 commit=0", db.rollbackCount, db.commitCount)
	}
}

func TestRotateAuditFailureRollsBackVersionWithTransaction(t *testing.T) {
	db := newCredentialAuditTxFakeDB()
	db.credential = &credentialAuditTxFakeCredential{
		id: db.credentialID, tenantID: db.tenantID, providerAccountID: db.providerAccountID,
		vendor: VendorOpenAI, authMode: AuthModeAPIKey, state: StateActive, version: 1,
		payloadFingerprint: "payload-before", refreshFingerprint: "refresh-before",
	}
	db.failAuditEvent = CredentialEventRotated
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	_, err := store.Rotate(context.Background(), RotateCredentialInput{
		TenantID:          db.tenantID,
		ProviderAccountID: db.providerAccountID,
		CredentialID:      db.credentialID,
		Payload:           []byte(`{"api_key":"sk-rotate-rollback"}`),
		ActorID:           "owner",
	})
	if !errors.Is(err, ErrCredentialAuditWriteFailed) {
		t.Fatalf("Rotate err=%v want ErrCredentialAuditWriteFailed", err)
	}
	if db.credential.version != 1 || db.credential.payloadFingerprint != "payload-before" || db.credential.refreshFingerprint != "refresh-before" {
		t.Fatalf("Rotate audit failure mutated credential: %+v", db.credential)
	}
	if db.rollbackCount != 1 || db.commitCount != 0 {
		t.Fatalf("tx counts rollback=%d commit=%d want rollback=1 commit=0", db.rollbackCount, db.commitCount)
	}
}

func TestRotateExpectedVersionRejectsBeforeMutation(t *testing.T) {
	db := newCredentialAuditTxFakeDB()
	db.credential = &credentialAuditTxFakeCredential{
		id: db.credentialID, tenantID: db.tenantID, providerAccountID: db.providerAccountID,
		vendor: VendorOpenAI, authMode: AuthModeAPIKey, state: StateActive, version: 2,
	}
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())
	expected := int32(1)
	_, err := store.Rotate(context.Background(), RotateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID,
		CredentialID: db.credentialID, ExpectedVersion: &expected,
		Payload: []byte(`{"api_key":"sk-must-not-write"}`),
	})
	if !errors.Is(err, ErrCredentialVersionConflict) {
		t.Fatalf("Rotate err=%v，期望 ErrCredentialVersionConflict", err)
	}
	if db.beginCount != 0 || db.credential.version != 2 {
		t.Fatalf("版本冲突仍开启事务或修改记录：begin=%d credential=%+v", db.beginCount, db.credential)
	}
}

func TestProjectRefPersistsAcrossCredentialWritePaths(t *testing.T) {
	db := newCredentialAuditTxFakeDB()
	store := NewStore(db, mustTestKeyProvider(t), DefaultHandlerRegistry())

	created, err := store.Create(context.Background(), CreateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID,
		Vendor: VendorAntigravity, AuthMode: AuthModeOAuth,
		Payload: []byte(`{"access_token":"access-create","refresh_token":"refresh-create","project_id":"project-create"}`),
	})
	if err != nil {
		t.Fatalf("Create 失败：%v", err)
	}
	if created.ProjectRef == nil || *created.ProjectRef != "project-create" {
		t.Fatalf("Create project_ref=%v，期望 project-create", created.ProjectRef)
	}

	rotated, err := store.Rotate(context.Background(), RotateCredentialInput{
		TenantID: db.tenantID, ProviderAccountID: db.providerAccountID, CredentialID: created.ID,
		Payload: []byte(`{"access_token":"access-rotate","refresh_token":"refresh-rotate","project_id":"project-rotate"}`),
	})
	if err != nil {
		t.Fatalf("Rotate 失败：%v", err)
	}
	if rotated.ProjectRef == nil || *rotated.ProjectRef != "project-rotate" {
		t.Fatalf("Rotate project_ref=%v，期望 project-rotate", rotated.ProjectRef)
	}

	err = store.SaveRefreshSuccess(context.Background(), CredentialRecord{
		ID: created.ID, TenantID: db.tenantID, ProviderAccountID: db.providerAccountID,
		Vendor: VendorAntigravity, AuthMode: AuthModeOAuth, CredentialVersion: rotated.Version,
	}, []byte(`{"access_token":"access-refresh","refresh_token":"refresh-refresh","project_id":"project-refresh"}`), time.Time{}, "refresh_succeeded")
	if err != nil {
		t.Fatalf("SaveRefreshSuccess 失败：%v", err)
	}
	if db.refreshProjectRef == nil || *db.refreshProjectRef != "project-refresh" {
		t.Fatalf("SaveRefreshSuccess project_ref=%v，期望 project-refresh", db.refreshProjectRef)
	}
	wantMaterial := CredentialMaterialFingerprint(
		db.tenantID, VendorAntigravity, AuthModeOAuth,
		[]byte(`{"access_token":"access-refresh","refresh_token":"refresh-refresh","project_id":"project-refresh"}`),
	)
	if db.credential.materialFingerprint != wantMaterial {
		t.Fatalf("SaveRefreshSuccess material fingerprint=%q，期望当前有效材料指纹 %q",
			db.credential.materialFingerprint, wantMaterial)
	}
	resolved, err := store.ResolveActive(context.Background(), db.tenantID, db.providerAccountID)
	if err != nil {
		t.Fatalf("ResolveActive 失败：%v", err)
	}
	defer privacy.Zeroize(resolved.PlaintextPayload)
	handler, err := store.HandlerRegistry().MustLookup(resolved.Vendor, resolved.AuthMode)
	if err != nil {
		t.Fatalf("查找物化 handler 失败：%v", err)
	}
	material, err := handler.RuntimeMaterial(resolved.PlaintextPayload)
	if err != nil {
		t.Fatalf("物化 ResolveActive 凭证失败：%v", err)
	}
	if material.Extra["project_id"] != "project-refresh" {
		t.Fatalf("ResolveActive project_id=%q，期望 project-refresh", material.Extra["project_id"])
	}
}

type credentialAuditTxFakeDB struct {
	tenantID          int64
	providerAccountID int64
	credentialID      int64
	nextCredentialID  int64
	failAuditEvent    string
	credential        *credentialAuditTxFakeCredential
	beginCount        int
	commitCount       int
	rollbackCount     int
	refreshProjectRef *string
	nextObservationID int64
	subscription      *credentialAuditTxFakeSubscription
}

type credentialAuditTxFakeSubscription struct {
	observationID int64
	vendor        string
	plan          string
	rawPlan       string
	scope         string
	subjectRef    string
	workspaceRef  string
	source        string
	trust         string
	verification  string
	status        string
	mapping       int
	errorClass    string
	firstObserved time.Time
	observedAt    time.Time
	changedAt     time.Time
}

type credentialAuditTxFakeCredential struct {
	id                     int64
	tenantID               int64
	providerAccountID      int64
	vendor                 string
	authMode               string
	state                  string
	version                int32
	payloadFingerprint     string
	refreshFingerprint     string
	materialFingerprint    string
	externalAccountID      *string
	externalSubjectID      *string
	externalAccountEmail   *string
	externalIdentitySource *string
	projectRef             *string
	encryptedPayload       []byte
	encryptionScheme       string
	keyID                  string
	nonce                  []byte
	aadHash                string
}

func newCredentialAuditTxFakeDB() *credentialAuditTxFakeDB {
	return &credentialAuditTxFakeDB{
		tenantID: 7, providerAccountID: 77, credentialID: 301, nextCredentialID: 301, nextObservationID: 1,
	}
}

func (db *credentialAuditTxFakeDB) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	db.beginCount++
	tx := &credentialAuditTxFakeTx{parent: db}
	if db.credential != nil {
		cp := *db.credential
		cp.encryptedPayload = append([]byte(nil), db.credential.encryptedPayload...)
		cp.nonce = append([]byte(nil), db.credential.nonce...)
		tx.staged = &cp
	}
	if db.subscription != nil {
		cp := *db.subscription
		tx.stagedSubscription = &cp
	}
	return tx, nil
}

func (db *credentialAuditTxFakeDB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return db.exec(ctx, sql, args...)
}

func (db *credentialAuditTxFakeDB) Query(context.Context, string, ...interface{}) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (db *credentialAuditTxFakeDB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return db.queryRow(ctx, sql, args...)
}

func (db *credentialAuditTxFakeDB) exec(_ context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "UPDATE account_credentials") && strings.Contains(sql, "last_refresh_outcome") && len(args) > 10 {
		db.refreshProjectRef = credentialAuditOptionalStringArg(args[11])
	}
	if strings.Contains(sql, "INSERT INTO credential_audit_events") && len(args) >= 4 && args[3] == db.failAuditEvent {
		return pgconn.CommandTag{}, errors.New("forced audit insert failure")
	}
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (db *credentialAuditTxFakeDB) queryRow(_ context.Context, sql string, args ...interface{}) pgx.Row {
	switch {
	case strings.Contains(sql, "FROM provider_accounts"):
		return credentialAuditTxScanRow{values: []any{db.providerAccountID}}
	case strings.Contains(sql, "WITH scoped_credentials AS"):
		if db.credential == nil {
			return credentialAuditTxScanRow{err: pgx.ErrNoRows}
		}
		values := append(db.credential.recordValues(), (*string)(nil), int64(1), int64(1), false)
		return credentialAuditTxScanRow{values: values}
	case strings.Contains(sql, "FROM account_credentials ac"):
		if db.credential == nil {
			return credentialAuditTxScanRow{err: pgx.ErrNoRows}
		}
		return credentialAuditTxScanRow{values: db.credential.recordValues()}
	case strings.Contains(sql, "INSERT INTO account_credentials"):
		id := db.nextCredentialID
		db.nextCredentialID++
		db.credential = credentialAuditCredentialFromCreateArgs(id, args)
		return credentialAuditTxScanRow{values: db.credential.createMetadataValues()}
	case strings.Contains(sql, "UPDATE account_credentials") && strings.Contains(sql, "credential_version = credential_version + 1"):
		if db.credential == nil {
			return credentialAuditTxScanRow{err: pgx.ErrNoRows}
		}
		db.credential.applyRotateArgs(args)
		return credentialAuditTxScanRow{values: db.credential.metadataValues()}
	default:
		return credentialAuditTxScanRow{err: errors.New("unexpected QueryRow")}
	}
}

type credentialAuditTxFakeTx struct {
	parent             *credentialAuditTxFakeDB
	staged             *credentialAuditTxFakeCredential
	stagedSubscription *credentialAuditTxFakeSubscription
}

func (tx *credentialAuditTxFakeTx) Begin(context.Context) (pgx.Tx, error) {
	return nil, errors.New("unexpected nested transaction")
}

func (tx *credentialAuditTxFakeTx) Commit(context.Context) error {
	tx.parent.commitCount++
	if tx.staged == nil {
		tx.parent.credential = nil
	} else {
		cp := *tx.staged
		cp.encryptedPayload = append([]byte(nil), tx.staged.encryptedPayload...)
		cp.nonce = append([]byte(nil), tx.staged.nonce...)
		tx.parent.credential = &cp
	}
	if tx.stagedSubscription == nil {
		tx.parent.subscription = nil
	} else {
		cp := *tx.stagedSubscription
		tx.parent.subscription = &cp
	}
	return nil
}

func (tx *credentialAuditTxFakeTx) Rollback(context.Context) error {
	tx.parent.rollbackCount++
	return nil
}

func (tx *credentialAuditTxFakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, errors.New("unexpected CopyFrom")
}

func (tx *credentialAuditTxFakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults {
	return nil
}

func (tx *credentialAuditTxFakeTx) LargeObjects() pgx.LargeObjects {
	return pgx.LargeObjects{}
}

func (tx *credentialAuditTxFakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, errors.New("unexpected Prepare")
}

func (tx *credentialAuditTxFakeTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "UPDATE account_credentials") && strings.Contains(sql, "last_refresh_outcome") {
		if tx.staged == nil {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		tx.staged.applyRefreshArgs(args)
		tx.parent.refreshProjectRef = credentialAuditOptionalStringArg(args[11])
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	if strings.Contains(sql, "INSERT INTO provider_account_subscription_states") {
		if tx.stagedSubscription != nil {
			return pgconn.NewCommandTag("INSERT 0 0"), nil
		}
		tx.stagedSubscription = credentialAuditSubscriptionFromArgs(args)
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	if strings.Contains(sql, "UPDATE provider_account_subscription_states") {
		if tx.stagedSubscription == nil {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		tx.stagedSubscription = credentialAuditSubscriptionFromUpdateArgs(args)
		return pgconn.NewCommandTag("UPDATE 1"), nil
	}
	return tx.parent.exec(ctx, sql, args...)
}

func (tx *credentialAuditTxFakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected Query")
}

func (tx *credentialAuditTxFakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "INSERT INTO account_credentials"):
		id := tx.parent.nextCredentialID
		tx.parent.nextCredentialID++
		tx.staged = credentialAuditCredentialFromCreateArgs(id, args)
		return credentialAuditTxScanRow{values: tx.staged.createMetadataValues()}
	case strings.Contains(sql, "UPDATE account_credentials") && strings.Contains(sql, "credential_version = credential_version + 1"):
		if tx.staged == nil {
			return credentialAuditTxScanRow{err: pgx.ErrNoRows}
		}
		tx.staged.applyRotateArgs(args)
		return credentialAuditTxScanRow{values: tx.staged.metadataValues()}
	case strings.Contains(sql, "INSERT INTO provider_account_subscription_observations"):
		id := tx.parent.nextObservationID
		tx.parent.nextObservationID++
		return credentialAuditTxScanRow{values: []any{id, time.Date(2026, 7, 18, 8, int(id), 0, 0, time.UTC)}}
	case strings.Contains(sql, "FROM provider_account_subscription_states"):
		if tx.stagedSubscription == nil {
			return credentialAuditTxScanRow{err: pgx.ErrNoRows}
		}
		return credentialAuditTxScanRow{values: tx.stagedSubscription.values()}
	default:
		return credentialAuditTxScanRow{err: errors.New("unexpected tx QueryRow")}
	}
}

func (tx *credentialAuditTxFakeTx) Conn() *pgx.Conn {
	return nil
}

func credentialAuditSubscriptionFromArgs(args []any) *credentialAuditTxFakeSubscription {
	return &credentialAuditTxFakeSubscription{
		observationID: args[2].(int64),
		vendor:        credentialAuditStringArg(args[3]),
		plan:          credentialAuditStringArg(args[4]),
		rawPlan:       credentialAuditStringArg(args[5]),
		scope:         credentialAuditStringArg(args[6]),
		subjectRef:    credentialAuditStringArg(args[7]),
		workspaceRef:  credentialAuditStringArg(args[8]),
		source:        credentialAuditStringArg(args[9]),
		trust:         credentialAuditStringArg(args[10]),
		verification:  credentialAuditStringArg(args[11]),
		status:        credentialAuditStringArg(args[12]),
		mapping:       args[13].(int),
		errorClass:    credentialAuditStringArg(args[14]),
		firstObserved: args[15].(time.Time),
		observedAt:    args[15].(time.Time),
		changedAt:     args[15].(time.Time),
	}
}

func credentialAuditSubscriptionFromUpdateArgs(args []any) *credentialAuditTxFakeSubscription {
	return &credentialAuditTxFakeSubscription{
		observationID: args[2].(int64),
		vendor:        credentialAuditStringArg(args[3]),
		plan:          credentialAuditStringArg(args[4]),
		rawPlan:       credentialAuditStringArg(args[5]),
		scope:         credentialAuditStringArg(args[6]),
		subjectRef:    credentialAuditStringArg(args[7]),
		workspaceRef:  credentialAuditStringArg(args[8]),
		source:        credentialAuditStringArg(args[9]),
		trust:         credentialAuditStringArg(args[10]),
		verification:  credentialAuditStringArg(args[11]),
		status:        credentialAuditStringArg(args[12]),
		mapping:       args[13].(int),
		errorClass:    credentialAuditStringArg(args[14]),
		firstObserved: args[15].(time.Time),
		observedAt:    args[16].(time.Time),
		changedAt:     args[17].(time.Time),
	}
}

func (s credentialAuditTxFakeSubscription) values() []any {
	return []any{
		s.observationID, s.vendor, s.plan, s.rawPlan,
		s.scope, s.subjectRef, s.workspaceRef,
		s.source, s.trust, s.verification, s.status,
		s.mapping, s.errorClass,
		s.firstObserved, s.observedAt, s.changedAt,
	}
}

type credentialAuditTxScanRow struct {
	values []any
	err    error
}

func (r credentialAuditTxScanRow) Scan(dest ...interface{}) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return errors.New("scan destination count mismatch")
	}
	for i := range dest {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(r.values[i]))
	}
	return nil
}

func credentialAuditCredentialFromCreateArgs(id int64, args []any) *credentialAuditTxFakeCredential {
	return &credentialAuditTxFakeCredential{
		id: id, tenantID: args[0].(int64), providerAccountID: args[1].(int64),
		vendor: args[2].(string), authMode: args[3].(string), state: StateActive, version: 1,
		payloadFingerprint: credentialAuditStringArg(args[9]), refreshFingerprint: credentialAuditStringArg(args[10]),
		materialFingerprint:    credentialAuditStringArg(args[11]),
		externalAccountID:      credentialAuditOptionalStringArg(args[16]),
		externalSubjectID:      credentialAuditOptionalStringArg(args[17]),
		externalAccountEmail:   credentialAuditOptionalStringArg(args[18]),
		externalIdentitySource: credentialAuditOptionalStringArg(args[19]),
		projectRef:             credentialAuditOptionalStringArg(args[20]),
		encryptedPayload:       append([]byte(nil), args[4].([]byte)...),
		encryptionScheme:       args[5].(string),
		keyID:                  args[6].(string),
		nonce:                  append([]byte(nil), args[7].([]byte)...),
		aadHash:                args[8].(string),
	}
}

func (c *credentialAuditTxFakeCredential) applyRotateArgs(args []any) {
	c.state = StateActive
	c.version++
	c.payloadFingerprint = credentialAuditStringArg(args[5])
	c.refreshFingerprint = credentialAuditStringArg(args[6])
	c.materialFingerprint = credentialAuditStringArg(args[7])
	c.projectRef = credentialAuditOptionalStringArg(args[11])
	if value := credentialAuditOptionalStringArg(args[13]); value != nil {
		c.externalAccountID = value
	}
	if value := credentialAuditOptionalStringArg(args[14]); value != nil {
		c.externalSubjectID = value
	}
	if value := credentialAuditOptionalStringArg(args[15]); value != nil {
		c.externalAccountEmail = value
	}
	if value := credentialAuditOptionalStringArg(args[16]); value != nil {
		c.externalIdentitySource = value
	}
	c.encryptedPayload = append(c.encryptedPayload[:0], args[0].([]byte)...)
	c.encryptionScheme = args[1].(string)
	c.keyID = args[2].(string)
	c.nonce = append(c.nonce[:0], args[3].([]byte)...)
	c.aadHash = args[4].(string)
}

func (c *credentialAuditTxFakeCredential) applyRefreshArgs(args []any) {
	c.state = StateActive
	c.version++
	c.payloadFingerprint = credentialAuditStringArg(args[5])
	c.refreshFingerprint = credentialAuditStringArg(args[6])
	c.materialFingerprint = credentialAuditStringArg(args[7])
	c.projectRef = credentialAuditOptionalStringArg(args[11])
	c.encryptedPayload = append(c.encryptedPayload[:0], args[0].([]byte)...)
	c.encryptionScheme = args[1].(string)
	c.keyID = args[2].(string)
	c.nonce = append(c.nonce[:0], args[3].([]byte)...)
	c.aadHash = args[4].(string)
}

func (c credentialAuditTxFakeCredential) metadataValues() []any {
	now := pgtype.Timestamptz{Time: time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC), Valid: true}
	emptyTime := pgtype.Timestamptz{}
	var nilString *string
	return []any{
		c.id, c.tenantID, c.providerAccountID, c.vendor, c.authMode, c.state, c.version,
		emptyTime, emptyTime, emptyTime, nilString,
		nilString, int32(0), c.externalAccountID, c.externalSubjectID,
		c.externalAccountEmail, c.projectRef, now, now,
	}
}

// createMetadataValues 与 Create 的 RETURNING 列表保持一致。
func (c credentialAuditTxFakeCredential) createMetadataValues() []any {
	now := pgtype.Timestamptz{Time: time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC), Valid: true}
	emptyTime := pgtype.Timestamptz{}
	var nilString *string
	return []any{
		c.id, c.tenantID, c.providerAccountID, c.vendor, c.authMode, c.state, c.version,
		emptyTime, emptyTime, emptyTime, nilString,
		nilString, int32(0), c.externalAccountID, c.externalSubjectID,
		c.externalAccountEmail, c.projectRef, now, now,
	}
}

func (c credentialAuditTxFakeCredential) recordValues() []any {
	now := pgtype.Timestamptz{Time: time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC), Valid: true}
	emptyTime := pgtype.Timestamptz{}
	var nilString *string
	payloadFP := c.payloadFingerprint
	refreshFP := c.refreshFingerprint
	return []any{
		c.id, c.tenantID, c.providerAccountID, c.vendor, c.authMode, c.state,
		c.version, append([]byte(nil), c.encryptedPayload...), c.encryptionScheme, c.keyID,
		append([]byte(nil), c.nonce...), c.aadHash, &payloadFP, &refreshFP,
		emptyTime, emptyTime, emptyTime, emptyTime,
		emptyTime, nilString, nilString, int32(0),
		emptyTime, now, now, emptyTime,
	}
}

func credentialAuditStringArg(v any) string {
	switch typed := v.(type) {
	case *string:
		if typed == nil {
			return ""
		}
		return *typed
	case string:
		return typed
	default:
		return ""
	}
}

func credentialAuditOptionalStringArg(v any) *string {
	value := credentialAuditStringArg(v)
	if value == "" {
		return nil
	}
	return &value
}
