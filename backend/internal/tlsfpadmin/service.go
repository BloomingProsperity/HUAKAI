package tlsfpadmin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Querier 是本 service 所需的 *admindb.Queries 子集。在此声明,便于
// handler/service 的测试注入 mock。*admindb.Queries 满足该接口。
type Querier interface {
	CreateTLSFingerprintProfile(context.Context, admindb.CreateTLSFingerprintProfileParams) (admindb.CreateTLSFingerprintProfileRow, error)
	GetTLSFingerprintProfile(context.Context, admindb.GetTLSFingerprintProfileParams) (admindb.GetTLSFingerprintProfileRow, error)
	UpdateTLSFingerprintProfile(context.Context, admindb.UpdateTLSFingerprintProfileParams) (admindb.UpdateTLSFingerprintProfileRow, error)
	SetTLSFingerprintProfileStatus(context.Context, admindb.SetTLSFingerprintProfileStatusParams) error
	SoftDeleteTLSFingerprintProfile(context.Context, admindb.SoftDeleteTLSFingerprintProfileParams) error
	ListTLSFingerprintProfilesByTenant(context.Context, int64) ([]admindb.ListTLSFingerprintProfilesByTenantRow, error)
}

// Service 负责校验输入、把 DB 错误映射为 sentinel 错误,并且(关键地)
// 通过预检 Get 来检测 `:exec` 类 SetStatus/SoftDelete 查询的 not-found——
// 这些原始查询在零行时返回 nil,否则对一个不存在或属于其他租户的 id
// 执行 delete/状态变更也会静默返回 200。
type Service struct{ q Querier }

// New 构造一个 Service。q 通常是 *admindb.Queries。
func New(q Querier) *Service { return &Service{q: q} }

func (s *Service) List(ctx context.Context, tenantID int64) ([]Profile, error) {
	if tenantID <= 0 {
		return nil, ErrInvalidInput
	}
	rows, err := s.q.ListTLSFingerprintProfilesByTenant(ctx, tenantID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]Profile, 0, len(rows)) // 空切片,绝不为 nil(JSON 序列化为 [] 而非 null)
	for _, r := range rows {
		out = append(out, fromListRow(r))
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, tenantID, id int64) (Profile, error) {
	if tenantID <= 0 || id <= 0 {
		return Profile{}, ErrInvalidInput
	}
	row, err := s.q.GetTLSFingerprintProfile(ctx, admindb.GetTLSFingerprintProfileParams{TenantID: tenantID, ID: id})
	if err != nil {
		return Profile{}, mapErr(err)
	}
	return fromGet(row), nil
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Profile, error) {
	if in.TenantID <= 0 || strings.TrimSpace(in.Name) == "" {
		return Profile{}, ErrInvalidInput
	}
	row, err := s.q.CreateTLSFingerprintProfile(ctx, admindb.CreateTLSFingerprintProfileParams{
		TenantID: in.TenantID, Name: strings.TrimSpace(in.Name), Description: in.Description,
		GreaseEnabled: in.GreaseEnabled, CipherSuites: in.CipherSuites, SupportedCurves: in.SupportedCurves,
		EcPointFormats: in.EcPointFormats, SignatureAlgorithms: in.SignatureAlgorithms, AlpnProtocols: in.AlpnProtocols,
		TLSSupportedVersions: in.TLSSupportedVersions, KeyShareGroups: in.KeyShareGroups, PskModes: in.PskModes,
		ExtensionsOrder: in.ExtensionsOrder, ExpectedJA3Hash: in.ExpectedJA3Hash,
	})
	if err != nil {
		return Profile{}, mapErr(err)
	}
	return fromCreate(row), nil
}

func (s *Service) Update(ctx context.Context, in UpdateInput) (Profile, error) {
	if in.TenantID <= 0 || in.ID <= 0 || strings.TrimSpace(in.Name) == "" {
		return Profile{}, ErrInvalidInput
	}
	row, err := s.q.UpdateTLSFingerprintProfile(ctx, admindb.UpdateTLSFingerprintProfileParams{
		Name: strings.TrimSpace(in.Name), Description: in.Description, GreaseEnabled: in.GreaseEnabled,
		CipherSuites: in.CipherSuites, SupportedCurves: in.SupportedCurves, EcPointFormats: in.EcPointFormats,
		SignatureAlgorithms: in.SignatureAlgorithms, AlpnProtocols: in.AlpnProtocols, TLSSupportedVersions: in.TLSSupportedVersions,
		KeyShareGroups: in.KeyShareGroups, PskModes: in.PskModes, ExtensionsOrder: in.ExtensionsOrder,
		ExpectedJA3Hash: in.ExpectedJA3Hash, TenantID: in.TenantID, ID: in.ID,
	})
	if err != nil {
		return Profile{}, mapErr(err) // UpdateTLSFingerprintProfile 是 :one,零行时返回 ErrNoRows -> ErrNotFound
	}
	return fromUpdate(row), nil
}

