package audit

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbaudit "github.com/BloomingProsperity/HUAKAI/internal/db/audit"
)

var (
	ErrDisputeStoreRequired = errors.New("audit: dispute store required")
	ErrDisputeInvalid       = errors.New("audit: invalid dispute")
	ErrDisputeDuplicate     = errors.New("audit: dispute already exists")
	ErrDisputeNotFound      = errors.New("audit: dispute not found")
)

const (
	DisputeStatusOpen      = "open"
	DisputeStatusReviewing = "reviewing"
	DisputeStatusResolved  = "resolved"
	DisputeStatusRejected  = "rejected"

	defaultDisputeListLimit = int32(100)
	maxDisputeListLimit     = int32(500)
)

type CostDispute struct {
	ID           int64
	DisputeID    string
	TenantID     int64
	UserID       int64
	RequestID    string
	Reason       string
	Status       string
	OperatorNote string
	CreatedAt    time.Time
	ResolvedAt   *time.Time
}

type CreateCostDisputeInput struct {
	TenantID  int64
	UserID    int64
	RequestID string
	Reason    string
}

type ResolveCostDisputeInput struct {
	TenantID     int64
	ID           int64
	Status       string
	OperatorNote string
}

type costDisputeQueries interface {
	CreateCostDispute(context.Context, dbaudit.CreateCostDisputeParams) (dbaudit.CostDispute, error)
	ListUserCostDisputes(context.Context, dbaudit.ListUserCostDisputesParams) ([]dbaudit.CostDispute, error)
	ResolveCostDispute(context.Context, dbaudit.ResolveCostDisputeParams) (dbaudit.CostDispute, error)
}

type CostDisputeStore struct {
	q costDisputeQueries
}

func NewPGXDisputeStore(pool *pgxpool.Pool) (*CostDisputeStore, error) {
	if pool == nil {
		return nil, ErrDisputeStoreRequired
	}
	return &CostDisputeStore{q: dbaudit.New(pool)}, nil
}

func NewCostDisputeStoreFromQueries(q costDisputeQueries) *CostDisputeStore {
	return &CostDisputeStore{q: q}
}

func (s *CostDisputeStore) CreateDispute(ctx context.Context, in CreateCostDisputeInput) (CostDispute, error) {
	if s == nil || s.q == nil {
		return CostDispute{}, ErrDisputeStoreRequired
	}
	if err := validateCreateDispute(in); err != nil {
		return CostDispute{}, err
	}
	row, err := s.q.CreateCostDispute(ctx, dbaudit.CreateCostDisputeParams{
		DisputeID: newDisputeID(),
		TenantID:  in.TenantID,
		UserID:    in.UserID,
		RequestID: strings.TrimSpace(in.RequestID),
		Reason:    strings.TrimSpace(in.Reason),
	})
	if err != nil {
		return CostDispute{}, mapCostDisputeError(err)
	}
	return costDisputeFromDB(row), nil
}

func (s *CostDisputeStore) ListUserDisputes(ctx context.Context, tenantID, userID int64, limit int32) ([]CostDispute, error) {
	if s == nil || s.q == nil {
		return nil, ErrDisputeStoreRequired
	}
	if tenantID <= 0 || userID <= 0 {
		return nil, ErrDisputeInvalid
	}
	if limit <= 0 {
		limit = defaultDisputeListLimit
	}
	if limit > maxDisputeListLimit {
		limit = maxDisputeListLimit
	}
	rows, err := s.q.ListUserCostDisputes(ctx, dbaudit.ListUserCostDisputesParams{
		TenantID:  tenantID,
		UserID:    userID,
		LimitRows: limit,
	})
	if err != nil {
		return nil, mapCostDisputeError(err)
	}
	out := make([]CostDispute, 0, len(rows))
	for _, row := range rows {
		out = append(out, costDisputeFromDB(row))
	}
	return out, nil
}

func (s *CostDisputeStore) ResolveDispute(ctx context.Context, in ResolveCostDisputeInput) (CostDispute, error) {
	if s == nil || s.q == nil {
		return CostDispute{}, ErrDisputeStoreRequired
	}
	if err := validateResolveDispute(in); err != nil {
		return CostDispute{}, err
	}
	row, err := s.q.ResolveCostDispute(ctx, dbaudit.ResolveCostDisputeParams{
		TenantID:     in.TenantID,
		ID:           in.ID,
		Status:       in.Status,
		OperatorNote: strings.TrimSpace(in.OperatorNote),
	})
	if err != nil {
		return CostDispute{}, mapCostDisputeError(err)
	}
	return costDisputeFromDB(row), nil
}

func validateCreateDispute(in CreateCostDisputeInput) error {
	switch {
	case in.TenantID <= 0:
		return fmt.Errorf("%w: tenant_id required", ErrDisputeInvalid)
	case in.UserID <= 0:
		return fmt.Errorf("%w: user_id required", ErrDisputeInvalid)
	case strings.TrimSpace(in.RequestID) == "":
		return fmt.Errorf("%w: request_id required", ErrDisputeInvalid)
	case strings.TrimSpace(in.Reason) == "":
		return fmt.Errorf("%w: reason required", ErrDisputeInvalid)
	case len(strings.TrimSpace(in.Reason)) > 4000:
		return fmt.Errorf("%w: reason too long", ErrDisputeInvalid)
	default:
		return nil
	}
}

func validateResolveDispute(in ResolveCostDisputeInput) error {
	switch {
	case in.TenantID <= 0:
		return fmt.Errorf("%w: tenant_id required", ErrDisputeInvalid)
	case in.ID <= 0:
		return fmt.Errorf("%w: id required", ErrDisputeInvalid)
	case !validDisputeStatus(in.Status):
		return fmt.Errorf("%w: invalid status", ErrDisputeInvalid)
	case len(strings.TrimSpace(in.OperatorNote)) > 4000:
		return fmt.Errorf("%w: operator_note too long", ErrDisputeInvalid)
	default:
		return nil
	}
}

func validDisputeStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case DisputeStatusOpen, DisputeStatusReviewing, DisputeStatusResolved, DisputeStatusRejected:
		return true
	default:
		return false
	}
}

func mapCostDisputeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDisputeNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDisputeDuplicate
	}
	return err
}

func costDisputeFromDB(row dbaudit.CostDispute) CostDispute {
	return CostDispute{
		ID:           row.ID,
		DisputeID:    row.DisputeID,
		TenantID:     row.TenantID,
		UserID:       row.UserID,
		RequestID:    row.RequestID,
		Reason:       row.Reason,
		Status:       row.Status,
		OperatorNote: row.OperatorNote,
		CreatedAt:    timeFromPg(row.CreatedAt),
		ResolvedAt:   optionalTimeFromPg(row.ResolvedAt),
	}
}

func timeFromPg(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func optionalTimeFromPg(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	t := value.Time.UTC()
	return &t
}

func newDisputeID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "disp_" + hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return "disp_" + hex.EncodeToString(b[:])
}
