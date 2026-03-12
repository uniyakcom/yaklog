package yaklog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockStringer 用于测试 Stringer 字段。
type mockStringer struct{ val string }

func (m mockStringer) String() string { return "stringer:" + m.val }

// ── 辅助 ────────────────────────────────────────────────────────────────────

// newTestLogger 创建测试用 Logger（同步写入 buf）。
// 默认 Level=Trace，Out=buf；可通过 extra 覆盖。
func newTestLogger(buf *bytes.Buffer, extra ...Options) *Logger {
	o := Options{Out: buf, Level: Trace}
	if len(extra) > 0 {
		e := extra[0]
		if e.Out != nil {
			o.Out = e.Out
		}
		o.Level = e.Level
		if e.Source {
			o.Source = true
		}
		if e.TimeFormat != 0 {
			o.TimeFormat = e.TimeFormat
		}
		if e.Sampler != nil {
			o.Sampler = e.Sampler
		}
		if e.ConsoleTimeFormat != "" {
			o.ConsoleTimeFormat = e.ConsoleTimeFormat
		}
	}
	return New(o)
}

// drainLogger 等待 Logger 的所有异步写入完成（Send 路径为 no-op）。
func drainLogger(l *Logger) { l.Wait() }

// decodeJSON 将输出解析为 JSON 对象；解析失败则 t.Fatal。
func decodeJSON(t *testing.T, data string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("not valid JSON: %v\nbuf=%s", err, data)
	}
	return m
}

// ── JSON 基础输出 ─────────────────────────────────────────────────────────────

func TestLogger_JSONOutput(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info().Str("key", "val").Msg("hello").Send()
	drainLogger(l)

	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	if m["msg"] != "hello" {
		t.Errorf("msg mismatch: %v", m["msg"])
	}
	if m["key"] != "val" {
		t.Errorf("key mismatch: %v", m["key"])
	}
	if _, ok := m["time"]; !ok {
		t.Error("missing time field")
	}
	if m["level"] != "INFO" {
		t.Errorf("level mismatch: %v", m["level"])
	}
}

// ── Text 基础输出（Console）────────────────────────────────────────────────────

func TestLogger_TextOutput(t *testing.T) {
	var buf bytes.Buffer
	// ConsoleNoColor=true: 禁用颜色，保证输出不含 ANSI 转义，方便字符串断言。
	l := New(Options{Out: Console(&buf), Level: Trace, ConsoleNoColor: true})
	l.Info().Str("k", "v").Msg("world").Send()
	drainLogger(l)

	out := buf.String()
	if !strings.Contains(out, "world") {
		t.Errorf("missing msg=world in: %q", out)
	}
	if !strings.Contains(out, "k=v") {
		t.Errorf("missing k=v in: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("text line should end with \\n")
	}
}

// ── 级别过滤 ──────────────────────────────────────────────────────────────────

func TestLogger_LevelFilter(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, Options{Level: Warn})
	l.Debug().Msg("hidden").Send()
	l.Info().Msg("hidden2").Send()
	l.Warn().Msg("visible").Send()
	drainLogger(l)

	out := buf.String()
	if strings.Contains(out, "hidden") {
		t.Error("debug/info should be filtered")
	}
	if !strings.Contains(out, "visible") {
		t.Error("warn should pass through")
	}
}

// ── 级别检查（返回 nil vs 非 nil）────────────────────────────────────────────

func TestLogger_LevelEnabled(t *testing.T) {
	l := newTestLogger(new(bytes.Buffer), Options{Level: Info})
	defer drainLogger(l)

	if e := l.Debug(); e != nil {
		t.Error("Debug should return nil when level is Info")
	}
	if e := l.Info(); e == nil {
		e.Send() // nil-safe
		t.Error("Info should not return nil when level is Info")
	}
	if e := l.Warn(); e == nil {
		t.Error("Warn should not return nil when level is Info")
	} else {
		e.Send()
	}
}

// ── SetLevel ─────────────────────────────────────────────────────────────────

func TestLogger_SetLevel(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, Options{Level: Info})
	l.Debug().Msg("before").Send()
	l.SetLevel(Debug)
	l.Debug().Msg("after").Send()
	drainLogger(l)

	out := buf.String()
	if strings.Contains(out, "before") {
		t.Error("debug should be filtered before SetLevel")
	}
	if !strings.Contains(out, "after") {
		t.Error("debug should pass after SetLevel(Debug)")
	}
}

// ── GetLevel ──────────────────────────────────────────────────────────────────

func TestLogger_GetLevel(t *testing.T) {
	l := newTestLogger(new(bytes.Buffer), Options{Level: Warn})
	defer drainLogger(l)

	if got := l.GetLevel(); got != Warn {
		t.Errorf("expected Warn level, got %v", got)
	}
	l.SetLevel(Trace)
	if got := l.GetLevel(); got != Trace {
		t.Errorf("after SetLevel Trace, got %v", got)
	}
}

// ── 链式调用全字段 ───────────────────────────────────────────────────────────

func TestLogger_ChainAllFields(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info().
		Str("service", "svc").
		Int("code", 200).
		Bool("ok", true).
		Float64("lat", 1.5).
		Msg("request").
		Send()
	drainLogger(l)

	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	checks := map[string]any{
		"service": "svc",
		"code":    float64(200),
		"ok":      true,
		"lat":     1.5,
		"msg":     "request",
	}
	for k, v := range checks {
		if m[k] != v {
			t.Errorf("%s: got %v, want %v", k, m[k], v)
		}
	}
}

// ── Err 字段 ──────────────────────────────────────────────────────────────────

func TestLogger_ErrField(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Error().Err(ErrWriterClosed).Msg("oops").Send()
	drainLogger(l)

	out := buf.String()
	if !strings.Contains(out, "worker closed") {
		t.Errorf("err field missing in: %q", out)
	}
}

// ── Err(nil) 不追加字段 ───────────────────────────────────────────────────────

func TestLogger_ErrNil(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info().Err(nil).Msg("nil-err").Send()
	drainLogger(l)
	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	if _, ok := m["error"]; ok {
		t.Errorf("expected no 'error' field when Err(nil), got: %v", m)
	}
}

// ── nil Event 安全 ────────────────────────────────────────────────────────────

func TestLogger_NilEvent(t *testing.T) {
	var e *Event
	// none of these should panic
	e.Str("k", "v").Int("n", 1).Bool("b", true).Msg("noop").Send()
}

// ── Label 固定字段 ────────────────────────────────────────────────────────────

func TestLogger_Label(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	l := base.Label("app", "test")
	l.Info().Msg("hi").Send()
	drainLogger(l)

	out := buf.String()
	if !strings.Contains(out, "app") {
		t.Errorf("label field missing in: %q", out)
	}
}

// ── Label 不影响父 Logger ──────────────────────────────────────────────────────

func TestLogger_Label_IsolatedFromParent(t *testing.T) {
	var buf bytes.Buffer
	parent := newTestLogger(&buf)
	child := parent.Label("module", "db")
	child.Info().Msg("from-child").Send()
	parent.Info().Msg("from-parent").Send()
	drainLogger(parent)

	count := strings.Count(buf.String(), "module")
	if count != 1 {
		t.Errorf("expected 'module' to appear exactly once (child only), got %d times", count)
	}
}

// ── Fork 独立级别 ─────────────────────────────────────────────────────────────

