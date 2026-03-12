package yaklog

import (
	"encoding/json"
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ─── jsonEncoder ─────────────────────────────────────────────────────────────

// TestJSONEncoder_Basic 验证 JSON 格式的基础字段输出。
func TestJSONEncoder_Basic(t *testing.T) {
	e := &jsonEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 1_741_000_000_000_000_000, Info)
	buf = e.appendStr(buf, "k", "val")
	buf = e.appendInt64(buf, "n", -42)
	buf = e.appendUint64(buf, "u", 99)
	buf = e.appendBool(buf, "ok", true)
	buf = e.finalize(buf, "msg", true, "")

	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("JSON 解析失败：%v\n原始：%s", err, buf)
	}
	checks := map[string]any{
		"level": "INFO",
		"k":     "val",
		"n":     float64(-42),
		"u":     float64(99),
		"ok":    true,
		"msg":   "msg",
	}
	for k, want := range checks {
		if m[k] != want {
			t.Errorf("字段 %s 期望 %v，得 %v", k, want, m[k])
		}
	}
}

// TestJSONEncoder_Escape 验证 JSON 字符串中控制字符和引号被正确转义。
func TestJSONEncoder_Escape(t *testing.T) {
	e := &jsonEncoder{timeFmt: TimeUnixMilli}
	cases := []struct {
		input string
		want  string
	}{
		{`hello "world"`, `"hello \"world\""`},
		{"line1\nline2", `"line1\nline2"`},
		{"tab\there", `"tab\there"`},
		{`back\slash`, `"back\\slash"`},
	}
	for _, c := range cases {
		var buf []byte
		buf = e.beginRecord(buf, 0, Info)
		buf = e.appendStr(buf, "v", c.input)
		buf = e.finalize(buf, "", false, "")
		if !strings.Contains(string(buf), c.want) {
			t.Errorf("input=%q 输出不含 %q\n原始：%s", c.input, c.want, buf)
		}
	}
}

// TestJSONEncoder_NaNInf 验证 NaN/Inf 以字符串形式安全输出。
func TestJSONEncoder_NaNInf(t *testing.T) {
	e := &jsonEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendFloat64(buf, "nan", math.NaN())
	buf = e.finalize(buf, "", false, "")

	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("非合法 JSON：%v\n原始：%s", err, buf)
	}
}

// TestJSONEncoder_NoMessageField 验证 hasMsg=false 时不输出 msg 字段。
func TestJSONEncoder_NoMessageField(t *testing.T) {
	e := &jsonEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.finalize(buf, "", false, "")

	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("JSON 解析失败：%v", err)
	}
	if _, ok := m["msg"]; ok {
		t.Error("hasMsg=false 时不应有 msg 字段")
	}
}

// TestJSONEncoder_TimeFormats 验证三种时间格式输出。
func TestJSONEncoder_TimeFormats(t *testing.T) {
	nsec := int64(1_741_174_800_000_000_000)

	cases := []struct {
		fmt  TimeFormat
		want string
	}{
		{TimeRFC3339Milli, "2025-03-"},
		{TimeUnixMilli, "1741174800000"},
		{TimeUnixNano, "1741174800000000000"},
	}
	for _, c := range cases {
		e := &jsonEncoder{timeFmt: c.fmt}
		var buf []byte
		buf = e.beginRecord(buf, nsec, Info)
		buf = e.finalize(buf, "", false, "")
		if !strings.Contains(string(buf), c.want) {
			t.Errorf("时间格式 %d 期望含 %q，原始：%s", c.fmt, c.want, buf)
		}
	}
}

// TestJSONEncoder_AppendTime 验证 appendTime 输出 RFC3339Milli 格式。
func TestJSONEncoder_AppendTime(t *testing.T) {
	e := &jsonEncoder{timeFmt: TimeRFC3339Milli}
	ts := time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendTime(buf, "ts", ts)
	buf = e.finalize(buf, "", false, "")

	out := string(buf)
	if !strings.Contains(out, "2026-03-05") {
		t.Errorf("时间字段缺少日期，原始：%q", out)
	}
}

