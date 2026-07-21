package balanceledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

var (
	ErrStoreNotConfigured         = errors.New("balance ledger: store not configured")
	ErrInvalidInput               = errors.New("balance ledger: invalid input")
	ErrUnsupportedCurrency        = errors.New("balance ledger: unsupported currency")
	ErrExternalTradeConflict      = errors.New("balance ledger: idempotency key conflicts with another request")
	ErrBalanceAdjustmentForbidden = errors.New("balance ledger: adjustment forbidden")
	ErrBalanceInsufficient        = errors.New("balance ledger: available balance is insufficient")
	ErrTenantNotFound             = errors.New("balance ledger: tenant not found")
	ErrUserNotFound               = errors.New("balance ledger: user not found")
	ErrAccountInactive            = errors.New("balance ledger: tenant or user is inactive")
)

const (
	BalanceActorPlatformAdmin  = "platform_admin"
	BalanceActorTenantOperator = "tenant_operator"

	BalanceTargetTenant = "tenant"
	BalanceTargetUser   = "user"

	balanceOperationPlatformTenantCredit = "platform_tenant_credit"
	balanceOperationPlatformTenantDebit  = "platform_tenant_debit"
	balanceOperationPlatformUserCredit   = "platform_user_credit"
	balanceOperationPlatformUserDebit    = "platform_user_debit"
	balanceOperationTenantUserCredit     = "tenant_user_credit"
	balanceOperationTenantUserDebit      = "tenant_user_debit"
)

var maxAdminBalanceAdjustment = decimal.NewFromInt(1_000_000_000)

const balanceAdjustmentTxRetryAttempts = 32

type AdminBalanceAdjustmentInput struct {
	TenantID           int64
	UserID             int64
	Amount             decimal.Decimal
	CurrencyCode       string
	ActorRole          string
	ActorScopeTenantID int64
	ActorRef           string
	Reason             string
	RequestID          string
	IdempotencyKey     string
	Now                time.Time
}

type AdminBalanceAdjustmentResult struct {
	TransactionID int64
	TenantID      int64
	UserID        int64
	TargetKind    string
	NewBalance    decimal.Decimal
	CurrencyCode  string
	Idempotent    bool
}

type AdminBalanceStore interface {
	ApplyAdminBalanceAdjustment(context.Context, AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, error)
}

// Service 提供三身份人工额度分发的唯一业务入口。
type Service struct {
	store AdminBalanceStore
}

// NewService 构造余额账本服务。
func NewService(store AdminBalanceStore) *Service {
	return &Service{store: store}
}

// PostgresStore 在 PostgreSQL 内原子更新余额投影与永久双边分录。
type PostgresStore struct {
	pool             *pgxpool.Pool
	platformTenantID int64
}

// NewPostgresStore 构造余额账本存储。
func NewPostgresStore(pool *pgxpool.Pool, platformTenantID int64) *PostgresStore {
	return &PostgresStore{pool: pool, platformTenantID: platformTenantID}
}

func (s *Service) AdminAdjustBalance(ctx context.Context, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, error) {
	if s == nil || s.store == nil {
		return AdminBalanceAdjustmentResult{}, ErrStoreNotConfigured
	}
	input = normalizeAdminBalanceAdjustmentInput(input)
	if err := validateAdminBalanceAdjustmentInput(input); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	return s.store.ApplyAdminBalanceAdjustment(ctx, input)
}

func nullableInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func isPgRetryableTxConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01")
}

func (s *PostgresStore) ApplyAdminBalanceAdjustment(ctx context.Context, input AdminBalanceAdjustmentInput) (AdminBalanceAdjustmentResult, error) {
	if s == nil || s.pool == nil || s.platformTenantID <= 0 {
		return AdminBalanceAdjustmentResult{}, ErrStoreNotConfigured
	}
	input = normalizeAdminBalanceAdjustmentInput(input)
	if err := validateAdminBalanceAdjustmentInput(input); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	operation, targetKind, err := classifyAdminBalanceAdjustment(input, s.platformTenantID)
	if err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	fingerprint := adminBalanceAdjustmentFingerprint(input, operation)
	var lastErr error
	for attempt := 0; attempt < balanceAdjustmentTxRetryAttempts; attempt++ {
		result, err := s.applyAdminBalanceAdjustmentOnce(ctx, input, operation, targetKind, fingerprint)
		if err == nil {
			return result, nil
		}
		if isPgRetryableTxConflict(err) || isUniqueViolation(err) {
			lastErr = err
			if err := waitBalanceAdjustmentRetry(ctx, attempt); err != nil {
				return AdminBalanceAdjustmentResult{}, err
			}
			continue
		}
		return AdminBalanceAdjustmentResult{}, err
	}
	return AdminBalanceAdjustmentResult{}, fmt.Errorf("balance ledger: adjustment exhausted retries: %w", lastErr)
}

func waitBalanceAdjustmentRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt+1) * 2 * time.Millisecond
	if delay > 50*time.Millisecond {
		delay = 50 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *PostgresStore) applyAdminBalanceAdjustmentOnce(
	ctx context.Context,
	input AdminBalanceAdjustmentInput,
	operation string,
	targetKind string,
	fingerprint string,
) (AdminBalanceAdjustmentResult, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return AdminBalanceAdjustmentResult{}, fmt.Errorf("balance ledger: begin adjustment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("balance-adjustment:%d:%s:%s", input.TenantID, input.ActorRef, input.IdempotencyKey)); err != nil {
		return AdminBalanceAdjustmentResult{}, fmt.Errorf("balance ledger: lock idempotency key: %w", err)
	}
	if existing, ok, err := readBalanceAdjustmentReplay(ctx, tx, input, fingerprint); ok || err != nil {
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return AdminBalanceAdjustmentResult{}, fmt.Errorf("balance ledger: commit replay: %w", err)
		}
		return existing, nil
	}

	if err := lockActiveTenant(ctx, tx, input.TenantID); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	absAmount := input.Amount.Abs()
	var source, target balanceLedgerAccount
	var resultBalance decimal.Decimal
	switch operation {
	case balanceOperationPlatformTenantCredit, balanceOperationPlatformTenantDebit:
		before, err := lockTenantWallet(ctx, tx, input.TenantID, input.Now)
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		delta := absAmount
		if operation == balanceOperationPlatformTenantDebit {
			delta = delta.Neg()
		}
		after, err := updateTenantWallet(ctx, tx, input.TenantID, before, delta, input.Now)
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		platform := platformLedgerAccount(s.platformTenantID, delta.Neg())
		tenant := tenantLedgerAccount(input.TenantID, delta, before, after)
		if delta.IsPositive() {
			source, target = platform, tenant
		} else {
			source, target = tenant, platform
		}
		resultBalance = after
	case balanceOperationPlatformUserCredit, balanceOperationPlatformUserDebit:
		before, held, err := lockUserBalance(ctx, tx, input.TenantID, input.UserID, input.Now)
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		delta := absAmount
		if operation == balanceOperationPlatformUserDebit {
			delta = delta.Neg()
		}
		after, err := updateUserBalance(ctx, tx, input.TenantID, input.UserID, before, held, delta, input.Now)
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		platform := platformLedgerAccount(s.platformTenantID, delta.Neg())
		user := userLedgerAccount(input.TenantID, input.UserID, delta, before, after)
		if delta.IsPositive() {
			source, target = platform, user
		} else {
			source, target = user, platform
		}
		resultBalance = after
	case balanceOperationTenantUserCredit, balanceOperationTenantUserDebit:
		tenantBefore, err := lockTenantWallet(ctx, tx, input.TenantID, input.Now)
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		userBefore, held, err := lockUserBalance(ctx, tx, input.TenantID, input.UserID, input.Now)
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		userDelta := absAmount
		if operation == balanceOperationTenantUserDebit {
			userDelta = userDelta.Neg()
		}
		tenantAfter, err := updateTenantWallet(ctx, tx, input.TenantID, tenantBefore, userDelta.Neg(), input.Now)
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		userAfter, err := updateUserBalance(ctx, tx, input.TenantID, input.UserID, userBefore, held, userDelta, input.Now)
		if err != nil {
			return AdminBalanceAdjustmentResult{}, err
		}
		tenant := tenantLedgerAccount(input.TenantID, userDelta.Neg(), tenantBefore, tenantAfter)
		user := userLedgerAccount(input.TenantID, input.UserID, userDelta, userBefore, userAfter)
		if userDelta.IsPositive() {
			source, target = tenant, user
		} else {
			source, target = user, tenant
		}
		resultBalance = userAfter
	default:
		return AdminBalanceAdjustmentResult{}, ErrInvalidInput
	}

	transactionID, err := insertBalanceLedgerTransaction(ctx, tx, s.platformTenantID, input, operation, fingerprint)
	if err != nil {
		if isUniqueViolation(err) {
			return AdminBalanceAdjustmentResult{}, ErrExternalTradeConflict
		}
		return AdminBalanceAdjustmentResult{}, err
	}
	if err := insertBalanceLedgerEntry(ctx, tx, transactionID, input.TenantID, source, input.Now); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	if err := insertBalanceLedgerEntry(ctx, tx, transactionID, input.TenantID, target, input.Now); err != nil {
		return AdminBalanceAdjustmentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AdminBalanceAdjustmentResult{}, fmt.Errorf("balance ledger: commit adjustment: %w", err)
	}
	return AdminBalanceAdjustmentResult{
		TransactionID: transactionID,
		TenantID:      input.TenantID,
		UserID:        input.UserID,
		TargetKind:    targetKind,
		NewBalance:    resultBalance,
		CurrencyCode:  input.CurrencyCode,
	}, nil
}

