package yaklog

import (
	"context"
	"encoding/hex"
)

// ─── context 键类型（私有，防止外部 key 冲突）────────────────────────────────

type traceKey struct{}
type fieldKey struct{ name string }
type fieldNamesKey struct{} // 追踪已注入的字段名列表，供 appendCtxFields 遍历
type loggerKey struct{}
type eventSinkKey struct{}

// ─── TraceID 注入 ─────────────────────────────────────────────────────────────

// WithTrace 将 [16]byte TraceID 注入 context，返回新 context。
//
// Logger.Context(ctx) 绑定或 Event.Ctx(ctx) 时会自动提取并追加 "trace_id" 字段。
func WithTrace(ctx context.Context, id [16]byte) context.Context {
	return context.WithValue(ctx, traceKey{}, id)
}

// traceFromCtx 从 context 提取 TraceID，ok=false 表示未注入。
func traceFromCtx(ctx context.Context) (id [16]byte, ok bool) {
	if ctx == nil {
		return id, false
	}
	v := ctx.Value(traceKey{})
	if v == nil {
		return id, false
	}
	id, ok = v.([16]byte)
	return id, ok
}

// appendTraceIDField 追加 trace_id 字段到 buf。
// JSON 路径：直接逐字节写入十六进制，无临时缓冲区，零堆分配。
// 文本路径：hex.Encode + string(...)，非热路径，允许分配。
func appendTraceIDField(buf []byte, enc encoder, jenc *jsonEncoder, id [16]byte) []byte {
	if jenc != nil {
		// JSON 快速路径：直接按位展开十六进制，不依赖临时数组或 unsafe 字符串转换。
		buf = appendJSONKey(buf, "trace_id")
		buf = append(buf, '"')
		for i := 0; i < 16; i++ {
			buf = append(buf, hexChars[id[i]>>4], hexChars[id[i]&0xF])
		}
		return append(buf, '"')
	}
	// 文本路径：hex.Encode 写入栈缓冲，转为 string 后追加（文本路径非热路径，允许分配）。
	var hexBuf [32]byte
	hex.Encode(hexBuf[:], id[:])
	return enc.appendStr(buf, "trace_id", string(hexBuf[:]))
}

// ─── 任意字段注入 ─────────────────────────────────────────────────────────────

// WithField 将任意键值对注入 context，返回新 context。
//
// 同一 key 多次注入时，后注入的覆盖先注入的（context 链语义）。
// Logger.Context(ctx) 绑定或 Event.Ctx(ctx) 时，这些字段会自动追加到每条日志。
func WithField(ctx context.Context, key string, val any) context.Context {
	// 维护名称追踪列表（去重：同一 key 二次注入时列表不增长，値由 context 链覆盖）
	names, _ := ctx.Value(fieldNamesKey{}).([]string)
	for _, n := range names {
		if n == key {
			// key 已在追踪列表中，只覆盖值即可
			return context.WithValue(ctx, fieldKey{name: key}, val)
		}
	}
	// 上限防护：防止高并发场景下不同键无界注入导致 fieldNamesKey slice OOM。
	// 超出上限后不做任何写入——值和名称列表均不变更，避免 context 链因仅値写入
	// 而导致“写入成功但日志不输出”的困惑行为。
	if len(names) >= maxCtxFields {
		return ctx
	}
	ctx = context.WithValue(ctx, fieldKey{name: key}, val)
	newNames := make([]string, len(names)+1)
	copy(newNames, names)
	newNames[len(names)] = key
	return context.WithValue(ctx, fieldNamesKey{}, newNames)
}

// fieldsFromCtx 返回通过 WithField 注入的所有字段名（插入顺序）。
func fieldsFromCtx(ctx context.Context) []string {
	names, _ := ctx.Value(fieldNamesKey{}).([]string)
	return names
}

// ─── Logger 注入 ─────────────────────────────────────────────────────────────

// WithLogger 将 *Logger 注入 context，返回新 context。
//
// FromCtx 可取出此 Logger；通常在 middleware 中绑定带请求信息的子 Logger。
func WithLogger(ctx context.Context, l *Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// FromCtx 从 context 中取出 *Logger。
//
// 若 context 中无 Logger，返回包级默认 Logger（全局 Config 效果）。
func FromCtx(ctx context.Context) *Logger {
	if ctx == nil {
		return defaultLogger()
	}
	if l, ok := ctx.Value(loggerKey{}).(*Logger); ok && l != nil {
		return l
	}
	return defaultLogger()
}

// ─── EventSink 注入 ───────────────────────────────────────────────────────────

// EventSink 事件挂载接口。实现此接口可在每条日志写入时同步触发事件
// （如向 yakevent 总线发布告警）。
//
// Emit 在日志编码完成后、写入目标前被同步调用。
// 实现须并发安全，且不得持有 fields 引用（传入的是当前日志行的独立副本）。
// 若 Emit 内部发生 panic，yaklog 会隔离并吞掉该 panic，避免影响日志主流程。
type EventSink interface {
	Emit(level Level, msg string, fields []byte)
}

// WithEventSink 将 EventSink 注入 context，返回新 context。
func WithEventSink(ctx context.Context, sink EventSink) context.Context {
	return context.WithValue(ctx, eventSinkKey{}, sink)
}

// eventSinkFromCtx 从 context 取出 EventSink；不存在时返回 nil。
func eventSinkFromCtx(ctx context.Context) EventSink {
	if ctx == nil {
		return nil
	}
	if s, ok := ctx.Value(eventSinkKey{}).(EventSink); ok {
		return s
	}
	return nil
}
