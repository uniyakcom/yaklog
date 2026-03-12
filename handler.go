package yaklog

import (
	"context"
	"io"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/uniyakcom/yakutil/bufpool"
	"github.com/uniyakcom/yakutil/coarsetime"
)

// ─── Logger（核心类型）────────────────────────────────────────────────────────

// Logger 高性能结构化日志器。
//
// 通过 New() 创建；通过 Label / Fork / Context / To 派生子 Logger。
// Logger 对象可赋值传递（字段均为指针/切片），并发安全。
//
// 生命周期：Logger 持有的 closer（若 Out 是 io.Closer）在进程退出时
// 由调用方管理（应用级 defer closer.Close()）；yaklog 本身不注册 finalizer。
type Logger struct {
	noCopy           noCopy
	enc              encoder                  // 编码器（JSON 或 Text，构造时固化）
	out              io.Writer                // 写入目标
	closer           io.Closer                // 若 out 实现 io.Closer，则保存引用（供外部 Close）；否则 nil
	level            *atomic.Int32            // 当前最低输出级别（Label 派生共享，Fork 派生独立）
	prefix           []byte                   // 预编码固定字段（Label 追加后独立副本）
	wg               *sync.WaitGroup          // Logger 级 Post 等待组（Label 共享，Fork 独立）
	sampler          Sampler                  // 采样器（nil = 全量）
	addSrc           bool                     // 是否附加 file:line
	isJSON           bool                     // true → JSON 编码器（热路径去虚拟化标记）
	jenc             *jsonEncoder             // JSON 编码器直接指针（isJSON=true 时非 nil，去虚拟化快速路径）
	boundCtx         context.Context          // Context(ctx) 绑定；nil 表示无绑定
	timeFmt          TimeFormat               // 时间格式（To 派生时沿用，避免重置为默认值）
	consoleFmt       string                   // Console 时间格式字符串（To 派生时沿用）
	consoleColor     bool                     // true = ANSI 颜色已启用（默认 Console() 开启）
	consoleNoColor   bool                     // 镜像 Options.ConsoleNoColor，供 To() 重构编码器时查询
	consoleLevelFull bool                     // true = 完整级别名称；false（默认）= 单字母简写
	tag              string                   // 可选组件标签；Console 在级别后渲染为 [tag]，JSON 渲染为 "tag":"name"
	callerFunc       func(string, int) string // Options.CallerFunc 副本；nil = 默认 file:line
	colorScheme      *ColorScheme             // Options.ColorScheme 指针，供 To() 重构编码器时使用（共享不可变副本）

	// prefixBuf 是 prefix 的内联后备存储。
	// 当 prefix 长度 ≤ prefixInline 时，clone() 和 Label() 将 prefix 指向此数组，
	// 避免为小前缀单独进行堆分配。放置在结构体末尾以保持热路径字段偏移不变。
	prefixBuf [prefixInline]byte
}

// ─── 构造 ─────────────────────────────────────────────────────────────────────

