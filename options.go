package yaklog

import (
	"io"
	"os"
	"sync/atomic"
	"time"
)

// ─── ColorScheme ──────────────────────────────────────────────────────────────

// ColorScheme 定义 Console 输出的 ANSI 颜色方案，允许用户替换任意元素的颜色。
// 零值（空字符串）字段使用内置默认色，无需填写全部字段。
// 仅在 Out = Console() 时生效；JSON 格式及 ConsoleNoColor=true 时忽略所有颜色配置。
//
// ANSI 转义码格式："\x1b[<code>m"，例如 "\x1b[32m" 为绿色，"\x1b[1;33m" 为加粗黄色。
// 支持的标准颜色代码：30–37（暗色前景）、90–97（亮色前景）、1;3x（加粗前景）等。
// 终端 256 色格式："\x1b[38;5;<n>m"；真彩色格式："\x1b[38;2;<r>;<g>;<b>m"。
type ColorScheme struct {
	Trace  string // Trace 级别色（含 level 标识及 Msg 文本）；默认 "\x1b[90m"（暗灰）
	Debug  string // Debug 级别色；默认 "\x1b[36m"（青色）
	Info   string // Info 级别色；默认 "\x1b[32m"（绿色）
	Warn   string // Warn 级别色；默认 "\x1b[33m"（黄色）
	Error  string // Error 级别色；默认 "\x1b[31m"（红色）
	Panic  string // Panic 级别色；默认 "\x1b[1;35m"（加粗洋红）
	Fatal  string // Fatal 级别色；默认 "\x1b[1;31m"（加粗红色）
	Time   string // 时间戳色；默认 "\x1b[2m"（暗淡 dim）
	Key    string // 字段 key= 色；默认 "\x1b[34m"（暗蓝）
	Tag    string // Tag 标签色；默认 "\x1b[38;5;166m"（橙色 256色）
	Source string // source 字段值色；默认 "\x1b[93m"（亮黄）
}

// ─── Options 配置结构体 ───────────────────────────────────────────────────────

// Options 是 Logger 全量配置结构体，零值即可直接使用（零值 = 全部默认值）。
//
// 零值含义：
//   - Level      → Info（int8 零值恰好是 Info 的值 0）
//   - Out        → nil，New() 中 fallback 到 JSON → os.Stderr
//   - FilePath   → 空，Out 非 nil 时忽略；Out 为 nil 且 FilePath 非空时自动 Save
//   - TimeFormat → TimeRFC3339Milli（iota=0）
//   - QueueLen   → 0，New() 中补 defaultQueueLen
//   - 其余 bool  → false
type Options struct {
	// ── 基础 ──────────────────────────────────────────────────────────────
	Level    Level     // 最低输出级别，零值为 Info
	Out      io.Writer // 输出目标；nil 且 FilePath 空 → JSON os.Stderr
	FilePath string    // 日志文件路径；Out 为 nil 时自动 Save(FilePath)
	Source   bool      // 是否附加调用方 file:line（有轻微性能开销）
	// CallerFunc 自定义 source 字段的显示值，接收原始 file 路径和行号，返回最终写入日志的字符串。
	// 返回空字符串则完全省略该字段。nil 表示使用默认行为（完整 file:line）。
	// 常用于：只保留文件名（filepath.Base(file)）、路径脱敏、相对路径截取等场景。
	CallerFunc func(file string, line int) string
	TimeFormat TimeFormat // 时间戳格式，零值为 TimeRFC3339Milli
	Sampler    Sampler    // 采样器，nil 表示全量输出

	// ── 异步 Post 队列 ────────────────────────────────────────────────────
	QueueLen      int           // 包级 worker channel 容量；0 → defaultQueueLen
	FlushInterval time.Duration // 周期刷写间隔；0 → 100ms

	// ── 文件轮转（Out = Save(...) 时生效）────────────────────────────────
	FileMaxSize    int  // 单文件最大体积（MB）；0 → 100
	FileMaxAge     int  // 备份文件最长保留天数；0 → 不限制
	FileMaxBackups int  // 最多保留旧文件数；0 → 不限
	FileCompress   bool // 是否压缩旧文件
	FileLocalTime  bool // 备份文件名时间戳使用本地时间（false = UTC）

	// ── Console 格式（Out = Console() 时生效）────────────────────────────
	// ANSI 颜色：Console() 默认启用，设 ConsoleNoColor=true 可关闭。
	// 启用后：级别有独立色彩，字段 key 以灰度（dim）弱化显示。
	ConsoleNoColor bool // true = 关闭 ANSI 颜色输出（默认 false，即颜色默认开启）
	// 级别显示：默认单字母简写（T/D/I/W/E/P/F），设 ConsoleLevelFull=true 使用完整名称（TRACE/DEBUG/INFO...）。
	ConsoleLevelFull  bool        // true = 完整级别名（TRACE/INFO/WARN...）；false（默认）= 单字母简写（T/I/W...）
	ConsoleTimeFormat string      // Console 时间格式；空 → ConsoleTimeMilli
	ColorScheme       ColorScheme // Console ANSI 颜色方案；零值字段使用内置默认色；ConsoleNoColor=true 时忽略
}

