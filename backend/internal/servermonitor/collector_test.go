package servermonitor

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

type fakeSystemSource struct {
	files    map[string][]byte
	readErrs map[string]error
	fs       filesystemReading
	fsErr    error
}

func (f *fakeSystemSource) ReadFile(path string) ([]byte, error) {
	if err := f.readErrs[path]; err != nil {
		return nil, err
	}
	raw, ok := f.files[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return raw, nil
}

func (f *fakeSystemSource) StatFilesystem(string) (filesystemReading, error) {
	return f.fs, f.fsErr
}

func TestHostCollectorRatesFailureAndRecovery(t *testing.T) {
	t0 := time.Date(2026, 7, 19, 1, 0, 0, 0, time.UTC)
	now := t0
	source := hostFixture()
	collector := &Collector{
		source:       source,
		now:          func() time.Time { return now },
		processStart: t0.Add(-time.Minute),
		goos:         "linux",
		goarch:       "amd64",
		pageSize:     4096,
	}

	first, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("首次采集: %v", err)
	}
	if first.ViewScope != ViewScopeHost {
		t.Fatalf("view_scope=%q want host", first.ViewScope)
	}
	for _, group := range []MetricGroup{MetricGroupCPU, MetricGroupDiskIO, MetricGroupNetwork} {
		if first.MetricStates[group] != MetricStateWarming || metricPresent(first.Metrics, group) {
			t.Fatalf("首次 %s 状态=%q present=%v，必须 warming 且为 null", group, first.MetricStates[group], metricPresent(first.Metrics, group))
		}
	}
	if first.CollectionStatus != CollectionStatusPartial {
		t.Fatalf("首次 status=%q want partial", first.CollectionStatus)
	}

	now = now.Add(10 * time.Second)
	source.files["/proc/stat"] = []byte("cpu  150 0 150 900 0 0 0 0 0 0\ncpu0 75 0 75 450\ncpu1 75 0 75 450\n")
	source.files["/proc/diskstats"] = []byte("8 1 sda1 110 0 300 0 220 0 600 0 0 0 0 0 0 0\n")
	source.files["/proc/net/dev"] = []byte("Inter-| Receive | Transmit\n eth0: 3000 0 0 0 0 0 0 0 5000 0 0 0 0 0 0 0\n")
	second, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("第二次采集: %v", err)
	}
	if second.CollectionStatus != CollectionStatusSuccess {
		t.Fatalf("第二次 status=%q errors=%v want success", second.CollectionStatus, second.ActiveErrorClasses)
	}
	assertNear(t, second.Metrics.CPU.UsagePercent, 50)
	assertNear(t, second.Metrics.DiskIO.ReadBytesPerSecond, 5120)
	assertNear(t, second.Metrics.Network.ReceiveBytesPerSecond, 200)

	delete(source.files, "/proc/meminfo")
	now = now.Add(10 * time.Second)
	failed, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("局部失败采集: %v", err)
	}
	if failed.Metrics.Memory != nil || failed.Metrics.Swap != nil {
		t.Fatal("内存读取失败后 memory/swap 必须为 null，不能沿用旧值")
	}
	if failed.MetricStates[MetricGroupMemory] != MetricStateError || failed.MetricStates[MetricGroupSwap] != MetricStateError {
		t.Fatalf("内存失败状态=%q swap=%q", failed.MetricStates[MetricGroupMemory], failed.MetricStates[MetricGroupSwap])
	}
	if !containsString(failed.ActiveErrorClasses, "memory_collection_failed") || !containsString(failed.ActiveErrorClasses, "swap_collection_failed") {
		t.Fatalf("活动错误分类缺失: %v", failed.ActiveErrorClasses)
	}

	source.files["/proc/meminfo"] = []byte(hostMeminfo)
	now = now.Add(10 * time.Second)
	recovered, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("恢复采集: %v", err)
	}
	if recovered.MetricStates[MetricGroupMemory] != MetricStateFresh || recovered.Metrics.Memory == nil {
		t.Fatalf("恢复后 memory state=%q value=%v", recovered.MetricStates[MetricGroupMemory], recovered.Metrics.Memory)
	}
	if containsString(recovered.ActiveErrorClasses, "memory_collection_failed") {
		t.Fatalf("恢复后仍保留活动错误: %v", recovered.ActiveErrorClasses)
	}
}

