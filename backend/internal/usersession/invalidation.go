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
			allowed, err := s.Store.FamilyBelongsToUser(ctx, in.TenantID, in.UserID, in.FamilyID)
			if err != nil {
				return 0, err
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

func (s *Service) RevokeOthers(ctx context.Context, in RevokeOthersInput) (int64, error) {
	if s == nil || s.Store == nil {
		return 0, ErrStoreNotConfigured
	}
	if in.TenantID <= 0 || in.UserID <= 0 || strings.TrimSpace(in.CurrentFamilyID) == "" {
		return 0, ErrInvalidInput
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "user_requested"
	}
	currentFamilyID := strings.TrimSpace(in.CurrentFamilyID)
	families, err := s.Store.ListFamilies(ctx, in.TenantID, in.UserID)
	if err != nil {
		return 0, err
	}
	foundCurrent := false
	var revoked int64
	now := s.now()
	for _, family := range families {
		if family.ID == currentFamilyID {
			foundCurrent = true
			continue
		}
		if family.Status != FamilyStatusActive && family.Status != FamilyStatusSuspicious {
			continue
		}
		if _, err := s.Store.RevokeFamily(ctx, in.TenantID, family.ID, reason, now); err != nil {
			return revoked, err
		}
		revoked++
	}
	if !foundCurrent {
		return 0, ErrFamilyNotFound
	}
	return revoked, nil
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

// FamilyBelongsToUser reports whether the given session family is owned by
// (tenantID, userID). It uses an index-backed store lookup instead of
// materializing the user's entire family list.
func (s *Service) FamilyBelongsToUser(ctx context.Context, tenantID, userID int64, familyID string) (bool, error) {
	if s == nil || s.Store == nil {
		return false, ErrStoreNotConfigured
	}
	if tenantID <= 0 || userID <= 0 || strings.TrimSpace(familyID) == "" {
		return false, ErrInvalidInput
	}
	return s.Store.FamilyBelongsToUser(ctx, tenantID, userID, familyID)
}
