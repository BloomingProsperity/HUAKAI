//go:build !linux

package servermonitor

import "errors"

func (osSystemSource) StatFilesystem(string) (filesystemReading, error) {
	return filesystemReading{}, errors.New("filesystem metrics unavailable on this platform")
}
