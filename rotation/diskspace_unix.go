//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package rotation

import "syscall"

// availableBytes 返回 path 所在文件系统的可用字节数（非特权进程视角）。
func availableBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
