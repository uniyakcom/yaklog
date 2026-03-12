package yaklog

import (
	"math"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"

	json "github.com/uniyakcom/yakjson"
)

// ─── encoder 接口 ─────────────────────────────────────────────────────────────

// encoder 日志行编码器内部接口。所有方法均为追加模式，不修改 dst[:len(dst)]。
type encoder interface {
	// beginRecord 写入记录头（时间戳 + 级别），返回追加后的 buf。
	beginRecord(dst []byte, tsNano int64, level Level) []byte
	// appendStr 追加字符串字段。
	appendStr(dst []byte, key, val string) []byte
	// appendInt64 追加 int64 字段。
	appendInt64(dst []byte, key string, val int64) []byte
	// appendUint64 追加 uint64 字段。
	appendUint64(dst []byte, key string, val uint64) []byte
	// appendFloat64 追加 float64 字段（NaN/Inf 以字符串形式输出）。
	appendFloat64(dst []byte, key string, val float64) []byte
	// appendBool 追加 bool 字段。
	appendBool(dst []byte, key string, val bool) []byte
	// appendTime 追加 time.Time 字段（RFC3339Nano 格式）。
	appendTime(dst []byte, key string, val time.Time) []byte
	// appendDur 追加 time.Duration 字段（如 "1.5s"）。
	appendDur(dst []byte, key string, val time.Duration) []byte
	// appendErr 追加 error 字段（key 固定为 "error"）。
	// valColor：Console 着色时用于 value 的 ANSI 色码（如 ansiError）；JSON / 无色模式传 ""。
	appendErr(dst []byte, err error, valColor string) []byte
	// appendAny 追加任意类型字段（通过 yakjson 序列化，有分配）。
	appendAny(dst []byte, key string, val any) []byte
	// appendRawJSON 追加已序列化的原始 JSON bytes 作为字段值（零分配，不经过 reflect）。
	// JSON 模式下 raw 直接嵌入为嵌套对象/数组；Console 模式下作为字段值原样输出。
	// 调用方负责保证 raw 是合法 JSON；传入 nil 时跳过该字段。
	appendRawJSON(dst []byte, key string, raw []byte) []byte
	// appendSource 追加调用方 file:line（addSource=true 时调用）。
	// pcs 应传入栈上分配的 [1]uintptr 的切片，避免堆分配。
	appendSource(dst []byte, pcs []uintptr) []byte
	// finalize 完成记录：若 hasMsg 则追加 msg 字段，加行尾，返回最终 buf。
	// msgColor 仅 Console 彩色模式使用，传入当前日志级别的 ANSI 色码（如 ansiError）；JSON / 无色模式传 ""。
	finalize(dst []byte, msg string, hasMsg bool, msgColor string) []byte
	// levelColorOf 返回当前颜色方案中对应级别的 ANSI 色码字符串。
	// JSON / 无色模式返回 "".
	levelColorOf(lvl Level) string
}

// newEncoder 根据 out 类型选择编码器。consoleSink → textEncoder，其余 → jsonEncoder。
func newEncoder(out interface{}, timeFmt TimeFormat, consoleFmt string, color bool, fullLevel bool, callerFunc func(string, int) string, scheme ColorScheme) encoder {
	if consoleFmt == "" {
		consoleFmt = ConsoleTimeMilli
	}
	if _, ok := out.(*consoleSink); ok {
		fracDigits := int8(-1)
		switch consoleFmt {
		case ConsoleTimeMilli:
			fracDigits = 3
		case ConsoleTimeMicro:
			fracDigits = 6
		}
		te := &textEncoder{timeFmt: timeFmt, consoleFmt: consoleFmt, color: color, fullLevel: fullLevel, fracDigits: fracDigits, callerFunc: callerFunc, scheme: scheme}
		te.buildLevelTables()
		return te
	}
	return &jsonEncoder{timeFmt: timeFmt, callerFunc: callerFunc}
}

