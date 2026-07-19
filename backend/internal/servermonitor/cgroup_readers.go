package servermonitor

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func readContainerCPU(source systemSource, now time.Time) (timedCPUSample, error) {
	if sample, err := readCgroupV2CPU(source, now); err == nil {
		return sample, nil
	}
	return readCgroupV1CPU(source, now)
}

func readCgroupV2CPU(source systemSource, now time.Time) (timedCPUSample, error) {
	raw, err := readCgroupV2File(source, "cpu.stat")
	if err != nil {
		return timedCPUSample{}, err
	}
	var usageMicros uint64
	foundUsage := false
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			foundUsage = true
			usageMicros, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil || usageMicros > math.MaxUint64/1000 {
				return timedCPUSample{}, errors.New("cgroup cpu usage invalid")
			}
		}
	}
	if !foundUsage {
		return timedCPUSample{}, errors.New("cgroup cpu usage missing")
	}
	capacity := float64(runtime.NumCPU())
	if maxRaw, readErr := readCgroupV2File(source, "cpu.max"); readErr == nil {
		fields := strings.Fields(string(maxRaw))
		if len(fields) >= 2 && fields[0] != "max" {
			quota, quotaErr := strconv.ParseFloat(fields[0], 64)
			period, periodErr := strconv.ParseFloat(fields[1], 64)
			if quotaErr == nil && periodErr == nil && quota > 0 && period > 0 {
				capacity = quota / period
			}
		}
	}
	capacity = applyCPUSetCapacityPaths(source, cgroupV2Files(source, "cpuset.cpus.effective"), capacity)
	return timedCPUSample{
		at:                  now,
		used:                usageMicros * 1000,
		total:               uint64(now.UnixNano()),
		capacity:            capacity,
		normalizeByCapacity: true,
	}, nil
}

func readCgroupV1CPU(source systemSource, now time.Time) (timedCPUSample, error) {
	usageNanos, err := parseFirstUintFile(source, cgroupV1Files(source, "cpuacct", "cpuacct.usage",
		"/sys/fs/cgroup/cpuacct", "/sys/fs/cgroup/cpu,cpuacct"))
	if err != nil {
		return timedCPUSample{}, err
	}
	capacity := float64(runtime.NumCPU())
	quota, quotaErr := parseFirstIntFile(source, cgroupV1Files(source, "cpu", "cpu.cfs_quota_us",
		"/sys/fs/cgroup/cpu", "/sys/fs/cgroup/cpu,cpuacct"))
	period, periodErr := parseFirstIntFile(source, cgroupV1Files(source, "cpu", "cpu.cfs_period_us",
		"/sys/fs/cgroup/cpu", "/sys/fs/cgroup/cpu,cpuacct"))
	if quotaErr == nil && periodErr == nil && quota > 0 && period > 0 {
		capacity = float64(quota) / float64(period)
	}
	capacity = applyCPUSetCapacityPaths(source, cgroupV1Files(source, "cpuset", "cpuset.cpus", "/sys/fs/cgroup/cpuset"), capacity)
	return timedCPUSample{
		at:                  now,
		used:                usageNanos,
		total:               uint64(now.UnixNano()),
		capacity:            capacity,
		normalizeByCapacity: true,
	}, nil
}

func readContainerMemory(source systemSource) (*MemoryStats, error) {
	if memory, err := readCgroupV2Memory(source); err == nil {
		return memory, nil
	}
	return readCgroupV1Memory(source)
}

func readCgroupV2Memory(source systemSource) (*MemoryStats, error) {
	currentRaw, err := readCgroupV2File(source, "memory.current")
	if err != nil {
		return nil, err
	}
	used, err := strconv.ParseUint(strings.TrimSpace(string(currentRaw)), 10, 64)
	if err != nil {
		return nil, errors.New("cgroup memory current invalid")
	}
	stats := &MemoryStats{UsedBytes: used}
	maxRaw, err := readCgroupV2File(source, "memory.max")
	if err != nil || strings.TrimSpace(string(maxRaw)) == "max" {
		return stats, nil
	}
	total, err := strconv.ParseUint(strings.TrimSpace(string(maxRaw)), 10, 64)
	if err != nil || total == 0 || used > total {
		return nil, errors.New("cgroup memory limit invalid")
	}
	available := total - used
	usage := percent(used, total)
	stats.TotalBytes = &total
	stats.AvailableBytes = &available
	stats.UsagePercent = &usage
	return stats, nil
}