type balanceLedgerAccount struct {
	kind     string
	tenantID int64
	userID   int64
	delta    decimal.Decimal
	before   *decimal.Decimal
	after    decimal.Decimal
}

func platformLedgerAccount(tenantID int64, delta decimal.Decimal) balanceLedgerAccount {
	return balanceLedgerAccount{kind: "platform", tenantID: tenantID, delta: delta}
}

func tenantLedgerAccount(tenantID int64, delta, before, after decimal.Decimal) balanceLedgerAccount {
	b := before
	return balanceLedgerAccount{kind: "tenant", tenantID: tenantID, delta: delta, before: &b, after: after}
}

func userLedgerAccount(tenantID, userID int64, delta, before, after decimal.Decimal) balanceLedgerAccount {
	b := before
	return balanceLedgerAccount{kind: "user", tenantID: tenantID, userID: userID, delta: delta, before: &b, after: after}
}

func normalizeAdminBalanceAdjustmentInput(input AdminBalanceAdjustmentInput) AdminBalanceAdjustmentInput {
	input.CurrencyCode = strings.ToUpper(strings.TrimSpace(input.CurrencyCode))
	if input.CurrencyCode == "" {
		input.CurrencyCode = "USD"
	}
	input.ActorRole = strings.TrimSpace(input.ActorRole)
	input.ActorRef = strings.TrimSpace(input.ActorRef)
	input.Reason = strings.TrimSpace(input.Reason)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.Now.IsZero() {
		input.Now = time.Now().UTC()
	} else {
		input.Now = input.Now.UTC()
	}
	return input
}

func validateAdminBalanceAdjustmentInput(input AdminBalanceAdjustmentInput) error {
	if input.TenantID <= 0 || input.Amount.IsZero() || input.ActorRef == "" || input.Reason == "" || input.IdempotencyKey == "" {
		return ErrInvalidInput
	}
	if len(input.IdempotencyKey) > 128 || input.CurrencyCode != "USD" {
		if input.CurrencyCode != "USD" {
			return ErrUnsupportedCurrency
		}
		return ErrInvalidInput
	}
	abs := input.Amount.Abs()
	if !abs.Equal(abs.Truncate(8)) || abs.GreaterThan(maxAdminBalanceAdjustment) {
		return ErrInvalidInput
	}
	if input.ActorRole != BalanceActorPlatformAdmin && input.ActorRole != BalanceActorTenantOperator {
		return ErrBalanceAdjustmentForbidden
	}
	return nil
}

func classifyAdminBalanceAdjustment(input AdminBalanceAdjustmentInput, platformTenantID int64) (string, string, error) {
	credit := input.Amount.IsPositive()
	switch input.ActorRole {
	case BalanceActorPlatformAdmin:
		if input.ActorScopeTenantID != 0 {
			return "", "", ErrBalanceAdjustmentForbidden
		}
		if input.TenantID == platformTenantID {
			if input.UserID <= 0 {
				return "", "", ErrInvalidInput
			}
			if credit {
				return balanceOperationPlatformUserCredit, BalanceTargetUser, nil
			}
			return balanceOperationPlatformUserDebit, BalanceTargetUser, nil
		}
		if input.UserID != 0 {
			return "", "", ErrBalanceAdjustmentForbidden
		}
		if credit {
			return balanceOperationPlatformTenantCredit, BalanceTargetTenant, nil
		}
		return balanceOperationPlatformTenantDebit, BalanceTargetTenant, nil
	case BalanceActorTenantOperator:
		if input.ActorScopeTenantID <= 0 || input.ActorScopeTenantID != input.TenantID || input.TenantID == platformTenantID || input.UserID <= 0 {
			return "", "", ErrBalanceAdjustmentForbidden
		}
		if credit {
			return balanceOperationTenantUserCredit, BalanceTargetUser, nil
		}
		return balanceOperationTenantUserDebit, BalanceTargetUser, nil
	default:
		return "", "", ErrBalanceAdjustmentForbidden
	}
}