// New 创建 Logger 实例。
//
//   - 零参：完全使用全局 Config() 的 Options
//   - 有参：完全使用传入的 Options（完整覆盖，不 merge 全局）
//
// 包级 worker 在第一次调用 New 或 Config 时惰性启动。
func New(opts ...Options) *Logger {
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	} else {
		o = loadGlobalOptions()
	}

	resolved, closer := resolveOptions(o)

	// 初始化 worker（双重保障：Config 可能未被调用）
	initGlobalWorker(resolved.QueueLen, resolved.FlushInterval)

	// 计算 ANSI 颜色是否开启：Console() sink 默认开启，除非显式设置 ConsoleNoColor=true
	var colorEnabled bool
	if _, ok := resolved.Out.(*consoleSink); ok {
		colorEnabled = !resolved.ConsoleNoColor
	}
	enc := newEncoder(resolved.Out, resolved.TimeFormat, resolved.ConsoleTimeFormat, colorEnabled, resolved.ConsoleLevelFull, resolved.CallerFunc, resolved.ColorScheme)

	lvl := new(atomic.Int32)
	lvl.Store(int32(resolved.Level))

	wg := new(sync.WaitGroup)

	// colorScheme 分配一次，所有 Label/Fork 派生共享指针，免去 clone 时共 176B 窗口内内巴内剨副本。
	cs := resolved.ColorScheme
	l := &Logger{
		enc:              enc,
		out:              resolved.Out,
		level:            lvl,
		prefix:           nil,
		wg:               wg,
		sampler:          resolved.Sampler,
		addSrc:           resolved.Source,
		boundCtx:         nil,
		timeFmt:          resolved.TimeFormat,
		consoleFmt:       resolved.ConsoleTimeFormat,
		consoleColor:     colorEnabled,
		consoleNoColor:   resolved.ConsoleNoColor,
		consoleLevelFull: resolved.ConsoleLevelFull,
		colorScheme:      &cs,
		callerFunc:       resolved.CallerFunc,
	}
	if closer != nil {
		l.closer = closer
	}
	if je, ok := enc.(*jsonEncoder); ok {
		l.isJSON = true
		l.jenc = je
	}
	return l
}

// Closer 返回底层写入器的 io.Closer（如 RotatingWriter）。
// 若 Out 不实现 io.Closer，返回 nil。调用方可在 defer 中关闭。
func (l *Logger) Closer() io.Closer { return l.closer }

// ─── 内部克隆 ─────────────────────────────────────────────────────────────────

// clone 创建浅拷贝；prefix 独立副本，enc/out/level/wg 共享。
func (l *Logger) clone() *Logger {
	// 显式构造，避免 c := *l 触发 go vet copylocks（noCopy 哨兵）。
	c := &Logger{
		enc:              l.enc,
		jenc:             l.jenc,
		out:              l.out,
		closer:           l.closer,
		level:            l.level,
		wg:               l.wg,
		sampler:          l.sampler,
		addSrc:           l.addSrc,
		isJSON:           l.isJSON,
		boundCtx:         l.boundCtx,
		timeFmt:          l.timeFmt,
		consoleFmt:       l.consoleFmt,
		consoleColor:     l.consoleColor,
		consoleNoColor:   l.consoleNoColor,
		consoleLevelFull: l.consoleLevelFull,
		tag:              l.tag,
		callerFunc:       l.callerFunc,
		colorScheme:      l.colorScheme, // 共享指针，免去复制 176B ColorScheme结构体
	}
	if plen := len(l.prefix); plen > 0 {
		if plen <= prefixInline {
			// 小前缀：复制到内联缓冲区，避免独立堆分配（Label 热路径优化）。
			copy(c.prefixBuf[:plen], l.prefix)
			c.prefix = c.prefixBuf[:plen]
		} else {
			// 大前缀：回退到堆分配，预留 prefixGrowHint 容量。
			c.prefix = make([]byte, plen, plen+prefixGrowHint)
			copy(c.prefix, l.prefix)
		}
	}
	return c
}

// ─── 派生子 Logger ────────────────────────────────────────────────────────────

