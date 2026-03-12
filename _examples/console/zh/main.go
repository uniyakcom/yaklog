// console/zh 演示 yaklog 的全部核心能力，每节配有说明性 banner。
//
// 运行：
//
//	cd _examples && go run ./console/zh
//
// 输出分六节：
//
//	§1  字段类型全览   — Str Int Int64 Uint64 Float64 Bool Dur Time Err Bytes Stringer
//	§2  级别与时间格式 — 单字母/完整级别、毫秒/微秒/RFC3339/自定义时间戳
//	§3  上下文与溯源   — WithTrace/WithField、Caller()、CallerFunc 脱敏
//	§4  性能特性       — Labels 预构建；JSON(零分配) vs Any；Post 异步 vs Send 同步
//	§5  自定义配色     — ColorScheme solarized-dark 完整替换内置色
//	§6  CI 无色 & 终止 — ConsoleNoColor、Panic、Fatal
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/uniyakcom/yakhash"
	"github.com/uniyakcom/yaklog"
)

// banner 输出无色节标题行。
func banner(plain *yaklog.Logger, num, title string) {
	plain.Info().Msg("══ §" + num + "  " + title).Send()
}

func main() {
	// ── 基础 logger（带 CallerFunc 脱敏，路径只保留文件名） ─────────────────
	log := yaklog.New(yaklog.Options{
		Out:   yaklog.Console(os.Stdout),
		Level: yaklog.Trace,
		CallerFunc: func(file string, line int) string {
			return path.Base(file) + ":" + strconv.Itoa(line)
		},
	})

	// ── 无色 logger（用于 banner 输出 / CI 场景） ─────────────────────────
	plain := yaklog.New(yaklog.Options{
		Out:            yaklog.Console(os.Stdout),
		Level:          yaklog.Trace,
		ConsoleNoColor: true,
	})

	// ════════════════════════════════════════════════════════════════════════
	// §1  字段类型全览
	//     Str Int Int64 Uint64 Float64 Bool Dur Time Err AnErr Bytes Any JSON Stringer
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "1", "字段类型全览  Str/Int/Float64/Bool/Dur/Time/Err/Bytes/Stringer")

	// 基础标量
	log.Info().
		Str("str", "hello").
		Int("int", 42).
		Int64("int64", -9007199254740992).
		Uint64("uint64", 18446744073709551615).
		Float64("f64", 3.1415).
		Bool("ok", true).
		Msg("基础标量字段").Send()

	// 时间与时长
	log.Info().
		Dur("elapsed", 2*time.Second+375*time.Millisecond).
		Time("at", time.Date(2026, 3, 9, 12, 0, 0, 0, time.Local)).
		Msg("时间与时长").Send()

	// error 字段：Err 固定 key=error，AnErr 可自定义 key
	log.Warn().
		Err(errors.New("connection refused")).
		AnErr("wrapped", fmt.Errorf("dial: %w", errors.New("timeout"))).
		Msg("Err → key=error  AnErr → 自定义 key").Send()

	// Bytes：将 []byte 以字符串安全输出
	log.Info().
		Bytes("raw", []byte("binary\x00data")).
		Msg("Bytes：[]byte 安全字符串输出").Send()

	// Stringer：任何实现 fmt.Stringer 的类型
	log.Info().
		Stringer("addr", &netAddr{"tcp", "127.0.0.1:8080"}).
		Msg("Stringer：调用 .String()").Send()

	fmt.Println()
	// ════════════════════════════════════════════════════════════════════════
	// §2  级别与时间格式
	//     单字母 T D I W E  /  完整 TRACE DEBUG INFO WARN ERROR
	//     ConsoleTimeMilli（默认）/ ConsoleTimeMicro / ConsoleTimeRFC3339Milli / 自定义
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "2", "级别与时间格式  短级别/完整级别/毫秒/微秒/RFC3339/自定义")

	// 全部五个级别——单字母 + 毫秒时间戳（默认格式），每条使用真实场景消息
	log.Trace().Str("component", "loader").Msg("配置加载完成").Send()
	log.Debug().Str("query", "SELECT 1").Dur("took", 2*time.Millisecond).Msg("db ping 正常").Send()
	log.Info().Int("port", 8080).Msg("服务器已监听").Send()
	log.Warn().Int("idle", 2).Str("pool", "db").Msg("连接池空闲连接不足").Send()
	log.Error().Err(errors.New("dial tcp: connection refused")).Msg("上游不可达").Send()

	// 完整级别名，右侧补齐至 5 字符
	full := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		ConsoleLevelFull: true,
	})
	full.Info().Str("fmt", "完整级别名 INFO").Send()
	full.Warn().Str("fmt", "完整级别名 WARN，对齐至5字符").Send()

	// 微秒时间戳（走缓存快速路径）
	micro := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		ConsoleTimeFormat: yaklog.ConsoleTimeMicro, // "15:04:05.000000"
	})
	micro.Debug().Dur("t", 843*time.Microsecond).Msg("微秒精度时间戳").Send()

	// RFC3339 + 时区偏移（含完整日期，走慢路径）
	rfc := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		ConsoleTimeFormat: yaklog.ConsoleTimeRFC3339Milli, // "2006-01-02T15:04:05.000Z07:00"
	})
	rfc.Info().Str("tz", "+08:00").Msg("RFC3339 含时区偏移").Send()

	// 完全自定义布局（任意 time.Format 格式串）
	custom := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		ConsoleTimeFormat: "2006-01-02 15:04:05.000",
	})
	custom.Info().Str("fmt", "日期+时间+毫秒").Msg("自定义时间布局").Send()

	fmt.Println()
	// ════════════════════════════════════════════════════════════════════════
	// §3  上下文与溯源
	//     WithTrace/WithField：将 trace_id / 通用字段注入 context，随 Ctx() 自动输出
	//     Caller()：逐事件附加 source=file:line（不影响其他记录）
	//     CallerFunc：完全自定义 source 格式或脱敏路径，返回 "" 则省略
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "3", "上下文与溯源  WithTrace/WithField / Caller() / CallerFunc 脱敏")

	// 生成 128-bit 追踪 ID（yakhash MurmurHash3 变种，无分配）
	traceID := yakhash.Sum3_128Seed([]byte("trace"), uint64(time.Now().UnixNano())).Bytes()
	ctx := yaklog.WithTrace(context.Background(), traceID)
	ctx = yaklog.WithField(ctx, "req_id", "req-9f2a")
	ctx = yaklog.WithField(ctx, "uid", "u:42")

	// Ctx()：从 context 提取 trace_id 及所有 WithField 注入的字段
	log.Tag("http").Info().
		Ctx(ctx).
		Int("status", 200).
		Msg("Ctx() 自动提取 trace_id / req_id / uid").Send()

	// Caller()：CallerFunc 已将绝对路径脱敏为 "basename:line"
	log.Tag("rpc").Error().
		Caller().
		Err(errors.New("upstream timeout")).
		Msg("Caller() + CallerFunc 脱敏为 basename:line").Send()

	// CallerFunc 返回 "" → source 字段完全省略
	noSource := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		CallerFunc: func(string, int) string { return "" },
	})
	noSource.Error().Caller().Err(errors.New("hidden")).Msg("CallerFunc 返回 \"\" 则省略 source").Send()

	fmt.Println()
	// ════════════════════════════════════════════════════════════════════════
	// §4  性能特性
	//     Labels：初始化阶段预构建子 Logger，固定字段仅拼接一次，运行时复用
	//     JSON(key, raw)：raw bytes 零分配嵌入，适合热路径中已有序列化结果的场景
	//     Any(key, val)：走 yakjson.AppendMarshal，有临时 buf 分配 + reflect 遍历
	//     Post()：异步入队立即返回（0 alloc）；Send()：同步等待落盘
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "4", "性能特性  Labels预构建 / JSON零分配 vs Any / Post异步 vs Send同步")

	// Labels 预构建：service / region 只在 Build() 时编码一次，后续所有调用复用前缀。
	// 热路径每条记录无重复 buf 分配，尤其适合高频日志场景。
	gw := log.Labels().
		Str("service", "gateway").
		Str("region", "cn-north").
		Build()

	gw.Tag("req").Info().Int("status", 200).Msg("Labels 预构建：固定字段只编码一次").Send()
	gw.Tag("req").Warn().Int("status", 429).Msg("同一子 Logger，前缀不再重复分配").Send()

	// JSON vs Any：输出等价，分配开销差异显著。
	// JSON(key, raw) 直接 append 原始字节，零分配、不经 reflect。
	// Any(key, val) 在调用时经 yakjson 序列化：临时 buf 分配 + reflect 遍历。
	type Meta struct {
		Region string `json:"region"`
		Nodes  int    `json:"nodes"`
	}
	log.Tag("upstream").Info().
		JSON("resp", []byte(`{"code":200,"latency_ms":4}`)).
		Msg("JSON(raw bytes) 零分配直接嵌入").Send()

	log.Tag("upstream").Info().
		Any("meta", Meta{Region: "cn-north", Nodes: 3}).
		Msg("Any(struct) 经 yakjson 序列化（有分配）").Send()

	// Post vs Send：按延迟预算选择。
	// Send() 阻塞直到记录写入完成；Post() 异步入队、立即返回（0 alloc）。
	log.Tag("perf").Info().Msg("Send()  同步——返回时数据已落盘，适合关键事件").Send()
	log.Tag("perf").Info().Msg("Post()  异步——立即返回，热路径零阻塞（0 alloc）").Post()
	yaklog.Wait() // 等待 worker 队列排空（示例保证有序输出）

	fmt.Println()
	// ════════════════════════════════════════════════════════════════════════
	// §5  自定义配色
	//     Options.ColorScheme 完整替换 13 个元素的 ANSI 色码
	//     零值字段自动回退到内置默认，无需全量指定
	//     示例：solarized-dark 调色板
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "5", "自定义配色  ColorScheme solarized-dark（Options.ColorScheme）")

	solar := yaklog.New(yaklog.Options{
		Out:   yaklog.Console(os.Stdout),
		Level: yaklog.Trace,
		ColorScheme: yaklog.ColorScheme{
			Trace:  "\x1b[38;5;244m", // gray
			Debug:  "\x1b[38;5;37m",  // teal
			Info:   "\x1b[38;5;64m",  // olive green
			Warn:   "\x1b[38;5;136m", // deep yellow
			Error:  "\x1b[38;5;160m", // deep red
			Panic:  "\x1b[38;5;125m", // deep magenta
			Fatal:  "\x1b[1;38;5;160m",
			Time:   "\x1b[38;5;240m", // dim gray
			Key:    "\x1b[38;5;33m",  // blue
			Tag:    "\x1b[35m",       // magenta
			Source: "\x1b[38;5;142m", // yellow-green
		},
	})

	solar.Tag("cache").Trace().Str("key", "user:42").Msg("solarized Trace").Send()
	solar.Tag("auth").Debug().Str("method", "POST").Msg("solarized Debug").Send()
	solar.Tag("api").Info().Int("status", 200).Msg("solarized Info").Send()
	solar.Tag("db").Warn().Int("idle", 0).Msg("solarized Warn").Send()
	solar.Tag("rpc").Error().Caller().Err(errors.New("timeout")).Msg("solarized Error + Caller()").Send()

	fmt.Println()
	// ════════════════════════════════════════════════════════════════════════
	// §6  CI 无色 & 终止级别
	//     ConsoleNoColor=true → 无 ANSI 转义码，适合 CI 或重定向到文件
	//     Panic  → 写完后调用 PanicFunc（默认 panic()，可被 recover）
	//     Fatal  → 写完后调用 FatalFunc（默认 os.Exit(1)）
	//     两者均可通过 SetPanicFunc / SetFatalFunc 替换为自定义钩子
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "6", "CI 无色 & 终止级别  ConsoleNoColor / Panic / Fatal")

	plain.Tag("ci").Info().Str("runner", "gh-actions").Msg("ConsoleNoColor=true，无 ANSI 转义").Send()
	plain.Tag("ci").Warn().Str("step", "lint").Int("exit", 1).Msg("适合日志文件或 CI 管道").Send()

	// 替换终止钩子以防止进程退出（示例专用）
	oldPanic := yaklog.GetPanicFunc()
	yaklog.SetPanicFunc(func(string) {})
	defer yaklog.SetPanicFunc(oldPanic)

	oldExit := yaklog.GetFatalFunc()
	yaklog.SetFatalFunc(func(int) {})
	defer yaklog.SetFatalFunc(oldExit)

	log.Tag("worker").Panic().
		Err(errors.New("nil pointer dereference")).
		Msg("Panic：写完后触发 PanicFunc（可 recover，此处已替换为 noop）").Send()

	log.Tag("proc").Fatal().
		Str("sig", "SIGTERM").
		Msg("Fatal：写完后触发 FatalFunc（默认 os.Exit(1)，此处已替换为 noop）").Send()
}

// ── 辅助类型 ──────────────────────────────────────────────────────────────────

type netAddr struct{ network, address string }

func (a *netAddr) String() string { return a.network + "://" + a.address }