func TestLogger_Fork_IndependentLevel(t *testing.T) {
	var buf bytes.Buffer
	parent := newTestLogger(&buf, Options{Level: Error})
	child := parent.Fork()
	child.SetLevel(Debug)
	defer drainLogger(parent)

	parent.Debug().Msg("parent-hidden").Send()
	child.Debug().Msg("child-visible").Send()
	drainLogger(parent)

	out := buf.String()
	if strings.Contains(out, "parent-hidden") {
		t.Error("parent should filter debug")
	}
	if !strings.Contains(out, "child-visible") {
		t.Error("child with SetLevel(Debug) should log debug")
	}
}

// ── Label 继承 Level ──────────────────────────────────────────────────────────

func TestLogger_LabelChild_InheritsLevel(t *testing.T) {
	var buf bytes.Buffer
	parent := newTestLogger(&buf, Options{Level: Warn})
	child := parent.Label("c", "1")
	child.Info().Msg("suppressed").Send()
	drainLogger(parent)

	if strings.Contains(buf.String(), "suppressed") {
		t.Error("child should inherit parent level filter")
	}
}

// ── SetLevel 共享影响 Label 派生子 ────────────────────────────────────────────

func TestLogger_SetLevel_SharedWithLabel(t *testing.T) {
	var buf bytes.Buffer
	parent := newTestLogger(&buf, Options{Level: Error})
	child := parent.Label("x", "1")

	// Hot-update parent level: child shares the same atomic
	parent.SetLevel(Debug)

	child.Debug().Msg("should-appear").Send()
	drainLogger(parent)

	if !strings.Contains(buf.String(), "should-appear") {
		t.Error("SetLevel on parent should affect label-derived children")
	}
}

// ── truncateMsg ───────────────────────────────────────────────────────────────

func TestTruncateMsg(t *testing.T) {
	short := "hello"
	if got := truncateMsg(short); got != short {
		t.Errorf("short message should not be truncated: %q", got)
	}

	long := strings.Repeat("x", maxMessageLen+100)
	got := truncateMsg(long)
	// 结果必须严格 ≤ maxMessageLen（含 truncateSuffix）
	if len(got) > maxMessageLen {
		t.Errorf("truncated message exceeds maxMessageLen: len=%d > %d", len(got), maxMessageLen)
	}
	if !strings.HasSuffix(got, truncateSuffix) {
		t.Errorf("truncated message should end with %q: %q", truncateSuffix, got[:50])
	}
}

// ── Event 全类型字段 ──────────────────────────────────────────────────────────

func TestLogger_EventAllTypes(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Debug().
		Int64("i64", -128).
		Uint64("u64", 255).
		Float64("f64", 3.14).
		AnErr("aerr", errors.New("custom-aerr")).
		Bytes("raw", []byte("rawbytes")).
		Any("obj", struct{ X int }{X: 99}).
		Stringer("str", mockStringer{"hello"}).
		Time("ts", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)).
		Dur("dur", 5*time.Second).
		Send()
	drainLogger(l)

	out := buf.String()
	for _, want := range []string{"-128", "255", "3.14", "custom-aerr", "rawbytes", "99", "stringer:hello", "2026", "5s"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output: %q", want, out)
		}
	}
}

// ── Trace 事件 ────────────────────────────────────────────────────────────────

func TestLogger_TraceEvent(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Trace().Str("x", "trace_val").Msg("traced").Send()
	drainLogger(l)
	if !strings.Contains(buf.String(), "trace_val") {
		t.Errorf("trace event not in output: %q", buf.String())
	}
}

func TestLogger_Trace_Disabled(t *testing.T) {
	l := newTestLogger(new(bytes.Buffer), Options{Level: Info})
	defer drainLogger(l)
	if e := l.Trace(); e != nil {
		t.Error("Trace() should return nil when level > Trace")
	}
}

// ── WithAddSource ─────────────────────────────────────────────────────────────

func TestLogger_AddSource(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: Trace, Source: true})
	l.Info().Msg("source test").Send()
	drainLogger(l)

	out := buf.String()
	if !strings.Contains(out, "source") {
		t.Errorf("expected 'source' field in output: %q", out)
	}
}

// ── Caller() per-event 定位 ───────────────────────────────────────────────────

// TestLogger_Caller 验证 Caller() 在未开启全局 Source 的情况下向记录附加 source 字段。
func TestLogger_Caller(t *testing.T) {
	var buf bytes.Buffer
	// Source=false：全局不开启，确保 source 字段完全来自 Caller()
	l := New(Options{Out: &buf, Level: Trace})
	l.Info().Caller().Msg("caller test").Send()
	drainLogger(l)

	out := buf.String()
	if !strings.Contains(out, "source") {
		t.Errorf("Caller() 应附加 source 字段，实际输出: %q", out)
	}
	// source 字段应包含当前文件名
	if !strings.Contains(out, "handler_test.go") {
		t.Errorf("source 字段应包含 handler_test.go，实际输出: %q", out)
	}
}

// TestLogger_Caller_NilSafe 验证 nil Event 调用 Caller() 不 panic。
func TestLogger_Caller_NilSafe(t *testing.T) {
	var e *Event
	// 所有 nil-safe 方法均应安全返回 nil
	if got := e.Caller(); got != nil {
		t.Error("nil Event.Caller() should return nil")
	}
}

// TestLogger_Caller_OverridesGlobalSource 验证 Caller() 与全局 Source=true 并存时，
// source 字段唯一存在（不重复出现）。
func TestLogger_Caller_OverridesGlobalSource(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: Trace, Source: true})
	l.Info().Caller().Msg("both source").Send()
	drainLogger(l)

	out := buf.String()
	// "source" 字段只应出现一次（Caller() 优先，全局分支被跳过）
	count := strings.Count(out, `"source"`)
	if count != 1 {
		t.Errorf("source 字段应恰好出现 1 次，实际 %d 次，输出: %q", count, out)
	}
}

// TestLogger_ErrColor_Console 验证 Console 模式下 Err() 对 error value 使用级别色。
func TestLogger_ErrColor_Console(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: Console(&buf), Level: Error})
	l.Error().Err(errors.New("upstream down")).Send()
	drainLogger(l)

	out := buf.String()
	// Error 级别色码为 ansiError = "\x1b[31m"
	if !strings.Contains(out, "\x1b[31m") {
		t.Errorf("Console Error 级别下 error value 应含红色 ANSI 码，实际: %q", out)
	}
	if !strings.Contains(out, "upstream down") {
		t.Errorf("error value 缺失，实际输出: %q", out)
	}
}

// ── WithTimeFormat ────────────────────────────────────────────────────────────

func TestLogger_WithTimeFormat(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	l1 := New(Options{Out: &buf1, Level: Trace, TimeFormat: TimeUnixMilli})
	l2 := New(Options{Out: &buf2, Level: Trace, TimeFormat: TimeUnixNano})
	defer drainLogger(l1)
	defer drainLogger(l2)

	l1.Info().Msg("t1").Send()
	l2.Info().Msg("t2").Send()
	drainLogger(l1)
	drainLogger(l2)

	// UnixMilli 输出时间为纯数字，不含 "T" 分隔符
	if strings.Contains(buf1.String(), `"time":"20`) {
		t.Errorf("UnixMilli should not contain ISO date: %q", buf1.String())
	}
	// 两个输出都应该是有效 JSON
	decodeJSON(t, strings.TrimSpace(buf1.String()))
	decodeJSON(t, strings.TrimSpace(buf2.String()))
}