func adminBalanceAdjustmentFingerprint(input AdminBalanceAdjustmentInput, operation string) string {
	canonical := fmt.Sprintf("v1\n%s\n%d\n%d\n%s\n%s\n%d\n%s\n%s",
		operation, input.TenantID, input.UserID, input.Amount.StringFixed(8), input.CurrencyCode,
		input.ActorScopeTenantID, input.ActorRef, input.Reason)
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}

func readBalanceAdjustmentReplay(ctx context.Context, tx pgx.Tx, input AdminBalanceAdjustmentInput, fingerprint string) (AdminBalanceAdjustmentResult, bool, error) {
	var result AdminBalanceAdjustmentResult
	var operation, storedFingerprint string
	err := tx.QueryRow(ctx, `
SELECT tx_record.id, tx_record.operation, COALESCE(tx_record.target_user_id, 0),
       tx_record.currency_code, tx_record.request_fingerprint,
       entry.balance_after
FROM balance_ledger_transactions tx_record
JOIN balance_ledger_entries entry
  ON entry.tenant_id=tx_record.tenant_id AND entry.transaction_id=tx_record.id
WHERE tx_record.tenant_id=$1 AND tx_record.actor_ref=$2 AND tx_record.idempotency_key=$3
  AND entry.account_kind=CASE WHEN tx_record.target_user_id IS NULL THEN 'tenant' ELSE 'user' END`,
		input.TenantID, input.ActorRef, input.IdempotencyKey,
	).Scan(&result.TransactionID, &operation, &result.UserID, &result.CurrencyCode, &storedFingerprint, &result.NewBalance)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminBalanceAdjustmentResult{}, false, nil
	}
	if err != nil {
		return AdminBalanceAdjustmentResult{}, false, fmt.Errorf("balance ledger: read replay: %w", err)
	}
	if storedFingerprint != fingerprint {
		return AdminBalanceAdjustmentResult{}, true, ErrExternalTradeConflict
	}
	result.TenantID = input.TenantID
	result.TargetKind = BalanceTargetUser
	if operation == balanceOperationPlatformTenantCredit || operation == balanceOperationPlatformTenantDebit {
		result.TargetKind = BalanceTargetTenant
	}
	result.Idempotent = true
	return result, true, nil
}

func lockActiveTenant(ctx context.Context, tx pgx.Tx, tenantID int64) error {
	var status string
	var deleted bool
	err := tx.QueryRow(ctx, `SELECT status, deleted_at IS NOT NULL FROM tenants WHERE id=$1 FOR UPDATE`, tenantID).Scan(&status, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrTenantNotFound
	}
	if err != nil {
		return fmt.Errorf("balance ledger: lock tenant: %w", err)
	}
	if status != "active" || deleted {
		return ErrAccountInactive
	}
	return nil
}

func lockTenantWallet(ctx context.Context, tx pgx.Tx, tenantID int64, now time.Time) (decimal.Decimal, error) {
	if _, err := tx.Exec(ctx, `
INSERT INTO tenant_wallets (tenant_id, balance, version, updated_at)
VALUES ($1, 0, 1, $2)
ON CONFLICT (tenant_id) DO NOTHING`, tenantID, now); err != nil {
		return decimal.Decimal{}, fmt.Errorf("balance ledger: ensure tenant wallet: %w", err)
	}
	var balance decimal.Decimal
	if err := tx.QueryRow(ctx, `SELECT balance FROM tenant_wallets WHERE tenant_id=$1 FOR UPDATE`, tenantID).Scan(&balance); err != nil {
		return decimal.Decimal{}, fmt.Errorf("balance ledger: lock tenant wallet: %w", err)
	}
	return balance, nil
}

