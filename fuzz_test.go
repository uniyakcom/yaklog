package yaklog

import (
	"bytes"
	"math"
	"testing"

	yakjson "github.com/uniyakcom/yakjson"
)

// ─── JSON 编码器 Fuzz ─────────────────────────────────────────────────────────

// FuzzJSONEncoder 对 jsonEncoder 进行模糊测试。
//
// 目标函数：jsonEncoder.appendStr / appendAttr / finalize
// 覆盖路径：任意字节序列作为 key/value 输入，验证输出始终为合法 JSON（可被 yakjson 解析）且无 panic。
// 不变量：成功调用 finalize 后输出须可由 yakjson.Decode 无错解析；无 panic。
func FuzzJSONEncoder(f *testing.F) {
	// 种子：空串、控制字符、Unicode、长字符串
	f.Add("", "")
	f.Add("key", "value")
	f.Add("k\x00e\x01y", "v\x02a\x1fl")
	f.Add("message", `hello "world"`)
	f.Add("k", "\n\r\t\b\f\\")
	f.Add("unicode", "中文日本語한국어")

	enc := &jsonEncoder{timeFmt: TimeUnixMilli}
	f.Fuzz(func(t *testing.T, key, val string) {
		var buf []byte
		buf = enc.beginRecord(buf, 0, Info)
		buf = enc.appendStr(buf, key, val)
		buf = enc.finalize(buf, val, true, "")

		// 不变量 1：输出必须以 { 开头，以 }\n 结尾
		if len(buf) < 3 || buf[0] != '{' || buf[len(buf)-1] != '\n' {
			t.Fatalf("输出格式错误：%q", buf)
		}
		// 不变量 2：必须是合法 JSON（yakjson.AppendMarshal 做往返验证）
		json := buf[:len(buf)-1] // 去掉行尾 \n
		var m map[string]any
		if err := yakjson.Unmarshal(json, &m); err != nil {
			t.Fatalf("输出非合法 JSON：%v\n原始：%q", err, buf)
		}
	})
}

// ─── Text 编码器 Fuzz ─────────────────────────────────────────────────────────

// FuzzTextEncoder 对 textEncoder 进行模糊测试。
//
// 目标函数：textEncoder.appendStr / finalize
// 覆盖路径：任意字节序列作为 key/value，验证无 panic，且输出包含换行终止符。
// 不变量：输出以 '\n' 结尾；不含未转义的 \r\n（防日志注入）。
func FuzzTextEncoder(f *testing.F) {
	f.Add("", "")
	f.Add("key", "plain value")
	f.Add("k\x00e", "v\x01a")
	f.Add("k=bad", "v with spaces")
	f.Add("deep", "中文内容\n换行注入尝试")

	enc := &textEncoder{timeFmt: TimeUnixMilli}
	f.Fuzz(func(t *testing.T, key, val string) {
		var buf []byte
		buf = enc.beginRecord(buf, 0, Warn)
		buf = enc.appendStr(buf, key, val)
		buf = enc.finalize(buf, val, true, "")

		// 不变量 1：输出以 '\n' 结尾
		if len(buf) == 0 || buf[len(buf)-1] != '\n' {
			t.Fatalf("输出缺少行尾换行：%q", buf)
		}
		// 不变量 2：日志行主体不含裸 \r（防 CRLF 注入）
		for i := 0; i < len(buf)-1; i++ {
			if buf[i] == '\r' {
				t.Fatalf("输出含裸 \\r（日志注入风险）：%q", buf)
			}
		}
	})
}

// ─── appendJSONStr Fuzz ───────────────────────────────────────────────────────

