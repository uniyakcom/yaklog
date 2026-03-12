package adapter_test

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/uniyakcom/yaklog"
	"github.com/uniyakcom/yaklog/adapter"
)

// ── 辅助 ──────────────────────────────────────────────────────────────────────

func newTestLogger(buf *bytes.Buffer, lvl yaklog.Level) *yaklog.Logger {
	return yaklog.New(yaklog.Options{Out: buf, Level: lvl})
}

// ── stdlog 适配 ───────────────────────────────────────────────────────────────

// TestToStdLogWriter_Basic 验证写入内容作为日志消息转发。
func TestToStdLogWriter_Basic(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	w := adapter.ToStdLogWriter(l, slog.LevelWarn)
	_, err := w.Write([]byte("legacy warning\n"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "legacy warning") {
		t.Errorf("expected message in output, got: %q", out)
	}
}

// TestToStdLogWriter_StripNewline 验证尾部换行被正确去除，消息内容不含 \n。
func TestToStdLogWriter_StripNewline(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Debug)

	w := adapter.ToStdLogWriter(l, slog.LevelInfo)
	// 带 \r\n 尾
	_, _ = w.Write([]byte("line one\r\n"))

	out := buf.String()
	// JSON 输出中 msg 字段值不应包含换行
	if strings.Contains(out, `\n"`) || strings.Contains(out, `\r`) {
		t.Errorf("message should not contain newline escapes, got: %q", out)
	}
}

// TestToStdLogWriter_BelowLevel 验证低于 Logger 级别时 Write 丢弃消息（零开销）。
func TestToStdLogWriter_BelowLevel(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Error) // Logger 仅接受 Error+

	w := adapter.ToStdLogWriter(l, slog.LevelInfo) // Info 低于 Error，应丢弃
	_, _ = w.Write([]byte("dropped message\n"))

	if buf.Len() != 0 {
		t.Errorf("expected no output for below-level message, got: %q", buf.String())
	}
}

// TestToStdLogWriter_WithStdLog 验证配合 log.SetOutput 使用。
func TestToStdLogWriter_WithStdLog(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	// 保存原始输出，测后还原
	orig := log.Writer()
	origFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(orig)
		log.SetFlags(origFlags)
	})

	log.SetOutput(adapter.ToStdLogWriter(l, slog.LevelInfo))
	log.SetFlags(0)
	log.Print("from stdlib log")

	out := buf.String()
	if !strings.Contains(out, "from stdlib log") {
		t.Errorf("expected stdlib log message in output, got: %q", out)
	}
}

// ── SetDefault ────────────────────────────────────────────────────────────────

// TestSetDefault 验证 SetDefault 将 yaklog.Logger 安装为 slog 默认 logger。
func TestSetDefault(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	// 保存原始默认 logger，测后还原
	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })

	adapter.SetDefault(l)

	// 调用 slog 包级函数，应路由到 l
	slog.Info("via slog default", "k", "v")

	out := buf.String()
	if !strings.Contains(out, "via slog default") {
		t.Errorf("expected message in output, got: %q", out)
	}
}

// TestRefreshDefault 验证 RefreshDefault 在 Logger.SetLevel 变更后使 slog.Default 的
// Enabled 反映新级别（注：这里通过验证新建的 slog.Logger 仍路由到同一 buf 来间接确认）。
func TestRefreshDefault(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })

	adapter.SetDefault(l)

	// 写一条消息确认已设置成功
	slog.Info("before-refresh")
	if !strings.Contains(buf.String(), "before-refresh") {
		t.Fatalf("SetDefault 后应输出日志，got: %q", buf.String())
	}

	// 调用 RefreshDefault 不应改变输出目标
	adapter.RefreshDefault()

	buf.Reset()
	slog.Info("after-refresh")
	if !strings.Contains(buf.String(), "after-refresh") {
		t.Errorf("RefreshDefault 后应仍能输出日志，got: %q", buf.String())
	}
}

// TestSetDefault_Warn 验证 slog.Warn 通过 adapter 路由到 yaklog Warn 路径。
func TestSetDefault_Warn(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })

	adapter.SetDefault(l)
	slog.Warn("warn-message", "key", "val")

	out := buf.String()
	if !strings.Contains(out, "warn-message") {
		t.Errorf("expected warn-message in output, got: %q", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("expected WARN level in output, got: %q", out)
	}
}

// ── slogAdapter WithAttrs / WithGroup ─────────────────────────────────────────

// TestSlogAdapter_WithAttrs 验证 WithAttrs 将属性预注入新 handler，每条后续日志都携带这些字段。
func TestSlogAdapter_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })

	// 通过 slog.New 直接构造，调用 WithAttrs
	h := slog.New(adapter.NewHandler(l))
	h = h.With("service", "gateway", "version", "1.2")

	h.Info("startup")

	out := buf.String()
	if !strings.Contains(out, "startup") {
		t.Errorf("WithAttrs: 期望含消息 startup, got: %q", out)
	}
	if !strings.Contains(out, "service") {
		t.Errorf("WithAttrs: 期望含预注入字段 service, got: %q", out)
	}
	if !strings.Contains(out, "gateway") {
		t.Errorf("WithAttrs: 期望含值 gateway, got: %q", out)
	}
}