// Label 追加一个固定字段，返回新 Logger（共享 level 和 wg）。
//
// 线程安全：原 Logger 不受影响；派生后各自 prefix 独立。
// val 支持 string / int / int64 / uint64 / float64 / bool，其他类型通过 any 序列化。
func (l *Logger) Label(key string, val any) *Logger {
	nl := l.clone()
	// 首次 Label 时 prefix 为 nil：指向内联缓冲区，避免 append 的隐式堆分配。
	if nl.prefix == nil {
		nl.prefix = nl.prefixBuf[:0]
	}
	if nl.isJSON {
		switch v := val.(type) {
		case string:
			nl.prefix = appendJSONKey(nl.prefix, key)
			nl.prefix = appendJSONStr(nl.prefix, v)
		case int:
			nl.prefix = appendJSONKey(nl.prefix, key)
			nl.prefix = strconv.AppendInt(nl.prefix, int64(v), 10)
		case int64:
			nl.prefix = appendJSONKey(nl.prefix, key)
			nl.prefix = strconv.AppendInt(nl.prefix, v, 10)
		case uint64:
			nl.prefix = appendJSONKey(nl.prefix, key)
			nl.prefix = strconv.AppendUint(nl.prefix, v, 10)
		case float64:
			nl.prefix = appendJSONKey(nl.prefix, key)
			nl.prefix = appendJSONFloat64(nl.prefix, v)
		case bool:
			nl.prefix = appendJSONKey(nl.prefix, key)
			if v {
				nl.prefix = append(nl.prefix, "true"...)
			} else {
				nl.prefix = append(nl.prefix, "false"...)
			}
		default:
			nl.prefix = nl.enc.appendAny(nl.prefix, key, v)
		}
	} else {
		switch v := val.(type) {
		case string:
			nl.prefix = nl.enc.appendStr(nl.prefix, key, v)
		case int:
			nl.prefix = nl.enc.appendInt64(nl.prefix, key, int64(v))
		case int64:
			nl.prefix = nl.enc.appendInt64(nl.prefix, key, v)
		case uint64:
			nl.prefix = nl.enc.appendUint64(nl.prefix, key, v)
		case float64:
			nl.prefix = nl.enc.appendFloat64(nl.prefix, key, v)
		case bool:
			nl.prefix = nl.enc.appendBool(nl.prefix, key, v)
		default:
			nl.prefix = nl.enc.appendAny(nl.prefix, key, v)
		}
	}
	// 防止 Label 链无界增长导致 OOM（与 maxMessageLen 对称的上限保护）。
	// 超出后截断 prefix 至上限，保证已写入的字段完整保留；新追加的字段被丢弃。
	if len(nl.prefix) > maxPrefixLen {
		nl.prefix = nl.prefix[:maxPrefixLen]
	}
	return nl
}

// Tag 设置一个组件标签，返回新 Logger（共享 level 和 wg）。
//
// Console 模式下标签紧跟级别标识后渲染为 [name]（洋红色），格式示例：
//
//	10:30:00.123 I [cache] key=val message
//
// JSON 模式下渲染为 "tag":"name" 字段，位于 level 字段之后。
// 标签不占用 Label 前缀空间，不参与 Label 字段编码，仅影响记录头部输出。
func (l *Logger) Tag(name string) *Logger {
	nl := l.clone()
	// sanitizeTag 将控制字符替换为 '_'，防止 tag 内嵌 '\n'/'\r' 导致日志输出那行被操纵▆4断。
	nl.tag = sanitizeTag(name)
	return nl
}

// ─── LabelBuilder（批量标签构建器）────────────────────────────────────────────

// LabelBuilder 批量追加固定字段的构建器。
//
// 相比多次调用 Label（每次 clone 一个 Logger），LabelBuilder 只在 Build 时
// 做一次 clone，减少中间 Logger 的堆分配（N 次 Label → N alloc → 1 alloc）。
//
// 使用方式：
//
//	sub := l.Labels().Str("module", "auth").Int("shard", 3).Build()
//
// LabelBuilder 使用值接收器，典型内联用法下可栈分配（零堆 alloc）。
type LabelBuilder struct {
	logger *Logger
	prefix []byte
}

// Labels 返回预填充当前 prefix 的 LabelBuilder。
//
// 后续通过 Str / Int / Bool 等方法追加字段，最后调用 Build() 生成新 Logger。
func (l *Logger) Labels() LabelBuilder {
	plen := len(l.prefix)
	// 预留 prefixGrowHint 容量：链式追加 Str/Int 时避免切片 regrowth。
	p := make([]byte, plen, plen+prefixGrowHint)
	copy(p, l.prefix)
	return LabelBuilder{
		logger: l,
		prefix: p,
	}
}

// Str 追加字符串字段。
func (b LabelBuilder) Str(key, val string) LabelBuilder {
	if b.logger.isJSON {
		b.prefix = appendJSONKey(b.prefix, key)
		b.prefix = appendJSONStr(b.prefix, val)
	} else {
		b.prefix = b.logger.enc.appendStr(b.prefix, key, val)
	}
	return b
}