// ── TimeOff 不输出时间字段 ────────────────────────────────────────────────────

func TestLogger_TimeOff(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: Trace, TimeFormat: TimeOff})
	l.Info().Msg("no-time").Send()
	drainLogger(l)

	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	if _, ok := m["time"]; ok {
		t.Errorf("expected no 'time' field with TimeOff, got: %v", m)
	}
}

// ── ctx 注入 trace_id ─────────────────────────────────────────────────────────

func TestLogger_CtxTrace(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	tid := [16]byte{0xde, 0xad, 0xbe, 0xef, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb}
	ctx := WithTrace(context.Background(), tid)
	l.Context(ctx).Info().Msg("traced").Send()
	drainLogger(l)

	out := buf.String()
	if !strings.Contains(out, "deadbeef") {
		t.Errorf("trace id missing in: %q", out)
	}
}

// ── Sampler 集成 ──────────────────────────────────────────────────────────────

func TestLogger_Sampler_DropAll(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: Trace, Sampler: NewHashSampler(0)})
	defer drainLogger(l)

	for i := 0; i < 200; i++ {
		l.Info().Msg("sampled").Send()
	}
	drainLogger(l)

	if strings.Contains(buf.String(), "sampled") {
		t.Error("rate=0 sampler should drop all messages")
	}
}

// ── Label 多个字段 ────────────────────────────────────────────────────────────

func TestLogger_Label_MultipleFields(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	l := base.
		Label("module", "auth").
		Label("version", "v2").
		Label("count", 42)
	l.Info().Msg("ctx-log").Send()
	drainLogger(l)

	out := buf.String()
	for _, want := range []string{"auth", "v2", "42"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output: %q", want, out)
		}
	}
}

// ── To() 切换输出目标 ─────────────────────────────────────────────────────────

func TestLogger_To_SwitchOutput(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	l := New(Options{Out: &buf1, Level: Trace})
	l2 := l.To(&buf2)

	l.Info().Msg("to-buf1").Send()
	l2.Info().Msg("to-buf2").Send()
	drainLogger(l)
	drainLogger(l2)

	if !strings.Contains(buf1.String(), "to-buf1") {
		t.Errorf("expected to-buf1 in buf1: %q", buf1.String())
	}
	if !strings.Contains(buf2.String(), "to-buf2") {
		t.Errorf("expected to-buf2 in buf2: %q", buf2.String())
	}
	if strings.Contains(buf2.String(), "to-buf1") {
		t.Error("to-buf1 should not appear in buf2")
	}
}

// ── Send() 同步写入立即可见 ───────────────────────────────────────────────────

func TestLogger_Send_Immediate(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: Trace})

	l.Info().Str("k", "v").Msg("sync-msg").Send()
	// Send 是同步的，无需 Wait
	out := buf.String()
	if !strings.Contains(out, "sync-msg") {
		t.Errorf("Send 应立即写入，但输出中未找到消息：%q", out)
	}
}

// ── Post() 异步写入经 Wait 可见 ───────────────────────────────────────────────

func TestLogger_Post_AsyncWithWait(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: Trace})
	defer Wait()

	l.Info().Msg("async-msg").Post()
	l.Wait() // 等待 Post 完成

	if !strings.Contains(buf.String(), "async-msg") {
		t.Errorf("Post + Wait 后应有输出：%q", buf.String())
	}
}

// ── Context() 绑定 Logger ─────────────────────────────────────────────────────

func TestLogger_Context_BoundCtx(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	ctx := WithField(context.Background(), "request_id", "abc123")
	tid := [16]byte{0xaa}
	ctx = WithTrace(ctx, tid)
	bound := l.Context(ctx)
	bound.Info().Msg("with-trace").Send()
	drainLogger(bound)

	out := buf.String()
	// WithTrace 产生的 trace_id 字段必须存在
	if !strings.Contains(out, "trace_id") {
		t.Errorf("expected trace_id in output: %q", out)
	}
	// WithField 注入的 request_id 字段必须存在
	if !strings.Contains(out, "request_id") {
		t.Errorf("expected request_id field in output: %q", out)
	}
	if !strings.Contains(out, "abc123") {
		t.Errorf("expected request_id value 'abc123' in output: %q", out)
	}
}

// ── Msg() 返回 Event，不调用则无 msg 字段 ─────────────────────────────────────

func TestLogger_NoMsg_NoMsgField(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info().Str("key", "value").Send() // 没有调用 Msg
	drainLogger(l)

	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	if _, ok := m["msg"]; ok {
		t.Errorf("expected no 'msg' field when Msg() not called, got: %v", m)
	}
	if m["key"] != "value" {
		t.Errorf("expected key=value, got: %v", m["key"])
	}
}

// ── Msg().Send() vs Send() 区别 ───────────────────────────────────────────────

func TestLogger_MsgThenSend(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	// Msg sets message, Send terminates
	l.Info().Str("x", "1").Msg("hello").Send()
	drainLogger(l)

	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	if m["msg"] != "hello" {
		t.Errorf("expected msg=hello, got: %v", m["msg"])
	}
	if m["x"] != "1" {
		t.Errorf("expected x=1, got: %v", m["x"])
	}
}

// ── 并发安全 ──────────────────────────────────────────────────────────────────

func TestLogger_ConcurrentSend(t *testing.T) {
	// bytes.Buffer 非线程安全，并发 Send 会 race；改用 Post（异步路径由 worker 序列化写入）。
	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: Trace})
	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			l.Info().Msg("parallel").Post()
		}()
	}
	wg.Wait()
	l.Wait() // 等待后台写入完成
	count := strings.Count(buf.String(), "parallel")
	if count != n {
		t.Errorf("并发 Post 期望 %d 条，得 %d 条", n, count)
	}
}

// ── ConsoleColor ANSI 颜色 ────────────────────────────────────────────────────

// TestConsoleColor_ANSI_Present 验证 ConsoleColor 输出中含 ANSI 转义序列，
// 且各级别颜色前缀均不相同。
func TestConsoleColor_ANSI_Present(t *testing.T) {
	levels := []struct {
		name string
		fn   func(*Logger) *Event
	}{
		{"Trace", func(l *Logger) *Event { return l.Trace() }},
		{"Debug", func(l *Logger) *Event { return l.Debug() }},
		{"Info", func(l *Logger) *Event { return l.Info() }},
		{"Warn", func(l *Logger) *Event { return l.Warn() }},
		{"Error", func(l *Logger) *Event { return l.Error() }},
		{"Panic", func(l *Logger) *Event { return l.Panic() }},
	}

	seen := map[string]string{} // color prefix → level name（验证颜色唯一）

	for _, tc := range levels {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			// 颜色默认开启，无需显式设 ConsoleColor
			l := New(Options{Out: Console(&buf), Level: Trace})

			if tc.name == "Panic" {
				// Panic 会 panic，需用 recover
				func() {
					defer func() { recover() }() //nolint:errcheck
					tc.fn(l).Msg("colortest").Send()
				}()
			} else {
				tc.fn(l).Msg("colortest").Send()
			}
			drainLogger(l)

			out := buf.String()
			if !strings.Contains(out, "\x1b[") {
				t.Errorf("level %s: expected ANSI escape in ConsoleColor output, got: %q", tc.name, out)
				return
			}
			// 时间戳使用 ansiTime (\x1b[2m) 开头，\x1b[0m 结尾。
			// 级别色在时间戳 Reset 之后，提取第二段 ANSI 转义作为唱色前缀。
			tsResetEnd := strings.Index(out, "\x1b[0m")
			if tsResetEnd < 0 {
				t.Fatalf("level %s: could not find timestamp reset in: %q", tc.name, out)
			}
			afterTs := out[tsResetEnd+4:] // 跳过 \x1b[0m
			escIdx := strings.Index(afterTs, "\x1b[")
			if escIdx < 0 {
				t.Fatalf("level %s: could not find level ANSI escape after timestamp in: %q", tc.name, out)
			}
			mIdx := strings.Index(afterTs[escIdx:], "m")
			if mIdx < 0 {
				t.Fatalf("level %s: could not find ANSI 'm' terminator in: %q", tc.name, out)
			}
			colorPrefix := afterTs[escIdx : escIdx+mIdx+1]
			if prev, ok := seen[colorPrefix]; ok {
				t.Errorf("level %s and %s share the same ANSI prefix %q", tc.name, prev, colorPrefix)
			}
			seen[colorPrefix] = tc.name
		})
	}
}