// TestSlogAdapter_WithGroup 验证 WithGroup 将后续字段键加上 "group." 前缀。
func TestSlogAdapter_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	h := slog.New(adapter.NewHandler(l))
	hg := h.WithGroup("req")
	if hg == nil {
		t.Fatal("WithGroup 不应返回 nil")
	}
	// 向 grouped handler 写一条日志，字段应带 "req." 前缀
	hg.Info("in-group", "id", "abc-123", "method", "GET")
	out := buf.String()
	if !strings.Contains(out, "in-group") {
		t.Errorf("WithGroup handler: 期望含消息 in-group, got: %q", out)
	}
	if !strings.Contains(out, "req.id") {
		t.Errorf("WithGroup handler: 期望字段 req.id，got: %q", out)
	}
	if !strings.Contains(out, "req.method") {
		t.Errorf("WithGroup handler: 期望字段 req.method，got: %q", out)
	}
	if !strings.Contains(out, "abc-123") {
		t.Errorf("WithGroup handler: 期望值 abc-123，got: %q", out)
	}
}

// TestSlogAdapter_WithGroup_Nested 验证嵌套 WithGroup 前缀正确叠加。
func TestSlogAdapter_WithGroup_Nested(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	h := slog.New(adapter.NewHandler(l))
	h.WithGroup("a").WithGroup("b").Info("nested", "k", "v")
	out := buf.String()
	if !strings.Contains(out, "a.b.k") {
		t.Errorf("嵌套 WithGroup: 期望字段 a.b.k，got: %q", out)
	}
}

// TestSlogAdapter_WithGroup_Empty 验证 WithGroup("") 返回原 handler，行为不变。
func TestSlogAdapter_WithGroup_Empty(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	h := slog.New(adapter.NewHandler(l))
	hg := h.WithGroup("")
	hg.Info("no-prefix", "k", "v")
	out := buf.String()
	if !strings.Contains(out, "\"k\"") {
		t.Errorf("WithGroup(\"\") 不应加前缀，got: %q", out)
	}
}

// TestSlogAdapter_WithGroup_WithAttrs 验证 WithGroup 后 With() 的属性键带前缀。
func TestSlogAdapter_WithGroup_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	h := slog.New(adapter.NewHandler(l)).WithGroup("http").With("method", "POST")
	h.Info("request")
	out := buf.String()
	if !strings.Contains(out, "http.method") {
		t.Errorf("WithGroup + With: 期望字段 http.method，got: %q", out)
	}
}

// ── WithAttrs 时序一致性 ──────────────────────────────────────────────────────

// TestSlogAdapter_WithAttrs_Consistency 验证 WithAttrs 预注入路径与 Handle 直接传入路径
// 产生相同的字段输出：两条日志除消息内容外，其余结构化字段应逐一匹配。
func TestSlogAdapter_WithAttrs_Consistency(t *testing.T) {
	cases := []struct {
		name  string
		attrs []slog.Attr
	}{
		{
			name:  "string",
			attrs: []slog.Attr{slog.String("env", "prod")},
		},
		{
			name:  "int64",
			attrs: []slog.Attr{slog.Int64("count", 42)},
		},
		{
			name:  "bool",
			attrs: []slog.Attr{slog.Bool("ok", true)},
		},
		{
			name:  "float64",
			attrs: []slog.Attr{slog.Float64("ratio", 0.75)},
		},
		{
			name:  "group",
			attrs: []slog.Attr{slog.Group("http", slog.String("method", "GET"), slog.Int("status", 200))},
		},
		{
			name: "multi",
			attrs: []slog.Attr{
				slog.String("svc", "api"),
				slog.Int64("port", 8080),
				slog.Bool("debug", false),
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// 路径一：WithAttrs 预注入
			var buf1 bytes.Buffer
			l1 := newTestLogger(&buf1, yaklog.Info)
			h1 := slog.New(adapter.NewHandler(l1)).With(attrsToAny(tc.attrs)...)
			h1.Info("consistency-check")

			// 路径二：Handle 直接传入（通过 slog.Logger.Info 的 args 路径）
			var buf2 bytes.Buffer
			l2 := newTestLogger(&buf2, yaklog.Info)
			h2 := slog.New(adapter.NewHandler(l2))
			h2.Info("consistency-check", attrsToAny(tc.attrs)...)

			out1 := buf1.String()
			out2 := buf2.String()

			// 验证所有属性键值在两条输出中均出现
			for _, attr := range flattenAttrs(tc.attrs, "") {
				if !strings.Contains(out1, attr) {
					t.Errorf("[WithAttrs 路径] 缺少字段 %q：%s", attr, out1)
				}
				if !strings.Contains(out2, attr) {
					t.Errorf("[Handle 路径] 缺少字段 %q：%s", attr, out2)
				}
			}
		})
	}
}

