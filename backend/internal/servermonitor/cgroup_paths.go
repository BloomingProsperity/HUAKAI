package servermonitor

import (
	"path"
	"slices"
	"strings"
)

func cgroupV2Files(source systemSource, name string) []string {
	return resolveCgroupFiles(source, "", name, "/sys/fs/cgroup")
}

func cgroupV1Files(source systemSource, controller, name string, bases ...string) []string {
	return resolveCgroupFiles(source, controller, name, bases...)
}

func resolveCgroupFiles(source systemSource, controller, name string, bases ...string) []string {
	groupPath, found := cgroupPath(source, controller)
	if !found {
		return rootCgroupFiles(name, bases)
	}
	if mountRaw, err := source.ReadFile("/proc/self/mountinfo"); err == nil {
		if mounted := mountedCgroupFiles(string(mountRaw), controller, groupPath, name, bases); len(mounted) > 0 {
			return mounted
		}
		// 已知进程属于非根 cgroup、却无法映射挂载根时不能回退读取根计数器。
		if groupPath != "/" {
			return nil
		}
	}
	if groupPath == "/" {
		return rootCgroupFiles(name, bases)
	}
	paths := make([]string, 0, len(bases))
	for _, base := range bases {
		paths = append(paths, path.Join(base, groupPath, name))
	}
	return paths
}

func mountedCgroupFiles(raw, controller, groupPath, name string, bases []string) []string {
	paths := make([]string, 0, len(bases))
	for _, line := range strings.Split(raw, "\n") {
		left, right, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		mountFields, fsFields := strings.Fields(left), strings.Fields(right)
		if len(mountFields) < 6 || len(fsFields) < 3 || !cgroupMountMatches(fsFields, controller) {
			continue
		}
		mountPoint := path.Clean(mountFields[4])
		if !slices.Contains(bases, mountPoint) {
			continue
		}
		if relative, ok := cgroupRelativePath(groupPath, path.Clean(mountFields[3])); ok {
			paths = append(paths, path.Join(mountPoint, relative, name))
		}
	}
	return deduplicatePaths(paths)
}

func cgroupMountMatches(fields []string, controller string) bool {
	if controller == "" {
		return fields[0] == "cgroup2"
	}
	if fields[0] != "cgroup" {
		return false
	}
	return containsController(fields[1], controller) || containsController(fields[2], controller)
}

func cgroupRelativePath(groupPath, mountRoot string) (string, bool) {
	groupPath = path.Clean("/" + strings.TrimSpace(groupPath))
	mountRoot = path.Clean("/" + strings.TrimSpace(mountRoot))
	if groupPath == mountRoot {
		return "/", true
	}
	if mountRoot == "/" {
		return groupPath, true
	}
	prefix := mountRoot + "/"
	if strings.HasPrefix(groupPath, prefix) {
		return "/" + strings.TrimPrefix(groupPath, prefix), true
	}
	return "", false
}

func rootCgroupFiles(name string, bases []string) []string {
	paths := make([]string, 0, len(bases))
	for _, base := range bases {
		paths = append(paths, path.Join(base, name))
	}
	return deduplicatePaths(paths)
}

func cgroupPath(source systemSource, controller string) (string, bool) {
	raw, err := source.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "/", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		if controller == "" {
			if parts[0] != "0" || parts[1] != "" {
				continue
			}
		} else if !containsController(parts[1], controller) {
			continue
		}
		return path.Clean("/" + strings.TrimSpace(parts[2])), true
	}
	return "/", false
}

func containsController(raw, want string) bool {
	for _, controller := range strings.Split(raw, ",") {
		if strings.TrimSpace(controller) == want {
			return true
		}
	}
	return false
}

func deduplicatePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, candidate := range paths {
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		out = append(out, candidate)
	}
	return out
}