// TestConsoleColor_ANSI_Absent 验证设置 ConsoleNoColor=true 后输出无 ANSI 转义。
func TestConsoleColor_ANSI_Absent(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: Console(&buf), Level: Trace, ConsoleNoColor: true})
	l.Info().Msg("plain").Send()
	drainLogger(l)

	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("Console (non-color) should not contain ANSI escapes; got: %q", buf.String())
	}
}

// ── Event.Ctx() 字段不重复 ────────────────────────────────────────────────────

// TestEventCtx_NoDuplicateTraceID 验证 Event.Ctx() 覆盖时 trace_id 仅出现一次。
func TestEventCtx_NoDuplicateTraceID(t *testing.T) {
	loggerCtx := WithTrace(context.Background(), [16]byte{1: 0xAA})
	eventCtx := WithTrace(context.Background(), [16]byte{1: 0xBB})

	var buf bytes.Buffer
	l := New(Options{Out: &buf, Level: Trace}).Context(loggerCtx)
	l.Info().Ctx(eventCtx).Msg("dedup").Send()
	drainLogger(l)

	line := strings.TrimSpace(buf.String())
	count := strings.Count(line, "trace_id")
	if count != 1 {
		t.Errorf("expected trace_id to appear exactly once, got %d; line: %s", count, line)
	}
	// 应该包含 eventCtx 的 trace_id 值（bb 出现），而不是 loggerCtx（aa 不应出现）
	if !strings.Contains(line, "bb") {
		t.Errorf("expected eventCtx trace_id (bb) in output; got: %s", line)
	}
	if strings.Contains(line, "\"00aa") {
		t.Errorf("loggerCtx trace_id (aa) should be overridden; got: %s", line)
	}
}

// ── Panic 级别可恢复 ──────────────────────────────────────────────────────────

// TestLogger_Panic_Recoverable 验证 Panic() 会写入日志并触发 panic，且 panic 可被 recover 捕获。
func TestLogger_Panic_Recoverable(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	var caught any
	func() {
		defer func() { caught = recover() }()
		l.Panic().Msg("oh no").Send()
	}()

	if caught == nil {
		t.Fatal("expected a panic from Logger.Panic(), but nothing was caught")
	}
	if caught != "oh no" {
		t.Errorf("expected panic value %q, got %v", "oh no", caught)
	}

	// 日志应已写入（sendNow 在 panic 前执行）
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Error("expected log output before panic, got empty buffer")
	}
	m := decodeJSON(t, line)
	if m["level"] != "PANIC" {
		t.Errorf("expected level=PANIC, got: %v", m["level"])
	}
	if m["msg"] != "oh no" {
		t.Errorf("expected msg='oh no', got: %v", m["msg"])
	}
}

// TestLogger_Panic_Post 验证 Post 路径同样触发 panic。
func TestLogger_Panic_Post(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	var caught any
	func() {
		defer func() { caught = recover() }()
		l.Panic().Msg("post panic").Post()
	}()

	if caught == nil {
		t.Fatal("expected a panic from Post on Panic level")
	}
}

// ── Console 简写级别  ─────────────────────────────────────────────────────────────────────────────

// TestConsoleOutput_ShortLevel 验证默认简写模式输出单字母级别标识。
func TestConsoleOutput_ShortLevel(t *testing.T) {
	levels := []struct {
		name string
		fn   func(*Logger) *Event
		want string
	}{
		{"Trace", (*Logger).Trace, "T"},
		{"Debug", (*Logger).Debug, "D"},
		{"Info", (*Logger).Info, "I"},
		{"Warn", (*Logger).Warn, "W"},
		{"Error", (*Logger).Error, "E"},
	}
	for _, tc := range levels {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(Options{Out: Console(&buf), Level: Trace, ConsoleNoColor: true})
			tc.fn(l).Msg("x").Send()
			drainLogger(l)
			out := buf.String()
			// 简写级别字母前后各有一个空格（前点来自 beginRecord，后点来自补位空格）
			if !strings.Contains(out, " "+tc.want+" ") {
				t.Errorf("expected short level %q surrounded by spaces in: %q", tc.want, out)
			}
		})
	}
}

// TestConsoleOutput_FullLevel 验证 ConsoleLevelFull=true 时输出完整级别名称。
func TestConsoleOutput_FullLevel(t *testing.T) {
	levels := []struct {
		name string
		fn   func(*Logger) *Event
		want string
	}{
		{"Trace", (*Logger).Trace, "TRACE"},
		{"Debug", (*Logger).Debug, "DEBUG"},
		{"Info", (*Logger).Info, "INFO"},
		{"Warn", (*Logger).Warn, "WARN"},
		{"Error", (*Logger).Error, "ERROR"},
	}
	for _, tc := range levels {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := New(Options{Out: Console(&buf), Level: Trace, ConsoleLevelFull: true, ConsoleNoColor: true})
			tc.fn(l).Msg("x").Send()
			drainLogger(l)
			out := buf.String()
			if !strings.Contains(out, tc.want) {
				t.Errorf("expected full level %q in: %q", tc.want, out)
			}
		})
	}
}

// TestConsoleOutput_ShortVsFullDiffer 验证简写和完整模式输出确实不同。
func TestConsoleOutput_ShortVsFullDiffer(t *testing.T) {
	var shortBuf, fullBuf bytes.Buffer
	lShort := New(Options{Out: Console(&shortBuf), Level: Info, ConsoleNoColor: true})
	lFull := New(Options{Out: Console(&fullBuf), Level: Info, ConsoleNoColor: true, ConsoleLevelFull: true})

	lShort.Info().Msg("test").Send()
	lFull.Info().Msg("test").Send()
	drainLogger(lShort)
	drainLogger(lFull)

	shortOut := shortBuf.String()
	fullOut := fullBuf.String()
	if !strings.Contains(shortOut, " I ") {
		t.Errorf("short mode: expected \" I \" in %q", shortOut)
	}
	if !strings.Contains(fullOut, "INFO") {
		t.Errorf("full mode: expected \"INFO\" in %q", fullOut)
	}
	if strings.Contains(shortOut, "INFO") {
		t.Errorf("short mode: should not contain \"INFO\", got %q", shortOut)
	}
}

// ── Console Key 颜色 ─────────────────────────────────────────────────────────────────────────────

