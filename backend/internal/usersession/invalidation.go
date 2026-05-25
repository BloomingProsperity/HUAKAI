package usersession

import (
	"context"
	"strings"
)

func (s *Service) Revoke(ctx context.Context, in RevokeInput) (int64, error) {
	if s == nil || s.Store == nil {
		return 0, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 {
		return 0, ErrInvalidInput
	}
	now := s.now()
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "user_requested"
	}
	switch {
	case strings.TrimSpace(in.SessionToken) != "":
		if err := s.Store.RevokeSessionToken(ctx, in.TenantID, HashSessionToken(in.SessionToken), now); err != nil {
			return 0, err
		}
		return 1, nil
	case strings.TrimSpace(in.RefreshToken) != "":
		if err := s.Store.RevokeToken(ctx, in.TenantID, HashRefreshToken(in.RefreshToken), reason, now); err != nil {
			return 0, err
		}
		return 1, nil
	case strings.TrimSpace(in.FamilyID) != "":
		if in.UserID > 0 {
			allowed := false
			families, err := s.Store.ListFamilies(ctx, in.TenantID, in.UserID)
			if err != nil {
				return 0, err
			}
			for _, family := range families {
				if family.ID == strings.TrimSpace(in.FamilyID) {
					allowed = true
					break
				}
			}
			if !allowed {
				return 0, ErrFamilyNotFound
			}
		}
		if _, err := s.Store.RevokeFamily(ctx, in.TenantID, in.FamilyID, reason, now); err != nil {
			return 0, err
		}
		return 1, nil
	case in.UserID > 0:
		return s.Store.RevokeUser(ctx, in.TenantID, in.UserID, reason, now)
	default:
		return 0, ErrInvalidInput
	}
}

func (s *Service) List(ctx context.Context, tenantID, userID int64) ([]SessionFamily, error) {
	if s == nil || s.Store == nil {
		return nil, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 {
		return nil, ErrInvalidInput
	}
	return s.Store.ListFamilies(ctx, tenantID, userID)
}
