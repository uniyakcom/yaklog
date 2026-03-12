package yaklog

import (
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/uniyakcom/yakutil"
	"github.com/uniyakcom/yakutil/swar"
)

// ─── 时间格式化（无分配追加模式）────────────────────────────────────────────

// appendTimestamp 将纳秒时间戳按指定格式追加到 dst。
func appendTimestamp(dst []byte, nsec int64, fmt TimeFormat) []byte {
	switch fmt {
	case TimeUnixSec:
		return strconv.AppendInt(dst, nsec/1e9, 10)
	case TimeUnixMilli:
		return strconv.AppendInt(dst, nsec/1e6, 10)
	case TimeUnixNano:
		return strconv.AppendInt(dst, nsec, 10)
	case TimeOff:
		return dst
	default: // TimeRFC3339Milli
		return time.Unix(0, nsec).UTC().AppendFormat(dst, "2006-01-02T15:04:05.000Z")
	}
}

// ─── 级别字符串 ───────────────────────────────────────────────────────────────

// levelString 返回级别的完整英文名称（用于 JSON 格式 "level" 字段）。
func levelString(l Level) string {
	switch l {
	case Trace:
		return "TRACE"
	case Debug:
		return "DEBUG"
	case Warn:
		return "WARN"
	case Error:
		return "ERROR"
	case Panic:
		return "PANIC"
	case Fatal:
		return "FATAL"
	default:
		return "INFO"
	}
}

// levelShort 返回级别的单字母简写（Console 简写模式默认使用）。
// 对应关系：Trace→T  Debug→D  Info→I  Warn→W  Error→E  Panic→P  Fatal→F
func levelShort(l Level) string {
	switch l {
	case Trace:
		return "T"
	case Debug:
		return "D"
	case Warn:
		return "W"
	case Error:
		return "E"
	case Panic:
		return "P"
	case Fatal:
		return "F"
	default: // Info
		return "I"
	}
}

// levelPad 返回级别名称（Console 完整模式用，固定 5 字符宽度，右补空格）。
func levelPad(l Level) string {
	switch l {
	case Trace:
		return "TRACE"
	case Debug:
		return "DEBUG"
	case Warn:
		return "WARN "
	case Error:
		return "ERROR"
	case Panic:
		return "PANIC"
	case Fatal:
		return "FATAL"
	default: // Info
		return "INFO "
	}
}

// ─── JSON 字符串编码（基础辅助，无依赖）─────────────────────────────────────

// hexChars 十六进制字符表，用于 JSON 控制字符转义。
var hexChars = [16]byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f'}

// appendJSONStr 将字符串 s 以 JSON 字符串格式（含引号）追加到 dst。
// 转义控制字符、双引号和反斜杠；不转义 HTML 特殊字符（<>& 直接保留）。
//
// 短字符串（<16B）使用预扫描 + 整体追加（安全快速路径命中率 >99.9%）；
// 长字符串使用 swar.FindEscape 8字节并行扫描，批量拷贝无需转义的前缀段。
func appendJSONStr(dst []byte, s string) []byte {
	dst = append(dst, '"')
	if len(s) >= 16 {
		return appendJSONStrSWAR(dst, s)
	}
	// 短字符串安全快速路径：预扫描确认全部字节无需转义，跳过逐字节分支。
	safe := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == '"' || c == '\\' {
			safe = false
			break
		}
	}
	if safe {
		dst = append(dst, s...)
		return append(dst, '"')
	}
	// 慢路径：含需转义字符，逐字节处理。
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		dst = append(dst, s[start:i]...)
		dst = appendEscapeByte(dst, c)
		start = i + 1
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// appendJSONStrSWAR 长字符串 JSON 编码路径。使用 swar.FindEscape 一次处理 8 字节，
// 批量拷贝无需转义的连续区域，减少循环迭代次数。
func appendJSONStrSWAR(dst []byte, s string) []byte {
	b := yakutil.S2B(s) // 零拷贝 string → []byte（只读安全）
	for len(b) > 0 {
		idx := swar.FindEscape(b)
		if idx < 0 {
			// 剩余字节均无需转义，一次性拷贝
			dst = append(dst, b...)
			break
		}
		// 批量拷贝无需转义的前缀
		dst = append(dst, b[:idx]...)
		dst = appendEscapeByte(dst, b[idx])
		b = b[idx+1:]
	}
	return append(dst, '"')
}