// TestConsoleOutput_KeyDimColor 验证颜色开启时字段 key 使用暗蓝色（\x1b[34m）显示，且 '=' 包含在色块内。
func TestConsoleOutput_KeyDimColor(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: Console(&buf), Level: Trace})
	l.Info().Str("mykey", "myval").Int("count", 3).Msg("dimtest").Send()
	drainLogger(l)

	out := buf.String()
	// ansiKey = "\x1b[34m" 应在 mykey 之前出现（dark blue）
	if !strings.Contains(out, "\x1b[34mmykey") {
		t.Errorf("expected dark-blue ANSI color (\\x1b[34m) before key 'mykey'; got: %q", out)
	}
	// '=' 在色块内，Reset \x1b[0m 应紧随 '=' 之后（value 不包裹色彩）
	if !strings.Contains(out, "mykey=\x1b[0m") {
		t.Errorf("expected 'mykey=' followed immediately by \\x1b[0m; got: %q", out)
	}
	// 顺序：dark-blue 在前，reset 在后
	blueIdx := strings.Index(out, "\x1b[34m")
	resetIdx := strings.Index(out, "mykey=\x1b[0m")
	if blueIdx < 0 || resetIdx < 0 || blueIdx > resetIdx {
		t.Errorf("expected dark-blue before reset; blueIdx=%d resetIdx=%d in: %q", blueIdx, resetIdx, out)
	}
}

// TestConsoleOutput_NoKeyColor_WhenNoColor 验证 ConsoleNoColor=true 时 key 无 dim 色。
func TestConsoleOutput_NoKeyColor_WhenNoColor(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: Console(&buf), Level: Trace, ConsoleNoColor: true})
	l.Info().Str("mykey", "myval").Msg("x").Send()
	drainLogger(l)

	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ConsoleNoColor=true: should not contain any ANSI escapes; got: %q", out)
	}
	// key 直接输出，不包裹色彩
	if !strings.Contains(out, "mykey=myval") {
		t.Errorf("expected plain 'mykey=myval'; got: %q", out)
	}
}

// ── File 输出（JSON 文件写入）───────────────────────────────────────────────────────────

// TestFileOutput_JSON 验证写入 JSON 日志文件的内容完整、格式正确。
func TestFileOutput_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	l := New(Options{Out: Save(path), Level: Info})

	l.Info().Str("env", "prod").Msg("file start").Send()
	l.Warn().Int("code", 42).Msg("warn entry").Send()
	l.Error().Err(errors.New("oops")).Msg("err entry").Send()
	drainLogger(l)
	if c := l.Closer(); c != nil {
		_ = c.Close()
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected >=3 log lines, got %d: %s", len(lines), raw)
	}

	m0 := decodeJSON(t, lines[0])
	if m0["level"] != "INFO" {
		t.Errorf("line0: expected level=INFO, got %v", m0["level"])
	}
	if m0["msg"] != "file start" {
		t.Errorf("line0: expected msg='file start', got %v", m0["msg"])
	}
	if m0["env"] != "prod" {
		t.Errorf("line0: expected env=prod, got %v", m0["env"])
	}

	m1 := decodeJSON(t, lines[1])
	if m1["level"] != "WARN" {
		t.Errorf("line1: expected level=WARN, got %v", m1["level"])
	}
	if v, _ := m1["code"].(float64); int(v) != 42 {
		t.Errorf("line1: expected code=42, got %v", m1["code"])
	}

	m2 := decodeJSON(t, lines[2])
	if m2["level"] != "ERROR" {
		t.Errorf("line2: expected level=ERROR, got %v", m2["level"])
	}
	if m2["error"] != "oops" {
		t.Errorf("line2: expected error=oops, got %v", m2["error"])
	}
}

// TestFileOutput_LevelFilter_JSON 验证文件输出同样遵守级别过滤。
func TestFileOutput_LevelFilter_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "filter.log")
	l := New(Options{Out: Save(path), Level: Warn})

	l.Debug().Msg("hidden debug").Send()
	l.Info().Msg("hidden info").Send()
	l.Warn().Msg("visible warn").Send()
	l.Error().Msg("visible error").Send()
	drainLogger(l)
	if c := l.Closer(); c != nil {
		_ = c.Close()
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(raw)
	if strings.Contains(content, "hidden") {
		t.Errorf("file output: Debug/Info lines should be filtered; got: %s", content)
	}
	if !strings.Contains(content, "visible warn") {
		t.Errorf("file output: expected Warn line; got: %s", content)
	}
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected exactly 2 lines (WARN+ERROR), got %d: %s", len(lines), content)
	}
}

// ── context 注入 ───────────────────────────────────────────────────────────────

// TestWithLogger_FromCtx 验证 WithLogger 注入的 Logger 能被 FromCtx 取出。
func TestWithLogger_FromCtx(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, Options{Level: Info})
	ctx := WithLogger(context.Background(), l)

	got := FromCtx(ctx)
	if got != l {
		t.Errorf("FromCtx 期望返回注入的 Logger, got %p, want %p", got, l)
	}
}

// TestFromCtx_NoLogger 验证未注入 Logger 时 FromCtx 返回非 nil 默认 Logger。
func TestFromCtx_NoLogger(t *testing.T) {
	got := FromCtx(context.Background())
	if got == nil {
		t.Fatal("FromCtx 未返回默认 Logger")
	}
}

// TestFromCtx_Nil 验证 FromCtx(nil) 返回非 nil 默认 Logger（不 panic）。
// nilContext 是一个测试辅助变量，允许触发 nil context 的防护路径但不触发 staticcheck SA1012。
func TestFromCtx_Nil(t *testing.T) {
	var nilCtx context.Context // 零值即 nil context，规避 staticcheck SA1012
	got := FromCtx(nilCtx)
	if got == nil {
		t.Fatal("FromCtx(nil) 未返回默认 Logger")
	}
}

// TestWithEventSink_Emit 验证 WithEventSink 注入的 sink 在日志写入时被触发。
func TestWithEventSink_Emit(t *testing.T) {
	type call struct {
		level Level
		msg   string
	}
	var mu sync.Mutex
	var calls []call

	sink := &testEventSink{fn: func(level Level, msg string, _ []byte) {
		mu.Lock()
		calls = append(calls, call{level, msg})
		mu.Unlock()
	}}

	var buf bytes.Buffer
	l := newTestLogger(&buf, Options{Level: Info})
	ctx := WithEventSink(context.Background(), sink)

	l.Info().Ctx(ctx).Msg("sink-message").Send()

	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("EventSink.Emit 未被调用")
	}
	if calls[0].msg != "sink-message" {
		t.Errorf("期望 msg=sink-message, got %q", calls[0].msg)
	}
	if calls[0].level != Info {
		t.Errorf("期望 level=Info, got %v", calls[0].level)
	}
}

// TestWithEventSink_PanicIsolated 验证 EventSink panic 不会影响日志主流程。
func TestWithEventSink_PanicIsolated(t *testing.T) {
	sink := &testEventSink{fn: func(Level, string, []byte) {
		panic("sink panic")
	}}

	var buf bytes.Buffer
	l := newTestLogger(&buf, Options{Level: Info})
	ctx := WithEventSink(context.Background(), sink)

	l.Info().Ctx(ctx).Msg("sink-safe").Send()
	if !strings.Contains(buf.String(), "sink-safe") {
		t.Fatalf("日志主流程不应因 EventSink panic 中断，got %q", buf.String())
	}
}

// testEventSink 用于测试的 EventSink 实现。
type testEventSink struct {
	fn func(Level, string, []byte)
}