// attrsToAny 将 []slog.Attr 转换为 slog.Logger.With/Info 接受的 []any 参数。
func attrsToAny(attrs []slog.Attr) []any {
	args := make([]any, 0, len(attrs))
	for _, a := range attrs {
		args = append(args, a)
	}
	return args
}

// flattenAttrs 递归展开 slog.Attr 列表为期望在 JSON 输出中出现的字符串片段。
// 与 appendSlogAttr / appendSlogAttrToBuilder 保持同等展平语义。
func flattenAttrs(attrs []slog.Attr, prefix string) []string {
	var result []string
	for _, a := range attrs {
		key := a.Key
		if prefix != "" {
			key = prefix + "." + a.Key
		}
		a.Value = a.Value.Resolve()
		if a.Value.Kind() == slog.KindGroup {
			result = append(result, flattenAttrs(a.Value.Group(), key)...)
			continue
		}
		result = append(result, `"`+key+`"`)
	}
	return result
}

// ── Handle — 全属性类型 ────────────────────────────────────────────────────────

// TestSlogAdapter_Handle_AllKinds 验证 Handle 对所有 slog.Attr Kind 均能正确路由输出。
func TestSlogAdapter_Handle_AllKinds(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })
	adapter.SetDefault(l)

	slog.Info("allkinds",
		slog.String("str", "hello"),
		slog.Int64("i64", -99),
		slog.Uint64("u64", 200),
		slog.Float64("f64", 3.14),
		slog.Bool("flag", true),
		slog.Duration("dur", 500*time.Millisecond),
		slog.Time("ts", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
		slog.Any("any", []int{1, 2, 3}),
	)

	out := buf.String()
	checks := []string{"str", "hello", "-99", "200", "3.14", "flag", "dur", "ts", "any"}
	for _, c := range checks {
		if !strings.Contains(out, c) {
			t.Errorf("Handle allkinds: 期望含 %q, got: %q", c, out)
		}
	}
}

// TestSlogAdapter_Handle_WithContext 验证 slog.InfoContext 传递的 ctx 中
// 由 yaklog.WithTrace / yaklog.WithField 注入的字段能正确输出到日志。
func TestSlogAdapter_Handle_WithContext(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)

	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })
	adapter.SetDefault(l)

	// 构造含 trace_id 和自定义字段的 ctx
	var traceID [16]byte
	copy(traceID[:], "test-trace-0001\x00")
	ctx := yaklog.WithTrace(context.Background(), traceID)
	ctx = yaklog.WithField(ctx, "req_id", "abc-123")

	slog.InfoContext(ctx, "ctx-aware-msg", "extra", "val")

	out := buf.String()
	if !strings.Contains(out, "ctx-aware-msg") {
		t.Errorf("expected message, got: %q", out)
	}
	if !strings.Contains(out, "trace_id") {
		t.Errorf("expected trace_id field from ctx, got: %q", out)
	}
	if !strings.Contains(out, "req_id") {
		t.Errorf("expected req_id field from ctx, got: %q", out)
	}
	if !strings.Contains(out, "abc-123") {
		t.Errorf("expected req_id value in output, got: %q", out)
	}
}

// TestSlogAdapter_Handle_DebugLevel 验证 Debug 级别（slog default < Info）经 Handle 路由到 yaklog.Debug。
func TestSlogAdapter_Handle_DebugLevel(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Debug)

	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })
	adapter.SetDefault(l)

	slog.Debug("debug-msg", "key", "val")

	out := buf.String()
	if !strings.Contains(out, "debug-msg") {
		t.Errorf("Handle Debug: 期望含 debug-msg, got: %q", out)
	}
}

// ── Group 展平与 LogValuer 解析 ───────────────────────────────────────────────

// TestSlogAdapter_Handle_Group 验证 KindGroup 属性被展平为"前缀.键"格式。
func TestSlogAdapter_Handle_Group(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)
	h := slog.New(adapter.NewHandler(l))

	h.Info("request",
		slog.Group("http",
			slog.String("method", "GET"),
			slog.Int("status", 200),
		),
	)

	out := buf.String()
	if !strings.Contains(out, "http.method") {
		t.Errorf("期望展平字段 http.method，got: %q", out)
	}
	if !strings.Contains(out, "GET") {
		t.Errorf("期望值 GET，got: %q", out)
	}
	if !strings.Contains(out, "http.status") {
		t.Errorf("期望展平字段 http.status，got: %q", out)
	}
}

// echoValuer 是一个简单的 slog.LogValuer，将自身展开为固定字符串。
type echoValuer struct{ v string }

func (e echoValuer) LogValue() slog.Value { return slog.StringValue(e.v) }

// TestSlogAdapter_Handle_LogValuer 验证 KindLogValuer 被正确解析并输出展开值。
func TestSlogAdapter_Handle_LogValuer(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf, yaklog.Info)
	h := slog.New(adapter.NewHandler(l))

	h.Info("msg", slog.Any("val", echoValuer{v: "expanded-value"}))

	out := buf.String()
	if !strings.Contains(out, "expanded-value") {
		t.Errorf("期望 LogValuer 被展开为 expanded-value，got: %q", out)
	}
}
