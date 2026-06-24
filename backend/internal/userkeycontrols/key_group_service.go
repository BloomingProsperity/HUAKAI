package userkeycontrols

import (
	"context"
	"fmt"
)

func (s *KeyControlService) SetKeyGroup(ctx context.Context, req SetKeyGroupRequest) (SetKeyGroupResult, error) {
	if s == nil || s.store == nil {
		return SetKeyGroupResult{}, fmt.Errorf("%w: store unset", ErrServiceMisconfig)
	}
	if req.TenantID <= 0 || req.UserID <= 0 || req.APIKeyID <= 0 {
		return SetKeyGroupResult{}, ErrKeyNotFound
	}
	if req.GroupID != nil && *req.GroupID <= 0 {
		return SetKeyGroupResult{}, ErrInvalidGroup
	}
	var out SetKeyGroupResult
	err := s.store.WithTx(ctx, func(txCtx context.Context, tx controlsStore) error {
		if req.GroupID != nil {
			group, err := tx.ValidateGroupBelongsToTenant(txCtx, req.TenantID, *req.GroupID)
			if err != nil {
				if isNoRows(err) {
					return ErrGroupNotFound
				}
				return fmt.Errorf("%w: validate group: %v", ErrBackend, err)
			}
			enabled := group.Enabled
			out.GroupID = &group.ID
			out.GroupName = group.Name
			out.GroupDescription = group.Description
			out.GroupEnabled = &enabled
		}
		affected, err := tx.SetAPIKeyGroupID(txCtx, groupAssignment{
			TenantID: req.TenantID,
			UserID:   req.UserID,
			APIKeyID: req.APIKeyID,
			GroupID:  req.GroupID,
		})
		if err != nil {
			return fmt.Errorf("%w: set group: %v", ErrBackend, err)
		}
		if affected == 0 {
			return ErrKeyNotFound
		}
		out.APIKeyID = req.APIKeyID
		return nil
	})
	if err != nil {
		return SetKeyGroupResult{}, err
	}
	return out, nil
}

func (s *KeyControlService) GetKeyGroup(ctx context.Context, tenantID, userID, apiKeyID int64) (KeyGroupView, error) {
	if s == nil || s.store == nil {
		return KeyGroupView{}, fmt.Errorf("%w: store unset", ErrServiceMisconfig)
	}
	if tenantID <= 0 || userID <= 0 || apiKeyID <= 0 {
		return KeyGroupView{}, ErrKeyNotFound
	}
	row, err := s.store.GetAPIKeyGroup(ctx, tenantID, userID, apiKeyID)
	if err != nil {
		if isNoRows(err) {
			return KeyGroupView{}, ErrKeyNotFound
		}
		return KeyGroupView{}, fmt.Errorf("%w: get group: %v", ErrBackend, err)
	}
	return KeyGroupView(row), nil
}
