package userauth

import (
	"context"
	"errors"
	"strings"
)

// LinkVerifiedSocialIdentity 把已校验的社交身份绑定到指定的已登录用户。
// 调用方负责验证上游凭证，租户和用户身份必须来自服务端会话。
func (s *Service) LinkVerifiedSocialIdentity(ctx context.Context, tenantID, userID int64, identity VerifiedIdentity) (User, error) {
	return s.linkVerifiedSocialIdentity(ctx, tenantID, userID, identity, "", 0)
}

// LinkVerifiedSocialIdentityForSession 在最终落库事务内复核发起绑定的会话仍属于当前安全代际。
func (s *Service) LinkVerifiedSocialIdentityForSession(
	ctx context.Context,
	tenantID, userID int64,
	identity VerifiedIdentity,
	familyID string,
	authVersion int,
) (User, error) {
	if strings.TrimSpace(familyID) == "" || authVersion <= 0 {
		return User{}, ErrInvalidInput
	}
	return s.linkVerifiedSocialIdentity(ctx, tenantID, userID, identity, familyID, authVersion)
}

type activeAuthSessionStore interface {
	AssertActiveAuthSession(context.Context, int64, int64, string, int) error
}

func (s *Service) linkVerifiedSocialIdentity(
	ctx context.Context,
	tenantID, userID int64,
	identity VerifiedIdentity,
	familyID string,
	authVersion int,
) (User, error) {
	if s == nil || s.Store == nil {
		return User{}, ErrStoreNotConfigured
	}
	provider := normalizeSocialProvider(identity.Provider)
	subject := strings.TrimSpace(identity.Subject)
	if tenantID <= 0 || userID <= 0 || provider == "" || subject == "" {
		return User{}, ErrInvalidInput
	}
	var linked User
	err := s.withStoreTx(ctx, func(store Store) error {
		if familyID != "" {
			guard, ok := store.(activeAuthSessionStore)
			if !ok {
				return ErrStoreNotConfigured
			}
			if err := guard.AssertActiveAuthSession(ctx, tenantID, userID, familyID, authVersion); err != nil {
				return err
			}
		}
		user, err := store.GetUserByID(ctx, tenantID, userID)
		if err != nil {
			return err
		}
		if err := ensureSocialLoginUserAllowed(user, s.now()); err != nil {
			return err
		}
		existing, err := store.GetUserBySocialIdentity(ctx, tenantID, provider, subject)
		switch {
		case err == nil:
			if existing.ID != userID {
				return ErrSocialIdentityAlreadyBound
			}
		case !errors.Is(err, ErrUserNotFound):
			return err
		}
		linked, err = store.LinkSocialIdentity(ctx, tenantID, userID, provider, subject)
		return err
	})
	if err != nil {
		return User{}, err
	}
	return linked, nil
}

type socialIdentityUnlinkStore interface {
	CountUserSocialIdentityLinks(context.Context, int64, int64) (int, error)
	CountSocialIdentityLinks(context.Context, int64, int64, string) (int, error)
	UnlinkSocialIdentity(context.Context, int64, int64, string) (bool, error)
}

func (s *Service) UnlinkSocialIdentity(ctx context.Context, tenantID, userID int64, provider string) (bool, error) {
	return s.unlinkSocialIdentity(ctx, tenantID, userID, provider, "", 0)
}

// UnlinkSocialIdentityForSession 与绑定入口使用同一会话代际守卫。
func (s *Service) UnlinkSocialIdentityForSession(
	ctx context.Context,
	tenantID, userID int64,
	provider, familyID string,
	authVersion int,
) (bool, error) {
	if strings.TrimSpace(familyID) == "" || authVersion <= 0 {
		return false, ErrInvalidInput
	}
	return s.unlinkSocialIdentity(ctx, tenantID, userID, provider, familyID, authVersion)
}

func (s *Service) unlinkSocialIdentity(
	ctx context.Context,
	tenantID, userID int64,
	provider, familyID string,
	authVersion int,
) (bool, error) {
	if s == nil || s.Store == nil {
		return false, ErrStoreNotConfigured
	}
	provider = normalizeSocialProvider(provider)
	if tenantID <= 0 || userID <= 0 || provider == "" {
		return false, ErrInvalidInput
	}
	var unlinked bool
	err := s.withStoreTx(ctx, func(store Store) error {
		if familyID != "" {
			guard, ok := store.(activeAuthSessionStore)
			if !ok {
				return ErrStoreNotConfigured
			}
			if err := guard.AssertActiveAuthSession(ctx, tenantID, userID, familyID, authVersion); err != nil {
				return err
			}
		}
		unlinker, ok := store.(socialIdentityUnlinkStore)
		if !ok {
			return ErrStoreNotConfigured
		}
		user, err := store.GetUserByID(ctx, tenantID, userID)
		if err != nil {
			return err
		}
		providerLinks, err := unlinker.CountSocialIdentityLinks(ctx, tenantID, userID, provider)
		if err != nil {
			return err
		}
		if providerLinks == 0 {
			return nil
		}
		if strings.TrimSpace(user.PasswordHash) == "" {
			totalLinks, err := unlinker.CountUserSocialIdentityLinks(ctx, tenantID, userID)
			if err != nil {
				return err
			}
			if totalLinks <= providerLinks {
				return ErrLastLoginMethod
			}
		}
		unlinked, err = unlinker.UnlinkSocialIdentity(ctx, tenantID, userID, provider)
		return err
	})
	if err != nil {
		return false, err
	}
	return unlinked, nil
}