// Int 追加 int 字段。
func (b LabelBuilder) Int(key string, val int) LabelBuilder {
	if b.logger.isJSON {
		b.prefix = appendJSONKey(b.prefix, key)
		b.prefix = strconv.AppendInt(b.prefix, int64(val), 10)
	} else {
		b.prefix = b.logger.enc.appendInt64(b.prefix, key, int64(val))
	}
	return b
}

// Int64 追加 int64 字段。
func (b LabelBuilder) Int64(key string, val int64) LabelBuilder {
	if b.logger.isJSON {
		b.prefix = appendJSONKey(b.prefix, key)
		b.prefix = strconv.AppendInt(b.prefix, val, 10)
	} else {
		b.prefix = b.logger.enc.appendInt64(b.prefix, key, val)
	}
	return b
}

// Uint64 追加 uint64 字段。
func (b LabelBuilder) Uint64(key string, val uint64) LabelBuilder {
	if b.logger.isJSON {
		b.prefix = appendJSONKey(b.prefix, key)
		b.prefix = strconv.AppendUint(b.prefix, val, 10)
	} else {
		b.prefix = b.logger.enc.appendUint64(b.prefix, key, val)
	}
	return b
}

// Float64 追加 float64 字段。
func (b LabelBuilder) Float64(key string, val float64) LabelBuilder {
	if b.logger.isJSON {
		b.prefix = appendJSONKey(b.prefix, key)
		b.prefix = appendJSONFloat64(b.prefix, val)
	} else {
		b.prefix = b.logger.enc.appendFloat64(b.prefix, key, val)
	}
	return b
}

// Bool 追加 bool 字段。
func (b LabelBuilder) Bool(key string, val bool) LabelBuilder {
	if b.logger.isJSON {
		b.prefix = appendJSONKey(b.prefix, key)
		if val {
			b.prefix = append(b.prefix, "true"...)
		} else {
			b.prefix = append(b.prefix, "false"...)
		}
	} else {
		b.prefix = b.logger.enc.appendBool(b.prefix, key, val)
	}
	return b
}

// Any 追加任意类型字段（通过 yakjson 序列化，有分配）。
func (b LabelBuilder) Any(key string, val any) LabelBuilder {
	b.prefix = b.logger.enc.appendAny(b.prefix, key, val)
	return b
}

// JSON 追加已序列化的原始 JSON bytes 作为字段值（零分配）。
// raw 为 nil 时跳过该字段。
func (b LabelBuilder) JSON(key string, raw []byte) LabelBuilder {
	b.prefix = b.logger.enc.appendRawJSON(b.prefix, key, raw)
	return b
}

// Build 完成构建，返回新 Logger（仅此处做一次构造）。
//
// 返回的 Logger 与原 Logger 共享 level 和 wg（与 Label 行为一致）。
func (b LabelBuilder) Build() *Logger {
	nl := &Logger{
		enc:              b.logger.enc,
		jenc:             b.logger.jenc,
		out:              b.logger.out,
		closer:           b.logger.closer,
		level:            b.logger.level,
		wg:               b.logger.wg,
		sampler:          b.logger.sampler,
		addSrc:           b.logger.addSrc,
		isJSON:           b.logger.isJSON,
		boundCtx:         b.logger.boundCtx,
		timeFmt:          b.logger.timeFmt,
		consoleFmt:       b.logger.consoleFmt,
		consoleColor:     b.logger.consoleColor,
		consoleNoColor:   b.logger.consoleNoColor,
		consoleLevelFull: b.logger.consoleLevelFull,
		tag:              b.logger.tag,
		callerFunc:       b.logger.callerFunc,
		colorScheme:      b.logger.colorScheme, // 共享指针
	}
	// prefix 上限防护：与 Label 路径保持一致，防止 LabelBuilder 链式调用绕过上限。
	if len(b.prefix) > maxPrefixLen {
		b.prefix = b.prefix[:maxPrefixLen]
	}
	// 小前缀复制到内联缓冲区，后续 Label 免去 prefix 堆分配。
	if plen := len(b.prefix); plen > 0 && plen <= prefixInline {
		copy(nl.prefixBuf[:plen], b.prefix)
		nl.prefix = nl.prefixBuf[:plen]
	} else {
		nl.prefix = b.prefix
	}
	return nl
}

