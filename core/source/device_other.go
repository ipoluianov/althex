//go:build !linux && !darwin && !windows

package source

import (
	"io"
	"os"
)

func isDevicePath(path string) bool { return false }

func deviceSize(f *os.File) (int64, error) { return f.Seek(0, io.SeekEnd) }

// ListDisks is not supported here: a disk is opened by its path
func ListDisks() ([]DiskInfo, error) { return nil, nil }
