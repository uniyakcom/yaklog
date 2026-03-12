package yaklog

import (
	"context"
	"fmt"
	"io"
	"math"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/uniyakcom/yakutil"
	"github.com/uniyakcom/yakutil/bufpool"
)

// ─── Event（链式日志事件）────────────────────────────────────────────────────

// Event 代表一条正在构建的日志事件。
//
// 通过 Logger.Info() / Logger.Debug() 等方法获取；级别未启用时返回 nil。
// 所有链式方法均 nil-safe：(*Event)(nil).Str(...) 合法且返回 nil，零分配。
//
// 生命周期：Event 由 eventPool（sync.Pool）管理；
// 调用 Send() / Post() 后 buf 所有权转移，Event 归还 pool。
// Send / Post 调用后不得再引用该 Event。
type Event struct {
	logger *Logger
	level  Level
	buf    []byte  // 从 bufpool 借出；Send/Post 后转移所有权
	bufPtr *[]byte // bufpool.GetPtr 返回的包装器；与 buf 绑定，生命周期：GetPtr→Event→Post→logEntry→drainRing→PutPtr
	// Send 路径同 buf 一起保留；Post 路径同 buf 一起转移；nil 表示 buf 由 Get（boxing）分配
	msg      string          // 由 Msg() 设置
	hasMsg   bool            // 是否调用了 Msg()
	isJSON   bool            // JSON 编码器标记（热路径去虚拟化，避免接口分派）
	boundCtx context.Context // 由 Ctx() 覆盖；nil 则继承 logger.boundCtx
	// ctxStart/ctxEnd 记录 buf 中 ctx 字段区间（beginRecord 后、prefix 前）。
	// Event.Ctx() 覆盖时用于原位替换已写入的 struct ctx 字段，避免重复输出。
	ctxStart int
	ctxEnd   int
	// callerPC 由 Caller() 在用户调用点采集。
	// 非零时优先于 Options.Source 全局捕捉，可获得更准确的行号。
	callerPC [1]uintptr
}

// eventPool *Event 对象池，避免每次日志分配。
var eventPool = sync.Pool{New: func() any { return new(Event) }}

// ─── ctx 覆盖 ─────────────────────────────────────────────────────────────────

// Ctx 以 ctx 替换此事件的上下文（覆盖 Logger 的 boundCtx）。
//
// 可从 ctx 中提取 WithTrace / WithField 注入的字段。
func (e *Event) Ctx(ctx context.Context) *Event {
	if e == nil {
		return nil
	}
	e.boundCtx = ctx
	return e
}

// Caller 为此事件附加调用方 file:line（source 字段）。
//
// 适合在特定日志点按需附加定位信息，无需全局开启 Options.Source。
// 若 Options.Source 已全局启用，Caller() 捕获的 PC 更准确（在链式调用处采集，而非 Send/Post 处）。
// 调用后返回同一 *Event，可继续链式调用。
func (e *Event) Caller() *Event {
	if e == nil {
		return nil
	}
	// skip=2: runtime.Callers 自身 + Caller 方法；下一帧即用户调用点。
	runtime.Callers(2, e.callerPC[:])
	return e
}

// ─── 消息字段 ─────────────────────────────────────────────────────────────────

// Msg 设置日志消息，返回 *Event（未终结，可继续追加字段或直接调用 Send/Post）。
//
// 若不调用 Msg，日志记录中不含 msg 字段（零分配）。
func (e *Event) Msg(msg string) *Event {
	if e == nil {
		return nil
	}
	e.msg = truncateMsg(msg)
	e.hasMsg = true
	return e
}

// ─── 字段追加方法（nil-safe）─────────────────────────────────────────────────

// Str 追加字符串字段。
func (e *Event) Str(key, val string) *Event {
	if e == nil {
		return nil
	}
	if e.isJSON {
		e.buf = appendJSONStr(appendJSONKeyFast(e.buf, key), val)
	} else {
		e.buf = e.logger.enc.appendStr(e.buf, key, val)
	}
	return e
}

// Int 追加 int 字段。
func (e *Event) Int(key string, val int) *Event {
	if e == nil {
		return nil
	}
	if e.isJSON {
		e.buf = strconv.AppendInt(appendJSONKeyFast(e.buf, key), int64(val), 10)
	} else {
		e.buf = e.logger.enc.appendInt64(e.buf, key, int64(val))
	}
	return e
}

// Int64 追加 int64 字段。
func (e *Event) Int64(key string, val int64) *Event {
	if e == nil {
		return nil
	}
	if e.isJSON {
		e.buf = strconv.AppendInt(appendJSONKeyFast(e.buf, key), val, 10)
	} else {
		e.buf = e.logger.enc.appendInt64(e.buf, key, val)
	}
	return e
}