// Fork 派生独立 Logger：拥有独立的 level 原子变量和 wg，不受父级 SetLevel 影响。
//
// 通常在需要对某个子模块独立控制日志级别时调用（如 db 模块 Debug 而全局 Info）。
func (l *Logger) Fork() *Logger {
	nl := l.clone()
	// 独立 level
	local := new(atomic.Int32)
	local.Store(l.level.Load())
	nl.level = local
	// 独立 wg（Wait 隔离）
	nl.wg = new(sync.WaitGroup)
	return nl
}

// Context 将 ctx 绑定到 Logger，返回新 Logger。
//
// 绑定后该 Logger 的每条日志自动提取 ctx 中注入的字段（trace_id、WithField 字段）。
// 无需再在每条 Event 上调用 .Ctx(ctx)，但 Event.Ctx(ctx) 仍可覆盖。
func (l *Logger) Context(ctx context.Context) *Logger {
	nl := l.clone()
	nl.boundCtx = ctx
	return nl
}

// To 替换输出目标，返回新 Logger。编码器根据新 Out 类型重新选择。
//
// 传入 nil 时，派生 Logger 的所有写入操作静默丢弃，并通过 OnWriteError
// 通知 [ErrWriterClosed]，同时计入 [ErrCount]。可用于彻底静默某个 Logger 分支。
//
// 派生后与原 Logger 共享 level/wg，但 out 和 enc 独立。
func (l *Logger) To(out io.Writer) *Logger {
	nl := l.clone()
	nl.out = out
	nl.closer = nil
	if c, ok := out.(io.Closer); ok {
		nl.closer = c
	}
	// 根据新 out 重新选择编码器（consoleSink → text，其他 → json），沿用父 Logger 的时间格式。
	var colorEnabled bool
	if _, ok := out.(*consoleSink); ok {
		colorEnabled = !nl.consoleNoColor
	}
	nl.consoleColor = colorEnabled
	var scheme ColorScheme
	if nl.colorScheme != nil {
		scheme = *nl.colorScheme
	}
	nl.enc = newEncoder(out, nl.timeFmt, nl.consoleFmt, colorEnabled, nl.consoleLevelFull, nl.callerFunc, scheme)
	if je, ok := nl.enc.(*jsonEncoder); ok {
		nl.isJSON = true
		nl.jenc = je
	} else {
		nl.isJSON = false
		nl.jenc = nil
	}
	return nl
}

// ─── 热更新 ───────────────────────────────────────────────────────────────────

// SetLevel 热更新最低输出级别。对所有共享此 level 指针的 Logger（Label 派生链）生效。
// Fork 派生的子 Logger 有独立指针，互不影响。
func (l *Logger) SetLevel(lvl Level) { l.level.Store(int32(lvl)) }

// GetLevel 返回当前最低输出级别。
func (l *Logger) GetLevel() Level { return Level(l.level.Load()) }

// ─── Logger 级 Wait ───────────────────────────────────────────────────────────

// Wait 等待该 Logger（及共享 wg 的所有 Label 派生子 Logger）的所有 Post 写入完成。
//
// 安全性：与 sync.WaitGroup.Wait 语义一致，Wait 期间不应有并发的 Post 调用。
func (l *Logger) Wait() { l.wg.Wait() }

// ─── 日志事件入口 ─────────────────────────────────────────────────────────────

