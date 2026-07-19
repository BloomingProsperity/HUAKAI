package servermonitor

import (
	"testing"
	"time"
)

func TestNestedCgroupV2ReadsCurrentProcessInsteadOfHierarchyRoot(t *testing.T) {
	now := time.Date(2026, 7, 19, 5, 0, 0, 0, time.UTC)
	group := "/kubepods.slice/pod-a/container-b"
	source := &fakeSystemSource{
		files: map[string][]byte{
			"/proc/self/cgroup": []byte("0::" + group + "\n"),
			"/proc/self/mountinfo": []byte(
				"31 20 0:29 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw\n",
			),
			"/sys/fs/cgroup" + group + "/cpu.stat":       []byte("usage_usec 200\n"),
			"/sys/fs/cgroup" + group + "/cpu.max":        []byte("100000 100000\n"),
			"/sys/fs/cgroup" + group + "/memory.current": []byte("400\n"),
			"/sys/fs/cgroup" + group + "/memory.max":     []byte("1000\n"),
			"/sys/fs/cgroup/cpu.stat":                    []byte("usage_usec 999999\n"),
			"/sys/fs/cgroup/cpu.max":                     []byte("800000 100000\n"),
			"/sys/fs/cgroup/memory.current":              []byte("9000\n"),
			"/sys/fs/cgroup/memory.max":                  []byte("10000\n"),
		},
		readErrs: map[string]error{},
	}
	cpu, err := readCgroupV2CPU(source, now)
	if err != nil {
		t.Fatalf("读取嵌套 cgroup CPU: %v", err)
	}
	if cpu.used != 200_000 || cpu.capacity != 1 {
		t.Fatalf("cpu=%+v，不能读取层级根计数器", cpu)
	}
	memory, err := readCgroupV2Memory(source)
	if err != nil {
		t.Fatalf("读取嵌套 cgroup 内存: %v", err)
	}
	if memory.UsedBytes != 400 || memory.TotalBytes == nil || *memory.TotalBytes != 1000 {
		t.Fatalf("memory=%+v，不能读取层级根限制", memory)
	}
}

func TestCgroupNamespaceMountRootMapsToMountedRoot(t *testing.T) {
	group := "/kubepods.slice/pod-a/container-b"
	source := &fakeSystemSource{
		files: map[string][]byte{
			"/proc/self/cgroup": []byte("0::" + group + "\n"),
			"/proc/self/mountinfo": []byte(
				"31 20 0:29 " + group + " /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw\n",
			),
			"/sys/fs/cgroup/memory.current": []byte("320\n"),
			"/sys/fs/cgroup/memory.max":     []byte("640\n"),
		},
		readErrs: map[string]error{},
	}
	memory, err := readCgroupV2Memory(source)
	if err != nil {
		t.Fatalf("读取命名空间根内存: %v", err)
	}
	if memory.UsedBytes != 320 || memory.TotalBytes == nil || *memory.TotalBytes != 640 {
		t.Fatalf("memory=%+v want current=320 max=640", memory)
	}
}

func TestNestedCgroupDoesNotFallbackToRootWhenMountCannotBeMapped(t *testing.T) {
	source := &fakeSystemSource{
		files: map[string][]byte{
			"/proc/self/cgroup":             []byte("0::/nested/container\n"),
			"/proc/self/mountinfo":          []byte("31 20 0:29 /other /sys/fs/cgroup rw - cgroup2 cgroup rw\n"),
			"/sys/fs/cgroup/memory.current": []byte("9000\n"),
			"/sys/fs/cgroup/memory.max":     []byte("10000\n"),
		},
		readErrs: map[string]error{},
	}
	if memory, err := readCgroupV2Memory(source); err == nil || memory != nil {
		t.Fatalf("无法映射嵌套 cgroup 时 memory=%+v err=%v，必须失败而不是读取根值", memory, err)
	}
}