func (s *testEventSink) Emit(level Level, msg string, fields []byte) {
	s.fn(level, msg, fields)
}

// ── writer errcount / dropped ───────────────────────────────────────────────

// TestErrCount_SendNowError 验证写入失败时 ErrCount 增加。
func TestErrCount_SendNowError(t *testing.T) {
	before := ErrCount()
	// failWriter 每次 Write 返回错误
	fw := &failWriter{}
	l := New(Options{Out: fw, Level: Info})
	l.Info().Msg("fail").Send()

	if ErrCount() <= before {
		t.Errorf("写入失败后 ErrCount 应增加，前=%d 后=%d", before, ErrCount())
	}
}

// failWriter 永远返回写入错误。
type failWriter struct{}

func (f *failWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write failed")
}

// TestDropped_ReturnsZeroOrMore 验证 Dropped() 不崩溃且返回 ≥0。
func TestDropped_ReturnsZeroOrMore(t *testing.T) {
	d := Dropped()
	if d < 0 {
		t.Errorf("Dropped() 不应返回负数, got %d", d)
	}
}

// TestSend_NilOut_NocrashErrCountInc 验证 out=nil 时 Send() 不崩溃，且 ErrCount 增加。
func TestSend_NilOut_NoCrashErrCountInc(t *testing.T) {
	before := ErrCount()
	// 直接传入 nil 作为 Out；options.go 会将其设置为 io.Discard，
	// 所以需要通过 To() 在构建后将 out 替换为 nil 来触发该分支。
	// 使用 To(nil) 构造 out=nil 的 Logger。
	base := New(Options{Level: Info, Out: new(bytes.Buffer)})
	nilOutLogger := base.To(nil)
	// Send 不应 panic
	nilOutLogger.Info().Msg("nil-out-test").Send()
	if ErrCount() <= before {
		t.Errorf("out=nil Send 应使 ErrCount 增加，前=%d 后=%d", before, ErrCount())
	}
}

// TestErrCount_InitialState 验证 ErrCount 在没有写入错误时不为负。
func TestErrCount_InitialState(t *testing.T) {
	c := ErrCount()
	if c < 0 {
		t.Errorf("ErrCount() 不应为负数, got %d", c)
	}
}

// ── Config / defaultLogger / resolveOptions ─────────────────────────────────

// TestConfig_SetsLevel 验证 Config 更改全局 Options 后 New() 零参使用新配置。
func TestConfig_SetsLevel(t *testing.T) {
	// 保存原始配置，测后还原
	orig := loadGlobalOptions()
	t.Cleanup(func() { Config(orig) })

	Config(Options{Level: Warn})
	l := New() // 零参，应继承全局 Warn 配置
	if l.GetLevel() != Warn {
		t.Errorf("New() 期望 Level=Warn, got %v", l.GetLevel())
	}
}

// TestDefaultLogger_NonNil 验证 defaultLogger() 返回非 nil Logger。
func TestDefaultLogger_NonNil(t *testing.T) {
	l := defaultLogger()
	if l == nil {
		t.Fatal("defaultLogger() 不应返回 nil")
	}
}

// TestDefaultLogger_Cached 验证 defaultLogger() 连续调用返回同一实例。
func TestDefaultLogger_Cached(t *testing.T) {
	l1 := defaultLogger()
	l2 := defaultLogger()
	if l1 != l2 {
		t.Errorf("defaultLogger 应返回缓存单例, l1=%p l2=%p", l1, l2)
	}
}

// TestDefaultLogger_RebuiltAfterConfig 验证 Config() 后 defaultLogger() 重建缓存。
func TestDefaultLogger_RebuiltAfterConfig(t *testing.T) {
	orig := loadGlobalOptions()
	t.Cleanup(func() { Config(orig) })

	l1 := defaultLogger()
	Config(Options{Level: Debug})
	l2 := defaultLogger()
	// Config 清除缓存，l2 是新实例
	if l1 == l2 {
		t.Errorf("Config 后 defaultLogger 应返回新实例，但仍为同一指针")
	}
}

// TestResolveOptions_FilePath 验证 Options.FilePath 非空时 resolveOptions 自动打开文件写入器。
func TestResolveOptions_FilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auto.log")

	opts, closer := resolveOptions(Options{FilePath: path, Level: Info})
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	if opts.Out == nil {
		t.Fatal("resolveOptions FilePath: opts.Out 不应为 nil")
	}
	if _, err := opts.Out.Write([]byte("probe\n")); err != nil {
		t.Errorf("resolveOptions FilePath: Write 失败: %v", err)
	}
}

// ── FatalFunc / PanicFunc 钩子 ──────────────────────────────────────────────────

// TestSetFatalFunc 验证 Fatal 使用 FatalFunc 而非直接 os.Exit。
func TestSetFatalFunc(t *testing.T) {
	var gotCode int
	var called bool

	old := GetFatalFunc()
	defer SetFatalFunc(old)
	SetFatalFunc(func(code int) {
		gotCode = code
		called = true
	})

	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Fatal().Msg("bye").Send()

	if !called {
		t.Fatal("FatalFunc 没有被调用")
	}
	if gotCode != 1 {
		t.Errorf("期望退出码 1，实际 %d", gotCode)
	}
	// 日志必须在 FatalFunc 调用前已写入
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Error("fatal 日志内容为空，写入应在 FatalFunc 之前完成")
	}
	m := decodeJSON(t, line)
	if m["level"] != "FATAL" {
		t.Errorf("期望 level=FATAL，got=%v", m["level"])
	}
	if m["msg"] != "bye" {
		t.Errorf("期望 msg=bye，got=%v", m["msg"])
	}
}

// TestSetFatalFunc_Post 验证 Fatal+Post 路径同样使用 FatalFunc。
// 注意：Post 路径调用 drainAndExit()，会关闭包级 worker goroutine；
// 本测试置于文件末尾，避免影响其他依赖 Post 的测试。
func TestSetFatalFunc_Post(t *testing.T) {
	var called bool

	old := GetFatalFunc()
	defer SetFatalFunc(old)
	SetFatalFunc(func(int) { called = true })

	var buf bytes.Buffer
	l := newTestLogger(&buf)
	// Post 路径：stolenBuf 投入 worker，drainAndExit 等待排空后调用 FatalFunc
	l.Fatal().Msg("bye async").Post()

	if !called {
		t.Fatal("Post 路径 FatalFunc 没有被调用")
	}
}

// TestSetPanicFunc 验证 Panic 级别使用 PanicFunc 而非内置 panic。
func TestSetPanicFunc(t *testing.T) {
	var gotMsg string
	var called bool

	old := GetPanicFunc()
	defer SetPanicFunc(old)
	SetPanicFunc(func(msg string) {
		gotMsg = msg
		called = true
		// 不真正 panic：模拟测试中捕获消息后静默返回
	})

	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Panic().Msg("test panic").Send()

	if !called {
		t.Fatal("PanicFunc 没有被调用")
	}
	if gotMsg != "test panic" {
		t.Errorf("期望 panic 消息 'test panic'，got=%q", gotMsg)
	}
	// 日志必须在 PanicFunc 调用前已写入
	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Error("panic 日志内容为空")
	}
	m := decodeJSON(t, line)
	if m["level"] != "PANIC" {
		t.Errorf("期望 level=PANIC，got=%v", m["level"])
	}
}

