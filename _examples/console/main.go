// console demonstrates all core yaklog capabilities with a section banner per feature.
//
// Run:
//
//	cd _examples && go run ./console      # English
//	cd _examples && go run ./console/zh   # Chinese
//
// Output is organized into six sections:
//
//	§1  Field types     — Str Int Int64 Uint64 Float64 Bool Dur Time Err Bytes Stringer
//	§2  Level & time    — short/full level names, milli/micro/RFC3339/custom timestamps
//	§3  Context & caller — WithTrace/WithField, Caller(), CallerFunc sanitization
//	§4  Performance     — Labels pre-build; JSON(zero-alloc) vs Any; Post async vs Send
//	§5  Color scheme    — ColorScheme solarized-dark replaces all built-in colors
//	§6  CI & terminators — ConsoleNoColor, Panic, Fatal
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

// banner prints a plain-text section heading.
func banner(plain *yaklog.Logger, num, title string) {
	plain.Info().Msg("══ §" + num + "  " + title).Send()
}

func main() {
	// ── base logger (CallerFunc strips absolute path, keeps basename:line) ──
	log := yaklog.New(yaklog.Options{
		Out:   yaklog.Console(os.Stdout),
		Level: yaklog.Trace,
		CallerFunc: func(file string, line int) string {
			return path.Base(file) + ":" + strconv.Itoa(line)
		},
	})

	// ── plain logger (no color — used for banners and CI output) ───────────
	plain := yaklog.New(yaklog.Options{
		Out:            yaklog.Console(os.Stdout),
		Level:          yaklog.Trace,
		ConsoleNoColor: true,
	})

	// ════════════════════════════════════════════════════════════════════════
	// §1  Field types
	//     Str Int Int64 Uint64 Float64 Bool Dur Time Err AnErr Bytes Any JSON Stringer
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "1", "Field types  Str/Int/Float64/Bool/Dur/Time/Err/Bytes/Stringer")

	// Scalar primitives
	log.Info().
		Str("str", "hello").
		Int("int", 42).
		Int64("int64", -9007199254740992).
		Uint64("uint64", 18446744073709551615).
		Float64("f64", 3.1415).
		Bool("ok", true).
		Msg("scalar primitives").Send()

	// Duration and time
	log.Info().
		Dur("elapsed", 2*time.Second+375*time.Millisecond).
		Time("at", time.Date(2026, 3, 9, 12, 0, 0, 0, time.Local)).
		Msg("duration and time").Send()

	// Error fields: Err uses fixed key "error"; AnErr accepts a custom key
	log.Warn().
		Err(errors.New("connection refused")).
		AnErr("wrapped", fmt.Errorf("dial: %w", errors.New("timeout"))).
		Msg("Err → key=error   AnErr → custom key").Send()

	// Bytes: []byte rendered as a safe string value
	log.Info().
		Bytes("raw", []byte("binary\x00data")).
		Msg("Bytes: []byte as safe string").Send()

	// Stringer: any type implementing fmt.Stringer
	log.Info().
		Stringer("addr", &netAddr{"tcp", "127.0.0.1:8080"}).
		Msg("Stringer: calls .String()").Send()

	fmt.Println()
	// ════════════════════════════════════════════════════════════════════════
	// §2  Level names & timestamps
	//     Single-letter T D I W E  /  full TRACE DEBUG INFO WARN ERROR
	//     ConsoleTimeMilli (default) / ConsoleTimeMicro / ConsoleTimeRFC3339Milli / custom
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "2", "Level & time  short/full level · milli/micro/RFC3339/custom timestamp")

	// All five levels — short single-letter + millisecond (default format)
	log.Trace().Str("component", "loader").Msg("config loaded").Send()
	log.Debug().Str("query", "SELECT 1").Dur("took", 2*time.Millisecond).Msg("db ping ok").Send()
	log.Info().Int("port", 8080).Msg("server listening").Send()
	log.Warn().Int("idle", 2).Str("pool", "db").Msg("connection pool running low").Send()
	log.Error().Err(errors.New("dial tcp: connection refused")).Msg("upstream unreachable").Send()

	// Full level names, right-padded to 5 chars (INFO·, WARN·, …)
	full := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		ConsoleLevelFull: true,
	})
	full.Info().Str("fmt", "full level name INFO").Send()
	full.Warn().Str("fmt", "full level name WARN, padded to 5 chars").Send()

	// Microsecond timestamp (cache fast-path, same as milli)
	micro := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		ConsoleTimeFormat: yaklog.ConsoleTimeMicro, // "15:04:05.000000"
	})
	micro.Debug().Dur("t", 843*time.Microsecond).Msg("microsecond precision timestamp").Send()

	// RFC3339 with timezone offset (full date + ms; slow path via time.AppendFormat)
	rfc := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		ConsoleTimeFormat: yaklog.ConsoleTimeRFC3339Milli, // "2006-01-02T15:04:05.000Z07:00"
	})
	rfc.Info().Str("tz", "+08:00").Msg("RFC3339 with timezone offset").Send()

	// Fully custom layout — any time.Format pattern string
	custom := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		ConsoleTimeFormat: "2006-01-02 15:04:05.000",
	})
	custom.Info().Str("fmt", "date+time+milli").Msg("custom time layout").Send()

	fmt.Println()
	// ════════════════════════════════════════════════════════════════════════
	// §3  Context & caller
	//     WithTrace/WithField: inject trace_id / fields into context; Ctx() extracts them
	//     Caller(): attach source=file:line per event (does not affect other records)
	//     CallerFunc: customize or sanitize the source value; return "" to omit it
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "3", "Context & caller  WithTrace/WithField / Caller() / CallerFunc sanitize")

	// 128-bit trace ID via yakhash MurmurHash3 (zero-alloc)
	traceID := yakhash.Sum3_128Seed([]byte("trace"), uint64(time.Now().UnixNano())).Bytes()
	ctx := yaklog.WithTrace(context.Background(), traceID)
	ctx = yaklog.WithField(ctx, "req_id", "req-9f2a")
	ctx = yaklog.WithField(ctx, "uid", "u:42")

	// Ctx(): automatically extracts trace_id and all WithField-injected fields
	log.Tag("http").Info().
		Ctx(ctx).
		Int("status", 200).
		Msg("Ctx() extracts trace_id / req_id / uid from context").Send()

	// Caller(): CallerFunc already strips absolute path to "basename:line"
	log.Tag("rpc").Error().
		Caller().
		Err(errors.New("upstream timeout")).
		Msg("Caller() + CallerFunc → source=main.go:N").Send()

	// CallerFunc returns "" → source field is omitted entirely
	noSource := yaklog.New(yaklog.Options{
		Out: yaklog.Console(os.Stdout), Level: yaklog.Trace,
		CallerFunc: func(string, int) string { return "" },
	})
	noSource.Error().Caller().Err(errors.New("hidden")).Msg("CallerFunc returns \"\" → source omitted").Send()

	fmt.Println()
	// ════════════════════════════════════════════════════════════════════════
	// §4  Performance
	//     Labels: pre-build a child Logger at init time; fixed fields encoded once,
	//             reused on every record — no repeated prefix allocation at runtime
	//     JSON(key, raw): append raw bytes as-is — zero-alloc, no reflect
	//     Any(key, val):  goes through yakjson.AppendMarshal — temp buf alloc + reflect
	//     Post(): enqueue and return immediately (0 alloc); Send(): wait for flush
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "4", "Performance  Labels pre-build / JSON zero-alloc vs Any / Post async vs Send")

	// Labels pre-build: service + region encoded once at Build(), reused on every call
	// Hot-path records share the prefix buffer — no repeated allocation at runtime.
	gw := log.Labels().
		Str("service", "gateway").
		Str("region", "cn-north").
		Build()

	gw.Tag("req").Info().Int("status", 200).Msg("Labels pre-built: fixed fields encoded once").Send()
	gw.Tag("req").Warn().Int("status", 429).Msg("same child logger — no prefix re-allocation").Send()

	// JSON vs Any: equivalent JSON output, very different allocation cost.
	// JSON(key, raw) appends raw bytes directly (append-only, zero-alloc, no reflect).
	// Any(key, val) serializes at call time via yakjson (temp buf alloc + reflection).
	type Meta struct {
		Region string `json:"region"`
		Nodes  int    `json:"nodes"`
	}
	log.Tag("upstream").Info().
		JSON("resp", []byte(`{"code":200,"latency_ms":4}`)).
		Msg("JSON(raw bytes) — zero-alloc, embedded directly").Send()

	log.Tag("upstream").Info().
		Any("meta", Meta{Region: "cn-north", Nodes: 3}).
		Msg("Any(struct) — serialized via yakjson (allocates)").Send()

	// Post vs Send: choose based on latency budget.
	// Send() blocks until the record is written; Post() enqueues and returns immediately.
	log.Tag("perf").Info().Msg("Send()  synchronous — flushed before call returns").Send()
	log.Tag("perf").Info().Msg("Post()  async — enqueued and returns immediately (0 alloc)").Post()
	yaklog.Wait() // drain the async worker queue (keeps demo output ordered)

	fmt.Println()
	// ════════════════════════════════════════════════════════════════════════
	// §5  Color scheme
	//     Options.ColorScheme replaces ANSI codes for all 11 visual elements.
	//     Zero-value fields fall back to built-in defaults — partial overrides work.
	//     Example: solarized-dark palette
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "5", "Color scheme  ColorScheme solarized-dark (Options.ColorScheme)")

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
	// §6  CI / no-color & terminating levels
	//     ConsoleNoColor=true  → no ANSI escape codes; safe for CI or file redirect
	//     Panic  → calls PanicFunc after writing (default: panic(), recoverable)
	//     Fatal  → calls FatalFunc after writing (default: os.Exit(1))
	//     Both hooks are replaceable via SetPanicFunc / SetFatalFunc
	// ════════════════════════════════════════════════════════════════════════
	banner(plain, "6", "CI & terminators  ConsoleNoColor / Panic / Fatal")

	plain.Tag("ci").Info().Str("runner", "gh-actions").Msg("ConsoleNoColor=true: no ANSI escape codes").Send()
	plain.Tag("ci").Warn().Str("step", "lint").Int("exit", 1).Msg("safe for log files and CI pipelines").Send()

	// Replace terminating hooks to prevent process exit (demo only)
	oldPanic := yaklog.GetPanicFunc()
	yaklog.SetPanicFunc(func(string) {})
	defer yaklog.SetPanicFunc(oldPanic)

	oldExit := yaklog.GetFatalFunc()
	yaklog.SetFatalFunc(func(int) {})
	defer yaklog.SetFatalFunc(oldExit)

	log.Tag("worker").Panic().
		Err(errors.New("nil pointer dereference")).
		Msg("Panic: calls PanicFunc after write (recoverable; replaced with noop here)").Send()

	log.Tag("proc").Fatal().
		Str("sig", "SIGTERM").
		Msg("Fatal: calls FatalFunc after write (default os.Exit(1); replaced with noop here)").Send()
}

// ── helper type ───────────────────────────────────────────────────────────────

type netAddr struct{ network, address string }

func (a *netAddr) String() string { return a.network + "://" + a.address }
