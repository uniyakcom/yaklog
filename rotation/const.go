package rotation

// 容量与安全限制常量——覆盖与跨包安全共同依赖。

const (
	// defaultMaxSize 默认单文件最大字节数（100 MiB）。
	defaultMaxSize = int64(100 << 20)

	// defaultExt 默认日志文件后缀。
	defaultExt = ".log"

	// maxAllowedBackups 上限——防止误配置导致目录爆满。
	maxAllowedBackups = 1000

	// minMaxSize 最小允许配置的单文件大小（1 KiB）。
	minMaxSize = int64(1 << 10)

	// defaultMaxAge 默认日志文件保留天数（30 天）；0 表示不限制。
	defaultMaxAge = 0

	// maxAllowedMaxAge 年龄上限（3650 天 ≈ 10 年），防止误配置。
	maxAllowedMaxAge = 3650

	// maxCompressConcurrent 并发压缩 goroutine 上限；溢出时降级为同步压缩，
	// 防止慢速存储（NFS / SAN）场景下 goroutine 无限累积。
	maxCompressConcurrent = 2
)
