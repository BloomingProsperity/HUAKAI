// hrw_ring.go — PASR-lite A1: HRW（最高随机权重）Rendezvous Hashing
// 数据结构 + top-K 选段。
//
// 算法（参 Thaler & Ravishankar 1996, 公开学术算法，无 license 风险）:
//
//	每个 (prefix_hash, account_id) pair 计算一个 64-bit "权重":
//	  mixed_acc = splitmix64(account_id)            // 高熵化低字节集中的 ID
//	  score = SHA256(seed_8B || prefix_hash || mixed_acc_8B)[0:8]
//	选取分值最高的 top-K 账号作为该 prefix 的"段"。
//
// splitmix64 是公开 PRNG 混合函数（Steele/Lea 2014, public domain），把
// 1..N 这样低熵账号 ID 散到全 64-bit 空间，避免低熵 trailing bytes 造成
// account 维度分布偏置。score 再用 SHA-256 做强混合, 避免不同 seed 只造成
// 高相关的线性扰动, 导致 HRW 排序在 seed 变化后仍过度重复。
//
// 与一致性哈希 ring（Karger）相比，HRW 的关键性质：
//   - 账号增减时只有约 1/N 段会换成员（最优 reshuffle 下界）
//   - 不需维护虚节点 ring 数据结构（每次查询直接算 N 次 hash 取 top-K）
//   - O(N log K) 选 top-K（用 K-element heap）；本实现 K=3 时近 O(N)
//
// 用途: PASR-lite 调度器把 prompt prefix 路由到 K=3 段 — Owner directive
// 2026-05-08: "用我们自己的东西"，本文件是自有调度算法第一个原子。
//
// clean-room: 算法引用学术 paper（公开），不读外部参考项目源码。
// 零新依赖: 仅用 stdlib crypto/sha256 + encoding/binary。
package router

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// splitmix64 是 Steele/Lea 2014 提出的 PRNG 混合函数（public domain），
// 把任意 uint64 输入映射到 64-bit 空间均匀分布。HRW 用它把低熵 account_id
// （1..N 这样的小整数）散到全 64-bit 后再进入 SHA-256，避免分布偏置。
func splitmix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// AccountRing 维护 PASR-lite 调度器的全局账号集 + HRW 哈希种子。
//
// 不变量:
//   - Accounts 内 AccountID 唯一（构造期 dedupe）
//   - Seed 由 admin 启动期注入；运行时不变（rebalance 不换 seed）
//
// 线程安全: 本类型是不可变值（读取并发安全）。账号增减必须 NewAccountRing
// 重建一个新 ring，调用方负责原子替换 *AccountRing 指针。
type AccountRing struct {
	// Accounts 是当前可被 PASR 选择的所有 provider account ID 集（按 ID 升序）。
	Accounts []int64

	// Seed 是 HRW 哈希种子，参与每次 score 计算。Owner 可 30 天轮换防对手
	// 猜测命中（DR-009 A05a 同源策略）。
	Seed uint64
}

// BuildAccountRingFromSnapshots 从 ListAccounts 拿到的 snapshots 直接派生 ring,
// 给 M4 SelectorDispatcher / M6 main.go 走 request-scoped 路径用 (synthesis D3 +
// 决策点 3): per (tenant, pool_group) 已经在 ListAccounts 上游过滤好, 这里
// 不需要额外缓存或 ticker, 也不需要新 SQL — 直接拿当前请求的可见 account 集
// 派生 HRW ring, 天然避开跨租户泄漏 + DB 抖动雪崩两类风险。
//
// 性能预期: O(N) 单遍提 ID + 一次 NewAccountRing (内含排序 O(N log N))。 N
// 通常 < 200, hot path 一次 ~1µs, 不构成瓶颈; 段命中后是 O(K=3) 不再用全 ring。
//
// 不做去重 — NewAccountRing 内已处理。 nil snapshots 返空 ring (caller 自决断
// ErrNoEligibleAccount)。
func BuildAccountRingFromSnapshots(snapshots []*AccountSnapshot, seed uint64) *AccountRing {
	if len(snapshots) == 0 {
		return NewAccountRing(nil, seed)
	}
	ids := make([]int64, 0, len(snapshots))
	for _, s := range snapshots {
		if s == nil || s.ID == 0 {
			continue
		}
		ids = append(ids, s.ID)
	}
	return NewAccountRing(ids, seed)
}

