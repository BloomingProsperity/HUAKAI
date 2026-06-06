package passkeyhttp

import (
	"context"
	"errors"
	"strings"

	"github.com/BloomingProsperity/HUAKAI/internal/twofa"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

var (
	ErrStepUpRequired      = errors.New("passkeyhttp: step-up required")
	ErrStepUpInvalid       = errors.New("passkeyhttp: step-up invalid")
	ErrStepUpNotConfigured = errors.New("passkeyhttp: step-up not configured")
)

type StepUpProof struct {
	Password      string `json:"password,omitempty"`
	TwoFactorCode string `json:"two_factor_code,omitempty"`
}

type StepUpVerifier interface {
	VerifyStepUp(context.Context, int64, int64, StepUpProof) error
}

type UserStore interface {
	GetUserByID(context.Context, int64, int64) (userauth.User, error)
}

type TwoFactorVerifier interface {
	VerifyLogin(context.Context, twofa.VerifyInput) (twofa.VerifyResult, error)
}

type LocalStepUpVerifier struct {
	Users     UserStore
	TwoFactor TwoFactorVerifier
}

func NewLocalStepUpVerifier(users UserStore, twoFactor TwoFactorVerifier) *LocalStepUpVerifier {
	return &LocalStepUpVerifier{Users: users, TwoFactor: twoFactor}
}

func (v *LocalStepUpVerifier) VerifyStepUp(ctx context.Context, tenantID, userID int64, proof StepUpProof) error {
	if tenantID <= 0 || userID <= 0 {
		return ErrStepUpInvalid
	}
	if strings.TrimSpace(proof.Password) == "" && strings.TrimSpace(proof.TwoFactorCode) == "" {
		return ErrStepUpRequired
	}
	if strings.TrimSpace(proof.Password) != "" {
		if v == nil || v.Users == nil {
			return ErrStepUpNotConfigured
		}
		user, err := v.Users.GetUserByID(ctx, tenantID, userID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(user.PasswordHash) == "" {
			return ErrStepUpInvalid
		}
		ok, err := userauth.VerifyPassword(user.PasswordHash, proof.Password)
		if err != nil || !ok {
			return ErrStepUpInvalid
		}
		return nil
	}
	if v == nil || v.TwoFactor == nil {
		return ErrStepUpNotConfigured
	}
	if _, err := v.TwoFactor.VerifyLogin(ctx, twofa.VerifyInput{
		TenantID: tenantID, UserID: userID, Code: proof.TwoFactorCode,
	}); err != nil {
		return err
	}
	return nil
}
