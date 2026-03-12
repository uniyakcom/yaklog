// Package rotation 提供基于大小的日志文件轮转写入器。
//
// RotatingWriter 实现 [io.Writer]，将日志写入文件；当文件超过设定阈值时自动轮转：
// 旧文件重命名并追加时间戳后缀，再打开新文件继续写入。
// 可选在轮转后对旧文件做 gzip 压缩（compress 选项），并根据 maxBackups 自动清理超量旧文件。
//
// # 安全
//
// 所有文件路径均通过 [filepath.Clean] 规范化，并验证最终路径在 Dir 目录内（防止路径穿越）。
// 实现默认拒绝符号链接目录和文件；建议日志目录满足以下条件以降低权限提升风险：
//
//   - 目录权限设为 0o750（所有者读写执行，组只读，其他无权限）
//   - 目录归属运行服务的专用用户，不与其他进程共享
//   - 不使用全局可写目录（如 /tmp）作为生产日志目录
//
// # 并发
//
// [RotatingWriter.Write] 和 [RotatingWriter.Rotate] 持有同一把 sync.Mutex，保证并发安全。
//
// # 磁盘空间与可观测性
//
// 使用 [WithMinFreeBytes] 设置最低可用磁盘空间阈值；当可用空间低于阈值时，
// [New] 和 [RotatingWriter.Rotate] 返回 [ErrInsufficientDiskSpace]。
// 将此错误与 yaklog 的 OnWriteError 回调结合，可在写入失败时触发告警：
//
//	yaklog.SetOnWriteError(func(err error) {
//	    if errors.Is(err, rotation.ErrInsufficientDiskSpace) {
//	        metrics.IncrCounter("log.disk_full", 1)
//	    }
//	})
//
// 非 Unix 平台（Windows 等）上 WithMinFreeBytes 不执行实际检查，视同无限可用空间。
//
// # 基本用法
//
//	w, err := rotation.New(
//	    rotation.WithDir("/var/log/app"),
//	    rotation.WithFilename("app"),
//	    rotation.WithMaxSize(100 << 20), // 100 MiB
//	    rotation.WithMaxBackups(7),
//	    rotation.WithCompress(true),
//	    rotation.WithMinFreeBytes(200 << 20), // 至少保留 200 MiB 可用空间
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer w.Close()
//
//	logger := yaklog.New(yaklog.WithWriter(w))
package rotation