func readCgroupV1Memory(source systemSource) (*MemoryStats, error) {
	used, err := parseFirstUintFile(source, cgroupV1Files(source, "memory", "memory.usage_in_bytes", "/sys/fs/cgroup/memory"))
	if err != nil {
		return nil, err
	}
	stats := &MemoryStats{UsedBytes: used}
	total, err := parseFirstUintFile(source, cgroupV1Files(source, "memory", "memory.limit_in_bytes", "/sys/fs/cgroup/memory"))
	if err != nil || total >= 1<<60 {
		return stats, nil
	}
	if total == 0 || used > total {
		return nil, errors.New("cgroup memory limit invalid")
	}
	available := total - used
	usage := percent(used, total)
	stats.TotalBytes = &total
	stats.AvailableBytes = &available
	stats.UsagePercent = &usage
	return stats, nil
}

func readContainerSwap(source systemSource) (*SwapStats, MetricState) {
	if swap, state := readCgroupV2Swap(source); state != MetricStateUnavailable {
		return swap, state
	}
	return readCgroupV1Swap(source)
}

func readCgroupV2Swap(source systemSource) (*SwapStats, MetricState) {
	currentRaw, err := readCgroupV2File(source, "memory.swap.current")
	if err != nil {
		return nil, MetricStateUnavailable
	}
	maxRaw, err := readCgroupV2File(source, "memory.swap.max")
	if err != nil || strings.TrimSpace(string(maxRaw)) == "max" {
		return nil, MetricStateUnavailable
	}
	used, err1 := strconv.ParseUint(strings.TrimSpace(string(currentRaw)), 10, 64)
	total, err2 := strconv.ParseUint(strings.TrimSpace(string(maxRaw)), 10, 64)
	if err1 != nil || err2 != nil || used > total {
		return nil, MetricStateError
	}
	return &SwapStats{TotalBytes: total, UsedBytes: used, UsagePercent: percent(used, total)}, MetricStateFresh
}

func readCgroupV1Swap(source systemSource) (*SwapStats, MetricState) {
	memoryUsage, err1 := parseFirstUintFile(source, cgroupV1Files(source, "memory", "memory.usage_in_bytes", "/sys/fs/cgroup/memory"))
	memoryLimit, err2 := parseFirstUintFile(source, cgroupV1Files(source, "memory", "memory.limit_in_bytes", "/sys/fs/cgroup/memory"))
	combinedUsage, err3 := parseFirstUintFile(source, cgroupV1Files(source, "memory", "memory.memsw.usage_in_bytes", "/sys/fs/cgroup/memory"))
	combinedLimit, err4 := parseFirstUintFile(source, cgroupV1Files(source, "memory", "memory.memsw.limit_in_bytes", "/sys/fs/cgroup/memory"))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || memoryLimit >= 1<<60 || combinedLimit >= 1<<60 {
		return nil, MetricStateUnavailable
	}
	if combinedUsage < memoryUsage || combinedLimit < memoryLimit {
		return nil, MetricStateError
	}
	used := combinedUsage - memoryUsage
	total := combinedLimit - memoryLimit
	if used > total {
		return nil, MetricStateError
	}
	return &SwapStats{TotalBytes: total, UsedBytes: used, UsagePercent: percent(used, total)}, MetricStateFresh
}

func readContainerDiskIO(source systemSource, now time.Time) (timedIOSample, error) {
	if sample, err := readCgroupV2DiskIO(source, now); err == nil {
		return sample, nil
	}
	return readCgroupV1DiskIO(source, now)
}

func readCgroupV2DiskIO(source systemSource, now time.Time) (timedIOSample, error) {
	raw, err := readCgroupV2File(source, "io.stat")
	if err != nil {
		return timedIOSample{}, err
	}
	var counters ioCounters
	seen := false
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		seen = true
		for _, field := range fields[1:] {
			pair := strings.SplitN(field, "=", 2)
			if len(pair) != 2 {
				continue
			}
			value, parseErr := strconv.ParseUint(pair[1], 10, 64)
			if parseErr != nil {
				return timedIOSample{}, errors.New("cgroup io counter invalid")
			}
			var target *uint64
			switch pair[0] {
			case "rbytes":
				target = &counters.readBytes
			case "wbytes":
				target = &counters.writeBytes
			case "rios":
				target = &counters.readOps
			case "wios":
				target = &counters.writeOps
			}
			if target != nil {
				if math.MaxUint64-*target < value {
					return timedIOSample{}, errors.New("cgroup io counter overflow")
				}
				*target += value
			}
		}
	}
	if !seen {
		return timedIOSample{}, errors.New("cgroup io counters unavailable")
	}
	return timedIOSample{at: now, value: counters}, nil
}