// appendEscapeByte 将单个需转义的字节追加为 JSON 转义序列。
func appendEscapeByte(dst []byte, c byte) []byte {
	switch c {
	case '"':
		return append(dst, '\\', '"')
	case '\\':
		return append(dst, '\\', '\\')
	case '\n':
		return append(dst, '\\', 'n')
	case '\r':
		return append(dst, '\\', 'r')
	case '\t':
		return append(dst, '\\', 't')
	case '\b':
		return append(dst, '\\', 'b')
	case '\f':
		return append(dst, '\\', 'f')
	default:
		return append(dst, '\\', 'u', '0', '0', hexChars[c>>4], hexChars[c&0xF])
	}
}

// appendSourceJSONField 将 file:line 以 JSON "source" 字段格式追加到 buf。
// file 经逐字节转义处理（消除路径中 '\' 或 '"' 导致的 JSON 损坏，如 Windows 路径），零分配。
func appendSourceJSONField(buf []byte, file string, line int) []byte {
	buf = append(buf, ',', '"', 's', 'o', 'u', 'r', 'c', 'e', '"', ':')
	buf = append(buf, '"')
	start := 0
	for i := 0; i < len(file); i++ {
		c := file[i]
		if c == '"' || c == '\\' || c < 0x20 {
			buf = append(buf, file[start:i]...)
			buf = appendEscapeByte(buf, c)
			start = i + 1
		}
	}
	buf = append(buf, file[start:]...)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, int64(line), 10)
	return append(buf, '"')
}

// appendStripCR appends s to dst while stripping bare \r (0x0D) bytes.
// This prevents CRLF injection in text-format log output via message content.
func appendStripCR(dst []byte, s string) []byte {
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\r' {
			dst = append(dst, s[start:i]...)
			start = i + 1
		}
	}
	return append(dst, s[start:]...)
}

// appendTextVal 将字符串 s 作为文本格式属性值追加到 dst。
// 若 s 含空格、等号或控制字符，则加引号并转义。
func appendTextVal(dst []byte, s string) []byte {
	needQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == '=' || c == '"' || c == '\\' {
			needQuote = true
			break
		}
	}
	if !needQuote {
		return append(dst, s...)
	}
	return appendJSONStr(dst, s) // 复用 JSON 转义逻辑，加引号
}

// appendJSONFloat64 将 float64 追加到 JSON 缓冲区。
// NaN/Inf 编码为 JSON 字符串（"NaN"/"Inf"/"-Inf"），保证输出始终为合法 JSON。
// 与 jsonEncoder.appendFloat64 / Event.Float64 行为一致。
func appendJSONFloat64(dst []byte, val float64) []byte {
	switch {
	case math.IsNaN(val):
		return append(dst, '"', 'N', 'a', 'N', '"')
	case math.IsInf(val, 1):
		return append(dst, '"', 'I', 'n', 'f', '"')
	case math.IsInf(val, -1):
		return append(dst, '"', '-', 'I', 'n', 'f', '"')
	}
	return strconv.AppendFloat(dst, val, 'f', -1, 64)
}

// sanitizeTag 返回安全的 tag 字符串：
//   - 将所有控制字符（< 0x20）替换为 '_'，防止日志输出被 '\n' / '\r' 注入断行。
//   - 超过 maxTagLen 时截断，防止 tag 过长占用每条日志行头部。
//
// 在 Tag() 构造时预处理一次，热路径无额外开销。
func sanitizeTag(s string) string {
	if len(s) > maxTagLen {
		s = s[:maxTagLen]
	}
	// 快速路径：逐字节确认全部字节安全，直接返回。
	safe := true
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	// 慢路径：将控制字符替换为 '_'。
	b := []byte(s)
	for i, c := range b {
		if c < 0x20 {
			b[i] = '_'
		}
	}
	return string(b)
}

// truncateMsg 若 msg 超过 maxMessageLen，截断并追加 [truncateSuffix]。
// 截断点选取 maxMessageLen-len(truncateSuffix)，保证追加后总长度严格 ≤ maxMessageLen。
// 截断位置向前对齐至合法 UTF-8 起始字节边界，避免切断多字节字符产生非法字节序列。
func truncateMsg(msg string) string {
	if len(msg) <= maxMessageLen {
		return msg
	}
	// 从 maxMessageLen-len(truncateSuffix) 开始向前回退至 UTF-8 起始字节（最多回退 3 字节）。
	// 这样 msg[:end] + truncateSuffix 的总长度 ≤ maxMessageLen。
	end := maxMessageLen - len(truncateSuffix)
	for end > 0 && !utf8.RuneStart(msg[end]) {
		end--
	}
	return msg[:end] + truncateSuffix
}
