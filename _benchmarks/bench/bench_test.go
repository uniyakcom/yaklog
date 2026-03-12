// Package bench compares yaklog against mainstream Go logging libraries.
//
// Mirrors the three classic benchmark tables from the zerolog README and
// the Uber/zap benchmark suite:
//
//   - BenchmarkStaticMsg      — "Log a static string"
//   - Benchmark10Fields       — "Log a message and 10 fields"
//   - BenchmarkContextFields  — "Log a message with a logger that has 10 fields of context"
//
// Covered libraries: yaklog · zerolog · zap · zap(sugared) · logrus · slog(JSON) · stdlib log
// All subtests use RunParallel + io.Discard, consistent with zerolog/zap README methodology.
// yaklog uses Send() (synchronous path) + TimeOff (no timestamp), matching zerolog's default
// no-timestamp behavior for a fair equal-payload comparison.
//
// Run:
//
//	go test -bench=. -benchmem -benchtime=3s ./bench/
package bench

import (
	"errors"
	"io"
	stdlog "log"
	"log/slog"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/sirupsen/logrus"
	"github.com/uniyakcom/yaklog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mainstream Go logging library comparison
//
// Mirrors the three classic benchmark tables from the zerolog README and
// the Uber/zap benchmark suite:
//
//   BenchmarkStaticMsg      ← "Log a static string"（无字段，仅消息）
//   Benchmark10Fields       ← "Log a message and 10 fields"（每次调用追加 10 个字段）
//   BenchmarkContextFields  ← "Log a message with a logger that has 10 fields of context"
//                            （字段预构建至 logger，循环内仅打印消息）
//
// 覆盖库：yaklog · zerolog · zap · zap(sugared) · logrus · slog(JSON) · stdlib log
// 所有子测试均使用 RunParallel + io.Discard，与 zerolog / zap README 数字口径一致。
// yaklog 使用 Send()（同步路径，与其他库直接写 io.Writer 等价）。
//
// yaklog 使用 TimeOff（不输出时间戳），与 zerolog.New(io.Discard) 默认无时间戳一致，
// 确保三组场景均为等量编码的公平对比。若需含时间戳的生产场景数字，改用 TimeUnixMilli。
// ─────────────────────────────────────────────────────────────────────────────

// benchFakeMsg 与 zerolog benchmark_test.go 中的 fakeMessage 完全相同。
const benchFakeMsg = "Test logging, but use a somewhat realistic message length."

// benchFakeErr 用于 10-字段基准中的 error 字段，在 init 阶段构造一次。
var benchFakeErr = errors.New("something went wrong")

// newZapLogger 创建配置与 logbench zerolog_test.go 对齐的 zap 生产 Logger：
// JSON 编码、无时间戳字段（zerolog 默认亦无时间戳），消息键为 "message"。
func newZapLogger() *zap.Logger {
	ec := zap.NewProductionEncoderConfig()
	ec.TimeKey = ""
	ec.MessageKey = "message"
	return zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(ec),
		zapcore.AddSync(io.Discard),
		zapcore.DebugLevel,
	))
}

// newLogrusLogger 创建 JSON 格式、写入 io.Discard 的 logrus Logger。
func newLogrusLogger() *logrus.Logger {
	l := logrus.New()
	l.Out = io.Discard
	l.Formatter = &logrus.JSONFormatter{}
	return l
}

