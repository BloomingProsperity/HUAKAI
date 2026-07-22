// Package hermesconfirm 提供 Hermes 改动提议到人工确认之间的单次消费凭据。
package hermesconfirm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// ConfirmTTL 是 dry-run 预览的 correlation_id 可以带 confirm=true 重新提交以真正执行的
// 时间窗口。超过此窗口后,该关联已陈旧,confirm=true 的请求会被拒绝(400)——绝不执行。
// 刻意保持短,这样泄露或被观测到的 correlation_id 影响窗口很小。
const ConfirmTTL = 5 * time.Minute

// PendingConfirmation 表示一个尚待确认的 dry-run 预览。它锁定预览计算时所针对的
// 确切 tool + tenant + actor + target,这样 confirm 不能被改向到与 operator 预览
// 时不同的 tool/tenant/target(字段不匹配的 confirm 会被拒绝)。
type PendingConfirmation struct {
	ToolName    string
	TenantID    int64
	ActorSource string
	ActorID     int64
	TargetID    int64
	ArgsDigest  BindingDigest
	PlanDigest  BindingDigest
	ExpiresAt   time.Time
}

// Store 是提议侧与确认侧共享的最小合同。生产使用 PostgreSQL 实现，使任意网关副本都能
// 原子消费同一份确认；Cache 只用于不依赖数据库的单元测试。
type Store interface {
	Issue(context.Context, PendingConfirmation) (string, error)
	ConsumeWithStatus(context.Context, string, PendingConfirmation) (PendingConfirmation, ConsumeStatus, error)
}

var ErrInvalidPending = errors.New("hermesconfirm: invalid pending confirmation")

// Cache 是支撑纯单元测试的进程内单次消费存储。
// correlation_id 在 dry-run 时签发,并在被取用执行的瞬间被消费(删除),因此它最多
// 只能驱动一次 mutation(重复使用的 correlation_id 找不到任何条目而被拒绝)。
type Cache struct {
	mu      sync.Mutex
	entries map[string]PendingConfirmation
	now     func() time.Time
}

type ConsumeStatus string

const (
	ConsumeOK       ConsumeStatus = "ok"
	ConsumeMissing  ConsumeStatus = "missing"
	ConsumeExpired  ConsumeStatus = "expired"
	ConsumeMismatch ConsumeStatus = "mismatch"
)

// NewCache 构造一个使用真实时钟的空 Cache。
func NewCache() *Cache {
	return &Cache{
		entries: make(map[string]PendingConfirmation),
		now:     time.Now,
	}
}

// Issue 存储一条待确认记录并返回一个全新、不可猜测的 correlation_id。该 id 是 128 位
// 的 crypto 随机 hex,因此没见过预览的调用方无法预测或枚举它。
func (c *Cache) Issue(_ context.Context, p PendingConfirmation) (string, error) {
	if err := validatePending(p); err != nil {
		return "", err
	}
	id, err := randomCorrelationID()
	if err != nil {
		return "", err
	}
	p.ExpiresAt = c.now().Add(ConfirmTTL)
	c.mu.Lock()
	c.entries[id] = p
	// 顺手清理过期条目,避免 map 因从未被确认的预览而无限增长。
	c.evictExpiredLocked()
	c.mu.Unlock()
	return id, nil
}

// Consume 原子地查找并删除 correlation_id(单次消费)。仅当 id 存在、未过期且与传入的
// tool/tenant/actor 绑定匹配时,才返回(entry, true);否则返回(零值, false)。由于删除
// 与查找在同一把锁下进行,针对同一 id 的两个并发 confirm 不可能都成功——最多一个能消费它。
func (c *Cache) Consume(ctx context.Context, id string, expected PendingConfirmation) (PendingConfirmation, bool) {
	entry, status, err := c.ConsumeWithStatus(ctx, id, expected)
	return entry, err == nil && status == ConsumeOK
}

// ConsumeWithStatus 与 Consume 同样原子消费 correlation_id,但保留失败类别。
// HTTP 层用 missing/expired 给 operator 返回可恢复的“重新 dry-run”提示;绑定不匹配
// 仍保持普通 invalid,避免向错误 operator 泄露有效 token 的存在。
func (c *Cache) ConsumeWithStatus(_ context.Context, id string, expected PendingConfirmation) (PendingConfirmation, ConsumeStatus, error) {
	if err := validatePending(expected); err != nil {
		return PendingConfirmation{}, ConsumeMismatch, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[id]
	if !ok {
		return PendingConfirmation{}, ConsumeMissing, nil
	}
	// 先删除,使得即便是不匹配或已过期的命中也是单次消费(被猜中的 id 无法被反复探测)。
	delete(c.entries, id)
	if c.now().After(entry.ExpiresAt) {
		return PendingConfirmation{}, ConsumeExpired, nil
	}
	// 将确认绑定到预览时的完整执行意图。除了管理员与目标，参数和预览计划也必须逐摘要
	// 一致，避免确认阶段换入另一份凭据或在目标状态变化后执行陈旧预览。
	if !sameBinding(entry, expected) {
		return PendingConfirmation{}, ConsumeMismatch, nil
	}
	return entry, ConsumeOK, nil
}

func (c *Cache) evictExpiredLocked() {
	now := c.now()
	for id, e := range c.entries {
		if now.After(e.ExpiresAt) {
			delete(c.entries, id)
		}
	}
}

func randomCorrelationID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "hmc_" + hex.EncodeToString(buf), nil
}

func validatePending(p PendingConfirmation) error {
	if p.ToolName == "" || p.TenantID <= 0 || p.ActorID <= 0 || p.TargetID <= 0 {
		return ErrInvalidPending
	}
	if p.ActorSource != "token" && p.ActorSource != "session" {
		return ErrInvalidPending
	}
	if !p.ArgsDigest.valid() || !p.PlanDigest.valid() {
		return ErrInvalidPending
	}
	return nil
}

func sameBinding(actual, expected PendingConfirmation) bool {
	return actual.ToolName == expected.ToolName &&
		actual.TenantID == expected.TenantID &&
		actual.ActorSource == expected.ActorSource &&
		actual.ActorID == expected.ActorID &&
		actual.TargetID == expected.TargetID &&
		actual.ArgsDigest == expected.ArgsDigest &&
		actual.PlanDigest == expected.PlanDigest
}