// ─── 全局默认 Options ─────────────────────────────────────────────────────────

var globalOptions atomic.Pointer[Options]

// cachedDefaultLogger 缓存 FromCtx 回退时使用的默认 Logger 单例。
// Config() 调用时置 nil 失效；下次 defaultLogger() 时重建。
var cachedDefaultLogger atomic.Pointer[Logger]

// Config 设置全局默认 Options，影响后续所有 New()（零参）调用。并发安全（原子替换）。
//
// 可多次调用，但 QueueLen 和 FlushInterval 仅首次调用（或首次 New()）时生效——
// 包级 worker 由 sync.Once 保证只启动一次；其余字段每次调用均生效。
// 建议在 main() 最开始调用一次完成初始化。
func Config(opts Options) {
	o := opts
	// 在 Config 时初始化全局 worker（应用队列和刷写间隔参数）
	qLen := o.QueueLen
	interval := o.FlushInterval
	if qLen == 0 {
		qLen = defaultQueueLen
	}
	if interval == 0 {
		interval = 100 * time.Millisecond
	}
	initGlobalWorker(qLen, interval)
	globalOptions.Store(&o)
	// 清除默认 Logger 缓存，下次 defaultLogger() 会以新配置重建
	cachedDefaultLogger.Store(nil)
}

// loadGlobalOptions 返回当前全局 Options 的副本。nil 时返回零值 Options。
func loadGlobalOptions() Options {
	if p := globalOptions.Load(); p != nil {
		return *p
	}
	return Options{}
}

// resolveOptions 将 Options 中的零值字段填入运行时默认值，并展开 lazySave。
// 返回最终有效配置和已打开的 io.WriteCloser（若需要关闭，否则 nil）。
func resolveOptions(o Options) (opts Options, closer io.Closer) {
	opts = o

	// Out 决策
	if opts.Out == nil {
		if opts.FilePath != "" {
			// FilePath 非空 → 自动 Save
			maxSize := opts.FileMaxSize
			if maxSize <= 0 {
				maxSize = 100
			}
			wc, err := openSave(opts.FilePath, maxSize, opts.FileMaxBackups, opts.FileMaxAge, opts.FileCompress, opts.FileLocalTime)
			if err != nil {
				// 无法打开文件，fallback 到 stderr
				opts.Out = os.Stderr
			} else {
				opts.Out = wc
				closer = wc
			}
		} else {
			opts.Out = os.Stderr
		}
	} else if ls, ok := opts.Out.(*lazySave); ok {
		// lazySave 占位符（Save() 无参时）
		path := ls.path
		if path == "" {
			path = opts.FilePath
		}
		if path == "" {
			opts.Out = os.Stderr
		} else {
			maxSize := opts.FileMaxSize
			if maxSize <= 0 {
				maxSize = 100
			}
			wc, err := openSave(path, maxSize, opts.FileMaxBackups, opts.FileMaxAge, opts.FileCompress, opts.FileLocalTime)
			if err != nil {
				opts.Out = os.Stderr
			} else {
				opts.Out = wc
				closer = wc
			}
		}
	}

	// ConsoleTimeFormat 默认
	if opts.ConsoleTimeFormat == "" {
		opts.ConsoleTimeFormat = ConsoleTimeMilli
	}

	// QueueLen / FlushInterval（worker 已由 Config 或 New 内 initGlobalWorker 初始化）
	if opts.QueueLen == 0 {
		opts.QueueLen = defaultQueueLen
	}
	if opts.FlushInterval == 0 {
		opts.FlushInterval = 100 * time.Millisecond
	}

	return opts, closer
}

// defaultLogger 返回包级默认 Logger 单例（仅供 FromCtx 回退使用）。
// 首次调用时以全局 Options 构建并缓存；Config() 调用后自动失效重建。
func defaultLogger() *Logger {
	if l := cachedDefaultLogger.Load(); l != nil {
		return l
	}
	l := New()
	// CAS 保证只有一个实例存入；竞争下建了多个，GC 回收，配置相同无副作用。
	if cachedDefaultLogger.CompareAndSwap(nil, l) {
		return l
	}
	if cached := cachedDefaultLogger.Load(); cached != nil {
		return cached
	}
	// 竞争期间若被 Config() 置空，退回当前已构造实例，确保绝不返回 nil。
	return l
}
