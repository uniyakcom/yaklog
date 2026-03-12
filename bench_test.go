package yaklog

import (
	"errors"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/uniyakcom/yakutil/bufpool"
)

// ─── Post（异步）写入基准 ─────────────────────────────────────────────────────

// BenchmarkPost_JSON 测量 Post 热路径吞吐量（JSON 格式，无采样）。
//
// 基准要求：allocs/op = 0（Event 来自 pool，buf 来自 bufpool，投递后转移所有权）。
func BenchmarkPost_JSON(b *testing.B) {
	l := New(Options{
		Out:      discardWriter{},
		Level:    Trace,
		QueueLen: maxQueueLen,
	})
	defer l.Wait()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info().Str("k", "v").Int("n", i).Msg("bench").Post()
	}
	l.Wait()
}

// BenchmarkPost_Text 测量 Text 格式（Console）热路径吞吐量。
func BenchmarkPost_Text(b *testing.B) {
	l := New(Options{
		Out:      Console(discardWriter{}),
		Level:    Trace,
		QueueLen: maxQueueLen,
	})
	defer l.Wait()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info().Str("k", "v").Int("n", i).Msg("bench").Post()
	}
	l.Wait()
}

// ─── Send（同步）写入基准 ─────────────────────────────────────────────────────

// BenchmarkSend_JSON 测量 Send 热路径吞吐量（JSON 格式，同步写入）。
func BenchmarkSend_JSON(b *testing.B) {
	l := New(Options{Out: discardWriter{}, Level: Trace})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info().Str("k", "v").Int("n", i).Msg("bench").Send()
	}
}

// BenchmarkSend_Disabled 测量被过滤级别的零开销路径。
func BenchmarkSend_Disabled(b *testing.B) {
	l := New(Options{Out: discardWriter{}, Level: Error})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Debug().Str("k", "v").Msg("noop").Send() // 级别未启用，应 0 alloc
	}
}

// BenchmarkPost_Dropped 测量队列已满时的丢弃路径开销（应极低）。
func BenchmarkPost_Dropped(b *testing.B) {
	l := New(Options{Out: discardWriter{}, Level: Trace, QueueLen: 1})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Info().Msg("drop").Post()
	}
	l.Wait()
}

// ─── Encoder 独立基准 ─────────────────────────────────────────────────────────

// BenchmarkEncoder_JSON 测量 jsonEncoder 单条记录编码开销（含 finalize）。
func BenchmarkEncoder_JSON(b *testing.B) {
	enc := &jsonEncoder{timeFmt: TimeUnixMilli}
	buf := bufpool.Get(defaultBufCap)[:0]
	defer bufpool.Put(buf)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		buf = enc.beginRecord(buf, 1_741_000_000_000_000_000, Info)
		buf = enc.appendStr(buf, "service", "api")
		buf = enc.appendInt64(buf, "latency_us", 42)
		buf = enc.finalize(buf, "request handled", true, "")
	}
}

// BenchmarkEncoder_Text 测量 textEncoder 单条记录编码开销。
func BenchmarkEncoder_Text(b *testing.B) {
	enc := &textEncoder{timeFmt: TimeUnixMilli, consoleFmt: ConsoleTimeMilli}
	buf := bufpool.Get(defaultBufCap)[:0]
	defer bufpool.Put(buf)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = buf[:0]
		buf = enc.beginRecord(buf, 1_741_000_000_000_000_000, Info)
		buf = enc.appendStr(buf, "service", "api")
		buf = enc.appendInt64(buf, "latency_us", 42)
		buf = enc.finalize(buf, "request handled", true, "")
	}
}

// BenchmarkAppendJSONStr 测量 JSON 字符串转义开销（普通值，无转义字符）。
func BenchmarkAppendJSONStr(b *testing.B) {
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf = appendJSONStr(buf[:0], "request.handled.successfully")
	}
}

// ─── Label 链式构建基准 ──────────────────────────────────────────────────────

// BenchmarkLabel_Chain 测量 Label().Label() 链式创建子 Logger 的分配数。
func BenchmarkLabel_Chain(b *testing.B) {
	l := New(Options{Out: discardWriter{}, Level: Trace})

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Label("k", "v").Label("n", i)
	}
}

// ─── 并发基准 ────────────────────────────────────────────────────────────────

// BenchmarkSend_Parallel 测量多核并发 Send 时的争用情况。
func BenchmarkSend_Parallel(b *testing.B) {
	l := New(Options{Out: discardWriter{}, Level: Trace})

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Info().Str("k", "v").Msg("parallel").Send()
		}
	})
}

// BenchmarkPost_Parallel 测量多核并发 Post 时的争用情况。
func BenchmarkPost_Parallel(b *testing.B) {
	l := New(Options{Out: discardWriter{}, Level: Trace, QueueLen: maxQueueLen})

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Info().Str("k", "v").Msg("parallel").Post()
		}
	})
	l.Wait()
}

// ─── 辅助 ────────────────────────────────────────────────────────────────────

// discardWriter 零分配的 /dev/null 写入器。
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// ─── nil check 开销 vs 直接路径 ──────────────────────────────────────────────
//
// 两个基准使用完全相同的 10 字段（与 _benchmarks/bench/Benchmark10Fields 一致）。
// Standard 走公开字段方法（含 nil guard）；DirectPath 绕过 nil guard 直接调用
// 内部 appendJSONKey/appendJSONStr 等函数，再调用 finishAndDispatch。
// 二者 ns/op 之差即 10 次 nil check 的实际代价。

