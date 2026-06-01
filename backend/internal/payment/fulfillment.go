package payment

import (
	"context"
)

type CallbackStore interface {
	FulfillCallback(context.Context, VerifiedCallback) (CallbackResult, error)
}

func (s *Service) HandleCallback(ctx context.Context, input CallbackInput) (CallbackResult, error) {
	if s == nil || s.store == nil {
		return CallbackResult{HTTPStatus: 500}, ErrStoreNotConfigured
	}
	if result, err := VerifyCallback(input, CallbackExpectation{}); err != nil {
		return result, err
	}
	store, ok := s.store.(CallbackStore)
	if !ok || store == nil {
		return CallbackResult{HTTPStatus: 500}, ErrStoreNotConfigured
	}
	return store.FulfillCallback(ctx, verifiedCallback(input))
}