// ─────────────────────────────────────────────────────────────────────────────
// BenchmarkStaticMsg — Log a static string, without any context or printf-style templating
//
// 对应 zerolog README 第 3 张对比表格。
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkStaticMsg(b *testing.B) {
	b.Run("yaklog", func(b *testing.B) {
		l := yaklog.New(yaklog.Options{Out: io.Discard, TimeFormat: yaklog.TimeOff})
		b.Cleanup(func() { l.Wait() })
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info().Msg(benchFakeMsg).Send()
			}
		})
	})

	b.Run("zerolog", func(b *testing.B) {
		l := zerolog.New(io.Discard)
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info().Msg(benchFakeMsg)
			}
		})
	})

	b.Run("zap", func(b *testing.B) {
		l := newZapLogger()
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg)
			}
		})
	})

	b.Run("zap_sugared", func(b *testing.B) {
		l := newZapLogger().Sugar()
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg)
			}
		})
	})

	b.Run("logrus", func(b *testing.B) {
		l := newLogrusLogger()
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg)
			}
		})
	})

	b.Run("slog_json", func(b *testing.B) {
		l := slog.New(slog.NewJSONHandler(io.Discard, nil))
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg)
			}
		})
	})

	b.Run("stdlib", func(b *testing.B) {
		l := stdlog.New(io.Discard, "", 0)
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Print(benchFakeMsg)
			}
		})
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Benchmark10Fields — Log a message and 10 fields
//
// 对应 zerolog README 第 1 张对比表格（来自 Uber/zap 对比基准）。
// 每次调用追加 10 个不同类型字段：
//   2×Str / 1×Int / 1×Int64 / 1×Float64 / 1×Bool / 1×Time / 1×Dur / 1×Err / 1×Str
// stdlib log 不支持结构化字段，不参与此组。
// ─────────────────────────────────────────────────────────────────────────────

func Benchmark10Fields(b *testing.B) {
	b.Run("yaklog", func(b *testing.B) {
		l := yaklog.New(yaklog.Options{Out: io.Discard, TimeFormat: yaklog.TimeOff})
		b.Cleanup(func() { l.Wait() })
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
					AnErr("error", benchFakeErr).
					Str("string2", "some realistic string value").
					Msg(benchFakeMsg).Send()
			}
		})
	})

	b.Run("zerolog", func(b *testing.B) {
		l := zerolog.New(io.Discard)
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
					AnErr("error", benchFakeErr).
					Str("string2", "some realistic string value").
					Msg(benchFakeMsg)
			}
		})
	})

	b.Run("zap", func(b *testing.B) {
		l := newZapLogger()
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg,
					zap.String("string", "four!"),
					zap.String("url", "/api/v1/users"),
					zap.Int("int", 1),
					zap.Int64("int64", int64(2)),
					zap.Float64("float64", 3.14),
					zap.Bool("bool", true),
					zap.Time("time", time.Time{}),
					zap.Duration("duration", time.Millisecond),
					zap.NamedError("error", benchFakeErr),
					zap.String("string2", "some realistic string value"),
				)
			}
		})
	})

	b.Run("zap_sugared", func(b *testing.B) {
		l := newZapLogger().Sugar()
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Infow(benchFakeMsg,
					"string", "four!",
					"url", "/api/v1/users",
					"int", 1,
					"int64", int64(2),
					"float64", 3.14,
					"bool", true,
					"time", time.Time{},
					"duration", time.Millisecond,
					"error", benchFakeErr,
					"string2", "some realistic string value",
				)
			}
		})
	})

	b.Run("logrus", func(b *testing.B) {
		l := newLogrusLogger()
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.WithFields(logrus.Fields{
					"string":   "four!",
					"url":      "/api/v1/users",
					"int":      1,
					"int64":    int64(2),
					"float64":  3.14,
					"bool":     true,
					"time":     time.Time{},
					"duration": time.Millisecond,
					"error":    benchFakeErr,
					"string2":  "some realistic string value",
				}).Info(benchFakeMsg)
			}
		})
	})

	b.Run("slog_json", func(b *testing.B) {
		l := slog.New(slog.NewJSONHandler(io.Discard, nil))
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg,
					"string", "four!",
					"url", "/api/v1/users",
					"int", 1,
					"int64", int64(2),
					"float64", 3.14,
					"bool", true,
					"time", time.Time{},
					"duration", time.Millisecond,
					"error", benchFakeErr,
					"string2", "some realistic string value",
				)
			}
		})
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// BenchmarkContextFields — Log a message with a logger that already has 10 fields of context
//
// 对应 zerolog README 第 2 张对比表格。
// 10 个字段在 b.ResetTimer() 前预构建到 logger，循环内仅发送消息。
// 此场景最能体现各库 "context 复用" 的零分配能力差异。
// ─────────────────────────────────────────────────────────────────────────────