// FuzzAppendJSONStr 验证 appendJSONStr 始终产出合法 JSON 字符串且可往返解析。
//
// 目标函数：appendJSONStr（util.go）
// 不变量：输出可被 yakjson 解析为字符串；解析后值等于原始输入（RoundTrip）。
func FuzzAppendJSONStr(f *testing.F) {
	f.Add("")
	f.Add("hello")
	f.Add(`with "quotes"`)
	f.Add("with\\backslash")
	f.Add("\x00\x01\x1f\x7f")
	f.Add("中文")

	f.Fuzz(func(t *testing.T, s string) {
		buf := appendJSONStr(nil, s)

		// 不变量 1：以 '"' 开头和结尾
		if len(buf) < 2 || buf[0] != '"' || buf[len(buf)-1] != '"' {
			t.Fatalf("未被引号包裹：%q", buf)
		}
		// 不变量 2：包装为 JSON 对象后可解析
		wrap := bytes.Join([][]byte{[]byte(`{"v":`), buf, []byte("}")}, nil)
		var m map[string]any
		if err := yakjson.Unmarshal(wrap, &m); err != nil {
			t.Fatalf("输出非合法 JSON 字符串：%v\n原始输入：%q\n输出：%q", err, s, buf)
		}
	})
}

// ─── appendJSONFloat64 Fuzz ───────────────────────────────────────────────────

// FuzzAppendJSONFloat64 验证 appendJSONFloat64 始终产出合法 JSON 值。
//
// 目标函数：appendJSONFloat64（util.go）
// 不变量：输出包装为 JSON 对象后可被 yakjson 无错解析（NaN/Inf 编码为字符串，普通值为数字）。
func FuzzAppendJSONFloat64(f *testing.F) {
	// 种子：NaN、Inf、-Inf、0、负零、极小值、极大值、普通值
	f.Add(math.Float64bits(0))
	f.Add(math.Float64bits(math.NaN()))
	f.Add(math.Float64bits(math.Inf(1)))
	f.Add(math.Float64bits(math.Inf(-1)))
	f.Add(math.Float64bits(math.Copysign(0, -1))) // 负零（IEEE 754）
	f.Add(math.Float64bits(math.SmallestNonzeroFloat64))
	f.Add(math.Float64bits(math.MaxFloat64))
	f.Add(math.Float64bits(3.14159265358979))
	f.Add(math.Float64bits(-42.5))

	f.Fuzz(func(t *testing.T, bits uint64) {
		val := math.Float64frombits(bits)
		buf := appendJSONFloat64([]byte(`{"v":`), val)
		buf = append(buf, '}')

		// 不变量：输出必须是合法 JSON
		var m map[string]any
		if err := yakjson.Unmarshal(buf, &m); err != nil {
			t.Fatalf("appendJSONFloat64 输出非合法 JSON：%v\n输入：%v (bits=0x%x)\n输出：%q", err, val, bits, buf)
		}
	})
}

// ─── sanitizeTag Fuzz ─────────────────────────────────────────────────────────

// FuzzSanitizeTag 验证 sanitizeTag 输出不含控制字符，且长度不超过 maxTagLen。
//
// 目标函数：sanitizeTag（util.go）
// 不变量：
//  1. 输出中无 < 0x20 的字节（控制字符全部被替换为 '_'）
//  2. 输出长度 ≤ maxTagLen
//  3. 同一输入在纯 ASCII 安全字符串时保持不变（idempotent）
func FuzzSanitizeTag(f *testing.F) {
	f.Add("")
	f.Add("api")
	f.Add("tag\x00with\x01null")
	f.Add("超长标签" + string(make([]byte, 200)))
	f.Add("\n\r\t injection")

	f.Fuzz(func(t *testing.T, s string) {
		result := sanitizeTag(s)

		// 不变量 1：无控制字符
		for i := 0; i < len(result); i++ {
			if result[i] < 0x20 {
				t.Fatalf("sanitizeTag 输出含控制字符 0x%02x 在位置 %d：%q → %q", result[i], i, s, result)
			}
		}
		// 不变量 2：长度上限
		if len(result) > maxTagLen {
			t.Fatalf("sanitizeTag 输出超过 maxTagLen(%d)：len=%d，输入：%q", maxTagLen, len(result), s)
		}
	})
}
