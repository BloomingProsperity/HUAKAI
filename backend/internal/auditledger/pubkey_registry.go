package auditledger

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const AuditSignerAlgorithmEd25519 = "ed25519"

var (
	ErrPubkeyNotFound           = errors.New("auditledger: signer pubkey not found")
	ErrInvalidPubkeyFingerprint = errors.New("auditledger: invalid signer pubkey fingerprint")
)

type Pubkey struct {
	Fingerprint   []byte
	Algorithm     string
	PublicKey     []byte
	EffectiveFrom time.Time
	EffectiveTo   *time.Time
	CreatedAt     time.Time
}

// SignatureOutsideKeyWindow 报告签名时刻 ts 是否落在 key 的有效窗口
// [EffectiveFrom, EffectiveTo] 之外。nil key 视为窗口外。导出供 receipt 验证路径
// (trusthttp / cost_receipt)与 audit-ledger 验证路径共用同一窗口策略,
// 避免 receipt 侧只验密码学有效性、忽略 key 轮换/未来生效窗口。
// 注:ts 为零值时调用方应自行决定是否豁免(无法确定签名时刻);本函数对零值
// ts 在 EffectiveFrom 非零时会判为窗口外。
func SignatureOutsideKeyWindow(ts time.Time, key *Pubkey) bool {
	if key == nil {
		return true
	}
	ts = ts.UTC()
	if !key.EffectiveFrom.IsZero() && ts.Before(key.EffectiveFrom.UTC()) {
		return true
	}
	if key.EffectiveTo != nil && ts.After(key.EffectiveTo.UTC()) {
		return true
	}
	return false
}

type PubkeyRegistry interface {
	GetByFingerprint(fingerprint []byte) (*Pubkey, error)
	ListAll() []*Pubkey
}

type PubkeyRegistrar interface {
	PubkeyRegistry
	EnsureActive(ctx context.Context, pubkey *Pubkey) error
	Rotate(ctx context.Context, oldFingerprint []byte, newPubkey *Pubkey, effectiveAt time.Time) error
}

type signerPublicKeySource interface {
	Fingerprint() string
	PublicKey() ed25519.PublicKey
}

type SignatureVerification struct {
	Valid     bool
	KeyStatus string
	Reason    string
}

type PGXPubkeyRegistry struct {
	pool *pgxpool.Pool
}

func NewPGXPubkeyRegistry(pool *pgxpool.Pool) (*PGXPubkeyRegistry, error) {
	if pool == nil {
		return nil, errors.New("auditledger: pgxpool.Pool required for pubkey registry")
	}
	return &PGXPubkeyRegistry{pool: pool}, nil
}

func (r *PGXPubkeyRegistry) GetByFingerprint(fingerprint []byte) (*Pubkey, error) {
	return r.GetByFingerprintContext(context.Background(), fingerprint)
}

func (r *PGXPubkeyRegistry) GetByFingerprintContext(ctx context.Context, fingerprint []byte) (*Pubkey, error) {
	if r == nil || r.pool == nil {
		return nil, ErrPubkeyNotFound
	}
	fp, err := NormalizePubkeyFingerprint(fingerprint)
	if err != nil {
		return nil, err
	}
	row := r.pool.QueryRow(ctx, `
SELECT fingerprint, algorithm, public_key, effective_from, effective_to, created_at
FROM audit_signer_pubkeys
WHERE fingerprint = $1`, fp)
	return scanPubkey(row)
}

func (r *PGXPubkeyRegistry) ListAll() []*Pubkey {
	keys, err := r.ListAllContext(context.Background())
	if err != nil {
		return nil
	}
	return keys
}

