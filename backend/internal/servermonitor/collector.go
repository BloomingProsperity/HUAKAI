package servermonitor

import (
	"context"
	"errors"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Collection struct {
	CollectedAt        time.Time
	ViewScope          ViewScope
	CollectionStatus   CollectionStatus
	ActiveErrorClasses []string
	OSName             string
	OSArch             string
	Metrics            Metrics
	MetricStates       map[MetricGroup]MetricState
}

type filesystemReading struct {
	TotalBytes     uint64
	AvailableBytes uint64
}

type systemSource interface {
	ReadFile(string) ([]byte, error)
	StatFilesystem(string) (filesystemReading, error)
}

type osSystemSource struct{}

func (osSystemSource) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

type timedCPUSample struct {
	at                  time.Time
	used                uint64
	total               uint64
	capacity            float64
	normalizeByCapacity bool
}

type timedIOSample struct {
	at    time.Time
	value ioCounters
}

type timedNetworkSample struct {
	at    time.Time
	value networkCounters
}

type Collector struct {
	mu           sync.Mutex
	source       systemSource
	now          func() time.Time
	processStart time.Time
	goos         string
	goarch       string
	pageSize     uint64
	lastCPU      *timedCPUSample
	lastDisk     *timedIOSample
	lastNetwork  *timedNetworkSample
}

func NewCollector() *Collector {
	return &Collector{
		source:       osSystemSource{},
		now:          time.Now,
		processStart: time.Now(),
		goos:         runtime.GOOS,
		goarch:       runtime.GOARCH,
		pageSize:     uint64(os.Getpagesize()),
	}
}

func (c *Collector) Collect(ctx context.Context) (Collection, error) {
	if c == nil || c.source == nil || c.now == nil {
		return Collection{}, errors.New("server monitor collector is not configured")
	}
	if err := ctx.Err(); err != nil {
		return Collection{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now().UTC()
	collection := Collection{
		CollectedAt:  now,
		ViewScope:    ViewScopeProcessOnly,
		OSName:       c.goos,
		OSArch:       c.goarch,
		MetricStates: make(map[MetricGroup]MetricState, len(MetricGroups)),
	}
	for _, group := range MetricGroups {
		collection.MetricStates[group] = MetricStateUnavailable
	}
	if c.goos == "linux" {
		collection.ViewScope = detectLinuxView(c.source)
		switch collection.ViewScope {
		case ViewScopeContainer:
			c.collectContainer(&collection, now)
		case ViewScopeHost:
			c.collectHost(&collection, now)
		}
	}
	c.collectProcess(&collection, now)
	collection.ActiveErrorClasses = normalizeErrorClasses(collection.ActiveErrorClasses)
	collection.CollectionStatus = DeriveCollectionStatus(collection.MetricStates)
	return collection, nil
}

func (c *Collector) collectHost(out *Collection, now time.Time) {
	if sample, err := readHostCPU(c.source, now); err != nil {
		c.lastCPU = nil
		setMetricError(out, MetricGroupCPU)
	} else if stats, fresh := rateCPU(c.lastCPU, sample); fresh {
		out.Metrics.CPU = stats
		out.MetricStates[MetricGroupCPU] = MetricStateFresh
		c.lastCPU = &sample
	} else {
		out.MetricStates[MetricGroupCPU] = MetricStateWarming
		c.lastCPU = &sample
	}

	if memory, swap, err := readHostMemory(c.source); err != nil {
		setMetricError(out, MetricGroupMemory)
		setMetricError(out, MetricGroupSwap)
	} else {
		out.Metrics.Memory = memory
		out.MetricStates[MetricGroupMemory] = MetricStateFresh
		out.Metrics.Swap = swap
		out.MetricStates[MetricGroupSwap] = MetricStateFresh
	}
	if load, err := readLoad(c.source); err != nil {
		setMetricError(out, MetricGroupLoad)
	} else {
		out.Metrics.Load = load
		out.MetricStates[MetricGroupLoad] = MetricStateFresh
	}
	c.collectFilesystem(out)

	if sample, err := readHostDiskIO(c.source, now); err != nil {
		c.lastDisk = nil
		setMetricError(out, MetricGroupDiskIO)
	} else if stats, fresh := rateDisk(c.lastDisk, sample); fresh {
		out.Metrics.DiskIO = stats
		out.MetricStates[MetricGroupDiskIO] = MetricStateFresh
		c.lastDisk = &sample
	} else {
		out.MetricStates[MetricGroupDiskIO] = MetricStateWarming
		c.lastDisk = &sample
	}
	c.collectNetwork(out, now)
	if uptime, err := readUptime(c.source); err != nil {
		setMetricError(out, MetricGroupUptime)
	} else {
		out.Metrics.Uptime = uptime
		out.MetricStates[MetricGroupUptime] = MetricStateFresh
	}
}

func (c *Collector) collectContainer(out *Collection, now time.Time) {
	if sample, err := readContainerCPU(c.source, now); err != nil {
		c.lastCPU = nil
		setMetricError(out, MetricGroupCPU)
	} else if stats, fresh := rateCPU(c.lastCPU, sample); fresh {
		out.Metrics.CPU = stats
		out.MetricStates[MetricGroupCPU] = MetricStateFresh
		c.lastCPU = &sample
	} else {
		out.MetricStates[MetricGroupCPU] = MetricStateWarming
		c.lastCPU = &sample
	}
	if memory, err := readContainerMemory(c.source); err != nil {
		setMetricError(out, MetricGroupMemory)
	} else {
		out.Metrics.Memory = memory
		out.MetricStates[MetricGroupMemory] = MetricStateFresh
	}
	if swap, state := readContainerSwap(c.source); state == MetricStateFresh {
		out.Metrics.Swap = swap
		out.MetricStates[MetricGroupSwap] = state
	} else if state == MetricStateError {
		setMetricError(out, MetricGroupSwap)
	} else {
		out.MetricStates[MetricGroupSwap] = state
	}
	c.collectFilesystem(out)
	if sample, err := readContainerDiskIO(c.source, now); err != nil {
		c.lastDisk = nil
		setMetricError(out, MetricGroupDiskIO)
	} else if stats, fresh := rateDisk(c.lastDisk, sample); fresh {
		out.Metrics.DiskIO = stats
		out.MetricStates[MetricGroupDiskIO] = MetricStateFresh
		c.lastDisk = &sample
	} else {
		out.MetricStates[MetricGroupDiskIO] = MetricStateWarming
		c.lastDisk = &sample
	}
	c.collectNetwork(out, now)
	// 容器内的 load 与 /proc/uptime 通常仍是宿主机口径，明确保持不可用。
	out.MetricStates[MetricGroupLoad] = MetricStateUnavailable
	out.MetricStates[MetricGroupUptime] = MetricStateUnavailable
}

func (c *Collector) collectFilesystem(out *Collection) {
	reading, err := c.source.StatFilesystem("/")
	if err != nil || reading.TotalBytes == 0 || reading.AvailableBytes > reading.TotalBytes {
		setMetricError(out, MetricGroupFilesystem)
		return
	}
	used := reading.TotalBytes - reading.AvailableBytes
	out.Metrics.Filesystem = &FilesystemStats{
		TotalBytes:     reading.TotalBytes,
		UsedBytes:      used,
		AvailableBytes: reading.AvailableBytes,
		UsagePercent:   percent(used, reading.TotalBytes),
	}
	out.MetricStates[MetricGroupFilesystem] = MetricStateFresh
}

func (c *Collector) collectNetwork(out *Collection, now time.Time) {
	counters, err := readNetwork(c.source)
	if err != nil {
		c.lastNetwork = nil
		setMetricError(out, MetricGroupNetwork)
		return
	}
	sample := timedNetworkSample{at: now, value: counters}
	if stats, fresh := rateNetwork(c.lastNetwork, sample); fresh {
		out.Metrics.Network = stats
		out.MetricStates[MetricGroupNetwork] = MetricStateFresh
	} else {
		out.MetricStates[MetricGroupNetwork] = MetricStateWarming
	}
	c.lastNetwork = &sample
}

func (c *Collector) collectProcess(out *Collection, now time.Time) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	stats := &ProcessStats{
		UptimeSeconds:  max(0, int64(now.Sub(c.processStart).Seconds())),
		HeapAllocBytes: mem.HeapAlloc,
		HeapSysBytes:   mem.HeapSys,
		Goroutines:     runtime.NumGoroutine(),
		GCCount:        mem.NumGC,
	}
	if c.goos == "linux" {
		if raw, err := c.source.ReadFile("/proc/self/statm"); err == nil {
			fields := strings.Fields(string(raw))
			if len(fields) >= 2 {
				if pages, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil && pages <= math.MaxUint64/c.pageSize {
					rss := pages * c.pageSize
					stats.RSSBytes = &rss
				}
			}
		}
	}
	out.Metrics.Process = stats
	out.MetricStates[MetricGroupProcess] = MetricStateFresh
}

func setMetricError(out *Collection, group MetricGroup) {
	out.MetricStates[group] = MetricStateError
	out.ActiveErrorClasses = append(out.ActiveErrorClasses, string(group)+"_collection_failed")
}

func detectLinuxView(source systemSource) ViewScope {
	if _, err := source.ReadFile("/.dockerenv"); err == nil {
		return ViewScopeContainer
	}
	if _, err := source.ReadFile("/run/.containerenv"); err == nil {
		return ViewScopeContainer
	}
	raw, err := source.ReadFile("/proc/1/cgroup")
	if err == nil {
		value := strings.ToLower(string(raw))
		for _, marker := range []string{"docker", "kubepods", "containerd", "libpod", "lxc"} {
			if strings.Contains(value, marker) {
				return ViewScopeContainer
			}
		}
	}
	if raw, err := source.ReadFile("/proc/self/mountinfo"); err == nil && rootFilesystemType(string(raw)) == "overlay" {
		return ViewScopeContainer
	}
	return ViewScopeHost
}

func rateCPU(previous *timedCPUSample, current timedCPUSample) (*CPUStats, bool) {
	if previous == nil || !current.at.After(previous.at) || current.used < previous.used || current.total < previous.total || math.Abs(current.capacity-previous.capacity) > 0.001 {
		return nil, false
	}
	usedDelta := current.used - previous.used
	totalDelta := current.total - previous.total
	if totalDelta == 0 || current.capacity <= 0 {
		return nil, false
	}
	usage := float64(usedDelta) / float64(totalDelta) * 100
	if current.normalizeByCapacity {
		usage /= current.capacity
	}
	return &CPUStats{
		UsagePercent: clamp(usage, 0, 100),
		Capacity:     current.capacity,
	}, true
}

func rateDisk(previous *timedIOSample, current timedIOSample) (*DiskIOStats, bool) {
	if previous == nil || !current.at.After(previous.at) || current.value.anyLess(previous.value) {
		return nil, false
	}
	seconds := current.at.Sub(previous.at).Seconds()
	return &DiskIOStats{
		ReadBytesPerSecond:  float64(current.value.readBytes-previous.value.readBytes) / seconds,
		WriteBytesPerSecond: float64(current.value.writeBytes-previous.value.writeBytes) / seconds,
		ReadOpsPerSecond:    float64(current.value.readOps-previous.value.readOps) / seconds,
		WriteOpsPerSecond:   float64(current.value.writeOps-previous.value.writeOps) / seconds,
	}, true
}

func rateNetwork(previous *timedNetworkSample, current timedNetworkSample) (*NetworkStats, bool) {
	if previous == nil || !current.at.After(previous.at) || current.value.receiveBytes < previous.value.receiveBytes || current.value.transmitBytes < previous.value.transmitBytes {
		return nil, false
	}
	seconds := current.at.Sub(previous.at).Seconds()
	return &NetworkStats{
		ReceiveBytesPerSecond:  float64(current.value.receiveBytes-previous.value.receiveBytes) / seconds,
		TransmitBytesPerSecond: float64(current.value.transmitBytes-previous.value.transmitBytes) / seconds,
	}, true
}

func percent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return clamp(float64(used)/float64(total)*100, 0, 100)
}

func clamp(value, low, high float64) float64 {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
