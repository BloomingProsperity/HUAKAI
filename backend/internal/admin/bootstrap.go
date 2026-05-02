// Bootstrap-admin token loader.
//
// Problem: N+4b2 ships a real /admin/v1/api-keys endpoint, but issuing
// the FIRST admin token requires authenticating as an admin first —
// chicken-and-egg. Solution: read HUAKAI_ADMIN_BOOTSTRAP_TOKEN at boot;
// if set AND admin_tokens is empty, INSERT a single platform_admin row
// with bootstrap=true. Operator uses this once to issue real admin
// tokens, then rotates / disables the bootstrap row.
//
// Security posture (CMB-5 + Owner concerns):
//   - The env var holds plaintext. Operators MUST treat it as a Secret
//     (k8s Secret, sealed-secret, vault) — not a ConfigMap.
//   - We intentionally accept ONLY when admin_tokens is empty. Setting
//     the env after a real admin exists is a no-op (logged, not crashed,
//     so accidental config drift doesn't break boot).
//   - The bootstrap row is marked bootstrap=true so admin tooling can
//     surface "rotate me" warnings in the dashboard later.

package admin

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
)

// BootstrapEnv is the environment variable read at boot. The value is
// the plaintext bearer for the first admin token.
const BootstrapEnv = "HUAKAI_ADMIN_BOOTSTRAP_TOKEN"

// MaybeBootstrap inserts a bootstrap admin token row when:
//  1. HUAKAI_ADMIN_BOOTSTRAP_TOKEN is set and non-empty
//  2. admin_tokens has no rows yet
//
// Returns nil for "no-op" (env unset or table non-empty); error only on
// datastore / bcrypt failures (which should fail boot).
//
// Codex N+4b2 pass-7 P2: count + insert run inside a TX with a constant
// advisory lock so two gateway instances starting against an empty DB
// can't both observe count=0 and double-insert. Lock is released
// automatically on commit/rollback.
func MaybeBootstrap(ctx context.Context, pool *pgxpool.Pool, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}

	bearer := os.Getenv(BootstrapEnv)
	if bearer == "" {
		return nil
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("%w: begin bootstrap tx: %v", ErrAdminBackend, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := db.New(tx)

	if err := qtx.AcquireAdminBootstrapLock(ctx); err != nil {
		return fmt.Errorf("%w: bootstrap lock: %v", ErrAdminBackend, err)
	}

	count, err := qtx.CountAdminTokensIncludingInactive(ctx)
	if err != nil {
		return fmt.Errorf("%w: count admin_tokens: %v", ErrAdminBackend, err)
	}
	if count > 0 {
		// Codex pass-5 P1 + pass-8 P1: count INCLUDES disabled/revoked
		// rows so bootstrap stays one-shot. Skip ALL further work — no
		// validation, no bcrypt, no slice — so a stale env value (which
		// could be too long for bcrypt or shorter than PrefixLen) cannot
		// crash a healthy boot.
		logger.Info("admin bootstrap skipped: admin_tokens has prior rows",
			zap.Int64("token_row_count", count))
		return nil
	}

	// Validate full generated bearer shape only after we know we'd
	// actually insert. hk_admin_ namespace + 24-char base32 suffix = 33
	// chars total. Earlier passes raised this before the count check
	// which broke the no-op contract for populated DBs.
	const adminNamespace = "hk_admin_"
	const expectedLen = len(adminNamespace) + 24
	if len(bearer) != expectedLen || bearer[:len(adminNamespace)] != adminNamespace {
		return fmt.Errorf("%w: %s must be a generated bearer of shape '%s<24-char-base32>' (%d chars)",
			ErrAdminBadRequest, BootstrapEnv, adminNamespace, expectedLen)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(bearer), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("%w: bcrypt bootstrap token: %v", ErrAdminBackend, err)
	}
	prefix := bearer[:PrefixLen]

	if _, err = qtx.InsertAdminToken(ctx, db.InsertAdminTokenParams{
		Name:      "bootstrap-admin",
		KeyHash:   string(hash),
		KeyPrefix: prefix,
		Role:      RolePlatformAdmin,
		Bootstrap: true,
	}); err != nil {
		return fmt.Errorf("%w: insert bootstrap admin: %v", ErrAdminBackend, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("%w: commit bootstrap tx: %v", ErrAdminBackend, err)
	}

	logger.Warn("admin bootstrap token loaded from env — rotate before public exposure",
		zap.String("env_var", BootstrapEnv),
		zap.String("key_prefix", prefix))
	return nil
}

var _ = errors.New // future expansion
