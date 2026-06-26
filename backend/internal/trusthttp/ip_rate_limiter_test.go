package trusthttp

import (
	"fmt"
	"testing"
	"time"
)

func TestIPRateLimiter_BucketsBoundedUnderManyIPs(t *testing.T) {
	// 抓 S3(对抗 bug-hunt):buckets map 此前只增不删,公开匿名端点(/v1/trust/verify)被大量不同源 IP
	// 打来会随独立 IP 数无界增长耗尽内存。修后必须有上限。本测试在同一时刻灌入 > maxBuckets 个不同 IP,
	// 断言 map 条目数不超上限。
	// 变异(已验证转红):去掉新建前的上限+清扫/reset 逻辑(Allow 直接 l.buckets[ip]=bucket)→ len 随
	// IP 数线性增长到 maxBuckets+5000 → 此处红。
	l := newIPRateLimiter(1, time.Minute)
	now := time.Now()
	for i := 0; i < ipRateLimiterMaxBuckets+5000; i++ {
		l.Allow(fmt.Sprintf("10.%d.%d.%d", (i>>16)&255, (i>>8)&255, i&255), now)
	}
	if got := len(l.buckets); got > ipRateLimiterMaxBuckets {
		t.Fatalf("buckets=%d 超上限 %d(无界增长 DoS 未堵)", got, ipRateLimiterMaxBuckets)
	}
}

func TestIPRateLimiter_ReclaimsExpiredAtCap(t *testing.T) {
	// 满载到上限后,推进时间过窗口使旧桶全部过期;新 IP 到来触发惰性清扫,过期条目被回收,map 缩回。
	// 这验证清扫是按"过期"回收(而非永久驻留),且仍正确放行新流量。
	l := newIPRateLimiter(2, time.Minute)
	now := time.Now()
	for i := 0; i < ipRateLimiterMaxBuckets; i++ {
		l.Allow(fmt.Sprintf("10.%d.%d.%d", (i>>16)&255, (i>>8)&255, i&255), now)
	}
	if len(l.buckets) != ipRateLimiterMaxBuckets {
		t.Fatalf("预置应满 %d, got %d", ipRateLimiterMaxBuckets, len(l.buckets))
	}
	later := now.Add(2 * time.Minute) // 过窗口:所有旧桶过期
	if !l.Allow("203.0.113.7", later) {
		t.Fatalf("新 IP 在窗口过后应放行")
	}
	if got := len(l.buckets); got != 1 {
		t.Fatalf("过期清扫后应只剩 1 个活跃桶, got %d(过期条目未回收=内存仍膨胀)", got)
	}
}

func TestIPRateLimiter_StillRateLimitsAfterBoundChange(t *testing.T) {
	// 回归:加上限后,核心限流语义不变——同一 IP 超 limit 即拒。
	l := newIPRateLimiter(2, time.Minute)
	now := time.Now()
	if !l.Allow("198.51.100.1", now) || !l.Allow("198.51.100.1", now) {
		t.Fatalf("前 2 次应放行(limit=2)")
	}
	if l.Allow("198.51.100.1", now) {
		t.Fatalf("第 3 次应被拒(超 limit)")
	}
}
