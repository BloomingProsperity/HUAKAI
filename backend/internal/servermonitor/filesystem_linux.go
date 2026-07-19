//go:build linux

package servermonitor

import "syscall"

func (osSystemSource) StatFilesystem(path string) (filesystemReading, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return filesystemReading{}, err
	}
	blockSize := uint64(stat.Bsize)
	return filesystemReading{
		TotalBytes:     stat.Blocks * blockSize,
		AvailableBytes: stat.Bavail * blockSize,
	}, nil
}