// newEvent 创建并初始化 Event。
//
// 调用方（Trace/Debug/Info/...）已完成级别检查，此处仅做采样过滤和 Event 构建。
func (l *Logger) newEvent(lvl Level) *Event {
	if l.sampler != nil && !l.sampler.Sample(lvl, "") {
		return nil
	}

	e := eventPool.Get().(*Event) //nolint:forcetypeassert
	e.logger = l
	e.level = lvl
	e.isJSON = l.isJSON
	e.hasMsg = false
	e.msg = ""

	// buf 复用策略：
	//   Send 路径：buf（和 bufPtr）保留在 Event 上，下次直接复用，零分配。
	//   Post/Panic 路径：buf 和 bufPtr 均已被摘走（finishAndDispatch 中 steal），
	//     此处通过 GetPtr 获取新 buf（稳态 0 alloc）；包装器指针随 logEntry 流向
	//     drainRing，PutPtr 归还后再被下次 GetPtr 取出，形成闭环。
	if e.buf == nil {
		e.bufPtr = bufpool.GetPtr(defaultBufCap)
		e.buf = *e.bufPtr
	}
	e.buf = e.buf[:0]
	// JSON 路径直接调用具体类型方法，避免接口分派开销。
	if l.isJSON {
		e.buf = l.jenc.beginRecord(e.buf, coarsetime.NowNano(), lvl)
	} else {
		e.buf = l.enc.beginRecord(e.buf, coarsetime.NowNano(), lvl)
	}

	// 若设置了 Tag，在 beginRecord 头部（时间戳+级别）之后、ctx 字段之前追加组件标签。
	// Console：[tagname]（ansiTag 洋红色包裹）；JSON："tag":"name" 字段紧跟 level。
	if l.tag != "" {
		if l.isJSON {
			e.buf = appendJSONKey(e.buf, "tag")
			e.buf = appendJSONStr(e.buf, l.tag)
		} else if l.consoleColor {
			e.buf = append(e.buf, ' ')
			if te, ok := l.enc.(*textEncoder); ok {
				e.buf = append(e.buf, te.schemeTag()...)
			} else {
				e.buf = append(e.buf, ansiTag...)
			}
			e.buf = append(e.buf, '[')
			e.buf = append(e.buf, l.tag...)
			e.buf = append(e.buf, ']')
			e.buf = append(e.buf, ansiReset...)
		} else {
			e.buf = append(e.buf, " ["...)
			e.buf = append(e.buf, l.tag...)
			e.buf = append(e.buf, ']')
		}
	}

	// 提取 boundCtx 中的 trace_id 和 WithField 字段
	e.ctxStart = len(e.buf)
	if l.boundCtx != nil {
		e.buf = appendCtxFields(e.buf, l.enc, l.jenc, l.boundCtx)
	}
	e.ctxEnd = len(e.buf)

	// 追加固定 prefix 字段
	e.buf = append(e.buf, l.prefix...)

	return e
}

// appendCtxFields 从 ctx 提取 trace_id 和 WithField 注入的字段，追加到 buf。
// jenc 非 nil 时走 JSON 去虚拟化快速路径，避免接口分派。
func appendCtxFields(buf []byte, enc encoder, jenc *jsonEncoder, ctx context.Context) []byte {
	if id, ok := traceFromCtx(ctx); ok {
		buf = appendTraceIDField(buf, enc, jenc, id)
	}
	// 提取 WithField 注入的任意字段，按插入顺序追加。
	// 对 string/int/bool 等常用类型走热路径（零分配），其他类型 fallback 到 appendAny。
	if jenc != nil {
		for _, name := range fieldsFromCtx(ctx) {
			val := ctx.Value(fieldKey{name: name})
			switch v := val.(type) {
			case string:
				buf = appendJSONKey(buf, name)
				buf = appendJSONStr(buf, v)
			case int:
				buf = appendJSONKey(buf, name)
				buf = strconv.AppendInt(buf, int64(v), 10)
			case int64:
				buf = appendJSONKey(buf, name)
				buf = strconv.AppendInt(buf, v, 10)
			case uint64:
				buf = appendJSONKey(buf, name)
				buf = strconv.AppendUint(buf, v, 10)
			case float64:
				buf = appendJSONKey(buf, name)
				buf = appendJSONFloat64(buf, v)
			case bool:
				buf = appendJSONKey(buf, name)
				if v {
					buf = append(buf, "true"...)
				} else {
					buf = append(buf, "false"...)
				}
			default:
				buf = enc.appendAny(buf, name, val)
			}
		}
	} else {
		for _, name := range fieldsFromCtx(ctx) {
			val := ctx.Value(fieldKey{name: name})
			switch v := val.(type) {
			case string:
				buf = enc.appendStr(buf, name, v)
			case int:
				buf = enc.appendInt64(buf, name, int64(v))
			case int64:
				buf = enc.appendInt64(buf, name, v)
			case uint64:
				buf = enc.appendUint64(buf, name, v)
			case float64:
				buf = enc.appendFloat64(buf, name, v)
			case bool:
				buf = enc.appendBool(buf, name, v)
			default:
				buf = enc.appendAny(buf, name, val)
			}
		}
	}
	return buf
}