// Uint64 追加 uint64 字段。
func (e *Event) Uint64(key string, val uint64) *Event {
	if e == nil {
		return nil
	}
	if e.isJSON {
		e.buf = strconv.AppendUint(appendJSONKeyFast(e.buf, key), val, 10)
	} else {
		e.buf = e.logger.enc.appendUint64(e.buf, key, val)
	}
	return e
}

// Float64 追加 float64 字段（NaN/Inf 安全）。
func (e *Event) Float64(key string, val float64) *Event {
	if e == nil {
		return nil
	}
	if e.isJSON {
		buf := appendJSONKeyFast(e.buf, key)
		switch {
		case math.IsNaN(val):
			e.buf = append(buf, `"NaN"`...)
		case math.IsInf(val, 1):
			e.buf = append(buf, `"Inf"`...)
		case math.IsInf(val, -1):
			e.buf = append(buf, `"-Inf"`...)
		default:
			e.buf = strconv.AppendFloat(buf, val, 'f', -1, 64)
		}
	} else {
		e.buf = e.logger.enc.appendFloat64(e.buf, key, val)
	}
	return e
}

// Bool 追加 bool 字段。
func (e *Event) Bool(key string, val bool) *Event {
	if e == nil {
		return nil
	}
	if e.isJSON {
		buf := appendJSONKeyFast(e.buf, key)
		if val {
			e.buf = append(buf, "true"...)
		} else {
			e.buf = append(buf, "false"...)
		}
	} else {
		e.buf = e.logger.enc.appendBool(e.buf, key, val)
	}
	return e
}

// Time 追加 time.Time 字段（使用 Logger 配置的时间格式）。
func (e *Event) Time(key string, val time.Time) *Event {
	if e == nil {
		return nil
	}
	if e.isJSON {
		var tb [36]byte
		buf := appendJSONKeyFast(e.buf, key)
		e.buf = append(append(append(buf, '"'), val.AppendFormat(tb[:0], time.RFC3339Nano)...), '"')
	} else {
		e.buf = e.logger.enc.appendTime(e.buf, key, val)
	}
	return e
}

// Dur 追加 time.Duration 字段（以毫秒浮点数表示）。
func (e *Event) Dur(key string, val time.Duration) *Event {
	if e == nil {
		return nil
	}
	if e.isJSON {
		e.buf = appendJSONStr(appendJSONKeyFast(e.buf, key), val.String())
	} else {
		e.buf = e.logger.enc.appendDur(e.buf, key, val)
	}
	return e
}

// Err 追加 error 字段（key = "error"）。err 为 nil 时不追加任何字段。
// Console 模式下，error 字段的展示色与当前日志级别一致（如 Error 级别红色）。
func (e *Event) Err(err error) *Event {
	if e == nil {
		return nil
	}
	if err == nil {
		return e
	}
	if e.isJSON {
		// 内联固定 key：省去 appendErr → appendJSONKey 的间接调用。
		e.buf = appendJSONStr(append(e.buf, ',', '"', 'e', 'r', 'r', 'o', 'r', '"', ':'), err.Error())
	} else {
		// 传入当前级别对应的 ANSI 色，必要时由 textEncoder.appendErr 着色 value。
		e.buf = e.logger.enc.appendErr(e.buf, err, e.logger.enc.levelColorOf(e.level))
	}
	return e
}

// AnErr 追加 error 字段，使用自定义键名。err 为 nil 时不追加。
func (e *Event) AnErr(key string, err error) *Event {
	if e == nil {
		return nil
	}
	if err == nil {
		return e
	}
	if e.isJSON {
		e.buf = appendJSONStr(appendJSONKeyFast(e.buf, key), err.Error())
	} else {
		e.buf = e.logger.enc.appendStr(e.buf, key, err.Error())
	}
	return e
}

// Bytes 追加 []byte 字段（零拷贝 B2S，适合 UTF-8 数据）。
//
// B2S 在 Bytes() 调用期间将 val 临时转为 string 并传入 appendJSONStr；
// appendJSONStr 在本次调用内同步将內容拷贝进 e.buf，之后不再持有 val 引用。
// 因此 val 只需在 Bytes() 调用期间保持有效，无需延续至 Send/Post。
func (e *Event) Bytes(key string, val []byte) *Event {
	if e == nil {
		return nil
	}
	if e.isJSON {
		e.buf = appendJSONStr(appendJSONKeyFast(e.buf, key), yakutil.B2S(val))
	} else {
		e.buf = e.logger.enc.appendStr(e.buf, key, yakutil.B2S(val))
	}
	return e
}

// Any 追加任意类型字段（通过 yakjson 序列化；会分配）。
func (e *Event) Any(key string, val any) *Event {
	if e == nil {
		return nil
	}
	e.buf = e.logger.enc.appendAny(e.buf, key, val)
	return e
}