func lockUserBalance(ctx context.Context, tx pgx.Tx, tenantID, userID int64, now time.Time) (decimal.Decimal, decimal.Decimal, error) {
	var status, role string
	var deleted bool
	err := tx.QueryRow(ctx, `
SELECT status, role, deleted_at IS NOT NULL
FROM users
WHERE tenant_id=$1 AND id=$2
FOR UPDATE`, tenantID, userID).Scan(&status, &role, &deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Decimal{}, decimal.Decimal{}, ErrUserNotFound
	}
	if err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("balance ledger: lock user: %w", err)
	}
	if status != "active" || deleted {
		return decimal.Decimal{}, decimal.Decimal{}, ErrAccountInactive
	}
	if role != "user" {
		return decimal.Decimal{}, decimal.Decimal{}, ErrBalanceAdjustmentForbidden
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO user_balances (tenant_id, user_id, balance, held, version, updated_at)
VALUES ($1, $2, 0, 0, 1, $3)
ON CONFLICT (tenant_id, user_id) DO NOTHING`, tenantID, userID, now); err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("balance ledger: ensure user balance: %w", err)
	}
	var balance, held decimal.Decimal
	if err := tx.QueryRow(ctx, `
SELECT balance, held FROM user_balances WHERE tenant_id=$1 AND user_id=$2 FOR UPDATE`, tenantID, userID).Scan(&balance, &held); err != nil {
		return decimal.Decimal{}, decimal.Decimal{}, fmt.Errorf("balance ledger: lock user balance: %w", err)
	}
	return balance, held, nil
}

func updateTenantWallet(ctx context.Context, tx pgx.Tx, tenantID int64, before, delta decimal.Decimal, now time.Time) (decimal.Decimal, error) {
	after := before.Add(delta)
	if after.IsNegative() {
		return decimal.Decimal{}, ErrBalanceInsufficient
	}
	command, err := tx.Exec(ctx, `
UPDATE tenant_wallets
SET balance=$2, version=version+1, updated_at=$3
WHERE tenant_id=$1 AND balance=$4`, tenantID, after, now, before)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("balance ledger: update tenant wallet: %w", err)
	}
	if command.RowsAffected() != 1 {
		return decimal.Decimal{}, fmt.Errorf("balance ledger: tenant wallet changed during locked adjustment")
	}
	return after, nil
}

func updateUserBalance(ctx context.Context, tx pgx.Tx, tenantID, userID int64, before, held, delta decimal.Decimal, now time.Time) (decimal.Decimal, error) {
	after := before.Add(delta)
	if delta.IsNegative() && before.Sub(held).LessThan(delta.Abs()) {
		return decimal.Decimal{}, ErrBalanceInsufficient
	}
	if after.IsNegative() {
		return decimal.Decimal{}, ErrBalanceInsufficient
	}
	command, err := tx.Exec(ctx, `
UPDATE user_balances
SET balance=$3, version=version+1, updated_at=$4
WHERE tenant_id=$1 AND user_id=$2 AND balance=$5 AND held=$6`, tenantID, userID, after, now, before, held)
	if err != nil {
		return decimal.Decimal{}, fmt.Errorf("balance ledger: update user balance: %w", err)
	}
	if command.RowsAffected() != 1 {
		return decimal.Decimal{}, fmt.Errorf("balance ledger: user balance changed during locked adjustment")
	}
	return after, nil
}

func insertBalanceLedgerTransaction(ctx context.Context, tx pgx.Tx, platformTenantID int64, input AdminBalanceAdjustmentInput, operation, fingerprint string) (int64, error) {
	var transactionID int64
	err := tx.QueryRow(ctx, `
INSERT INTO balance_ledger_transactions (
    tenant_id, platform_tenant_id, operation, target_user_id, amount, currency_code,
    actor_role, actor_ref, actor_scope_tenant_id, idempotency_key,
    request_fingerprint, reason, request_id, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
RETURNING id`,
		input.TenantID, platformTenantID, operation, nullableInt64(input.UserID), input.Amount.Abs(), input.CurrencyCode,
		input.ActorRole, input.ActorRef, nullableInt64(input.ActorScopeTenantID), input.IdempotencyKey,
		fingerprint, input.Reason, nullableText(input.RequestID), input.Now,
	).Scan(&transactionID)
	if err != nil {
		return 0, fmt.Errorf("balance ledger: insert transaction: %w", err)
	}
	return transactionID, nil
}

func insertBalanceLedgerEntry(ctx context.Context, tx pgx.Tx, transactionID, tenantID int64, account balanceLedgerAccount, now time.Time) error {
	var before, after any
	if account.before != nil {
		before = *account.before
		after = account.after
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO balance_ledger_entries (
    tenant_id, transaction_id, account_kind, account_tenant_id, account_user_id,
    delta, balance_before, balance_after, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		tenantID, transactionID, account.kind, account.tenantID, nullableInt64(account.userID),
		account.delta, before, after, now,
	); err != nil {
		return fmt.Errorf("balance ledger: insert entry: %w", err)
	}
	return nil
}