// ─── jsonEncoder ──────────────────────────────────────────────────────────────

// jsonTimeCache 缓存同一秒内的 RFC3339 日期时间前缀。
// 同一秒内（>99.9% 命中率）直接复用 19 字节的 "2006-01-02T15:04:05" 部分，
// 仅手动追加 ".000Z"，避免 time.AppendFormat 的完整解析开销。
type jsonTimeCache struct {
	sec    int64    // 对应的 Unix 秒
	prefix [19]byte // "2006-01-02T15:04:05" 格式化结果
}

// jsonLevelField 预计算好的完整 JSON 级别字段（含前导逗号 + key + value）。
// beginRecord 中直接单次 append 替代 switch + appendJSONStr，消除多余分支与函数调用。
var jsonLevelField = [7]string{
	`,"level":"TRACE"`,
	`,"level":"DEBUG"`,
	`,"level":"INFO"`,
	`,"level":"WARN"`,
	`,"level":"ERROR"`,
	`,"level":"PANIC"`,
	`,"level":"FATAL"`,
}

// jsonUnixMilliCache 缓存 TimeUnixMilli 的秒级前缀。
// 同一秒内（>99.9% 命中率）直接复用秒数的字符串表示，仅手动追加 3 位毫秒数字，
// 避免 strconv.AppendInt 对完整 13 位十进制数的格式化开销。
type jsonUnixMilliCache struct {
	sec  int64    // 对应的 Unix 秒
	text [20]byte // strconv.FormatInt(sec, 10) 结果（当前纪元 ≤10 位）
	tlen uint8    // text 有效长度
}

// jsonEncoder JSON 格式编码器（零分配热路径）。
// 输出示例：{"time":"2026-03-08T10:30:00.123Z","level":"INFO","module":"http","msg":"hello"}\n
type jsonEncoder struct {
	timeFmt    TimeFormat
	callerFunc func(string, int) string           // 自定义 source 显示；nil = 默认行为
	cache      atomic.Pointer[jsonTimeCache]      // RFC3339Milli 秒级缓存，并发安全
	milliCache atomic.Pointer[jsonUnixMilliCache] // UnixMilli 秒级缓存，并发安全
}