// JSON 追加已序列化的原始 JSON bytes 作为字段值（零分配）。
//
// JSON 输出模式下 raw 直接嵌入为嵌套对象，不做二次转义。
// Console 输出模式下 raw 作为字段值原样输出。
// 调用方负责保证 raw 是合法 JSON；raw 为 nil 时跳过该字段。
//
// Console 模式注意：若 raw 包含换行符（如美化格式的 JSON），输出行会被拆分，
// 可能导致结构化日志解析器丢失字段关联。建议 Console 模式下传入紧凑格式 JSON。
func (e *Event) JSON(key string, raw []byte) *Event {
	if e == nil {
		return nil
	}
	e.buf = e.logger.enc.appendRawJSON(e.buf, key, raw)
	return e
}

// Stringer 追加实现 fmt.Stringer 接口的对象（调用 .String()）。
func (e *Event) Stringer(key string, val fmt.Stringer) *Event {
	if e == nil {
		return nil
	}
	if val == nil {
		return e
	}
	if e.isJSON {
		e.buf = appendJSONStr(appendJSONKeyFast(e.buf, key), val.String())
	} else {
		e.buf = e.logger.enc.appendStr(e.buf, key, val.String())
	}
	return e
}

// ─── 终结方法 ─────────────────────────────────────────────────────────────────

// Send 同步写入：在调用方 goroutine 中直接 io.Write，不经过 worker 队列。
//
// 写入完成后 goroutine 继续，适合对延迟敏感的场景或需要等待结果的场景。
// 调用后不得再引用该 Event。
func (e *Event) Send() {
	if e == nil {
		return
	}
	e.finishAndDispatch(false)
}

// Post 异步写入：将 buf 投递给包级 worker goroutine，不阻塞调用方。
//
// 适合大并发高吞吐场景。进程退出前应调用 yaklog.Wait() 或 logger.Wait() 排空。
// 调用后不得再引用该 Event。
func (e *Event) Post() {
	if e == nil {
		return
	}
	e.finishAndDispatch(true)
}

