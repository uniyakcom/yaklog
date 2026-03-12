package rotation

// options 保存 RotatingWriter 的配置。
// 零值无效；必须通过 New() + Option 函数初始化。
type options struct {
	dir          string // 必填：日志文件存储目录（绝对路径）
	filename     string // 必填：基础文件名（不含扩展名）
	ext          string // 文件扩展名，默认 ".log"
	maxSize      int64  // 单文件最大字节数，默认 100 MiB
	maxBackups   int    // 保留历史备份数，0 表示无限
	maxAge       int    // 备份文件保留天数，0 表示永不过期
	compress     bool   // 轮转后对旧文件 gzip 压缩
	localTime    bool   // 备份文件名时间戳使用本地时间（false = UTC）
	minFreeBytes int64  // 打开文件前磁盘最低可用字节数，0 表示不检查
	maxTotalSize int64  // 所有备份文件总大小上限（字节），0 表示不限制
	// onWriteErrFn 写入失败时的回调函数；参数为失败原因和未写入的数据。
	// nil 时回退至 os.Stderr，保证日志不丢失。
	onWriteErrFn func(err error, p []byte)
}

// Option 是 RotatingWriter 的配置函数。
type Option func(*options)

// WithDir 设置日志文件存储目录（必填）。
// dir 必须是已存在的绝对路径。
func WithDir(dir string) Option {
	return func(o *options) { o.dir = dir }
}

// WithFilename 设置基础文件名（不含扩展名）（必填）。
// 例如 "app" 对应文件 "app.log"。
func WithFilename(name string) Option {
	return func(o *options) { o.filename = name }
}

// WithExt 设置文件扩展名，默认 ".log"。
func WithExt(ext string) Option {
	return func(o *options) { o.ext = ext }
}

// WithMaxSize 设置单文件最大字节数（默认 100 MiB）。
// size 必须 >= 1024。
func WithMaxSize(size int64) Option {
	return func(o *options) { o.maxSize = size }
}

// WithMaxBackups 设置保留历史备份文件数（0 表示不限制）。
// 上限为 maxAllowedBackups。
func WithMaxBackups(n int) Option {
	return func(o *options) { o.maxBackups = n }
}

// WithMaxAge 设置备份文件的最大保留天数（0 表示永不过期）。
// 超过天数的旧文件在下次轮转时被删除。上限为 maxAllowedMaxAge（3650 天）。
func WithMaxAge(days int) Option {
	return func(o *options) { o.maxAge = days }
}

// WithCompress 控制是否在轮转后对旧文件进行 gzip 压缩。
func WithCompress(c bool) Option {
	return func(o *options) { o.compress = c }
}

// WithLocalTime 控制备份文件名时间戳是否使用本地时间（false = UTC）。
func WithLocalTime(local bool) Option {
	return func(o *options) { o.localTime = local }
}

// WithMinFreeBytes 设置打开日志文件前磁盘最低可用字节数。
// 若目录所在文件系统的可用空间低于此阈值，[New] 和轮转将返回 [ErrInsufficientDiskSpace]。
// 0（默认）表示不进行磁盘空间检查。非 Unix 平台忽略此选项。
func WithMinFreeBytes(n int64) Option {
	return func(o *options) { o.minFreeBytes = n }
}

// WithMaxTotalSize 设置所有备份文件的总大小上限（字节）。
// 每次轮转清理后，若全部备份的体积之和超出此阈值，则从最旧的文件开始删除，
// 直至总大小低于阈值。0（默认）表示不限制总磁盘用量。
func WithMaxTotalSize(bytes int64) Option {
	return func(o *options) { o.maxTotalSize = bytes }
}

// WithOnWriteError 设置写入失败回调。
// 当底层文件返回错误时，cb 接收原始错误和未写入的数据切片，
// 可用于告警、应急落地或指标计数。
// 未调用此选项时，默认行为为回退写入 os.Stderr。
// cb 不应随意持有 p 引用：调用返回后 p 的内容可能被复用。
func WithOnWriteError(cb func(err error, p []byte)) Option {
	return func(o *options) { o.onWriteErrFn = cb }
}

// applyDefaults 填充未设置字段的默认值。
func applyDefaults(o *options) {
	if o.ext == "" {
		o.ext = defaultExt
	}
	if o.maxSize <= 0 {
		o.maxSize = defaultMaxSize
	}
	if o.maxBackups < 0 {
		o.maxBackups = 0
	}
	if o.maxAge < 0 {
		o.maxAge = defaultMaxAge
	}
	if o.maxAge > maxAllowedMaxAge {
		o.maxAge = maxAllowedMaxAge
	}
	if o.maxBackups > maxAllowedBackups {
		o.maxBackups = maxAllowedBackups
	}
}
