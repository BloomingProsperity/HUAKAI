package servermonitor

import (
	"errors"
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type ioCounters struct {
	readBytes  uint64
	writeBytes uint64
	readOps    uint64
	writeOps   uint64
}

func (c ioCounters) anyLess(other ioCounters) bool {
	return c.readBytes < other.readBytes || c.writeBytes < other.writeBytes || c.readOps < other.readOps || c.writeOps < other.writeOps
}

type networkCounters struct {
	receiveBytes  uint64
	transmitBytes uint64
}

func readHostCPU(source systemSource, now time.Time) (timedCPUSample, error) {
	raw, err := source.ReadFile("/proc/stat")
	if err != nil {
		return timedCPUSample{}, err
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) == 0 {
		return timedCPUSample{}, errors.New("cpu aggregate missing")
	}
	fields := strings.Fields(lines[0])
	if len(fields) < 5 || fields[0] != "cpu" {
		return timedCPUSample{}, errors.New("cpu aggregate invalid")
	}
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return timedCPUSample{}, errors.New("cpu counter invalid")
		}
		values = append(values, value)
	}
	var total uint64
	// guest 与 guest_nice 已包含在 user/nice 中，不能再次累加。
	for _, value := range values[:min(len(values), 8)] {
		if math.MaxUint64-total < value {
			return timedCPUSample{}, errors.New("cpu counter overflow")
		}
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	if idle > total {
		return timedCPUSample{}, errors.New("cpu idle exceeds total")
	}
	capacity := 0
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.HasPrefix(fields[0], "cpu") && len(fields[0]) > 3 {
			if _, err := strconv.Atoi(fields[0][3:]); err == nil {
				capacity++
			}
		}
	}
	if capacity == 0 {
		capacity = runtime.NumCPU()
	}
	return timedCPUSample{at: now, used: total - idle, total: total, capacity: float64(capacity)}, nil
}

func readHostMemory(source systemSource) (*MemoryStats, *SwapStats, error) {
	raw, err := source.ReadFile("/proc/meminfo")
	if err != nil {
		return nil, nil, err
	}
	values := make(map[string]uint64)
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || value > math.MaxUint64/1024 {
			continue
		}
		values[key] = value * 1024
	}
	total, ok := values["MemTotal"]
	if !ok || total == 0 {
		return nil, nil, errors.New("memory total missing")
	}
	available, ok := values["MemAvailable"]
	if !ok {
		available = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	if available > total {
		return nil, nil, errors.New("memory available exceeds total")
	}
	used := total - available
	totalCopy, availableCopy, usage := total, available, percent(used, total)
	memory := &MemoryStats{
		TotalBytes:     &totalCopy,
		UsedBytes:      used,
		AvailableBytes: &availableCopy,
		UsagePercent:   &usage,
	}
	swapTotal := values["SwapTotal"]
	swapFree := values["SwapFree"]
	if swapFree > swapTotal {
		return nil, nil, errors.New("swap free exceeds total")
	}
	swapUsed := swapTotal - swapFree
	return memory, &SwapStats{
		TotalBytes:   swapTotal,
		UsedBytes:    swapUsed,
		UsagePercent: percent(swapUsed, swapTotal),
	}, nil
}

func readLoad(source systemSource) (*LoadStats, error) {
	raw, err := source.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 3 {
		return nil, errors.New("load average missing")
	}
	values := make([]float64, 3)
	for i := range values {
		values[i], err = strconv.ParseFloat(fields[i], 64)
		if err != nil || math.IsNaN(values[i]) || math.IsInf(values[i], 0) || values[i] < 0 {
			return nil, errors.New("load average invalid")
		}
	}
	return &LoadStats{OneMinute: values[0], FiveMinutes: values[1], FifteenMinutes: values[2]}, nil
}

func readUptime(source systemSource) (*UptimeStats, error) {
	raw, err := source.ReadFile("/proc/uptime")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return nil, errors.New("uptime missing")
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return nil, errors.New("uptime invalid")
	}
	return &UptimeStats{Seconds: int64(seconds)}, nil
}

func readHostDiskIO(source systemSource, now time.Time) (timedIOSample, error) {
	mountRaw, err := source.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return timedIOSample{}, err
	}
	major, minor, err := rootDeviceNumbers(string(mountRaw))
	if err != nil {
		return timedIOSample{}, err
	}
	raw, err := source.ReadFile("/proc/diskstats")
	if err != nil {
		return timedIOSample{}, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 14 || fields[0] != major || fields[1] != minor {
			continue
		}
		readOps, err1 := strconv.ParseUint(fields[3], 10, 64)
		readSectors, err2 := strconv.ParseUint(fields[5], 10, 64)
		writeOps, err3 := strconv.ParseUint(fields[7], 10, 64)
		writeSectors, err4 := strconv.ParseUint(fields[9], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || readSectors > math.MaxUint64/512 || writeSectors > math.MaxUint64/512 {
			return timedIOSample{}, errors.New("disk counters invalid")
		}
		return timedIOSample{at: now, value: ioCounters{
			readBytes:  readSectors * 512,
			writeBytes: writeSectors * 512,
			readOps:    readOps,
			writeOps:   writeOps,
		}}, nil
	}
	return timedIOSample{}, errors.New("root disk counters unavailable")
}

func rootDeviceNumbers(raw string) (string, string, error) {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 6 || fields[4] != "/" {
			continue
		}
		parts := strings.Split(fields[2], ":")
		if len(parts) == 2 {
			return parts[0], parts[1], nil
		}
	}
	return "", "", errors.New("root mount device unavailable")
}

func rootFilesystemType(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 || fields[4] != "/" {
			continue
		}
		for i, field := range fields {
			if field == "-" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}

func readNetwork(source systemSource) (networkCounters, error) {
	raw, err := source.ReadFile("/proc/net/dev")
	if err != nil {
		return networkCounters{}, err
	}
	var result networkCounters
	seen := false
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		received, err1 := strconv.ParseUint(fields[0], 10, 64)
		transmitted, err2 := strconv.ParseUint(fields[8], 10, 64)
		if err1 != nil || err2 != nil || math.MaxUint64-result.receiveBytes < received || math.MaxUint64-result.transmitBytes < transmitted {
			return networkCounters{}, errors.New("network counters invalid")
		}
		result.receiveBytes += received
		result.transmitBytes += transmitted
		seen = true
	}
	if !seen {
		return networkCounters{}, errors.New("network interfaces unavailable")
	}
	return result, nil
}