func (e *jsonEncoder) beginRecord(dst []byte, tsNano int64, level Level) []byte {
	if e.timeFmt != TimeOff {
		dst = append(dst, `{"time":`...)
		switch e.timeFmt {
		case TimeRFC3339Milli:
			// 快速路径：缓存 "2006-01-02T15:04:05" 前缀（同一秒内 >99.9% 命中），
			// 手动追加 ".000Z" 毫秒尾部，避免 time.AppendFormat 开销。
			sec := tsNano / 1_000_000_000
			tc := e.cache.Load()
			if tc == nil || tc.sec != sec {
				var newTC jsonTimeCache
				newTC.sec = sec
				var tb [19]byte
				copy(newTC.prefix[:], time.Unix(sec, 0).UTC().AppendFormat(tb[:0], "2006-01-02T15:04:05"))
				tc = &newTC
				e.cache.Store(tc)
			}
			dst = append(dst, '"')
			dst = append(dst, tc.prefix[:]...)
			dst = append(dst, '.')
			ms := (tsNano - sec*1_000_000_000) / 1_000_000
			dst = appendFracDigits(dst, ms, 3)
			dst = append(dst, 'Z', '"')
		case TimeUnixMilli:
			// 快速路径：缓存秒部分字符串（同一秒内 >99.9% 命中），
			// 仅追加 3 位毫秒数字，避免 13 位十进制数的完整格式化开销。
			// 仅 sec > 0 时启用缓存（覆盖所有实际时间戳），
			// sec ≤ 0（纪元前、测试零值等）回退到标准格式化。
			sec := tsNano / 1_000_000_000
			if sec > 0 {
				mc := e.milliCache.Load()
				if mc == nil || mc.sec != sec {
					var newMC jsonUnixMilliCache
					newMC.sec = sec
					b := strconv.AppendInt(newMC.text[:0], sec, 10)
					newMC.tlen = uint8(len(b))
					mc = &newMC
					e.milliCache.Store(mc)
				}
				dst = append(dst, mc.text[:mc.tlen]...)
				ms := (tsNano - sec*1_000_000_000) / 1_000_000
				dst = appendFracDigits(dst, ms, 3)
			} else {
				dst = strconv.AppendInt(dst, tsNano/1_000_000, 10)
			}
		default:
			// Unix 其他格式不加引号
			dst = appendTimestamp(dst, tsNano, e.timeFmt)
		}
		// 级别字段（含前导逗号）：预计算完整字符串，单次 append 替代多步拼接
		idx := int(level) + 2
		if idx >= 0 && idx < len(jsonLevelField) {
			dst = append(dst, jsonLevelField[idx]...)
		} else {
			dst = append(dst, ',', '"', 'l', 'e', 'v', 'e', 'l', '"', ':')
			dst = appendJSONStr(dst, levelString(level))
		}
	} else {
		// TimeOff 路径：级别是首个字段（无前导逗号）
		dst = append(dst, '{')
		idx := int(level) + 2
		if idx >= 0 && idx < len(jsonLevelField) {
			// jsonLevelField 含前导逗号，TimeOff 路径跳过首字节逗号
			dst = append(dst, jsonLevelField[idx][1:]...)
		} else {
			dst = append(dst, '"', 'l', 'e', 'v', 'e', 'l', '"', ':')
			dst = appendJSONStr(dst, levelString(level))
		}
	}
	return dst
}

// appendJSONKey 追加 JSON 键格式：`,"key":`（含前导逗号）。
//
// 热路径优化：绝大多数 key 为安全 ASCII（无引号/反斜杠/控制字符），
// 直接追加避免 appendJSONStr 的函数调用和逐字节扫描开销。
func appendJSONKey(dst []byte, key string) []byte {
	dst = append(dst, ',', '"')
	for i := 0; i < len(key); i++ {
		if c := key[i]; c < 0x20 || c == '"' || c == '\\' {
			return appendJSONKeyEsc(dst, key)
		}
	}
	dst = append(dst, key...)
	return append(dst, '"', ':')
}

// appendJSONKeyFast 追加 JSON 键格式：`,"key":`（零扫描，直接 append）。
//
// 假定 key 为开发者定义的合法 JSON 标识符（ASCII 字母/数字/下划线），
// 不含 `"`、`\` 及控制字符。与 zerolog AppendKey 策略一致。
// 用于 Event 字段方法热路径；来自用户输入的 key（如 ctx 字段）应使用 appendJSONKey。
//
// 安全合约：key 必须是开发者控制的静态字符串常量或可信任标识符。
// 不得将用户输入（HTTP 头、请求参数等）直接作为 key；
// 如需将用户控制的字符串作为 key，请改用 appendJSONKey（含转义）。
func appendJSONKeyFast(dst []byte, key string) []byte {
	dst = append(dst, ',', '"')
	dst = append(dst, key...)
	return append(dst, '"', ':')
}

// appendJSONKeyEsc 处理包含需转义字符的罕见 key。
// dst 已包含 `,"` 前缀。
func appendJSONKeyEsc(dst []byte, key string) []byte {
	start := 0
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		dst = append(dst, key[start:i]...)
		dst = appendEscapeByte(dst, c)
		start = i + 1
	}
	dst = append(dst, key[start:]...)
	return append(dst, '"', ':')
}

func (e *jsonEncoder) appendStr(dst []byte, key, val string) []byte {
	dst = appendJSONKey(dst, key)
	return appendJSONStr(dst, val)
}