func TestCounterResetReturnsToWarming(t *testing.T) {
	previous := &timedNetworkSample{
		at:    time.Unix(100, 0),
		value: networkCounters{receiveBytes: 1000, transmitBytes: 2000},
	}
	current := timedNetworkSample{
		at:    time.Unix(110, 0),
		value: networkCounters{receiveBytes: 10, transmitBytes: 20},
	}
	if stats, fresh := rateNetwork(previous, current); fresh || stats != nil {
		t.Fatalf("计数器回绕后 fresh=%v stats=%v，必须重新 warming", fresh, stats)
	}
}

func TestContainerCollectorUsesContainerScope(t *testing.T) {
	t0 := time.Date(2026, 7, 19, 2, 0, 0, 0, time.UTC)
	now := t0
	source := &fakeSystemSource{
		files: map[string][]byte{
			"/.dockerenv":                        {},
			"/sys/fs/cgroup/cpu.stat":            []byte("usage_usec 1000000\n"),
			"/sys/fs/cgroup/cpu.max":             []byte("200000 100000\n"),
			"/sys/fs/cgroup/memory.current":      []byte("400\n"),
			"/sys/fs/cgroup/memory.max":          []byte("1000\n"),
			"/sys/fs/cgroup/memory.swap.current": []byte("20\n"),
			"/sys/fs/cgroup/memory.swap.max":     []byte("100\n"),
			"/sys/fs/cgroup/io.stat":             []byte("8:0 rbytes=1000 wbytes=2000 rios=10 wios=20\n"),
			"/proc/net/dev":                      []byte("Inter-| Receive | Transmit\n eth0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0\n"),
			"/proc/self/statm":                   []byte("100 20 0 0 0 0 0\n"),
		},
		readErrs: map[string]error{},
		fs:       filesystemReading{TotalBytes: 10000, AvailableBytes: 2500},
	}
	collector := &Collector{
		source:       source,
		now:          func() time.Time { return now },
		processStart: t0.Add(-time.Minute),
		goos:         "linux",
		goarch:       "amd64",
		pageSize:     4096,
	}
	if _, err := collector.Collect(context.Background()); err != nil {
		t.Fatalf("容器首次采集: %v", err)
	}
	now = now.Add(10 * time.Second)
	source.files["/sys/fs/cgroup/cpu.stat"] = []byte("usage_usec 11000000\n")
	source.files["/sys/fs/cgroup/io.stat"] = []byte("8:0 rbytes=3000 wbytes=5000 rios=30 wios=50\n")
	source.files["/proc/net/dev"] = []byte("Inter-| Receive | Transmit\n eth0: 3000 0 0 0 0 0 0 0 6000 0 0 0 0 0 0 0\n")
	got, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("容器第二次采集: %v", err)
	}
	if got.ViewScope != ViewScopeContainer {
		t.Fatalf("view_scope=%q want container", got.ViewScope)
	}
	if got.MetricStates[MetricGroupLoad] != MetricStateUnavailable || got.Metrics.Load != nil || got.MetricStates[MetricGroupUptime] != MetricStateUnavailable || got.Metrics.Uptime != nil {
		t.Fatal("容器不得把宿主 load/uptime 冒充容器指标")
	}
	if got.CollectionStatus != CollectionStatusSuccess {
		t.Fatalf("容器状态=%q errors=%v want success", got.CollectionStatus, got.ActiveErrorClasses)
	}
	assertNear(t, got.Metrics.CPU.UsagePercent, 50)
	if got.Metrics.CPU.Capacity != 2 {
		t.Fatalf("cpu capacity=%v want 2", got.Metrics.CPU.Capacity)
	}
	if got.Metrics.Memory.TotalBytes == nil || *got.Metrics.Memory.TotalBytes != 1000 || got.Metrics.Memory.UsedBytes != 400 {
		t.Fatalf("container memory=%+v", got.Metrics.Memory)
	}
}

