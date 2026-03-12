package yaklog

// ─── 日志级别 ─────────────────────────────────────────────────────────────────

// Level 日志输出级别，零值为 Info。
type Level int8

const (
	Trace Level = iota - 2 // -2，最低追踪级别
	Debug                  // -1
	Info                   // 0，零值，默认最低输出级别
	Warn                   // 1
	Error                  // 2
	Panic                  // 3，Send/Post 后调用 PanicFunc（默认为内置 panic），可被 defer/recover 捕获
	Fatal                  // 4，Send/Post 后调用 FatalFunc（默认为 os.Exit(1)）
)

// ─── 时间格式 ─────────────────────────────────────────────────────────────────

// TimeFormat JSON 输出时间戳格式，零值为 TimeRFC3339Milli。
type TimeFormat uint8

const (
	TimeRFC3339Milli TimeFormat = iota // 默认，"2026-03-08T10:30:00.123Z"
	TimeUnixSec                        // 秒级 Unix 时间戳，如 1741426200
	TimeUnixMilli                      // 毫秒级，如 1741426200123
	TimeUnixNano                       // 纳秒级，如 1741426200123456789
	TimeOff                            // 不输出时间字段
)

// ─── Console 时间格式 ─────────────────────────────────────────────────────────
//
// ConsoleTimeMilli / ConsoleTimeMicro（仅时间）走缓存快速路径；
// 其余格式走 time.AppendFormat 慢路径，功能完整。

// ConsoleTimeMilli Console 输出时间格式，仅时间，毫秒精度（默认）。
// 示例：16:38:43.152
const ConsoleTimeMilli = "15:04:05.000"

// ConsoleTimeMicro Console 输出时间格式，仅时间，微秒精度。
// 示例：16:38:43.152148
const ConsoleTimeMicro = "15:04:05.000000"

// ConsoleTimeDateMilli Console 输出时间格式，日期+时间，毫秒精度。
// 示例：2026-03-08 16:38:43.152
const ConsoleTimeDateMilli = "2006-01-02 15:04:05.000"

// ConsoleTimeDateMicro Console 输出时间格式，日期+时间，微秒精度。
// 示例：2026-03-08 16:38:43.152148
const ConsoleTimeDateMicro = "2006-01-02 15:04:05.000000"

// ConsoleTimeRFC3339Milli Console 输出时间格式，RFC3339 含毫秒与时区偏移。
// 示例：2026-03-08T16:38:43.152+08:00
const ConsoleTimeRFC3339Milli = "2006-01-02T15:04:05.000Z07:00"

// ─── 内部常量 ─────────────────────────────────────────────────────────────────

const (
	defaultBufCap   = 512   // 单条记录初始缓冲容量（字节）
	maxMessageLen   = 8192  // 消息字段最大字节数，超出截断并追加 truncateSuffix
	defaultQueueLen = 4096  // 包级 worker channel 默认容量
	maxQueueLen     = 65536 // 包级 worker channel 上限
	prefixGrowHint  = 128   // clone / Labels 预留 prefix 增长容量（避免单次 Label 追加触发 regrowth）
	prefixInline    = 64    // Logger 内联前缀缓冲大小；Label 链中小于此值时免去独立 heap alloc

	// truncateSuffix 消息截断时追加的标记字符串。
	// truncateMsg 在 maxMessageLen-len(truncateSuffix) 处截断，保证最终消息严格 ≤ maxMessageLen 字节。
	truncateSuffix = "[truncated]"

	// maxPrefixLen 是 Logger prefix（Label 固定字段集合）允许的最大字节数。
	// 超出上限时 Label() 跳过追加，防止无界链式调用导致 prefix 膨胀引发 OOM。
	maxPrefixLen = 16 * 1024 // 16 KB
	// maxCtxFields 是单个 context 链上允许的新字段数（不含 trace_id）。
	// 超出后 WithField 静默忽略新字段，防止高并发请求使用全不同键注入 ctx 时 fieldNamesKey slice 无界增长。
	maxCtxFields = 64

	// maxTagLen 是 Tag 名字最大允许字节数，超出时截断。
	maxTagLen = 64
	// maxEventBufCap 是 Event 内联 buf 归还到 eventPool 时允许保留的最大容量。
	// Send 路径会将 buf 保留在 Event 中以供下条复用（阵分配优化），
	// 但如果单条日志包含超大字段（如巨型 Any() 对象）导致 buf 膨胀，
	// 就不保留该 buf（释放大内存块），下次从 bufpool 重新获取。
	maxEventBufCap = 32 * 1024 // 32 KB
)
