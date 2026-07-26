// Package settlementintenttest 为协议入口测试提供线程安全的结算意图记录器。
// 该包只被 _test.go 引用，不进入生产二进制。
package settlementintenttest

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/BloomingProsperity/HUAKAI/internal/settlementintent"
)

// Store 记录单请求的状态迁移，返回严格递增的乐观锁版本。
type Store struct {
	mu                   sync.Mutex
	DeliveryError        error
	created              settlementintent.CreateParams
	events               []string
	version              int32
	recoveryPayload      json.RawMessage
	recoveryFailureClass string
}

func (s *Store) Insert(_ context.Context, in settlementintent.CreateParams) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = in
	s.events = append(s.events, "pending")
	s.version = 0
	return 1, nil
}

func (s *Store) MarkDelivering(_ context.Context, _ int64, version int32, _ time.Time) (int32, error) {
	s.mu.Lock()
	err := s.DeliveryError
	s.mu.Unlock()
	if err != nil {
		return version, err
	}
	return s.advance("delivering", version), nil
}

func (s *Store) MarkSettling(_ context.Context, _ int64, version int32, _ decimal.Decimal) (int32, error) {
	return s.advance("settling", version), nil
}

func (s *Store) MarkSettled(_ context.Context, _ int64, version int32, _ decimal.Decimal, _ time.Time) (int32, error) {
	return s.advance("settled", version), nil
}

func (s *Store) MarkAborted(_ context.Context, _ int64, version int32) (int32, error) {
	return s.advance("aborted", version), nil
}

func (s *Store) MarkFailed(_ context.Context, _ int64, version int32, _ decimal.Decimal) (int32, error) {
	return s.advance("failed", version), nil
}

func (s *Store) MarkRecoveryPending(
	_ context.Context,
	_ int64,
	version int32,
	_ decimal.Decimal,
	payload json.RawMessage,
	failureClass string,
) (int32, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version != s.version {
		return s.version, nil
	}
	s.recoveryPayload = append(json.RawMessage(nil), payload...)
	s.recoveryFailureClass = failureClass
	s.version++
	s.events = append(s.events, "recovery_pending")
	return s.version, nil
}

func (s *Store) ListStaleNonTerminalSettlementIntents(context.Context, time.Time, time.Time, int32) ([]settlementintent.StaleSettlementIntent, error) {
	return nil, nil
}

func (s *Store) MarkSettledIfStale(_ context.Context, _ int64, version int32, _ decimal.Decimal, _ time.Time) (int32, error) {
	return s.advance("settled", version), nil
}

func (s *Store) MarkAbortedIfStale(_ context.Context, _ int64, version int32) (int32, error) {
	return s.advance("aborted", version), nil
}

func (s *Store) MarkSupersededIfStale(_ context.Context, _ int64, version int32) (int32, error) {
	return s.advance("superseded", version), nil
}

func (s *Store) MarkSettlingIfStale(_ context.Context, _ int64, version int32) (int32, error) {
	return s.advance("settling", version), nil
}

func (s *Store) advance(event string, version int32) int32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version != s.version {
		return s.version
	}
	s.version++
	s.events = append(s.events, event)
	return s.version
}

// Events 返回状态副本，调用方可直接做完整顺序断言。
func (s *Store) Events() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

// Created 返回首条 pending 的身份与账本关联字段。
func (s *Store) Created() settlementintent.CreateParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.created
}

// RecoveryEvidence 返回双故障时写入旁路的可重放载荷和稳定失败分类。
func (s *Store) RecoveryEvidence() (json.RawMessage, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append(json.RawMessage(nil), s.recoveryPayload...), s.recoveryFailureClass
}