const bench10FieldsMsg = "Test logging, but use a somewhat realistic message length."

var bench10FieldsErr = errors.New("something went wrong")

// Benchmark10Fields_Standard 标准路径：经由 nil-safe 字段方法。
func Benchmark10Fields_Standard(b *testing.B) {
	l := New(Options{Out: io.Discard, TimeFormat: TimeOff})
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Info().
				Str("string", "four!").
				Str("url", "/api/v1/users").
				Int("int", 1).
				Int64("int64", int64(2)).
				Float64("float64", 3.14).
				Bool("bool", true).
				Time("time", time.Time{}).
				Dur("duration", time.Millisecond).
				AnErr("error", bench10FieldsErr).
				Str("string2", "some realistic string value").
				Msg(bench10FieldsMsg).Send()
		}
	})
}

// Benchmark10Fields_DirectPath 直接路径：绕过 nil guard，直接操作 Event.buf。
// 与 Standard 的 ns/op 之差 = 10 次 nil check 的实测代价。
func Benchmark10Fields_DirectPath(b *testing.B) {
	l := New(Options{Out: io.Discard, TimeFormat: TimeOff})
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			e := l.newEvent(Info)
			e.buf = appendJSONStr(appendJSONKeyFast(e.buf, "string"), "four!")
			e.buf = appendJSONStr(appendJSONKeyFast(e.buf, "url"), "/api/v1/users")
			e.buf = strconv.AppendInt(appendJSONKeyFast(e.buf, "int"), 1, 10)
			e.buf = strconv.AppendInt(appendJSONKeyFast(e.buf, "int64"), 2, 10)
			e.buf = strconv.AppendFloat(appendJSONKeyFast(e.buf, "float64"), 3.14, 'f', -1, 64)
			{
				buf := appendJSONKeyFast(e.buf, "bool")
				e.buf = append(buf, "true"...)
			}
			{
				var tb [36]byte
				buf := appendJSONKeyFast(e.buf, "time")
				e.buf = append(append(append(buf, '"'), time.Time{}.AppendFormat(tb[:0], time.RFC3339Nano)...), '"')
			}
			e.buf = appendJSONStr(appendJSONKeyFast(e.buf, "duration"), time.Millisecond.String())
			e.buf = appendJSONStr(appendJSONKeyFast(e.buf, "error"), bench10FieldsErr.Error())
			e.buf = appendJSONStr(appendJSONKeyFast(e.buf, "string2"), "some realistic string value")
			e.msg = bench10FieldsMsg
			e.hasMsg = true
			e.finishAndDispatch(false)
		}
	})
}

// ─── key 编码方式独立对比 ──────────────────────────────────────────────────────
//
// EscapeLoop：当前 appendJSONKey（含逐字节转义扫描）。
// DirectAppend：跳过扫描，直接 append（与 zerolog AppendKey 等价）。
// 10 个 key 循环体的 ns/op 之差 = 转义扫描的实测代价。
//
// 注：两个基准均为单线程顺序执行，不使用 RunParallel，
// 以排除并发调度抖动，精确测量编码指令序列本身的开销。

// BenchmarkKeyEncode_EscapeLoop 使用当前 appendJSONKey（含转义扫描）追加 10 个 key。
func BenchmarkKeyEncode_EscapeLoop(b *testing.B) {
	buf := make([]byte, 0, 256)
	buf = append(buf, '{')
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf = buf[:1]
		buf = appendJSONKey(buf, "string")
		buf = appendJSONKey(buf, "url")
		buf = appendJSONKey(buf, "int")
		buf = appendJSONKey(buf, "int64")
		buf = appendJSONKey(buf, "float64")
		buf = appendJSONKey(buf, "bool")
		buf = appendJSONKey(buf, "time")
		buf = appendJSONKey(buf, "duration")
		buf = appendJSONKey(buf, "error")
		buf = appendJSONKey(buf, "string2")
	}
	_ = buf
}

// BenchmarkKeyEncode_DirectAppend 直接 append（无转义扫描），追加相同 10 个 key。
// 等价于 zerolog AppendKey（信任 key 为合法 JSON 标识符）。
func BenchmarkKeyEncode_DirectAppend(b *testing.B) {
	buf := make([]byte, 0, 256)
	buf = append(buf, '{')
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf = buf[:1]
		buf = append(append(append(buf, ',', '"'), "string"...), '"', ':')
		buf = append(append(append(buf, ',', '"'), "url"...), '"', ':')
		buf = append(append(append(buf, ',', '"'), "int"...), '"', ':')
		buf = append(append(append(buf, ',', '"'), "int64"...), '"', ':')
		buf = append(append(append(buf, ',', '"'), "float64"...), '"', ':')
		buf = append(append(append(buf, ',', '"'), "bool"...), '"', ':')
		buf = append(append(append(buf, ',', '"'), "time"...), '"', ':')
		buf = append(append(append(buf, ',', '"'), "duration"...), '"', ':')
		buf = append(append(append(buf, ',', '"'), "error"...), '"', ':')
		buf = append(append(append(buf, ',', '"'), "string2"...), '"', ':')
	}
	_ = buf
}