// TestGetFatalFunc_Default 验证默认 FatalFunc 不为 nil。
func TestGetFatalFunc_Default(t *testing.T) {
	fn := GetFatalFunc()
	if fn == nil {
		t.Fatal("GetFatalFunc 不应返回 nil")
	}
}

// TestGetPanicFunc_Default 验证默认 PanicFunc 行为与内置 panic 一致。
func TestGetPanicFunc_Default(t *testing.T) {
	fn := GetPanicFunc()
	if fn == nil {
		t.Fatal("GetPanicFunc 不应返回 nil")
	}
	var caught any
	func() {
		defer func() { caught = recover() }()
		fn("default panic test")
	}()
	if caught == nil {
		t.Fatal("默认 PanicFunc 应触发 panic")
	}
	if caught != "default panic test" {
		t.Errorf("期望捕获 'default panic test'，got=%v", caught)
	}
}

// ── Event.JSON 方法 ──────────────────────────────────────────────────────────

// TestLogger_EventJSON_JSONOutput 验证 JSON 输出模式下 raw bytes 直接嵌入为嵌套对象。
func TestLogger_EventJSON_JSONOutput(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info().JSON("resp", []byte(`{"code":200,"desc":"ok"}`)).Msg("upstream").Send()
	drainLogger(l)

	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	resp, ok := m["resp"]
	if !ok {
		t.Fatalf("JSON 输出缺少 resp 字段: %s", buf.String())
	}
	obj, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("resp 应为嵌套对象，got: %T", resp)
	}
	if obj["code"] != float64(200) {
		t.Errorf("resp.code 期望 200，got: %v", obj["code"])
	}
}

// TestLogger_EventJSON_ConsoleOutput 验证 Console 输出模式下 key=rawValue 原样嵌入。
func TestLogger_EventJSON_ConsoleOutput(t *testing.T) {
	var buf bytes.Buffer
	l := New(Options{Out: Console(&buf), Level: Trace, ConsoleNoColor: true})
	l.Info().JSON("resp", []byte(`{"code":200}`)).Msg("ok").Send()
	drainLogger(l)

	out := buf.String()
	if !strings.Contains(out, `resp=`) {
		t.Errorf("Console 输出缺少 resp= 字段: %q", out)
	}
	if !strings.Contains(out, `{"code":200}`) {
		t.Errorf("Console 输出 raw JSON 应原样出现: %q", out)
	}
}

// TestLogger_EventJSON_Nil 验证 raw 为 nil 时字段被跳过。
func TestLogger_EventJSON_Nil(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info().JSON("resp", nil).Msg("ok").Send()
	drainLogger(l)

	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	if _, ok := m["resp"]; ok {
		t.Error("raw=nil 时不应输出 resp 字段")
	}
}

// TestLogger_EventJSON_Array 验证 JSON 数组原样嵌入。
func TestLogger_EventJSON_Array(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info().JSON("ids", []byte(`[1,2,3]`)).Msg("list").Send()
	drainLogger(l)

	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	ids, ok := m["ids"]
	if !ok {
		t.Fatalf("JSON 输出缺少 ids 字段: %s", buf.String())
	}
	arr, ok := ids.([]any)
	if !ok {
		t.Fatalf("ids 应为数组，got: %T", ids)
	}
	if len(arr) != 3 {
		t.Errorf("ids 长度期望 3，got: %d", len(arr))
	}
}

// TestLabelBuilder_JSON_JSONOutput 验证 LabelBuilder.JSON 预置字段在 JSON 输出中嵌入正确。
func TestLabelBuilder_JSON_JSONOutput(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	child := l.Labels().JSON("meta", []byte(`{"env":"prod"}`)).Build()
	child.Info().Msg("startup").Send()
	drainLogger(child)

	m := decodeJSON(t, strings.TrimSpace(buf.String()))
	meta, ok := m["meta"]
	if !ok {
		t.Fatalf("LabelBuilder.JSON 输出缺少 meta 字段: %s", buf.String())
	}
	obj, ok := meta.(map[string]any)
	if !ok {
		t.Fatalf("meta 应为嵌套对象，got: %T", meta)
	}
	if obj["env"] != "prod" {
		t.Errorf("meta.env 期望 prod，got: %v", obj["env"])
	}
}

// ── OOM 防护：Send 路径大 buf 高水位清理 ──────────────────────────────────────

// TestSend_LargeLog_BufCapBounded 验证写入超大日志后，归还到 eventPool 的 Event
// 的 buf 容量不超过 maxEventBufCap，防止大内存块永驻 pool（高水位 OOM 问题）。
func TestSend_LargeLog_BufCapBounded(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	// 构造一个超过 maxEventBufCap 的日志：字符串字段值为 64KB。
	largeVal := strings.Repeat("x", maxEventBufCap+1024)
	l.Info().Str("large", largeVal).Send()

	// 从 eventPool 取出 Event，检查其 buf 容量已被清理。
	e := eventPool.Get().(*Event) //nolint:forcetypeassert
	capAfter := cap(e.buf)
	eventPool.Put(e) // 立即放回，不污染后续测试

	if capAfter > maxEventBufCap {
		t.Errorf("Send 超大日志后 eventPool 中 Event.buf cap = %d > maxEventBufCap %d（高水位泄漏）",
			capAfter, maxEventBufCap)
	}
}

// TestSend_NormalLog_BufReused 验证普通大小日志 Send 后，Event 的 buf 被正常保留（性能优化路径有效）。
func TestSend_NormalLog_BufReused(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	// 正常日志：buf 应保留在 Event 中（buf 非 nil，cap 在正常范围内）。
	l.Info().Str("k", "v").Msg("normal").Send()

	// 连续发送多次，验证 buf 复用路径未引入分配问题（无 panic）。
	for range 100 {
		l.Info().Int("n", 1).Send()
	}
}

// TestSend_LargeLog_Pressure 验证高压场景下反复写入超大日志不会 OOM（压力验证）。
// 若高水位修复缺失，此测试运行完内存应有明显膨胀；修复后应保持平稳。
func TestSend_LargeLog_Pressure(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	largeVal := strings.Repeat("y", maxEventBufCap*2) // 每条 ~64KB

	for range 200 {
		l.Info().Str("data", largeVal).Send()
	}
	// 验证输出中确有日志（不静默丢弃）
	if !strings.Contains(buf.String(), "data") {
		t.Error("压力测试：日志内容应包含 data 字段")
	}
}

// ── 安全：appendJSONKeyFast 合约验证 ──────────────────────────────────────────

// TestAppendJSONKeyFast_UnsafeKey_Documentation 文档化 appendJSONKeyFast 不转义 key 的已知行为：
// 当 key 含 `"` 或 `\` 时，JSON 输出将不合法（JSON 注入风险）。
// 这是有意识的性能设计决策，调用方合约要求 key 必须为"合法 ASCII 标识符"。
// 此测试作为回归锚点：若 appendJSONKeyFast 将来改为全转义，此测试会失败，提醒更新文档。
func TestAppendJSONKeyFast_UnsafeKey_Documentation(t *testing.T) {
	dst := appendJSONKeyFast([]byte{}, `safe_key`)
	got := string(dst)
	want := `,"safe_key":`
	if got != want {
		t.Errorf("appendJSONKeyFast 合法 key: got %q, want %q", got, want)
	}

	// 含特殊字符的 key：直接插入，不转义——这是已知行为，非 bug，
	// 但调用方必须保证 key 来自可信来源（非用户输入）。
	dstUnsafe := appendJSONKeyFast([]byte{}, `key"injected`)
	gotUnsafe := string(dstUnsafe)
	// 期望原样输出（无转义），即输出包含未转义引号。
	if !strings.Contains(gotUnsafe, `key"injected`) {
		t.Errorf("appendJSONKeyFast 文档化行为：未转义 key 应原样输出，got %q", gotUnsafe)
	}
	// 验证未经 appendJSONKey（含转义版）的差异
	dstSafe := appendJSONKey([]byte{}, `key"injected`)
	gotSafe := string(dstSafe)
	if strings.Contains(gotSafe, `key"injected`) {
		t.Errorf("appendJSONKey（安全版）应转义 key 中的引号，got %q", gotSafe)
	}
}