func (r *PGXPubkeyRegistry) ListAllContext(ctx context.Context) ([]*Pubkey, error) {
	if r == nil || r.pool == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
SELECT fingerprint, algorithm, public_key, effective_from, effective_to, created_at
FROM audit_signer_pubkeys
ORDER BY effective_from ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Pubkey
	for rows.Next() {
		key, err := scanPubkey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *PGXPubkeyRegistry) EnsureActive(ctx context.Context, pubkey *Pubkey) error {
	key, err := normalizePubkey(pubkey)
	if err != nil {
		return err
	}
	now := key.EffectiveFrom
	if now.IsZero() {
		now = time.Now().UTC()
		key.EffectiveFrom = now
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
UPDATE audit_signer_pubkeys
SET effective_to = $1
WHERE algorithm = $2 AND effective_to IS NULL AND fingerprint <> $3`,
		now, key.Algorithm, key.Fingerprint); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit_signer_pubkeys (
    fingerprint, algorithm, public_key, effective_from, effective_to
) VALUES ($1, $2, $3, $4, NULL)
ON CONFLICT (fingerprint) DO UPDATE
SET algorithm = EXCLUDED.algorithm,
    public_key = EXCLUDED.public_key,
    effective_to = NULL`,
		key.Fingerprint, key.Algorithm, key.PublicKey, key.EffectiveFrom); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *PGXPubkeyRegistry) Rotate(ctx context.Context, oldFingerprint []byte, newPubkey *Pubkey, effectiveAt time.Time) error {
	key, err := normalizePubkey(newPubkey)
	if err != nil {
		return err
	}
	if effectiveAt.IsZero() {
		effectiveAt = time.Now().UTC()
	}
	key.EffectiveFrom = effectiveAt.UTC()
	oldFP, err := NormalizePubkeyFingerprint(oldFingerprint)
	if err != nil && len(strings.TrimSpace(string(oldFingerprint))) > 0 {
		return err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if len(oldFP) > 0 {
		if _, err := tx.Exec(ctx, `
UPDATE audit_signer_pubkeys
SET effective_to = $1
WHERE fingerprint = $2 AND effective_to IS NULL`, key.EffectiveFrom, oldFP); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE audit_signer_pubkeys
SET effective_to = $1
WHERE algorithm = $2 AND effective_to IS NULL AND fingerprint <> $3`,
		key.EffectiveFrom, key.Algorithm, key.Fingerprint); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO audit_signer_pubkeys (
    fingerprint, algorithm, public_key, effective_from, effective_to
) VALUES ($1, $2, $3, $4, NULL)
ON CONFLICT (fingerprint) DO UPDATE
SET algorithm = EXCLUDED.algorithm,
    public_key = EXCLUDED.public_key,
    effective_to = NULL`,
		key.Fingerprint, key.Algorithm, key.PublicKey, key.EffectiveFrom); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type MemoryPubkeyRegistry struct {
	mu   sync.RWMutex
	keys map[string]*Pubkey
}

func NewMemoryPubkeyRegistry(keys ...*Pubkey) *MemoryPubkeyRegistry {
	r := &MemoryPubkeyRegistry{keys: make(map[string]*Pubkey)}
	for _, key := range keys {
		normalized, err := normalizePubkey(key)
		if err == nil {
			r.keys[string(normalized.Fingerprint)] = normalized
		}
	}
	return r
}

func (r *MemoryPubkeyRegistry) GetByFingerprint(fingerprint []byte) (*Pubkey, error) {
	fp, err := NormalizePubkeyFingerprint(fingerprint)
	if err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	key := r.keys[string(fp)]
	if key == nil {
		return nil, fmt.Errorf("%w: %s", ErrPubkeyNotFound, string(fp))
	}
	return clonePubkey(key), nil
}

func (r *MemoryPubkeyRegistry) ListAll() []*Pubkey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Pubkey, 0, len(r.keys))
	for _, key := range r.keys {
		out = append(out, clonePubkey(key))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].EffectiveFrom.Equal(out[j].EffectiveFrom) {
			return out[i].EffectiveFrom.Before(out[j].EffectiveFrom)
		}
		return string(out[i].Fingerprint) < string(out[j].Fingerprint)
	})
	return out
}

