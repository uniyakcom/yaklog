// slogbridge 演示将 yaklog 安装为标准库 log/slog 的默认后端，
// 以及通过 adapter.NewHandler 同时维护 slog.Logger 和 yaklog.Logger。
//
// 运行：
//
//	cd _examples && go run ./slogbridge
package main

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/uniyakcom/yaklog"
	"github.com/uniyakcom/yaklog/adapter"
)

func main() {
	// ── 1. SetDefault：yaklog 接管全局 slog 包函数 ───────────────────────────
	//
	// 安装后 slog.Info / slog.Warn / slog.ErrorContext 等全部输出到 yaklog。
	base := yaklog.New(yaklog.Options{
		Out:   os.Stdout,
		Level: yaklog.Debug,
	})
	adapter.SetDefault(base)

	slog.Info("via slog.Info", "key", "value")
	slog.Warn("via slog.Warn", "threshold", 100)
	slog.Debug("via slog.Debug", "detail", "extra")

	// ── 2. InfoContext / WithTrace：ctx 字段透传 ─────────────────────────────
	//
	// slog.InfoContext 会将 ctx 传给 yaklog adapter 的 Handle(ctx, r)，
	// 从而自动提取 WithTrace / WithField 注入的字段。
	var traceID [16]byte
	copy(traceID[:], "slog-trace-00001")
	ctx := yaklog.WithTrace(context.Background(), traceID)
	ctx = yaklog.WithField(ctx, "tenant", "acme-corp")

	slog.InfoContext(ctx, "带 TraceID 的请求", "endpoint", "/api/pay")

	// ── 3. WithAttrs：slog 预注入固定字段 ───────────────────────────────────
	//
	// h.With(...)  →  adapter.WithAttrs  →  yaklog.Label（共享 level，零分配前缀）
	h := slog.New(adapter.NewHandler(base))
	serviceLogger := h.With("service", "payment", "version", "v3")

	serviceLogger.Info("服务启动")
	serviceLogger.Warn("余额不足", "user_id", 10086, "balance", 0.5)

	// ── 4. 接管 log 标准库（无结构日志遗留代码迁移） ─────────────────────────
	//
	// 将 log.SetOutput 重定向到 yaklog，使老代码的 log.Printf 以 Info 打出。
	log.SetOutput(adapter.ToStdLogWriter(base, slog.LevelInfo))
	log.SetFlags(0) // 去掉 log 包自带的时间前缀（yaklog 自己添加）
	log.Print("来自 log 标准库的遗留日志")

	// ── 5. 不同类型字段的精确路由 ────────────────────────────────────────────
	//
	// adapter 按 slog.Value.Kind() 路由，避免所有值被序列化为字符串。
	slog.Info("全类型字段",
		slog.Int64("i64", -99),
		slog.Uint64("u64", 200),
		slog.Float64("f64", 3.14159),
		slog.Bool("flag", true),
	)
}
