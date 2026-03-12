package rotation

import (
	"compress/gzip"
	"io"
	"os"
)

// compressFile 将 src 原地压缩为 src+".gz"，成功后删除原始文件。
// 失败时保留原始文件，确保日志不丢失。
// 此函数在独立 goroutine 中调用；错误静默丢弃（防止循环日志）。
func compressFile(src string) {
	dst := src + ".gz"
	if err := gzipFile(src, dst); err != nil {
		// 删除可能残留的不完整压缩文件
		_ = os.Remove(dst)
		return
	}
	_ = os.Remove(src)
}

// gzipFile 读取 src 并以 gzip 格式写入 dst。
//
// 使用具名返回值 retErr，在 defer 中捕获 out.Close() 的错误：
// 若 gz.Close()（写入 gzip 校验尾）成功但 out.Close()（OS flush）失败，
// 函数仍返回非 nil 错误，调用方 compressFile 会删除不完整的 dst，
// 避免原始日志文件被误删后留下损坏的压缩包。
func gzipFile(src, dst string) (retErr error) {
	in, err := openFileReadNoFollow(src)
	if err != nil {
		return err
	}
	defer in.Close() //nolint:errcheck

	out, err := openFileCreateTruncNoFollow(dst, 0o640)
	if err != nil {
		return err
	}
	// 显式捕获 out.Close() 错误（不用 nolint:errcheck）：
	// Close 失败意味着 OS 缓冲区未完全落盘，压缩包可能不完整。
	defer func() {
		if cerr := out.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()

	gz, err := gzip.NewWriterLevel(out, gzip.BestSpeed)
	if err != nil {
		return err
	}

	if _, err := io.Copy(gz, in); err != nil {
		_ = gz.Close() // 尽力关闭，忽略次级错误（io.Copy 错误已返回）
		return err
	}
	return gz.Close()
}