// TestJSONEncoder_AppendDur 验证 appendDur 输出 duration 字符串。
func TestJSONEncoder_AppendDur(t *testing.T) {
	e := &jsonEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendDur(buf, "elapsed", 3*time.Minute+7*time.Second)
	buf = e.finalize(buf, "", false, "")

	out := string(buf)
	if !strings.Contains(out, "3m7s") {
		t.Errorf("duration 字段期望含 3m7s，原始：%q", out)
	}
}

// TestJSONEncoder_AppendErr 验证 appendErr 输出 error 字符串。
func TestJSONEncoder_AppendErr(t *testing.T) {
	e := &jsonEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Error)
	buf = e.appendErr(buf, errors.New("connection refused"), "")
	buf = e.finalize(buf, "oops", true, "")

	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("JSON 解析失败：%v\n原始：%s", err, buf)
	}
	if m["error"] != "connection refused" {
		t.Errorf("error 字段期望 connection refused，得 %v", m["error"])
	}
}

// ─── textEncoder ─────────────────────────────────────────────────────────────

// TestTextEncoder_Basic 验证 Text 格式基础输出。
func TestTextEncoder_Basic(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli, fullLevel: true}
	var buf []byte
	buf = e.beginRecord(buf, 0, Warn)
	buf = e.appendStr(buf, "k", "v")
	buf = e.appendInt64(buf, "n", 7)
	buf = e.finalize(buf, "wrn msg", true, "")

	out := string(buf)
	if !strings.Contains(out, "WARN") {
		t.Errorf("Text 格式应含 WARN，得：%q", out)
	}
	if !strings.Contains(out, "k=v") {
		t.Errorf("缺少 k=v，原始：%q", out)
	}
	if !strings.Contains(out, "n=7") {
		t.Errorf("缺少 n=7，原始：%q", out)
	}
	if !strings.Contains(out, "wrn msg") {
		t.Errorf("缺少消息文本，原始：%q", out)
	}
}

// TestTextEncoder_KeySanitize 验证含空格/等号的 key 被替换为下划线。
func TestTextEncoder_KeySanitize(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendStr(buf, "bad key", "val")
	buf = e.appendStr(buf, "eq=key", "val")
	buf = e.finalize(buf, "", false, "")

	out := string(buf)
	if strings.Contains(out, "bad key=") {
		t.Errorf("含空格的 key 未被净化，原始：%q", out)
	}
	if strings.Contains(out, "eq=key=") {
		t.Errorf("含等号的 key 未被净化，原始：%q", out)
	}
}

// TestTextEncoder_QuotedValue 验证含空格的 value 加引号。
func TestTextEncoder_QuotedValue(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendStr(buf, "msg", "hello world")
	buf = e.finalize(buf, "", false, "")

	out := string(buf)
	if !strings.Contains(out, `msg="hello world"`) {
		t.Errorf("含空格的值应加引号，原始：%q", out)
	}
}

// TestTextEncoder_Levels 验证所有级别名称均正确。
func TestTextEncoder_Levels(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli, fullLevel: true}
	cases := []struct {
		level Level
		want  string
	}{
		{Trace, "TRACE"},
		{Debug, "DEBUG"},
		{Info, "INFO"},
		{Warn, "WARN"},
		{Error, "ERROR"},
		{Fatal, "FATAL"},
	}
	for _, c := range cases {
		var buf []byte
		buf = e.beginRecord(buf, 0, c.level)
		buf = e.finalize(buf, "", false, "")
		if !strings.Contains(string(buf), c.want) {
			t.Errorf("级别 %v 期望含 %s，原始：%q", c.level, c.want, buf)
		}
	}
}