func readCgroupV1DiskIO(source systemSource, now time.Time) (timedIOSample, error) {
	bytesRaw, err := readFirstFile(source, cgroupV1Files(source, "blkio", "blkio.throttle.io_service_bytes", "/sys/fs/cgroup/blkio"))
	if err != nil {
		return timedIOSample{}, err
	}
	opsRaw, err := readFirstFile(source, cgroupV1Files(source, "blkio", "blkio.throttle.io_serviced", "/sys/fs/cgroup/blkio"))
	if err != nil {
		return timedIOSample{}, err
	}
	readBytes, writeBytes, err := parseCgroupV1IO(string(bytesRaw))
	if err != nil {
		return timedIOSample{}, err
	}
	readOps, writeOps, err := parseCgroupV1IO(string(opsRaw))
	if err != nil {
		return timedIOSample{}, err
	}
	return timedIOSample{at: now, value: ioCounters{
		readBytes: readBytes, writeBytes: writeBytes, readOps: readOps, writeOps: writeOps,
	}}, nil
}

func parseCgroupV1IO(raw string) (uint64, uint64, error) {
	var read, write uint64
	seen := false
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || strings.EqualFold(fields[0], "Total") {
			continue
		}
		value, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			return 0, 0, errors.New("cgroup io counter invalid")
		}
		switch strings.ToLower(fields[1]) {
		case "read":
			if math.MaxUint64-read < value {
				return 0, 0, errors.New("cgroup io counter overflow")
			}
			read += value
			seen = true
		case "write":
			if math.MaxUint64-write < value {
				return 0, 0, errors.New("cgroup io counter overflow")
			}
			write += value
			seen = true
		}
	}
	if !seen {
		return 0, 0, errors.New("cgroup io counters unavailable")
	}
	return read, write, nil
}

func applyCPUSetCapacityPaths(source systemSource, paths []string, capacity float64) float64 {
	raw, err := readFirstFile(source, paths)
	if err != nil {
		return capacity
	}
	count, err := cpuSetCount(strings.TrimSpace(string(raw)))
	if err == nil && count > 0 && float64(count) < capacity {
		return float64(count)
	}
	return capacity
}

func cpuSetCount(raw string) (int, error) {
	if raw == "" {
		return 0, errors.New("cpu set empty")
	}
	count := 0
	for _, part := range strings.Split(raw, ",") {
		bounds := strings.SplitN(strings.TrimSpace(part), "-", 2)
		start, err := strconv.Atoi(bounds[0])
		if err != nil || start < 0 {
			return 0, errors.New("cpu set invalid")
		}
		end := start
		if len(bounds) == 2 {
			end, err = strconv.Atoi(bounds[1])
			if err != nil || end < start {
				return 0, errors.New("cpu set invalid")
			}
		}
		count += end - start + 1
	}
	return count, nil
}

func parseFirstUintFile(source systemSource, paths []string) (uint64, error) {
	for _, path := range paths {
		if value, err := parseUintFile(source, path); err == nil {
			return value, nil
		}
	}
	return 0, errors.New("numeric cgroup value unavailable")
}

func parseFirstIntFile(source systemSource, paths []string) (int64, error) {
	for _, path := range paths {
		raw, err := source.ReadFile(path)
		if err != nil {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err == nil {
			return value, nil
		}
	}
	return 0, errors.New("numeric cgroup value unavailable")
}

func parseUintFile(source systemSource, path string) (uint64, error) {
	raw, err := source.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("numeric value invalid")
	}
	return value, nil
}

func readCgroupV2File(source systemSource, name string) ([]byte, error) {
	return readFirstFile(source, cgroupV2Files(source, name))
}

func readFirstFile(source systemSource, paths []string) ([]byte, error) {
	for _, candidate := range paths {
		if raw, err := source.ReadFile(candidate); err == nil {
			return raw, nil
		}
	}
	return nil, errors.New("cgroup file unavailable")
}
