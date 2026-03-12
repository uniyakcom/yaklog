package rotation

import "time"

// SetNow 替换 w 的时间函数（供测试注入固定时间），返回恢复函数。
// 实例级替换，无全局状态竞争，竞态安全。
func SetNow(w *RotatingWriter, f func() time.Time) (restore func()) {
	orig := w.nowFn
	w.nowFn = f
	return func() { w.nowFn = orig }
}

// SetAvailableBytes 替换 w 的磁盘空间查询函数（供测试注入模拟值），返回恢复函数。
func SetAvailableBytes(w *RotatingWriter, f func(string) (uint64, error)) (restore func()) {
	orig := w.availBytesFn
	w.availBytesFn = f
	return func() { w.availBytesFn = orig }
}

// SetCompressFn 替换 w 的压缩函数（供测试注入可控实现），返回恢复函数。
func SetCompressFn(w *RotatingWriter, f func(string)) (restore func()) {
	orig := w.compressFn
	w.compressFn = f
	return func() { w.compressFn = orig }
}

// BreakFile 强制关闭底层文件，使后续 Write 返回写入错误（用于测试错误回调）。
// 调用方负责在测试结束前 Close() 写入器以释放资源。
func BreakFile(w *RotatingWriter) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		_ = w.file.Close()
		// 保留 w.file 非 nil 以阻止 Write 路径绕过 file.Write
	}
}

// MaxCompressConcurrent 暴露信号量容量上限供测试断言。
const MaxCompressConcurrent = maxCompressConcurrent