func TestCgroupV1FallbackParsesQuotaMemorySwapAndIO(t *testing.T) {
	now := time.Date(2026, 7, 19, 2, 30, 0, 0, time.UTC)
	source := &fakeSystemSource{
		files: map[string][]byte{
			"/sys/fs/cgroup/cpuacct/cpuacct.usage":                 []byte("1000000000\n"),
			"/sys/fs/cgroup/cpu/cpu.cfs_quota_us":                  []byte("200000\n"),
			"/sys/fs/cgroup/cpu/cpu.cfs_period_us":                 []byte("100000\n"),
			"/sys/fs/cgroup/cpuset/cpuset.cpus":                    []byte("0\n"),
			"/sys/fs/cgroup/memory/memory.usage_in_bytes":          []byte("400\n"),
			"/sys/fs/cgroup/memory/memory.limit_in_bytes":          []byte("1000\n"),
			"/sys/fs/cgroup/memory/memory.memsw.usage_in_bytes":    []byte("450\n"),
			"/sys/fs/cgroup/memory/memory.memsw.limit_in_bytes":    []byte("1200\n"),
			"/sys/fs/cgroup/blkio/blkio.throttle.io_service_bytes": []byte("8:0 Read 1000\n8:0 Write 2000\nTotal 3000\n"),
			"/sys/fs/cgroup/blkio/blkio.throttle.io_serviced":      []byte("8:0 Read 10\n8:0 Write 20\nTotal 30\n"),
		},
		readErrs: map[string]error{},
	}
	cpu, err := readContainerCPU(source, now)
	if err != nil {
		t.Fatalf("读取 cgroup v1 CPU: %v", err)
	}
	if cpu.capacity != 1 || cpu.used != 1_000_000_000 || !cpu.normalizeByCapacity {
		t.Fatalf("cgroup v1 cpu=%+v", cpu)
	}
	memory, err := readContainerMemory(source)
	if err != nil {
		t.Fatalf("读取 cgroup v1 memory: %v", err)
	}
	if memory.TotalBytes == nil || *memory.TotalBytes != 1000 || memory.UsedBytes != 400 {
		t.Fatalf("cgroup v1 memory=%+v", memory)
	}
	swap, state := readContainerSwap(source)
	if state != MetricStateFresh || swap == nil || swap.TotalBytes != 200 || swap.UsedBytes != 50 {
		t.Fatalf("cgroup v1 swap state=%q value=%+v", state, swap)
	}
	disk, err := readContainerDiskIO(source, now)
	if err != nil {
		t.Fatalf("读取 cgroup v1 io: %v", err)
	}
	if disk.value.readBytes != 1000 || disk.value.writeBytes != 2000 || disk.value.readOps != 10 || disk.value.writeOps != 20 {
		t.Fatalf("cgroup v1 disk=%+v", disk.value)
	}
}

func TestOverlayRootIsDetectedAsContainerWithoutCgroupMarker(t *testing.T) {
	source := &fakeSystemSource{
		files: map[string][]byte{
			"/proc/1/cgroup":       []byte("0::/\n"),
			"/proc/self/mountinfo": []byte("22 1 0:99 / / rw,relatime - overlay overlay rw\n"),
		},
		readErrs: map[string]error{},
	}
	if got := detectLinuxView(source); got != ViewScopeContainer {
		t.Fatalf("view_scope=%q want container", got)
	}
}

func hostFixture() *fakeSystemSource {
	return &fakeSystemSource{
		files: map[string][]byte{
			"/proc/1/cgroup":       []byte("0::/\n"),
			"/proc/stat":           []byte("cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 50 0 50 400\ncpu1 50 0 50 400\n"),
			"/proc/meminfo":        []byte(hostMeminfo),
			"/proc/loadavg":        []byte("0.10 0.20 0.30 1/100 123\n"),
			"/proc/self/mountinfo": []byte("22 1 8:1 / / rw,relatime - ext4 /dev/sda1 rw\n"),
			"/proc/diskstats":      []byte("8 1 sda1 10 0 200 0 20 0 400 0 0 0 0 0 0 0\n"),
			"/proc/net/dev":        []byte("Inter-| Receive | Transmit\n lo: 999 0 0 0 0 0 0 0 999 0 0 0 0 0 0 0\n eth0: 1000 0 0 0 0 0 0 0 2000 0 0 0 0 0 0 0\n"),
			"/proc/uptime":         []byte("1234.50 0.0\n"),
			"/proc/self/statm":     []byte("100 20 0 0 0 0 0\n"),
		},
		readErrs: map[string]error{},
		fs:       filesystemReading{TotalBytes: 10000, AvailableBytes: 2500},
	}
}

const hostMeminfo = "MemTotal: 1000 kB\nMemAvailable: 400 kB\nSwapTotal: 200 kB\nSwapFree: 150 kB\n"

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.001 {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