// Trace 创建 Trace 级别日志事件。nil 表示级别未启用（零分配）。
//
// 级别检查内联于此，disabled 路径无需调用 newEvent 函数，节省函数调用开销。
func (l *Logger) Trace() *Event {
	if Trace < Level(l.level.Load()) {
		return nil
	}
	return l.newEvent(Trace)
}

// Debug 创建 Debug 级别日志事件。nil 表示级别未启用（零分配）。
func (l *Logger) Debug() *Event {
	if Debug < Level(l.level.Load()) {
		return nil
	}
	return l.newEvent(Debug)
}

// Info 创建 Info 级别日志事件。nil 表示级别未启用（零分配）。
func (l *Logger) Info() *Event {
	if Info < Level(l.level.Load()) {
		return nil
	}
	return l.newEvent(Info)
}

// Warn 创建 Warn 级别日志事件。nil 表示级别未启用（零分配）。
func (l *Logger) Warn() *Event {
	if Warn < Level(l.level.Load()) {
		return nil
	}
	return l.newEvent(Warn)
}

// Error 创建 Error 级别日志事件。nil 表示级别未启用（零分配）。
func (l *Logger) Error() *Event {
	if Error < Level(l.level.Load()) {
		return nil
	}
	return l.newEvent(Error)
}

// Panic 创建 Panic 级别日志事件。Send/Post 后同步写入日志，然后调用 PanicFunc（默认为内置 panic）。
// PanicFunc 默认可被 defer/recover 捕获；可通过 SetPanicFunc 替换，与 Fatal 的差异在于可恢复性。
func (l *Logger) Panic() *Event {
	if Panic < Level(l.level.Load()) {
		return nil
	}
	return l.newEvent(Panic)
}

// Fatal 创建 Fatal 级别日志事件。Send/Post 后排空队列并调用 FatalFunc（默认为 os.Exit(1)）。
func (l *Logger) Fatal() *Event {
	if Fatal < Level(l.level.Load()) {
		return nil
	}
	return l.newEvent(Fatal)
}

// Event 创建指定级别的日志事件。nil 表示级别未启用（零分配）。
//
// 适用于级别由运行时决定的场景（如 slog 适配器），避免 switch 分派开销。
func (l *Logger) Event(lvl Level) *Event {
	if lvl < Level(l.level.Load()) {
		return nil
	}
	return l.newEvent(lvl)
}

// ─── 进程级 Fatal 清理 ────────────────────────────────────────────────────────

// drainAndExit 等待包级 worker 排空后调用 FatalFunc 退出进程（Fatal 路径使用）。
//
// 若 FatalFunc 被测试替换为非退出函数，则该函数返回后继续执行（调用方无需特殊处理）。
func drainAndExit() {
	// 排空已入队的 Post 任务，保证 Fatal 前的日志全部落盘。
	// 无论是否替换了 FatalFunc，都先执行排空操作，防止日志丢失。
	Wait()

	// 仅在默认路径（os.Exit）中关闭 worker goroutine。
	// 自定义路径（测试拦截）保留 worker 活跃，后续 Post 仍可正常投递。
	if !fatalFuncCustom.Load() {
		_ = shutdownGlobalWorker()
	}
	(*fatalFuncPtr.Load())(1)
}