func BenchmarkContextFields(b *testing.B) {
	b.Run("yaklog", func(b *testing.B) {
		base := yaklog.New(yaklog.Options{Out: io.Discard, TimeFormat: yaklog.TimeOff})
		// Label 在构建时将字段序列化进 prefix 字节切片，循环内只做指针读取，零分配。
		// time.Time 走 any 路径（yakjson），此开销在 b.ResetTimer() 前已完成。
		l := base.
			Label("string", "four!").
			Label("url", "/api/v1/users").
			Label("int", 1).
			Label("int64", int64(2)).
			Label("float64", 3.14).
			Label("bool", true).
			Label("time", time.Time{}).
			Label("duration", time.Millisecond).
			Label("error", benchFakeErr.Error()).
			Label("string2", "some realistic string value")
		b.Cleanup(func() { base.Wait() })
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info().Msg(benchFakeMsg).Send()
			}
		})
	})

	b.Run("zerolog", func(b *testing.B) {
		l := zerolog.New(io.Discard).With().
			Str("string", "four!").
			Str("url", "/api/v1/users").
			Int("int", 1).
			Int64("int64", int64(2)).
			Float64("float64", 3.14).
			Bool("bool", true).
			Time("time", time.Time{}).
			Dur("duration", time.Millisecond).
			AnErr("error", benchFakeErr).
			Str("string2", "some realistic string value").
			Logger()
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info().Msg(benchFakeMsg)
			}
		})
	})

	b.Run("zap", func(b *testing.B) {
		l := newZapLogger().With(
			zap.String("string", "four!"),
			zap.String("url", "/api/v1/users"),
			zap.Int("int", 1),
			zap.Int64("int64", int64(2)),
			zap.Float64("float64", 3.14),
			zap.Bool("bool", true),
			zap.Time("time", time.Time{}),
			zap.Duration("duration", time.Millisecond),
			zap.NamedError("error", benchFakeErr),
			zap.String("string2", "some realistic string value"),
		)
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg)
			}
		})
	})

	b.Run("zap_sugared", func(b *testing.B) {
		l := newZapLogger().Sugar().With(
			"string", "four!",
			"url", "/api/v1/users",
			"int", 1,
			"int64", int64(2),
			"float64", 3.14,
			"bool", true,
			"time", time.Time{},
			"duration", time.Millisecond,
			"error", benchFakeErr,
			"string2", "some realistic string value",
		)
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg)
			}
		})
	})

	b.Run("logrus", func(b *testing.B) {
		l := newLogrusLogger().WithFields(logrus.Fields{
			"string":   "four!",
			"url":      "/api/v1/users",
			"int":      1,
			"int64":    int64(2),
			"float64":  3.14,
			"bool":     true,
			"time":     time.Time{},
			"duration": time.Millisecond,
			"error":    benchFakeErr,
			"string2":  "some realistic string value",
		})
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg)
			}
		})
	})

	b.Run("slog_json", func(b *testing.B) {
		l := slog.New(slog.NewJSONHandler(io.Discard, nil)).With(
			"string", "four!",
			"url", "/api/v1/users",
			"int", 1,
			"int64", int64(2),
			"float64", 3.14,
			"bool", true,
			"time", time.Time{},
			"duration", time.Millisecond,
			"error", benchFakeErr,
			"string2", "some realistic string value",
		)
		b.ResetTimer()
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				l.Info(benchFakeMsg)
			}
		})
	})
}