// NewAccountRing 构造 ring，对 accounts 去重并升序排序保证 deterministic。
func NewAccountRing(accounts []int64, seed uint64) *AccountRing {
	if len(accounts) == 0 {
		return &AccountRing{Accounts: nil, Seed: seed}
	}
	seen := make(map[int64]bool, len(accounts))
	out := make([]int64, 0, len(accounts))
	for _, id := range accounts {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return &AccountRing{Accounts: out, Seed: seed}
}

// HRWScore 计算单个 (prefix_hash, account_id) pair 的 64-bit 权重。
//
// 算法: account_id 先经 splitmix64 高熵化, 再以 SHA-256 强混合
// (seed_8B || prefix_hash || mixed_account_8B), 取 digest 前 8 bytes 得最终 score。
// 输入域:
//   - seed_8B          big-endian uint64（大端）
//   - prefix_hash      任意长度 raw bytes（caller 保证 deterministic）
//   - mixed_account_8B splitmix64(uint64(accountID)) big-endian
//
// 不可变 / deterministic: 同输入永远同输出。
func (r *AccountRing) HRWScore(prefixHash []byte, accountID int64) uint64 {
	mixedAcc := splitmix64(uint64(accountID))
	needed := 16 + len(prefixHash)
	var stack [256]byte
	payload := stack[:0]
	if needed > len(stack) {
		payload = make([]byte, 0, needed)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], r.Seed)
	payload = append(payload, buf[:]...)
	payload = append(payload, prefixHash...)
	binary.BigEndian.PutUint64(buf[:], mixedAcc)
	payload = append(payload, buf[:]...)
	sum := sha256.Sum256(payload)
	return binary.BigEndian.Uint64(sum[:8])
}

// TopK 选 prefix 在当前 ring 中 HRW score 最高的前 K 个账号。
//
// 行为:
//   - len(Accounts) == 0          → 返 nil
//   - len(Accounts) <= K          → 返全部账号（按 score 降序排序，决定段内顺序）
//   - len(Accounts) > K           → 返 K 个 score 最高的（降序）
//
// 段内顺序（HRW score 降序）有意义: members[0] 是首选 steward，[1][2] 是
// 候补；PASRSelector 在 cache_control 优先级 + load 均衡时按此顺序遍历。
//
// 实现: 当 N 远大于 K 时用 K-element min-heap，本实现 K 通常 == 3 故
// 直接 sort.Slice 选取（足够快, 万级 account 一次 schedule 也只 ~50µs）。
func (r *AccountRing) TopK(prefixHash []byte, k int) []int64 {
	if k <= 0 || len(r.Accounts) == 0 {
		return nil
	}
	type scored struct {
		id    int64
		score uint64
	}
	scoredAll := make([]scored, len(r.Accounts))
	for i, id := range r.Accounts {
		scoredAll[i] = scored{id: id, score: r.HRWScore(prefixHash, id)}
	}
	sort.Slice(scoredAll, func(i, j int) bool {
		// 降序按 score; tie-break 按 account_id 升序保证 deterministic。
		if scoredAll[i].score != scoredAll[j].score {
			return scoredAll[i].score > scoredAll[j].score
		}
		return scoredAll[i].id < scoredAll[j].id
	})
	if k > len(scoredAll) {
		k = len(scoredAll)
	}
	out := make([]int64, k)
	for i := 0; i < k; i++ {
		out[i] = scoredAll[i].id
	}
	return out
}

// Top3 是 K=3 的便捷封装（PASR-lite v1 默认 K=3，Owner 锁定）。
func (r *AccountRing) Top3(prefixHash []byte) []int64 {
	return r.TopK(prefixHash, 3)
}

// Contains 检查 ring 是否含该 account（O(log N) 二分查询，因 Accounts 已排序）。
func (r *AccountRing) Contains(accountID int64) bool {
	idx := sort.Search(len(r.Accounts), func(i int) bool {
		return r.Accounts[i] >= accountID
	})
	return idx < len(r.Accounts) && r.Accounts[idx] == accountID
}

// AffectedSegments 给定一个 account（即将增/减），返回所有"top-K 段成员
// 会因此变化"的 prefix_hash 列表的判定函数。
//
// 用法: caller 提供"已存在的所有 prefix_hash + 它们的当前 top-K 段"，本
// 函数遍历每个 prefix 重新算 top-K，返回有差异的那些。
//
// 性质（HRW 数学保证）: 加/减一个账号时，期望仅有 1/N 段需要重算。caller
// 在 rebalance 时调本函数，命中差异的段才走 soft_migrate；其余跳过。
//
// 本签名仅声明 helper 输入/输出，具体调用方在 A6 rebalance handler 实现。
func (r *AccountRing) AffectedSegments(prefixHashes [][]byte, oldRing *AccountRing, k int) [][]byte {
	if oldRing == nil || k <= 0 {
		return prefixHashes
	}
	affected := make([][]byte, 0, len(prefixHashes))
	for _, ph := range prefixHashes {
		oldTop := oldRing.TopK(ph, k)
		newTop := r.TopK(ph, k)
		if !int64SliceEqual(oldTop, newTop) {
			affected = append(affected, ph)
		}
	}
	return affected
}

// int64SliceEqual 顺序敏感比较两 int64 切片。
func int64SliceEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
