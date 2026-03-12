package yaklog

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

// ── fieldsFromCtx ─────────────────────────────────────────────────────────────

func TestFieldsFromCtx_Empty(t *testing.T) {
	names := fieldsFromCtx(context.Background())
	if len(names) != 0 {
		t.Fatalf("expected empty, got %v", names)
	}
}

func TestWithField_SingleField(t *testing.T) {
	ctx := WithField(context.Background(), "req_id", "abc")
	names := fieldsFromCtx(ctx)
	if len(names) != 1 || names[0] != "req_id" {
		t.Fatalf("unexpected names: %v", names)
	}
	if v, _ := ctx.Value(fieldKey{name: "req_id"}).(string); v != "abc" {
		t.Fatalf("unexpected value: %q", v)
	}
}

func TestWithField_MultipleFields_Order(t *testing.T) {
	ctx := context.Background()
	ctx = WithField(ctx, "a", "1")
	ctx = WithField(ctx, "b", "2")
	ctx = WithField(ctx, "c", "3")

	names := fieldsFromCtx(ctx)
	want := []string{"a", "b", "c"}
	if len(names) != len(want) {
		t.Fatalf("expected %v, got %v", want, names)
	}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, n, want[i])
		}
	}
}

func TestWithField_DuplicateKey_NoGrowth(t *testing.T) {
	ctx := context.Background()
	ctx = WithField(ctx, "key", "first")
	ctx = WithField(ctx, "key", "second") // 覆盖，列表不增长

	names := fieldsFromCtx(ctx)
	if len(names) != 1 {
		t.Fatalf("expected 1 name, got %v", names)
	}
	// 值应为最新注入的
	if v, _ := ctx.Value(fieldKey{name: "key"}).(string); v != "second" {
		t.Fatalf("expected 'second', got %q", v)
	}
}

// ── Logger.Context + WithField 输出验证 ──────────────────────────────────────

func TestWithField_AppearsInOutput_ViaLoggerContext(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	ctx := WithField(context.Background(), "request_id", "req-xyz")
	ctx = WithField(ctx, "user_id", "u-42")
	bound := l.Context(ctx)
	bound.Info().Msg("hi").Send()
	drainLogger(bound)

	out := buf.String()
	if !strings.Contains(out, "request_id") {
		t.Errorf("expected request_id in output: %q", out)
	}
	if !strings.Contains(out, "req-xyz") {
		t.Errorf("expected req-xyz in output: %q", out)
	}
	if !strings.Contains(out, "user_id") {
		t.Errorf("expected user_id in output: %q", out)
	}
	if !strings.Contains(out, "u-42") {
		t.Errorf("expected u-42 in output: %q", out)
	}
}

func TestWithField_AppearsInOutput_ViaEventCtx(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	ctx := WithField(context.Background(), "trace", "t-1")
	l.Info().Ctx(ctx).Msg("event-ctx").Send()
	drainLogger(l)

	out := buf.String()
	if !strings.Contains(out, "trace") {
		t.Errorf("expected trace field in output: %q", out)
	}
	if !strings.Contains(out, "t-1") {
		t.Errorf("expected t-1 in output: %q", out)
	}
}

func TestWithField_IntType_NoStringify(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	ctx := WithField(context.Background(), "count", 42)
	bound := l.Context(ctx)
	bound.Info().Msg("int-field").Send()
	drainLogger(bound)

	out := buf.String()
	// 整数字段在 JSON 中应为裸数字 42，而非 "42"（带引号为 string 序列化）
	if !strings.Contains(out, "count") {
		t.Errorf("expected count field in output: %q", out)
	}
}

func TestWithField_BoolType(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	ctx := WithField(context.Background(), "ok", true)
	bound := l.Context(ctx)
	bound.Info().Msg("bool-field").Send()
	drainLogger(bound)

	out := buf.String()
	if !strings.Contains(out, "ok") {
		t.Errorf("expected ok field in output: %q", out)
	}
}

func TestWithField_DuplicateKey_LatestValueInOutput(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	ctx := WithField(context.Background(), "key", "first")
	ctx = WithField(ctx, "key", "second")
	bound := l.Context(ctx)
	bound.Info().Msg("overwrite").Send()
	drainLogger(bound)

	out := buf.String()
	if strings.Contains(out, "first") {
		t.Errorf("stale value 'first' should not appear in output: %q", out)
	}
	if !strings.Contains(out, "second") {
		t.Errorf("expected 'second' in output: %q", out)
	}
}

// ── WithTrace + WithField 组合 ────────────────────────────────────────────────

func TestWithTrace_And_WithField_Combined(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)

	ctx := WithTrace(context.Background(), [16]byte{0x01})
	ctx = WithField(ctx, "svc", "auth")
	bound := l.Context(ctx)
	bound.Info().Msg("combined").Send()
	drainLogger(bound)

	out := buf.String()
	if !strings.Contains(out, "trace_id") {
		t.Errorf("expected trace_id in output: %q", out)
	}
	if !strings.Contains(out, "svc") {
		t.Errorf("expected svc field in output: %q", out)
	}
	if !strings.Contains(out, "auth") {
		t.Errorf("expected auth in output: %q", out)
	}
}

// TestWithField_CtxFieldsCap 验证向同一 context 注入超过 maxCtxFields 个不同
// 字段时，fieldNamesKey slice 长度被限制在 maxCtxFields 以内，不发生无界增长。
func TestWithField_CtxFieldsCap(t *testing.T) {
	ctx := context.Background()
	const n = 200
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("field_%d", i)
		ctx = WithField(ctx, key, i)
	}
	names, _ := ctx.Value(fieldNamesKey{}).([]string)
	if len(names) > maxCtxFields {
		t.Errorf("fieldNamesKey 长度 %d 超过 maxCtxFields=%d，存在 OOM 风险",
			len(names), maxCtxFields)
	}
}
