package usersession

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// device_confirmation_store.go 实现新设备确认 (device confirmation) 流的存储层 (PG + Memory)。
// 只持有 token_hash (sha256), 永不存原文 token。语义:
//   - CreateDeviceConfirmation: 落一条 pending 记录 (含设备上下文 + 过期)。
//   - GetDeviceConfirmationByTokenHash: 按 (tenant, hash) 取记录; 无则 ErrDeviceConfirmationNotFound。
//   - MarkDeviceConfirmationConfirmed: 条件 UPDATE status='pending'→'confirmed' 设 confirmed_at,
//     RowsAffected==1 才返回 true (幂等防重放: 二次调用命中 0 行返回 false)。

// CreateDeviceConfirmation 插入一条 pending 设备确认记录 (PG)。
func (s *PostgresStore) CreateDeviceConfirmation(ctx context.Context, dc DeviceConfirmation) error {
	if s == nil || s.db == nil {
		return ErrStoreNotConfigured
	}
	deviceInfo := dc.DeviceInfo
	if deviceInfo == nil {
		deviceInfo = map[string]any{}
	}
	infoJSON, err := json.Marshal(deviceInfo)
	if err != nil {
		return err
	}
	status := dc.Status
	if status == "" {
		status = DeviceConfirmationStatusPending
	}
	_, err = s.db.Exec(ctx, `
INSERT INTO device_confirmations (
    tenant_id, user_id, token_hash, device_info, ip, user_agent, status, created_at, expires_at
) VALUES (
    $1, $2, $3, $4::jsonb, $5, $6, $7, $8, $9
)`,
		dc.TenantID, dc.UserID, dc.TokenHash, infoJSON, dc.IP, dc.UserAgent,
		string(status), dc.CreatedAt.UTC(), dc.ExpiresAt.UTC(),
	)
	return err
}

// GetDeviceConfirmationByTokenHash 按 (tenant, hash) 取记录 (PG)。无则 ErrDeviceConfirmationNotFound。
func (s *PostgresStore) GetDeviceConfirmationByTokenHash(ctx context.Context, tenantID int64, hash []byte) (DeviceConfirmation, error) {
	if s == nil || s.db == nil {
		return DeviceConfirmation{}, ErrStoreNotConfigured
	}
	const q = `
SELECT id, tenant_id, user_id, token_hash, device_info, ip, user_agent, status,
       created_at, expires_at, confirmed_at
FROM device_confirmations
WHERE tenant_id = $1 AND token_hash = $2`
	dc, err := scanDeviceConfirmation(s.db.QueryRow(ctx, q, tenantID, hash))
	if errors.Is(err, pgx.ErrNoRows) {
		return DeviceConfirmation{}, ErrDeviceConfirmationNotFound
	}
	if err != nil {
		return DeviceConfirmation{}, err
	}
	return dc, nil
}

// MarkDeviceConfirmationConfirmed 条件 UPDATE pending→confirmed (PG)。RowsAffected==1 才 true。
// 二次调用 (已 confirmed) 命中 0 行返回 false —— 这是幂等防重放的根: 确认流据此绝不重复腾位。
func (s *PostgresStore) MarkDeviceConfirmationConfirmed(ctx context.Context, id int64, now time.Time) (bool, error) {
	if s == nil || s.db == nil {
		return false, ErrStoreNotConfigured
	}
	tag, err := s.db.Exec(ctx, `
UPDATE device_confirmations
SET status = 'confirmed', confirmed_at = $2
WHERE id = $1 AND status = 'pending'`,
		id, now.UTC(),
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// scanDeviceConfirmation 把一行 device_confirmations 扫进 DeviceConfirmation。
func scanDeviceConfirmation(row pgx.Row) (DeviceConfirmation, error) {
	var out DeviceConfirmation
	var status string
	var deviceInfo []byte
	var confirmedAt pgtype.Timestamptz
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.UserID,
		&out.TokenHash,
		&deviceInfo,
		&out.IP,
		&out.UserAgent,
		&status,
		&out.CreatedAt,
		&out.ExpiresAt,
		&confirmedAt,
	); err != nil {
		return DeviceConfirmation{}, err
	}
	out.Status = DeviceConfirmationStatus(status)
	if len(deviceInfo) > 0 {
		_ = json.Unmarshal(deviceInfo, &out.DeviceInfo)
	}
	if out.DeviceInfo == nil {
		out.DeviceInfo = map[string]any{}
	}
	if confirmedAt.Valid {
		t := confirmedAt.Time
		out.ConfirmedAt = &t
	}
	return out, nil
}

// CreateDeviceConfirmation 在内存里落一条 pending 记录 (Memory)。
func (s *MemoryStore) CreateDeviceConfirmation(_ context.Context, dc DeviceConfirmation) error {
	if s == nil {
		return ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dcNextID++
	dc.ID = s.dcNextID
	if dc.Status == "" {
		dc.Status = DeviceConfirmationStatusPending
	}
	if dc.DeviceInfo == nil {
		dc.DeviceInfo = map[string]any{}
	}
	s.deviceConfirmations[dc.ID] = dc
	s.dcByHash[hashKey(dc.TokenHash)] = dc.ID
	return nil
}

// GetDeviceConfirmationByTokenHash 按 (tenant, hash) 取记录 (Memory)。无 / 跨租户则 ErrDeviceConfirmationNotFound。
func (s *MemoryStore) GetDeviceConfirmationByTokenHash(_ context.Context, tenantID int64, hash []byte) (DeviceConfirmation, error) {
	if s == nil {
		return DeviceConfirmation{}, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.dcByHash[hashKey(hash)]
	if !ok {
		return DeviceConfirmation{}, ErrDeviceConfirmationNotFound
	}
	dc, ok := s.deviceConfirmations[id]
	if !ok || dc.TenantID != tenantID {
		// 跨租户探测: token_hash 撞到别租户的记录也按"不存在"处理, 杜绝跨租户确认。
		return DeviceConfirmation{}, ErrDeviceConfirmationNotFound
	}
	return dc, nil
}

// MarkDeviceConfirmationConfirmed 条件翻转 pending→confirmed (Memory)。已 confirmed/expired 返回 false。
func (s *MemoryStore) MarkDeviceConfirmationConfirmed(_ context.Context, id int64, now time.Time) (bool, error) {
	if s == nil {
		return false, ErrStoreNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dc, ok := s.deviceConfirmations[id]
	if !ok || dc.Status != DeviceConfirmationStatusPending {
		// 幂等: 只有当前仍为 pending 才翻转; 二次调用此处短路返回 false, 绝不重复腾位。
		return false, nil
	}
	dc.Status = DeviceConfirmationStatusConfirmed
	t := now.UTC()
	dc.ConfirmedAt = &t
	s.deviceConfirmations[id] = dc
	return true, nil
}
