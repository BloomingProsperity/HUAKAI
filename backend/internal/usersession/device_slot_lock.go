package usersession

import (
	"encoding/binary"
	"hash/fnv"
	"sync"
)

// deviceSlotStripes 条分片锁, 用 (tenant,user) 哈希选一条, 把"数活跃 family → 建新 family"
// 的判定+落库串行化, 关闭 B7 的 TOCTOU 窗口 (count 与 insert 之间无锁 → 并发登录越限)。
// 分片而非 per-key map: 容量恒定不随用户增长, 无需清理; 极小概率的跨用户假共享只是短暂串行,
// 不影响正确性。注意: 这是进程内保护, 已覆盖单实例内并发 HTTP handler 的越限面;
// 多实例部署仍需 DB advisory lock / 唯一约束兜底 (见 fix notes)。
const deviceSlotStripes = 256

var deviceSlotLocks [deviceSlotStripes]sync.Mutex

// lockDeviceSlot 取 (tenant,user) 对应分片锁并返回释放函数。
// MaxActiveFamilies<=0 (设备策略休眠, 默认) 时不加锁, 返回 no-op, 保持零行为变更。
func (s *Service) lockDeviceSlot(tenantID, userID int64) func() {
	if s == nil || s.MaxActiveFamilies <= 0 {
		return func() {}
	}
	idx := deviceSlotStripe(tenantID, userID)
	deviceSlotLocks[idx].Lock()
	return func() { deviceSlotLocks[idx].Unlock() }
}

func deviceSlotStripe(tenantID, userID int64) uint64 {
	var buf [16]byte
	binary.LittleEndian.PutUint64(buf[0:8], uint64(tenantID))
	binary.LittleEndian.PutUint64(buf[8:16], uint64(userID))
	h := fnv.New64a()
	_, _ = h.Write(buf[:])
	return h.Sum64() % deviceSlotStripes
}
