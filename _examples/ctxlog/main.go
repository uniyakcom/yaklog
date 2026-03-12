// ctxlog 演示 context 集成：TraceID 注入、WithField 任意字段、Logger 跨层传递。
//
// 运行：
//
//	cd _examples && go run ./ctxlog
package main

import (
	"context"
	"os"

	"github.com/uniyakcom/yaklog"
)

// ── 模拟 HTTP 中间件 ──────────────────────────────────────────────────────────

// middleware 模拟请求入口：向 ctx 注入 TraceID + 请求字段，绑定子 Logger。
func middleware(ctx context.Context, log *yaklog.Logger, method, path string) context.Context {
	// 1. 注入 TraceID（128-bit，通常来自分布式追踪系统）
	var traceID [16]byte
	copy(traceID[:], "trace-abc-00001\x00")
	ctx = yaklog.WithTrace(ctx, traceID)

	// 2. 注入任意请求字段
	ctx = yaklog.WithField(ctx, "method", method)
	ctx = yaklog.WithField(ctx, "path", path)

	// 3. 将绑定了 ctx 的子 Logger 写入 context（下游无需手动传递 log 实例）
	reqLog := log.Context(ctx)
	ctx = yaklog.WithLogger(ctx, reqLog)

	reqLog.Info().Msg("请求开始").Send()
	return ctx
}

// handleUser 模拟业务层：通过 FromCtx 取出 Logger，无需参数传递。
func handleUser(ctx context.Context, userID int) {
	log := yaklog.FromCtx(ctx) // 取出 middleware 绑定的 Logger（含 trace_id + method + path）

	log.Debug().
		Int("user_id", userID).
		Str("action", "fetch_profile").
		Msg("查询用户资料").Send()

	// 追加本层字段（不影响上层 Logger）
	userLog := log.Label("user_id", userID)
	userLog.Info().Str("cache", "hit").Msg("缓存命中").Send()
}

// ── 演示多 ctx 字段覆盖与合并 ────────────────────────────────────────────────

func demoCtxMerge() {
	log := yaklog.New(yaklog.Options{
		Out:   yaklog.Console(os.Stdout),
		Level: yaklog.Debug,
	})

	// 父 ctx：注入 request_id
	ctx := yaklog.WithField(context.Background(), "request_id", "req-001")

	// 子 ctx：追加 span_id（不覆盖 request_id）
	ctx = yaklog.WithField(ctx, "span_id", "span-999")

	// 再次注入同 key（覆盖：context 链语义，names 列表不增长）
	ctx = yaklog.WithField(ctx, "request_id", "req-001-retry")

	log.Context(ctx).Info().Msg("ctx 合并：request_id 被覆盖，span_id 追加").Send()
}

func main() {
	log := yaklog.New(yaklog.Options{
		Out:   os.Stdout,
		Level: yaklog.Debug,
	})

	// ── 场景 1：中间件注入 + 业务层取用 ─────────────────────────────────────
	ctx := context.Background()
	ctx = middleware(ctx, log, "GET", "/api/users/42")
	handleUser(ctx, 42)

	// ── 场景 2：Event 级 Ctx() 覆盖（覆盖 Logger 的 boundCtx） ─────────────
	var traceID2 [16]byte
	copy(traceID2[:], "trace-xyz-99999\x00")
	overrideCtx := yaklog.WithTrace(context.Background(), traceID2)

	boundLog := log.Context(ctx) // 绑定场景 1 的 ctx（含 trace-abc-00001）
	boundLog.Info().
		Ctx(overrideCtx). // 本条日志临时使用 traceID2，不影响 boundLog 本身
		Str("note", "本条 trace_id 被 Ctx() 覆盖为 trace-xyz-99999").
		Msg("Ctx() 覆盖演示").Send()

	boundLog.Info().Msg("此条恢复 boundCtx 中的 trace-abc-00001").Send()

	// ── 场景 3：ctx 字段合并与覆盖 ──────────────────────────────────────────
	demoCtxMerge()
}