func (e *jsonEncoder) appendInt64(dst []byte, key string, val int64) []byte {
	dst = appendJSONKey(dst, key)
	return strconv.AppendInt(dst, val, 10)
}

func (e *jsonEncoder) appendUint64(dst []byte, key string, val uint64) []byte {
	dst = appendJSONKey(dst, key)
	return strconv.AppendUint(dst, val, 10)
}

func (e *jsonEncoder) appendFloat64(dst []byte, key string, val float64) []byte {
	dst = appendJSONKey(dst, key)
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

func (e *jsonEncoder) appendBool(dst []byte, key string, val bool) []byte {
	dst = appendJSONKey(dst, key)
	if val {
		return append(dst, 't', 'r', 'u', 'e')
	}
	return append(dst, 'f', 'a', 'l', 's', 'e')
}

func (e *jsonEncoder) appendTime(dst []byte, key string, val time.Time) []byte {
	dst = appendJSONKey(dst, key)
	dst = append(dst, '"')
	var tb [36]byte
	dst = append(dst, val.AppendFormat(tb[:0], time.RFC3339Nano)...)
	return append(dst, '"')
}

func (e *jsonEncoder) appendDur(dst []byte, key string, val time.Duration) []byte {
	return e.appendStr(dst, key, val.String())
}

func (e *jsonEncoder) appendErr(dst []byte, err error, _ string) []byte {
	if err == nil {
		return dst
	}
	// 内联固定 key：省去 appendStr → appendJSONKey 的间接调用。
	// JSON 格式无颜色输出，忽略 valColor 参数。
	dst = append(dst, ',', '"', 'e', 'r', 'r', 'o', 'r', '"', ':')
	return appendJSONStr(dst, err.Error())
}

func (e *jsonEncoder) appendAny(dst []byte, key string, val any) []byte {
	raw, err := json.AppendMarshal(nil, val)
	if err != nil {
		return e.appendStr(dst, key, "!MARSHAL_ERROR")
	}
	dst = appendJSONKey(dst, key)
	return append(dst, raw...)
}

func (e *jsonEncoder) appendRawJSON(dst []byte, key string, raw []byte) []byte {
	if raw == nil {
		return dst
	}
	// 安全合约：caller 负责保证 raw 是合法 JSON。
	// 传入损坏或不完整的 JSON（如 "}" 提前关闭对象）将静默污染输出行，
	// 可能导致下游 SIEM / 告警系统解析整行失败。
	// 如需运行时校验，可在调用前自行使用 yakjson.Valid.
	dst = appendJSONKey(dst, key)
	return append(dst, raw...)
}

func (e *jsonEncoder) appendSource(dst []byte, pcs []uintptr) []byte {
	if len(pcs) == 0 || pcs[0] == 0 {
		return dst
	}
	// CallersFrames 能正确展开内联函数调用帧，提供准确的 file:line。
	// 不在此处创建 []uintptr 切片：和调用方共用已在栈上分配的数组切片。
	frames := runtime.CallersFrames(pcs)
	f, _ := frames.Next()
	if f.File == "" {
		return dst
	}
	if e.callerFunc != nil {
		// 自定义格式：CallerFunc 返回空字符串则完全省略该字段。
		// val 来自用户代码，可能含 '"' 或'\\'，必须经 appendJSONStr 转义。
		val := e.callerFunc(f.File, f.Line)
		if val == "" {
			return dst
		}
		dst = append(dst, ',')
		return appendJSONStr(append(dst, '"', 's', 'o', 'u', 'r', 'c', 'e', '"', ':'), val)
	}
	// 默认路径：appendSourceJSONField 对 file 逐字节转义，
	// 消除 Windows 路径 '\' 或含 '"' 的构建工具路径导致的 JSON 损坏。
	return appendSourceJSONField(dst, f.File, f.Line)
}

func (e *jsonEncoder) finalize(dst []byte, msg string, hasMsg bool, _ string) []byte {
	if hasMsg {
		// 内联固定 key：避免 appendJSONKey 的函数调用和安全检查开销。
		dst = append(dst, ',', '"', 'm', 's', 'g', '"', ':')
		dst = appendJSONStr(dst, msg)
	}
	return append(dst, '}', '\n')
}

func (e *jsonEncoder) levelColorOf(_ Level) string { return "" }

// ─── textEncoder ──────────────────────────────────────────────────────────────

// textTimeCache 缓存同一秒内的 "HH:MM:SS" 格式化结果。
// 同一秒内（>99.9% 命中率）直接复用，避免 time.AppendFormat 开销。
type textTimeCache struct {
	sec int64   // 对应的 Unix 秒
	hms [8]byte // "15:04:05" 格式化结果
}

// textEncoder Console 文本格式编码器（彩色，人类可读）。
// 输出示例（彩色开启、简写模式）：\x1b[2m10:30:00.123\x1b[0m \x1b[32mI\x1b[0m \x1b[34mmodule=\x1b[0mhttp hello\n
type textEncoder struct {
	timeFmt    TimeFormat
	consoleFmt string                   // Go 时间格式字符串，仅含时间部分
	color      bool                     // true 时输出 ANSI 颜色码
	fullLevel  bool                     // true → 完整级别名（TRACE/INFO...）；false（默认）→ 单字母（T/I...）
	fracDigits int8                     // 3=毫秒、6=微秒、-1=自定义格式（禁用缓存）
	callerFunc func(string, int) string // 自定义 source 显示；nil = 默认行为
	scheme     ColorScheme              // ANSI 颜色方案；零值字段使用内置默认色
	// 预计算的级别颜色字符串（含 ANSI 码 + Reset），由 buildLevelTables 填充。
	levelShortColor [7]string
	levelFullColor  [7]string
	cache           atomic.Pointer[textTimeCache] // 秒级格式化缓存，并发安全
}

// 各级别的 ANSI 前景色默认值（Reset 级别后）。
const (
	ansiReset  = "\x1b[0m"
	ansiTrace  = "\x1b[90m"       // 暗灰
	ansiDebug  = "\x1b[36m"       // 青色
	ansiInfo   = "\x1b[32m"       // 绿色
	ansiWarn   = "\x1b[33m"       // 黄色
	ansiError  = "\x1b[31m"       // 红色
	ansiPanic  = "\x1b[1;35m"     // 加粗洋红（可被 recover）
	ansiFatal  = "\x1b[1;31m"     // 加粗红色（不可恢复）
	ansiTime   = "\x1b[2m"        // 暗淡（dim），用于时间戳弱化
	ansiKey    = "\x1b[34m"       // 暗蓝色，用于字段 key
	ansiTag    = "\x1b[38;5;166m" // 橙色（256色），用于 Tag 标签
	ansiSource = "\x1b[93m"       // 亮黄色，用于 source 字段值
)

// colorOr 返回 custom（非空时）或 fallback，用于 ColorScheme 零值退回默认色。
func colorOr(custom, fallback string) string {
	if custom != "" {
		return custom
	}
	return fallback
}

func (e *textEncoder) levelColorOf(lvl Level) string {
	if !e.color {
		return ""
	}
	return e.levelColorOf2(lvl)
}

// levelColorOf2 内部无色判断版本，供 buildLevelTables 和 beginRecord 使用。
func (e *textEncoder) levelColorOf2(lvl Level) string {
	switch {
	case lvl <= Trace:
		return colorOr(e.scheme.Trace, ansiTrace)
	case lvl <= Debug:
		return colorOr(e.scheme.Debug, ansiDebug)
	case lvl <= Info:
		return colorOr(e.scheme.Info, ansiInfo)
	case lvl <= Warn:
		return colorOr(e.scheme.Warn, ansiWarn)
	case lvl <= Error:
		return colorOr(e.scheme.Error, ansiError)
	case lvl <= Panic:
		return colorOr(e.scheme.Panic, ansiPanic)
	default:
		return colorOr(e.scheme.Fatal, ansiFatal)
	}
}

// schemeKey 返回字段 key 颜色（含 = 号的暗蓝色部分）。
func (e *textEncoder) schemeKey() string { return colorOr(e.scheme.Key, ansiKey) }

// schemeTag 返回 Tag 标签颜色。
func (e *textEncoder) schemeTag() string { return colorOr(e.scheme.Tag, ansiTag) }

// schemeTime 返回时间戳颜色。
func (e *textEncoder) schemeTime() string { return colorOr(e.scheme.Time, ansiTime) }

// schemeSource 返回 source 字段值颜色。
func (e *textEncoder) schemeSource() string { return colorOr(e.scheme.Source, ansiSource) }

// buildLevelTables 根据当前 scheme 预计算各级别的彩色字符串（含 Reset）。
// 在 newEncoder 构造时调用一次，热路径中单次 append 替代三次 append。
func (e *textEncoder) buildLevelTables() {
	shorts := [7]string{"T", "D", "I", "W", "E", "P", "F"}
	fullNames := [7]string{"TRACE", "DEBUG", "INFO ", "WARN ", "ERROR", "PANIC", "FATAL"}
	levels := [7]Level{Trace, Debug, Info, Warn, Error, Panic, Fatal}
	for i, lvl := range levels {
		c := e.levelColorOf2(lvl)
		e.levelShortColor[i] = c + shorts[i] + ansiReset
		e.levelFullColor[i] = c + fullNames[i] + ansiReset
	}
}

// appendTextKey 追加文本格式键：` key=`（含前导空格）。
// color=true 时，key= 整体以暗蓝色显示（包含 '='），value 保持默认色，
// 实现「key= 暗蓝 · value 默认」的视觉层次。
//
// 热路径优化：绝大多数 key 为安全 ASCII（无控制字符、无空格、无 '='），
// 预扫描确认后直接整体追加，避免逐字节分支和单字节 append 的开销。
func (e *textEncoder) appendTextKey(dst []byte, key string) []byte {
	dst = append(dst, ' ')
	// 安全 key 快速路径：扫描不修改 dst，仅读取 key 字节（pipeline 友好）。
	safe := true
	for i := 0; i < len(key); i++ {
		if c := key[i]; c <= ' ' || c == '=' {
			safe = false
			break
		}
	}
	if safe {
		if e.color {
			dst = append(dst, e.schemeKey()...)
			dst = append(dst, key...)
			dst = append(dst, '=')
			dst = append(dst, ansiReset...)
			return dst
		}
		dst = append(dst, key...)
		return append(dst, '=')
	}
	// 慢路径：逐字节清理不安全字符（替换为 '_'）。
	if e.color {
		dst = append(dst, e.schemeKey()...)
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c <= ' ' || c == '=' {
			c = '_'
		}
		dst = append(dst, c)
	}
	if e.color {
		dst = append(dst, '=')
		dst = append(dst, ansiReset...)
		return dst
	}
	return append(dst, '=')
}

func (e *textEncoder) beginRecord(dst []byte, tsNano int64, level Level) []byte {
	if e.fracDigits >= 0 {
		// 快速路径：缓存 "HH:MM:SS" 部分（同一秒内 >99.9% 命中），手动追加小数位
		sec := tsNano / 1_000_000_000
		tc := e.cache.Load()
		if tc == nil || tc.sec != sec {
			var newTC textTimeCache
			newTC.sec = sec
			var tb [8]byte
			copy(newTC.hms[:], time.Unix(sec, 0).Local().AppendFormat(tb[:0], "15:04:05"))
			tc = &newTC
			e.cache.Store(tc)
		}
		if e.color {
			dst = append(dst, e.schemeTime()...)
		}
		dst = append(dst, tc.hms[:]...)
		dst = append(dst, '.')
		nsRem := tsNano - sec*1_000_000_000
		if e.fracDigits == 3 {
			dst = appendFracDigits(dst, nsRem/1_000_000, 3)
		} else {
			dst = appendFracDigits(dst, nsRem/1_000, 6)
		}
		if e.color {
			dst = append(dst, ansiReset...)
		}
	} else {
		// 慢路径：自定义格式，完整 AppendFormat
		if e.color {
			dst = append(dst, e.schemeTime()...)
		}
		dst = time.Unix(0, tsNano).Local().AppendFormat(dst, e.consoleFmt)
		if e.color {
			dst = append(dst, ansiReset...)
		}
	}
	dst = append(dst, ' ')
	// 级别：预计算字符串单次 append，替代 levelColorOf + levelShort + ansiReset 三次 append。
	idx := int(level) + 2
	if idx >= 0 && idx < len(e.levelShortColor) {
		if e.color {
			if e.fullLevel {
				dst = append(dst, e.levelFullColor[idx]...)
			} else {
				dst = append(dst, e.levelShortColor[idx]...)
			}
		} else {
			if e.fullLevel {
				dst = append(dst, levelPad(level)...)
			} else {
				dst = append(dst, levelShort(level)...)
			}
		}
	} else {
		// 未知级别回退
		if e.color {
			dst = append(dst, e.levelColorOf2(level)...)
			if e.fullLevel {
				dst = append(dst, levelPad(level)...)
			} else {
				dst = append(dst, levelShort(level)...)
			}
			dst = append(dst, ansiReset...)
		} else {
			if e.fullLevel {
				dst = append(dst, levelPad(level)...)
			} else {
				dst = append(dst, levelShort(level)...)
			}
		}
	}
	return dst
}

func (e *textEncoder) appendStr(dst []byte, key, val string) []byte {
	dst = e.appendTextKey(dst, key)
	return appendTextVal(dst, val)
}

func (e *textEncoder) appendInt64(dst []byte, key string, val int64) []byte {
	dst = e.appendTextKey(dst, key)
	return strconv.AppendInt(dst, val, 10)
}

func (e *textEncoder) appendUint64(dst []byte, key string, val uint64) []byte {
	dst = e.appendTextKey(dst, key)
	return strconv.AppendUint(dst, val, 10)
}

func (e *textEncoder) appendFloat64(dst []byte, key string, val float64) []byte {
	dst = e.appendTextKey(dst, key)
	switch {
	case math.IsNaN(val):
		return append(dst, 'N', 'a', 'N')
	case math.IsInf(val, 1):
		return append(dst, 'I', 'n', 'f')
	case math.IsInf(val, -1):
		return append(dst, '-', 'I', 'n', 'f')
	}
	return strconv.AppendFloat(dst, val, 'f', -1, 64)
}

func (e *textEncoder) appendBool(dst []byte, key string, val bool) []byte {
	dst = e.appendTextKey(dst, key)
	if val {
		return append(dst, 't', 'r', 'u', 'e')
	}
	return append(dst, 'f', 'a', 'l', 's', 'e')
}

func (e *textEncoder) appendTime(dst []byte, key string, val time.Time) []byte {
	dst = e.appendTextKey(dst, key)
	var tb [36]byte
	return append(dst, val.AppendFormat(tb[:0], time.RFC3339Nano)...)
}

func (e *textEncoder) appendDur(dst []byte, key string, val time.Duration) []byte {
	return e.appendStr(dst, key, val.String())
}

func (e *textEncoder) appendErr(dst []byte, err error, valColor string) []byte {
	if err == nil {
		return dst
	}
	// 内联固定 key "error"（已知安全 ASCII），跳过 appendTextKey 的安全扫描。
	// valColor 非空时，value 使用与当前记录同级别的 ANSI 色（如 Error 级别用红色），
	// 增强 Console 模式下错误信息的视觉区分度。
	dst = append(dst, ' ')
	if e.color {
		// key= 整体暗蓝色（含 '='），与普通字段样式一致；
		// valColor 非空时 value 跟随日志级别色（如 Error→红）。
		dst = append(dst, e.schemeKey()+"error="+ansiReset...)
		if valColor != "" {
			dst = append(dst, valColor...)
		}
	} else {
		dst = append(dst, "error="...)
	}
	dst = appendTextVal(dst, err.Error())
	if e.color && valColor != "" {
		dst = append(dst, ansiReset...)
	}
	return dst
}

func (e *textEncoder) appendAny(dst []byte, key string, val any) []byte {
	raw, err := json.AppendMarshal(nil, val)
	if err != nil {
		return e.appendStr(dst, key, "!MARSHAL_ERROR")
	}
	dst = e.appendTextKey(dst, key)
	return append(dst, raw...)
}

// appendRawJSON 追加原始 JSON 到 Console 日志行。
// 注意：raw 中的换行符会直接输出，导致日志行被拆分。
// 调用方（Event.JSON）应在 doc 中提示用户使用紧凑格式 JSON。
func (e *textEncoder) appendRawJSON(dst []byte, key string, raw []byte) []byte {
	if raw == nil {
		return dst
	}
	dst = e.appendTextKey(dst, key)
	return append(dst, raw...)
}

func (e *textEncoder) appendSource(dst []byte, pcs []uintptr) []byte {
	if len(pcs) == 0 || pcs[0] == 0 {
		return dst
	}
	// CallersFrames 能正确展开内联函数调用帧，提供准确的 file:line。
	frames := runtime.CallersFrames(pcs)
	f, _ := frames.Next()
	if f.File == "" {
		return dst
	}
	// 内联固定 key "source"（已知安全 ASCII），跳过 appendTextKey 的安全扫描。
	if e.callerFunc != nil {
		// 自定义格式：CallerFunc 返回空字符串则完全省略该字段。
		// val 来自用户代码；含空格或 '=' 时 appendTextVal 自动加引号，
		// 防止 logfmt / Vector 等解析器将含空格的值截断为多个字段。
		val := e.callerFunc(f.File, f.Line)
		if val == "" {
			return dst
		}
		dst = append(dst, ' ')
		if e.color {
			dst = append(dst, e.schemeKey()+"source="+ansiReset...)
			dst = append(dst, e.schemeSource()...)
			dst = appendTextVal(dst, val)
			return append(dst, ansiReset...)
		}
		dst = append(dst, "source="...)
		return appendTextVal(dst, val)
	}
	dst = append(dst, ' ')
	if e.color {
		dst = append(dst, e.schemeKey()+"source="+ansiReset...)
		dst = append(dst, e.schemeSource()...)
		dst = append(dst, f.File...)
		dst = append(dst, ':')
		dst = strconv.AppendInt(dst, int64(f.Line), 10)
		return append(dst, ansiReset...)
	}
	dst = append(dst, "source="...)
	dst = append(dst, f.File...)
	dst = append(dst, ':')
	return strconv.AppendInt(dst, int64(f.Line), 10)
}

func (e *textEncoder) finalize(dst []byte, msg string, hasMsg bool, msgColor string) []byte {
	if hasMsg {
		dst = append(dst, ' ')
		if e.color && msgColor != "" {
			dst = append(dst, msgColor...)
			dst = appendStripCR(dst, msg)
			dst = append(dst, ansiReset...)
		} else {
			dst = appendStripCR(dst, msg)
		}
	}
	return append(dst, '\n')
}

// appendFracDigits 追加零填充的小数位数字（3=毫秒、6=微秒）。
func appendFracDigits(dst []byte, val int64, n int) []byte {
	var buf [6]byte
	for i := n - 1; i >= 0; i-- {
		buf[i] = byte(val%10) + '0'
		val /= 10
	}
	return append(dst, buf[:n]...)
}
