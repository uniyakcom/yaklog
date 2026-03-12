//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package rotation

// availableBytes は非 Unix 環境では未実装のため、常に十分な空き容量を返す。
func availableBytes(_ string) (uint64, error) {
	return ^uint64(0), nil
}