// finishAndDispatch 完成 buf 编码并派发（同步或异步）。
//
// buf 所有权策略：
//   - Send 路径：同步写入后保留 buf 在 Event 上（reset 为 [:0]），下次 newEvent 直接复用，
//     消除 bufpool.Get 的 sync.Pool 装箱分配（24B/1alloc → 0alloc）。
//   - Post/Panic 路径：buf 摘走（e.buf = nil），所有权转移给 worker 或立即写入后归还 bufpool。
func (e *Event) finishAndDispatch(async bool) {
	l := e.logger
	out := l.out

	// source field：per-event Caller() 优先（在用户链式调用处采集，更准确）；
	// 否则全局 addSrc 在 Send/Post 处采采样（skip=3）。
	if e.callerPC[0] != 0 {
		if e.isJSON {
			frames := runtime.CallersFrames(e.callerPC[:])
			f, _ := frames.Next()
			if f.File != "" {
				// appendSourceJSONField 对 file 路径转义，消除 Windows '\' 导致的 JSON 损坏。
				e.buf = appendSourceJSONField(e.buf, f.File, f.Line)
			}
		} else {
			e.buf = l.enc.appendSource(e.buf, e.callerPC[:])
		}
	} else if l.addSrc {
		var pcs [1]uintptr
		// skip=3: runtime.Callers, finishAndDispatch, Send/Post
		if n := runtime.Callers(3, pcs[:]); n > 0 {
			if e.isJSON {
				// JSON 路径内联：省去 encoder 接口分派。
				// appendSourceJSONField 对 file 路径转义，消除 Windows '\' 导致的 JSON 损坏。
				frames := runtime.CallersFrames(pcs[:n])
				f, _ := frames.Next()
				if f.File != "" {
					e.buf = appendSourceJSONField(e.buf, f.File, f.Line)
				}
			} else {
				e.buf = l.enc.appendSource(e.buf, pcs[:n])
			}
		}
	}

	// Event.Ctx() 覆盖场景：用新 ctx 的字段原位替换 newEvent 已写入的 ctx 字段。
	// 这样 ctx 字段顺序不变，且不会因 ctx 派生链而重复输出相同字段。
	// tail = e.buf[ctxEnd:] 含 prefix + 用户字段，整体保留。
	if e.boundCtx != nil && e.boundCtx != l.boundCtx {
		tailLen := len(e.buf) - e.ctxEnd
		tailPtr := bufpool.GetPtr(tailLen)
		tail := (*tailPtr)[:tailLen]
		copy(tail, e.buf[e.ctxEnd:])
		// 重建：beginRecord 头 + 新 ctx 字段 + 尾部（prefix 与用户字段）
		e.buf = appendCtxFields(e.buf[:e.ctxStart], l.enc, l.jenc, e.boundCtx)
		e.buf = append(e.buf, tail...)
		bufpool.PutPtr(tailPtr)
	}

	// finalize：JSON 热路径内联消除接口分派。
	if e.isJSON {
		if e.hasMsg {
			e.buf = append(e.buf, `,"msg":`...)
			e.buf = appendJSONStr(e.buf, e.msg)
		}
		e.buf = append(e.buf, '}', '\n')
	} else {
		e.buf = l.enc.finalize(e.buf, e.msg, e.hasMsg, l.enc.levelColorOf(e.level))
	}

	// EventSink 快速路径：无 ctx 绑定时跳过 effectiveCtx + eventSinkFromCtx 的函数调用。
	if e.boundCtx != nil || l.boundCtx != nil {
		if sink := eventSinkFromCtx(e.effectiveCtx(l)); sink != nil {
			// 传递 buf 副本（sink 不应持有 buf 引用）
			cp := make([]byte, len(e.buf))
			copy(cp, e.buf)
			safeEmitEventSink(sink, e.level, e.msg, cp)
		}
	}

	lvl := e.level
	panicMsg := e.msg
	wg := l.wg

	// ── buf 所有权分支 ──────────────────────────────────────────────────
	var stolenBuf []byte
	var stolenPtr *[]byte
	if async || lvl == Panic {
		// Post / Panic 路径：摘走 buf 与 bufPtr（所有权转移给 worker 或立即使用后归还 bufpool）
		stolenBuf = e.buf
		stolenPtr = e.bufPtr
		e.buf = nil
		e.bufPtr = nil
	} else {
		// Send 路径：同步写入后保留 buf（和 bufPtr）在 Event 上，下次 newEvent 直接复用
		// io.Discard 跳过 Write 接口分派，消除 ~5ns 开销（bench / 静默 Logger 场景）。
		if out == nil {
			sendErrCnt.Add(1)
			fireOnWriteError(ErrWriterClosed)
		} else if out != io.Discard {
			if _, err := out.Write(e.buf); err != nil {
				sendErrCnt.Add(1)
				fireOnWriteError(err)
			}
		}
		// 检测 buf 扩容：append 可能导致 e.buf 重新分配，使 bufPtr 指向旧 backing array。
		// 将旧 buf（仍在 *e.bufPtr 中，有效可复用）归还 ptrTiers 池，清空 bufPtr，
		// 下次 newEvent 重新 GetPtr；扩容后的大 buf 继续由 e.buf 保留。
		if e.bufPtr != nil && cap(e.buf) != cap(*e.bufPtr) {
			bufpool.PutPtr(e.bufPtr) // 旧 buf 归还 ptrTiers
			e.bufPtr = nil
		}
		e.buf = e.buf[:0]
		// 高水位防护：单条日志导致 buf 远超正常大小时
		// （如包含 MB 级 Any() 字段），不将其保留在 eventPool，
		// 改为清除引用，让 GC 回收大内存块；下次 newEvent 再从 bufpool 获取。
		if cap(e.buf) > maxEventBufCap {
			if e.bufPtr != nil {
				bufpool.PutPtr(e.bufPtr)
				e.bufPtr = nil
			}
			e.buf = nil
		}
	}

	// 归还 Event 到 Pool（Send 路径保留 buf+bufPtr，Post/Panic 路径 buf+bufPtr 已 nil）
	// 仅清除持有外部引用的字段：logger/boundCtx/msg（GC 安全）。
	// hasMsg/level/ctxStart/ctxEnd 由 newEvent 无条件重置，此处可跳过。
	e.logger = nil
	e.boundCtx = nil
	e.msg = ""
	e.callerPC[0] = 0 // 防止下次复用时老 PC 残留
	eventPool.Put(e)

	// ── 后续派发 ────────────────────────────────────────────────────────
	if lvl == Panic {
		if _, err := out.Write(stolenBuf); err != nil {
			sendErrCnt.Add(1)
			fireOnWriteError(err)
		}
		if stolenPtr != nil {
			bufpool.PutPtr(stolenPtr) // Panic 路径：0-alloc 归还
		} else {
			bufpool.Put(stolenBuf) // 无包装器（扩容等边缘情路径）
		}
		(*panicFuncPtr.Load())(panicMsg)
		return // PanicFunc 未真正 panic 时（如测试替换）从此返回，不落入后续逻辑
	}
	if async {
		postTask(stolenBuf, stolenPtr, wg, out)
	}
	if lvl >= Fatal {
		drainAndExit()
	}
}

// effectiveCtx 返回事件实际使用的 ctx（Event.Ctx() 覆盖优先）。
func (e *Event) effectiveCtx(l *Logger) context.Context {
	if e.boundCtx != nil {
		return e.boundCtx
	}
	return l.boundCtx
}
