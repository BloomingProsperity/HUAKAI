// Package hermesconfirm 持有支撑 Hermes mutating-tool 的 dry-run→confirm 安全原语(L2)的
// 进程内单次消费 correlation-id 存储(Cache)。
//
// 它从 internal/hermeshttp 提取到独立共享包,**纯重构、行为零变**:这样 operator 确认侧
// (hermeshttp)与未来 LLM 提议侧(hermeschat,Phase B)能注入**同一个 Cache 实例**——
// hermeshttp 单向 import hermeschat,故确认/提议共用的类型必须落在两者都能 import 的中立包,
// 否则会构成 import 环。逻辑与原 confirmCache 逐字保留(单次消费 + 六元组绑定 + 5 分钟 TTL +
// crypto 随机 id),仅大小写导出 + 改包名。
package hermesconfirm

import (
	"crypto/rand"
	"encoding/hex"
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
	ToolName  string
	TenantID  int64
	ActorID   int64
	TokenID   int64
	TargetID  int64
	ExpiresAt time.Time
}

// Cache 是支撑 L2(dry-run 优先 + 确认)的进程内单次消费 correlation-id 存储。
// correlation_id 在 dry-run 时签发,并在被取用执行的瞬间被消费(删除),因此它最多
// 只能驱动一次 mutation(重复使用的 correlation_id 找不到任何条目而被拒绝)。
//
// 本阶段刻意采用进程内方案:confirm 必须落在签发预览的同一个 replica 上。多 replica
// 部署需要 sticky 路由,否则 operator UI 命中不同 replica 时必须重新发起 dry-run
// (预览成本低且只读);共享缓存是已记录在案的后续工作,而非安全漏洞(缺失的
// correlation_id 总是 fail closed → 400,绝不执行)。
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
func (c *Cache) Issue(p PendingConfirmation) (string, error) {
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
func (c *Cache) Consume(id, toolName string, tenantID, actorID, tokenID int64) (PendingConfirmation, bool) {
	entry, status := c.ConsumeWithStatus(id, toolName, tenantID, actorID, tokenID)
	return entry, status == ConsumeOK
}

// ConsumeWithStatus 与 Consume 同样原子消费 correlation_id,但保留失败类别。
// HTTP 层用 missing/expired 给 operator 返回可恢复的“重新 dry-run”提示;绑定不匹配
// 仍保持普通 invalid,避免向错误 operator 泄露有效 token 的存在。
func (c *Cache) ConsumeWithStatus(id, toolName string, tenantID, actorID, tokenID int64) (PendingConfirmation, ConsumeStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[id]
	if !ok {
		return PendingConfirmation{}, ConsumeMissing
	}
	// 先删除,使得即便是不匹配或已过期的命中也是单次消费(被猜中的 id 无法被反复探测)。
	delete(c.entries, id)
	if c.now().After(entry.ExpiresAt) {
		return PendingConfirmation{}, ConsumeExpired
	}
	// 将 confirm 绑定到执行预览的那个确切 operator:tool + tenant + actor-user +
	// operator 的 admin TokenID。若没有 TokenID 校验,在同一 tenant-user 上下文中操作的
	// 另一个 operator(不同的 admin token)就能消费别人的预览并执行该 mutation。
	if entry.ToolName != toolName || entry.TenantID != tenantID || entry.ActorID != actorID || entry.TokenID != tokenID {
		return PendingConfirmation{}, ConsumeMismatch
	}
	return entry, ConsumeOK
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