// ── P1: source 字段 JSON 转义 ─────────────────────────────────────────────────

// TestSourceJSON_WindowsPathEscaped 验证含反斜杠的文件路径（Windows 构建场景）
// 在 JSON source 字段中被正确转义，输出始终为合法 JSON。
func TestSourceJSON_WindowsPathEscaped(t *testing.T) {
	// appendSourceJSONField 是包内函数，直接测试。
	buf := appendSourceJSONField(nil, `C:\Users\project\main.go`, 42)
	// 期望：,"source":"C:\\Users\\project\\main.go:42"
	got := string(buf)
	if !strings.Contains(got, `C:\\Users\\project\\main.go`) {
		t.Errorf("Windows 路径 '\\' 未被转义: %q", got)
	}
	// 输出必须是合法 JSON 片段（包裹为完整对象后解析）
	jsonStr := `{` + got[1:] + `}`
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		t.Errorf("source 字段生成了非法 JSON：%v\n原始：%q", err, got)
	}
}

// TestSourceJSON_QuoteInPath 验证文件路径含 '"' 时被转义，输出合法 JSON。
func TestSourceJSON_QuoteInPath(t *testing.T) {
	buf := appendSourceJSONField(nil, `/path/to/"quoted"/file.go`, 10)
	got := string(buf)
	// 期望引号被转义
	if strings.Contains(got, `/"quoted"`) {
		t.Errorf("路径中的 '\"' 未被转义: %q", got)
	}
	jsonStr := `{` + got[1:] + `}`
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		t.Errorf("source 字段生成了非法 JSON：%v\n原始：%q", err, got)
	}
}

// TestSourceJSON_NormalPath 验证常规 Unix 路径（无特殊字符）输出合法 JSON 且值正确。
func TestSourceJSON_NormalPath(t *testing.T) {
	buf := appendSourceJSONField(nil, `/home/user/project/main.go`, 123)
	got := string(buf)
	jsonStr := `{` + got[1:] + `}`
	var m map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		t.Errorf("正常路径 source 字段应为合法 JSON：%v\n原始：%q", err, got)
	}
	src, _ := m["source"].(string)
	if src != `/home/user/project/main.go:123` {
		t.Errorf("source 值错误：got %q, want %q", src, `/home/user/project/main.go:123`)
	}
}

// ── P2: Label prefix 上限防护 ─────────────────────────────────────────────────

// TestLabel_PrefixBound_NoOOM 验证链式 Label 超过 maxPrefixLen 时 prefix 被限制，
// 不会无界增长，后续日志仍能正常输出（不 panic、不 OOM）。
func TestLabel_PrefixBound_NoOOM(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	// 每个 Label 追加约 32 字节；超过 maxPrefixLen(16KB) 需约 600 次。
	for i := 0; i < 800; i++ {
		l = l.Label("field_key_padding", "value_padding_data")
	}
	// prefix 不得超过上限
	if len(l.prefix) > maxPrefixLen {
		t.Errorf("prefix 超过 maxPrefixLen：got %d, want ≤ %d", len(l.prefix), maxPrefixLen)
	}
	// 日志仍然可以正常输出
	buf.Reset()
	l.Info().Msg("still works").Send()
	if buf.Len() == 0 {
		t.Error("prefix 达到上限后日志应仍能正常输出")
	}
}

// TestLabel_PrefixBound_OutputValid 验证 prefix 截断后输出仍为合法 JSON。
func TestLabel_PrefixBound_OutputValid(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	for i := 0; i < 800; i++ {
		l = l.Label("k", "v")
	}
	buf.Reset()
	l.Info().Msg("test").Send()

	line := strings.TrimRight(buf.String(), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Errorf("prefix 截断后输出非合法 JSON：%v\n原始：%q", err, line)
	}
}

// ── P1: LabelBuilder prefix 上限防护 ─────────────────────────────────────────

// TestLabelBuilder_PrefixBound_NoOOM 验证 LabelBuilder 链式 Str() 超过 maxPrefixLen
// 后在 Build() 处截断，不会无界增长，且日志仍能正常输出。
func TestLabelBuilder_PrefixBound_NoOOM(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	b := l.Labels()
	for i := 0; i < 800; i++ {
		b = b.Str("field_key_padding", "value_padding_data")
	}
	nl := b.Build()

	if len(nl.prefix) > maxPrefixLen {
		t.Errorf("LabelBuilder.Build() prefix 超过 maxPrefixLen：got %d, want ≤ %d",
			len(nl.prefix), maxPrefixLen)
	}

	buf.Reset()
	nl.Info().Msg("lb still works").Send()
	if buf.Len() == 0 {
		t.Error("LabelBuilder prefix 截断后日志应仍能正常输出")
	}
}

// TestTag_ControlCharSanitized 验证 Tag 中的控制字符（\n \r \x00）
// 在输出时被替换为 '_'，防止日志注入。
func TestTag_ControlCharSanitized(t *testing.T) {
	injected := []struct {
		name  string
		input string
	}{
		{"LF", "bad\ntag"},
		{"CR", "bad\rtag"},
		{"NUL", "bad\x00tag"},
		{"ctrl", "\x01\x02\x1f"},
	}
	for _, tc := range injected {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := newTestLogger(&buf)
			l = l.Tag(tc.input)
			buf.Reset()
			l.Info().Msg("msg").Send()
			// 去除正常行尾换行后再检测，避免把日志行终止符误判为注入字符。
			line := strings.TrimRight(buf.String(), "\n")
			for _, bad := range []string{"\n", "\r", "\x00"} {
				if strings.Contains(line, bad) {
					t.Errorf("tag %q: 输出中仍存在控制字符 %q\n原始：%q", tc.input, bad, line)
				}
			}
		})
	}
}

// TestTag_MaxLen 验证超长 tag 被截断。
func TestTag_MaxLen(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	long := strings.Repeat("x", 200)
	l = l.Tag(long)
	buf.Reset()
	l.Info().Msg("msg").Send()
	out := buf.String()
	if strings.Contains(out, long) {
		t.Errorf("超长 tag 未截断，输出：%q", out)
	}
}

// TestLabelBuilder_PrefixBound_OutputValid 验证 LabelBuilder.Build() prefix 截断后
// 输出仍为合法 JSON。
func TestLabelBuilder_PrefixBound_OutputValid(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	b := l.Labels()
	for i := 0; i < 800; i++ {
		b = b.Str("k", "v")
	}
	buf.Reset()
	b.Build().Info().Msg("lb test").Send()

	line := strings.TrimRight(buf.String(), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Errorf("LabelBuilder prefix 截断后输出非合法 JSON：%v\n原始：%q", err, line)
	}
}
