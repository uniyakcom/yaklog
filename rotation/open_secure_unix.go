//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package rotation

import (
	"os"
	"syscall"
)

func mapSymlinkOpenErr(err error) error {
	if err == syscall.ELOOP {
		return ErrSymlinkDetected
	}
	return err
}

func openFileAppendNoFollow(path string, perm os.FileMode) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_WRONLY|syscall.O_APPEND|syscall.O_NOFOLLOW, uint32(perm))
	if err != nil {
		return nil, mapSymlinkOpenErr(err)
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openFileReadNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, mapSymlinkOpenErr(err)
	}
	return os.NewFile(uintptr(fd), path), nil
}

func openFileCreateTruncNoFollow(path string, perm os.FileMode) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_WRONLY|syscall.O_TRUNC|syscall.O_NOFOLLOW, uint32(perm))
	if err != nil {
		return nil, mapSymlinkOpenErr(err)
	}
	return os.NewFile(uintptr(fd), path), nil
}