// TestTextEncoder_Uint64Float64Bool 覆盖 textEncoder.appendUint64/appendFloat64/appendBool。
func TestTextEncoder_Uint64Float64Bool(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendUint64(buf, "u", 18446744073709551615)
	buf = e.appendFloat64(buf, "f", 2.718)
	buf = e.appendBool(buf, "b1", true)
	buf = e.appendBool(buf, "b2", false)
	buf = e.finalize(buf, "num-types", true, "")

	out := string(buf)
	for _, want := range []string{"18446744073709551615", "2.718", "b1=true", "b2=false"} {
		if !strings.Contains(out, want) {
			t.Errorf("textEncoder数值: 期望含 %q，原始：%q", want, out)
		}
	}
}

// ─── appendJSONStr ────────────────────────────────────────────────────────────

// TestAppendJSONStr_Unicode 验证控制字符 \u00XX 转义格式正确。
func TestAppendJSONStr_Unicode(t *testing.T) {
	input := "\x01\x1f"
	out := appendJSONStr(nil, input)
	s := string(out)
	if !strings.Contains(s, `\u0001`) || !strings.Contains(s, `\u001f`) {
		t.Errorf("控制字符未转义，得：%q", s)
	}
}

// TestAppendTextVal_Empty 验证空字符串不加引号（直接为空）。
func TestAppendTextVal_Empty(t *testing.T) {
	out := appendTextVal(nil, "")
	if len(out) != 0 {
		t.Errorf("空字符串应输出空，得：%q", out)
	}
}

// TestAppendTextVal_Plain 验证普通值不加引号。
func TestAppendTextVal_Plain(t *testing.T) {
	out := string(appendTextVal(nil, "plain"))
	if out != "plain" {
		t.Errorf("普通值期望 plain，得 %q", out)
	}
}

// ─── textEncoder 零覆盖方法 ───────────────────────────────────────────────────

// TestTextEncoder_AppendTime 验证 textEncoder.appendTime 输出 RFC3339Nano 时间。
func TestTextEncoder_AppendTime(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	ts := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendTime(buf, "at", ts)
	buf = e.finalize(buf, "", false, "")
	out := string(buf)
	if !strings.Contains(out, "2026-05-01") {
		t.Errorf("textEncoder.appendTime 期望含日期, got: %q", out)
	}
	if !strings.Contains(out, "at=") {
		t.Errorf("textEncoder.appendTime 期望含 key \"at=\", got: %q", out)
	}
}

// TestTextEncoder_AppendDur 验证 textEncoder.appendDur 输出可读 duration 字符串。
func TestTextEncoder_AppendDur(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendDur(buf, "latency", 2*time.Second+500*time.Millisecond)
	buf = e.finalize(buf, "", false, "")
	out := string(buf)
	if !strings.Contains(out, "2.5s") {
		t.Errorf("textEncoder.appendDur 期望 2.5s, got: %q", out)
	}
}

// TestTextEncoder_AppendErr_NonNil 验证 textEncoder.appendErr 输出 error 字段。
func TestTextEncoder_AppendErr_NonNil(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Error)
	buf = e.appendErr(buf, errors.New("disk full"), "")
	buf = e.finalize(buf, "crash", true, "")
	out := string(buf)
	// appendStr 对含空格的值加引号，故检查 key 存在即可
	if !strings.Contains(out, "error=") {
		t.Errorf("textEncoder.appendErr 期望含 error=..., got: %q", out)
	}
	if !strings.Contains(out, "disk full") {
		t.Errorf("textEncoder.appendErr 期望含 disk full, got: %q", out)
	}
}

// TestTextEncoder_AppendErr_Nil 验证 textEncoder.appendErr 对 nil error 不输出任何字段。
func TestTextEncoder_AppendErr_Nil(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	before := len(buf)
	buf = e.appendErr(buf, nil, "")
	if len(buf) != before {
		t.Errorf("textEncoder.appendErr(nil) 不应追加任何内容")
	}
}

// TestTextEncoder_AppendAny_Map 验证 textEncoder.appendAny 对 map 序列化为 JSON。
func TestTextEncoder_AppendAny_Map(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendAny(buf, "meta", map[string]int{"n": 42})
	buf = e.finalize(buf, "", false, "")
	out := string(buf)
	if !strings.Contains(out, "meta=") {
		t.Errorf("textEncoder.appendAny 期望含 meta=, got: %q", out)
	}
	if !strings.Contains(out, "42") {
		t.Errorf("textEncoder.appendAny 期望含 42, got: %q", out)
	}
}

