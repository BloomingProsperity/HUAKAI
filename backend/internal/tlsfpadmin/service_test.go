package tlsfpadmin

import (
	"context"
	"errors"
	"testing"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type mockQ struct {
	getRow          admindb.GetTLSFingerprintProfileRow
	getErr          error
	getCalls        int
	createErr       error
	createCalls     int
	updateErr       error
	setStatusErr    error
	setStatusCalls  int
	softDeleteErr   error
	softDeleteCalls int
	listRows        []admindb.ListTLSFingerprintProfilesByTenantRow
	listErr         error
}

func (m *mockQ) CreateTLSFingerprintProfile(context.Context, admindb.CreateTLSFingerprintProfileParams) (admindb.CreateTLSFingerprintProfileRow, error) {
	m.createCalls++
	return admindb.CreateTLSFingerprintProfileRow{}, m.createErr
}
func (m *mockQ) GetTLSFingerprintProfile(context.Context, admindb.GetTLSFingerprintProfileParams) (admindb.GetTLSFingerprintProfileRow, error) {
	m.getCalls++
	return m.getRow, m.getErr
}
func (m *mockQ) UpdateTLSFingerprintProfile(context.Context, admindb.UpdateTLSFingerprintProfileParams) (admindb.UpdateTLSFingerprintProfileRow, error) {
	return admindb.UpdateTLSFingerprintProfileRow{}, m.updateErr
}
func (m *mockQ) SetTLSFingerprintProfileStatus(context.Context, admindb.SetTLSFingerprintProfileStatusParams) error {
	m.setStatusCalls++
	return m.setStatusErr
}
func (m *mockQ) SoftDeleteTLSFingerprintProfile(context.Context, admindb.SoftDeleteTLSFingerprintProfileParams) error {
	m.softDeleteCalls++
	return m.softDeleteErr
}
func (m *mockQ) ListTLSFingerprintProfilesByTenant(context.Context, int64) ([]admindb.ListTLSFingerprintProfilesByTenantRow, error) {
	return m.listRows, m.listErr
}

// HOLE-1/HOLE-3 (critique must-fix): SoftDelete is `:exec` and returns nil on zero
// rows. The service MUST pre-flight Get to detect not-found. Mutation: drop the
// pre-flight Get and call SoftDelete directly -> SoftDelete returns nil -> Delete
// returns nil -> this test goes red (errors.Is fails AND softDeleteCalls != 0).
func TestDelete_NotFound_PreflightCatchesIt(t *testing.T) {
	m := &mockQ{getErr: pgx.ErrNoRows}
	err := New(m).Delete(context.Background(), 7, 9)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete err = %v; want ErrNotFound", err)
	}
	if m.softDeleteCalls != 0 {
		t.Fatalf("SoftDelete called %d times; want 0 (pre-flight Get must short-circuit)", m.softDeleteCalls)
	}
}

func TestDelete_Existing_CallsSoftDelete(t *testing.T) {
	m := &mockQ{} // getErr nil => found
	if err := New(m).Delete(context.Background(), 7, 9); err != nil {
		t.Fatalf("Delete err = %v; want nil", err)
	}
	if m.softDeleteCalls != 1 {
		t.Fatalf("SoftDelete called %d times; want 1", m.softDeleteCalls)
	}
}

// HOLE-2 (critique must-fix): SetStatus is `:exec`; pre-flight Get detects not-found.
func TestSetStatus_NotFound_PreflightCatchesIt(t *testing.T) {
	m := &mockQ{getErr: pgx.ErrNoRows}
	_, err := New(m).SetStatus(context.Background(), SetStatusInput{TenantID: 1, ID: 2, Status: "disabled"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetStatus err = %v; want ErrNotFound", err)
	}
	if m.setStatusCalls != 0 {
		t.Fatalf("SetStatus DB called %d times; want 0", m.setStatusCalls)
	}
}

// drift_detected is drift-worker-only; admin SetStatus must reject it before any DB call.
func TestSetStatus_DriftDetected_RejectedBeforeDB(t *testing.T) {
	m := &mockQ{}
	_, err := New(m).SetStatus(context.Background(), SetStatusInput{TenantID: 1, ID: 2, Status: "drift_detected"})
	if !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("SetStatus(drift_detected) err = %v; want ErrInvalidStatus", err)
	}
	if m.getCalls != 0 || m.setStatusCalls != 0 {
		t.Fatalf("DB touched (get=%d set=%d); want 0 — must reject before DB", m.getCalls, m.setStatusCalls)
	}
}

func TestSetStatus_UnknownValue_Rejected(t *testing.T) {
	if _, err := New(&mockQ{}).SetStatus(context.Background(), SetStatusInput{TenantID: 1, ID: 2, Status: "bogus"}); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("err = %v; want ErrInvalidStatus", err)
	}
}

func TestCreate_EmptyName_RejectsBeforeDB(t *testing.T) {
	m := &mockQ{}
	if _, err := New(m).Create(context.Background(), CreateInput{TenantID: 1, Name: "   "}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v; want ErrInvalidInput", err)
	}
	if m.createCalls != 0 {
		t.Fatalf("Create DB called %d; want 0", m.createCalls)
	}
}

func TestCreate_ZeroTenant_RejectsBeforeDB(t *testing.T) {
	if _, err := New(&mockQ{}).Create(context.Background(), CreateInput{TenantID: 0, Name: "ok"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err = %v; want ErrInvalidInput", err)
	}
}

func TestCreate_DuplicateName_MapsToSentinel(t *testing.T) {
	m := &mockQ{createErr: &pgconn.PgError{Code: "23505"}}
	if _, err := New(m).Create(context.Background(), CreateInput{TenantID: 1, Name: "dup"}); !errors.Is(err, ErrDuplicateName) {
		t.Fatalf("err = %v; want ErrDuplicateName", err)
	}
}

func TestUpdate_NotFound_MapsToSentinel(t *testing.T) {
	m := &mockQ{updateErr: pgx.ErrNoRows}
	if _, err := New(m).Update(context.Background(), UpdateInput{TenantID: 1, ID: 2, Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v; want ErrNotFound", err)
	}
}

func TestGet_NotFound_MapsToSentinel(t *testing.T) {
	m := &mockQ{getErr: pgx.ErrNoRows}
	if _, err := New(m).Get(context.Background(), 1, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v; want ErrNotFound", err)
	}
}

func TestList_EmptyResult_ReturnsEmptySliceNotNil(t *testing.T) {
	out, err := New(&mockQ{listRows: nil}).List(context.Background(), 1)
	if err != nil {
		t.Fatalf("List err = %v", err)
	}
	if out == nil {
		t.Fatal("List returned nil slice; want non-nil empty (JSON [] not null)")
	}
	if len(out) != 0 {
		t.Fatalf("len = %d; want 0", len(out))
	}
}