// SetStatus 修改状态。预检 Get 用于映射 not-found(SetStatus 是 :exec,
// 零行时返回 nil);随后的再次 Get 返回变更后的行,使响应能反映
// last_validated_at。两次检查之间的 TOCTOU 对于单管理员的 CRUD 接口
// 是可接受的(并发的 soft-delete 会在再次 Get 时表现为 404,这是正确的)。
func (s *Service) SetStatus(ctx context.Context, in SetStatusInput) (Profile, error) {
	if in.TenantID <= 0 || in.ID <= 0 {
		return Profile{}, ErrInvalidInput
	}
	if !adminSettableStatuses[in.Status] {
		return Profile{}, ErrInvalidStatus
	}
	if _, err := s.q.GetTLSFingerprintProfile(ctx, admindb.GetTLSFingerprintProfileParams{TenantID: in.TenantID, ID: in.ID}); err != nil {
		return Profile{}, mapErr(err)
	}
	if err := s.q.SetTLSFingerprintProfileStatus(ctx, admindb.SetTLSFingerprintProfileStatusParams{Status: in.Status, TenantID: in.TenantID, ID: in.ID}); err != nil {
		return Profile{}, mapErr(err)
	}
	row, err := s.q.GetTLSFingerprintProfile(ctx, admindb.GetTLSFingerprintProfileParams{TenantID: in.TenantID, ID: in.ID})
	if err != nil {
		return Profile{}, mapErr(err)
	}
	return fromGet(row), nil
}

// Delete 执行软删除。预检 Get 用于映射 not-found(SoftDelete 是 :exec,
// 零行时返回 nil——若没有这一步,对不存在或属于其他租户的 id 执行删除
// 会静默返回 200,形成一个信息泄露的探测口子)。
func (s *Service) Delete(ctx context.Context, tenantID, id int64) error {
	if tenantID <= 0 || id <= 0 {
		return ErrInvalidInput
	}
	if _, err := s.q.GetTLSFingerprintProfile(ctx, admindb.GetTLSFingerprintProfileParams{TenantID: tenantID, ID: id}); err != nil {
		return mapErr(err)
	}
	if err := s.q.SoftDeleteTLSFingerprintProfile(ctx, admindb.SoftDeleteTLSFingerprintProfileParams{TenantID: tenantID, ID: id}); err != nil {
		return mapErr(err)
	}
	return nil
}

// mapErr 把原始 DB 错误映射为 sentinel 错误。ErrBackend 包裹原始错误以供
// 内部日志记录;HTTP 层绝不能把它回显给客户端。
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDuplicateName
	}
	return fmt.Errorf("%w: %v", ErrBackend, err)
}

func ts(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func i32(a []int32) []int32 {
	if a == nil {
		return []int32{}
	}
	return a
}

func strs(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

func fromGet(r admindb.GetTLSFingerprintProfileRow) Profile {
	return Profile{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Description: r.Description, GreaseEnabled: r.GreaseEnabled,
		CipherSuites: i32(r.CipherSuites), SupportedCurves: i32(r.SupportedCurves), EcPointFormats: i32(r.EcPointFormats),
		SignatureAlgorithms: i32(r.SignatureAlgorithms), AlpnProtocols: strs(r.AlpnProtocols), TLSSupportedVersions: i32(r.TLSSupportedVersions),
		KeyShareGroups: i32(r.KeyShareGroups), PskModes: i32(r.PskModes), ExtensionsOrder: i32(r.ExtensionsOrder),
		ExpectedJA3Hash: r.ExpectedJA3Hash, Status: r.Status, LastValidatedAt: tsPtr(r.LastValidatedAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromCreate(r admindb.CreateTLSFingerprintProfileRow) Profile {
	return Profile{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Description: r.Description, GreaseEnabled: r.GreaseEnabled,
		CipherSuites: i32(r.CipherSuites), SupportedCurves: i32(r.SupportedCurves), EcPointFormats: i32(r.EcPointFormats),
		SignatureAlgorithms: i32(r.SignatureAlgorithms), AlpnProtocols: strs(r.AlpnProtocols), TLSSupportedVersions: i32(r.TLSSupportedVersions),
		KeyShareGroups: i32(r.KeyShareGroups), PskModes: i32(r.PskModes), ExtensionsOrder: i32(r.ExtensionsOrder),
		ExpectedJA3Hash: r.ExpectedJA3Hash, Status: r.Status, LastValidatedAt: tsPtr(r.LastValidatedAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromUpdate(r admindb.UpdateTLSFingerprintProfileRow) Profile {
	return Profile{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Description: r.Description, GreaseEnabled: r.GreaseEnabled,
		CipherSuites: i32(r.CipherSuites), SupportedCurves: i32(r.SupportedCurves), EcPointFormats: i32(r.EcPointFormats),
		SignatureAlgorithms: i32(r.SignatureAlgorithms), AlpnProtocols: strs(r.AlpnProtocols), TLSSupportedVersions: i32(r.TLSSupportedVersions),
		KeyShareGroups: i32(r.KeyShareGroups), PskModes: i32(r.PskModes), ExtensionsOrder: i32(r.ExtensionsOrder),
		ExpectedJA3Hash: r.ExpectedJA3Hash, Status: r.Status, LastValidatedAt: tsPtr(r.LastValidatedAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}

func fromListRow(r admindb.ListTLSFingerprintProfilesByTenantRow) Profile {
	return Profile{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Description: r.Description, GreaseEnabled: r.GreaseEnabled,
		CipherSuites: i32(r.CipherSuites), SupportedCurves: i32(r.SupportedCurves), EcPointFormats: i32(r.EcPointFormats),
		SignatureAlgorithms: i32(r.SignatureAlgorithms), AlpnProtocols: strs(r.AlpnProtocols), TLSSupportedVersions: i32(r.TLSSupportedVersions),
		KeyShareGroups: i32(r.KeyShareGroups), PskModes: i32(r.PskModes), ExtensionsOrder: i32(r.ExtensionsOrder),
		ExpectedJA3Hash: r.ExpectedJA3Hash, Status: r.Status, LastValidatedAt: tsPtr(r.LastValidatedAt),
		CreatedAt: ts(r.CreatedAt), UpdatedAt: ts(r.UpdatedAt),
	}
}