// TestTextEncoder_AppendAny_MarshalError 验证无法序列化时输出 !MARSHAL_ERROR 占位符。
func TestTextEncoder_AppendAny_MarshalError(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	// errMarshaler 实现 json.Marshaler 但总是返回错误，触发 appendAny 的错误路径。
	var val errMarshaler
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendAny(buf, "bad", val)
	buf = e.finalize(buf, "", false, "")
	out := string(buf)
	if !strings.Contains(out, "MARSHAL_ERROR") {
		t.Errorf("textEncoder.appendAny marshal error 期望含 MARSHAL_ERROR, got: %q", out)
	}
}

// errMarshaler 是一个总是在序列化时返回错误的辅助类型。
type errMarshaler struct{}

func (errMarshaler) MarshalJSON() ([]byte, error) {
	return nil, errors.New("intentional marshal failure")
}

// TestTextEncoder_AppendSource_Empty 验证 appendSource 对空 pcs 不追加字段。
func TestTextEncoder_AppendSource_Empty(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	before := len(buf)
	buf = e.appendSource(buf, nil)
	if len(buf) != before {
		t.Errorf("appendSource(nil) 不应追加任何内容")
	}
}

// TestTextEncoder_AppendSource_Valid 验证 appendSource 对有效 PC 追加 source 字段。
func TestTextEncoder_AppendSource_Valid(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var pcs [1]uintptr
	runtime.Callers(1, pcs[:])
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendSource(buf, pcs[:])
	buf = e.finalize(buf, "", false, "")
	out := string(buf)
	if !strings.Contains(out, "source=") {
		t.Errorf("appendSource 期望含 source=, got: %q", out)
	}
	if !strings.Contains(out, ".go:") {
		t.Errorf("appendSource 期望含 .go:, got: %q", out)
	}
}

// TestJSONEncoder_AppendRawJSON_Object 验证 jsonEncoder.appendRawJSON 将对象直接嵌入记录。
func TestJSONEncoder_AppendRawJSON_Object(t *testing.T) {
	e := &jsonEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendRawJSON(buf, "resp", []byte(`{"code":200,"desc":"ok"}`))
	buf = e.finalize(buf, "", false, "")
	out := strings.TrimSpace(string(buf))
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("appendRawJSON 输出应合法 JSON: %v\nbuf=%s", err, out)
	}
	resp, ok := m["resp"]
	if !ok {
		t.Fatalf("appendRawJSON 期望字段 resp 存在, got: %s", out)
	}
	obj, ok := resp.(map[string]any)
	if !ok {
		t.Fatalf("resp 期望为嵌套对象, got: %T", resp)
	}
	if obj["code"] != float64(200) {
		t.Errorf("resp.code 期望 200, got: %v", obj["code"])
	}
}

// TestJSONEncoder_AppendRawJSON_Nil 验证 raw 为 nil 时跳过字段。
func TestJSONEncoder_AppendRawJSON_Nil(t *testing.T) {
	e := &jsonEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	before := len(buf)
	buf = e.appendRawJSON(buf, "resp", nil)
	if len(buf) != before {
		t.Error("appendRawJSON(nil) 不应追加任何内容")
	}
}

// TestTextEncoder_AppendRawJSON_Object 验证 textEncoder.appendRawJSON 按 key=value 输出。
func TestTextEncoder_AppendRawJSON_Object(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendRawJSON(buf, "resp", []byte(`{"code":200}`))
	buf = e.finalize(buf, "", false, "")
	out := string(buf)
	if !strings.Contains(out, "resp=") {
		t.Errorf("textEncoder.appendRawJSON 期望含 resp=, got: %q", out)
	}
	if !strings.Contains(out, `{"code":200}`) {
		t.Errorf("textEncoder.appendRawJSON 期望 raw 内容原样输出, got: %q", out)
	}
}