func (r *MemoryPubkeyRegistry) EnsureActive(_ context.Context, pubkey *Pubkey) error {
	key, err := normalizePubkey(pubkey)
	if err != nil {
		return err
	}
	if key.EffectiveFrom.IsZero() {
		key.EffectiveFrom = time.Now().UTC()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil {
		r.keys = make(map[string]*Pubkey)
	}
	for fp, existing := range r.keys {
		if fp == string(key.Fingerprint) || existing.Algorithm != key.Algorithm || existing.EffectiveTo != nil {
			continue
		}
		ts := key.EffectiveFrom
		existing.EffectiveTo = &ts
	}
	r.keys[string(key.Fingerprint)] = clonePubkey(key)
	return nil
}

func (r *MemoryPubkeyRegistry) Rotate(ctx context.Context, oldFingerprint []byte, newPubkey *Pubkey, effectiveAt time.Time) error {
	key, err := normalizePubkey(newPubkey)
	if err != nil {
		return err
	}
	if effectiveAt.IsZero() {
		effectiveAt = time.Now().UTC()
	}
	key.EffectiveFrom = effectiveAt.UTC()
	oldFP, err := NormalizePubkeyFingerprint(oldFingerprint)
	if err != nil && len(strings.TrimSpace(string(oldFingerprint))) > 0 {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil {
		r.keys = make(map[string]*Pubkey)
	}
	if existing := r.keys[string(oldFP)]; existing != nil && existing.EffectiveTo == nil {
		ts := key.EffectiveFrom
		existing.EffectiveTo = &ts
	}
	for fp, existing := range r.keys {
		if fp == string(key.Fingerprint) || existing.Algorithm != key.Algorithm || existing.EffectiveTo != nil {
			continue
		}
		ts := key.EffectiveFrom
		existing.EffectiveTo = &ts
	}
	r.keys[string(key.Fingerprint)] = clonePubkey(key)
	_ = ctx
	return nil
}

func EnsureSignerPubkey(ctx context.Context, registry PubkeyRegistrar, signer any, effectiveFrom time.Time) error {
	if registry == nil {
		return nil
	}
	key, err := PubkeyFromSigner(signer, effectiveFrom)
	if err != nil {
		return err
	}
	return registry.EnsureActive(ctx, key)
}

func RotateSignerPubkey(ctx context.Context, registry PubkeyRegistrar, oldFingerprint []byte, signer any, effectiveAt time.Time) error {
	if registry == nil {
		return nil
	}
	key, err := PubkeyFromSigner(signer, effectiveAt)
	if err != nil {
		return err
	}
	return registry.Rotate(ctx, oldFingerprint, key, effectiveAt)
}

func PubkeyFromSigner(signer any, effectiveFrom time.Time) (*Pubkey, error) {
	src, ok := signer.(signerPublicKeySource)
	if !ok || src == nil {
		return nil, ErrInvalidLedgerPublicKey
	}
	pub := src.PublicKey()
	if len(pub) != ed25519.PublicKeySize {
		return nil, ErrInvalidLedgerPublicKey
	}
	fp := strings.TrimSpace(src.Fingerprint())
	if fp == "" {
		fp = PubkeyFingerprint(pub)
	}
	normalizedFP, err := NormalizePubkeyFingerprint([]byte(fp))
	if err != nil {
		return nil, err
	}
	if string(normalizedFP) != PubkeyFingerprint(pub) {
		return nil, fmt.Errorf("%w: fingerprint mismatch", ErrInvalidLedgerPublicKey)
	}
	return &Pubkey{
		Fingerprint:   normalizedFP,
		Algorithm:     AuditSignerAlgorithmEd25519,
		PublicKey:     append([]byte(nil), pub...),
		EffectiveFrom: effectiveFrom.UTC(),
	}, nil
}

func LookupPubkey(ctx context.Context, registry PubkeyRegistry, fingerprint []byte) (*Pubkey, error) {
	if registry == nil {
		return nil, ErrPubkeyNotFound
	}
	if r, ok := registry.(interface {
		GetByFingerprintContext(context.Context, []byte) (*Pubkey, error)
	}); ok {
		return r.GetByFingerprintContext(ctx, fingerprint)
	}
	return registry.GetByFingerprint(fingerprint)
}

func ListPubkeys(ctx context.Context, registry PubkeyRegistry) ([]*Pubkey, error) {
	if registry == nil {
		return nil, nil
	}
	if r, ok := registry.(interface {
		ListAllContext(context.Context) ([]*Pubkey, error)
	}); ok {
		return r.ListAllContext(ctx)
	}
	return registry.ListAll(), nil
}

func VerifySignatureWithRegistry(ctx context.Context, registry PubkeyRegistry, payload []byte, sig []byte, fingerprint []byte) (SignatureVerification, error) {
	key, err := LookupPubkey(ctx, registry, fingerprint)
	if errors.Is(err, ErrPubkeyNotFound) || errors.Is(err, ErrLedgerPubkeyNotFound) {
		return SignatureVerification{Valid: false, KeyStatus: "unknown", Reason: "unknown_signer"}, nil
	}
	if err != nil {
		return SignatureVerification{}, err
	}
	if strings.ToLower(strings.TrimSpace(key.Algorithm)) != AuditSignerAlgorithmEd25519 {
		return SignatureVerification{Valid: false, KeyStatus: key.Status(), Reason: "unsupported_algorithm"}, nil
	}
	if len(key.PublicKey) != ed25519.PublicKeySize {
		return SignatureVerification{Valid: false, KeyStatus: key.Status(), Reason: "invalid_public_key"}, nil
	}
	if len(sig) != ed25519.SignatureSize {
		return SignatureVerification{Valid: false, KeyStatus: key.Status(), Reason: "invalid_signature"}, nil
	}
	if !ed25519.Verify(ed25519.PublicKey(key.PublicKey), payload, sig) {
		return SignatureVerification{Valid: false, KeyStatus: key.Status(), Reason: "signature_mismatch"}, nil
	}
	return SignatureVerification{Valid: true, KeyStatus: key.Status()}, nil
}

func (p *Pubkey) Status() string {
	if p == nil {
		return "unknown"
	}
	if p.EffectiveTo == nil {
		return "active"
	}
	return "rotated"
}

func NormalizePubkeyFingerprint(fingerprint []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(fingerprint))
	if trimmed != "" {
		lower := strings.ToLower(trimmed)
		if len(lower) == 16 {
			if _, err := hex.DecodeString(lower); err == nil {
				return []byte(lower), nil
			}
		}
	}
	if len(fingerprint) == 8 {
		out := make([]byte, 16)
		hex.Encode(out, fingerprint)
		return out, nil
	}
	return nil, ErrInvalidPubkeyFingerprint
}

func normalizePubkey(pubkey *Pubkey) (*Pubkey, error) {
	if pubkey == nil {
		return nil, ErrInvalidLedgerPublicKey
	}
	fp, err := NormalizePubkeyFingerprint(pubkey.Fingerprint)
	if err != nil {
		return nil, err
	}
	algorithm := strings.ToLower(strings.TrimSpace(pubkey.Algorithm))
	if algorithm == "" {
		algorithm = AuditSignerAlgorithmEd25519
	}
	if algorithm != AuditSignerAlgorithmEd25519 {
		return nil, ErrUnsupportedSigner
	}
	if len(pubkey.PublicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidLedgerPublicKey
	}
	if string(fp) != PubkeyFingerprint(ed25519.PublicKey(pubkey.PublicKey)) {
		return nil, fmt.Errorf("%w: fingerprint mismatch", ErrInvalidLedgerPublicKey)
	}
	out := clonePubkey(pubkey)
	out.Fingerprint = fp
	out.Algorithm = algorithm
	out.PublicKey = append([]byte(nil), pubkey.PublicKey...)
	out.EffectiveFrom = out.EffectiveFrom.UTC()
	if out.EffectiveTo != nil {
		ts := out.EffectiveTo.UTC()
		out.EffectiveTo = &ts
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}

func clonePubkey(in *Pubkey) *Pubkey {
	if in == nil {
		return nil
	}
	out := *in
	out.Fingerprint = append([]byte(nil), in.Fingerprint...)
	out.PublicKey = append([]byte(nil), in.PublicKey...)
	if in.EffectiveTo != nil {
		ts := *in.EffectiveTo
		out.EffectiveTo = &ts
	}
	return &out
}

func scanPubkey(row interface {
	Scan(dest ...any) error
}) (*Pubkey, error) {
	var (
		fp            []byte
		algorithm     string
		publicKey     []byte
		effectiveFrom time.Time
		effectiveTo   sql.NullTime
		createdAt     time.Time
	)
	err := row.Scan(&fp, &algorithm, &publicKey, &effectiveFrom, &effectiveTo, &createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPubkeyNotFound
	}
	if err != nil {
		return nil, err
	}
	out := &Pubkey{
		Fingerprint:   append([]byte(nil), fp...),
		Algorithm:     algorithm,
		PublicKey:     append([]byte(nil), publicKey...),
		EffectiveFrom: effectiveFrom.UTC(),
		CreatedAt:     createdAt.UTC(),
	}
	if effectiveTo.Valid {
		ts := effectiveTo.Time.UTC()
		out.EffectiveTo = &ts
	}
	return normalizePubkey(out)
}