// TestTextEncoder_AppendRawJSON_Array 验证 textEncoder.appendRawJSON 对数组 JSON 原样输出。
func TestTextEncoder_AppendRawJSON_Array(t *testing.T) {
	e := &textEncoder{timeFmt: TimeUnixMilli}
	var buf []byte
	buf = e.beginRecord(buf, 0, Info)
	buf = e.appendRawJSON(buf, "ids", []byte(`[1,2,3]`))
	buf = e.finalize(buf, "", false, "")
	out := string(buf)
	if !strings.Contains(out, "ids=") {
		t.Errorf("textEncoder.appendRawJSON array 期望含 ids=, got: %q", out)
	}
	if !strings.Contains(out, "[1,2,3]") {
		t.Errorf("textEncoder.appendRawJSON array 期望原样输出, got: %q", out)
	}
}

// ── P2: jsonEncoder.appendSource 转义验证 ──────────────────────────────────────

// TestJSONEncoder_AppendSource_CallerFuncEscaped 验证 callerFunc 返回带反斜杠路径时
// JSON source 字段由 appendJSONStr 正确转义。
func TestJSONEncoder_AppendSource_CallerFuncEscaped(t *testing.T) {
	var pcs [1]uintptr
	runtime.Callers(1, pcs[:])

	enc := &jsonEncoder{
		timeFmt:    TimeUnixMilli,
		callerFunc: func(_ string, _ int) string { return `C:\Users\project\main.go:42` },
	}
	dst := enc.beginRecord(nil, 0, Info)
	dst = enc.appendSource(dst, pcs[:])
	dst = enc.finalize(dst, "msg", true, "")

	var m map[string]any
	if err := json.Unmarshal(dst[:len(dst)-1], &m); err != nil {
		t.Fatalf("callerFunc 含反斜杠路径导致 JSON 非法：%v\n原始：%q", err, dst)
	}
	got, _ := m["source"].(string)
	if got != `C:\Users\project\main.go:42` {
		t.Errorf("source 字段往返失败：got %q", got)
	}
}

// TestJSONEncoder_AppendSource_DefaultPathValid 验证默认路径（无 callerFunc）
// 经 appendSourceJSONField 转义后输出合法 JSON。
func TestJSONEncoder_AppendSource_DefaultPathValid(t *testing.T) {
	var pcs [1]uintptr
	runtime.Callers(1, pcs[:])

	enc := &jsonEncoder{timeFmt: TimeUnixMilli}
	dst := enc.beginRecord(nil, 0, Info)
	dst = enc.appendSource(dst, pcs[:])
	dst = enc.finalize(dst, "ok", true, "")

	var m map[string]any
	if err := json.Unmarshal(dst[:len(dst)-1], &m); err != nil {
		t.Fatalf("默认路径 appendSource 导致 JSON 非法：%v\n原始：%q", err, dst)
	}
	if _, ok := m["source"]; !ok {
		t.Errorf("有效 pcs 应包含 source 字段，实际输出：%q", dst)
	}
}

// ── P3: textEncoder.appendSource callerFunc 引号验证 ──────────────────────────

// TestTextEncoder_AppendSource_CallerFunc_SpaceQuoted 验证 callerFunc 返回含空格的字符串时
// textEncoder.appendSource 使用 appendTextVal 加引号，防止解析器截断字段值。
func TestTextEncoder_AppendSource_CallerFunc_SpaceQuoted(t *testing.T) {
	var pcs [1]uintptr
	runtime.Callers(1, pcs[:])

	enc := &textEncoder{
		timeFmt:    TimeUnixMilli,
		callerFunc: func(_ string, _ int) string { return "my module main.go:10" },
	}
	dst := enc.beginRecord(nil, 0, Info)
	dst = enc.appendSource(dst, pcs[:])
	out := string(dst)

	// 含空格的 source 值应被引号包裹，而非被空格拖断
	if !strings.Contains(out, `source="my module main.go:10"`) {
		t.Errorf("含空格 source 应被引号包裹，实际输出: %q", out)
	}
}
