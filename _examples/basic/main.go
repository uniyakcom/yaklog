// basic 演示 yaklog 核心功能：JSON 格式输出到 stderr，覆盖所有字段类型。
//
// 运行：
//
//	cd _examples && go run ./basic
package main

import (
	"errors"
	"time"

	"github.com/uniyakcom/yaklog"
)

func main() {
	// ── 1. 最简创建：零值 Options，JSON → os.Stderr ───────────────────────
	log := yaklog.New(yaklog.Options{
		Level: yaklog.Debug,
	})

	log.Info().Msg("yaklog 启动").Send()

	// ── 2. 全字段类型演示 ────────────────────────────────────────────────────
	log.Debug().
		Str("service", "auth").
		Int("port", 8080).
		Int64("snowflake_id", 1_741_426_200_000_123).
		Uint64("request_count", 9_999_999).
		Float64("latency_ms", 1.234).
		Bool("tls", true).
		Time("started_at", time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)).
		Dur("uptime", 72*time.Hour).
		Msg("服务状态").Send()

	// ── 3. 错误字段 ──────────────────────────────────────────────────────────
	err := errors.New("connection refused")
	log.Error().
		Str("target", "db.internal:5432").
		Err(err).
		Msg("数据库连接失败").Send()

	// ── 4. Label 固定字段（派生子 Logger，共享 level） ───────────────────────
	dbLog := log.Labels().
		Str("module", "db").
		Str("db", "postgres").
		Build()

	dbLog.Info().Str("query", "SELECT 1").Dur("elapsed", 3*time.Millisecond).Msg("心跳查询").Send()

	// ── 5. Fork 独立 Logger（独立 level，不受父级 SetLevel 影响） ──────────
	auditLog := log.Fork()
	auditLog.SetLevel(yaklog.Info) // audit 独立控制，保持 Info+
	log.SetLevel(yaklog.Warn)      // 主 Logger 静默 Debug/Info

	log.Debug().Msg("（此条不会输出，主 Logger 已设为 Warn）").Send()
	auditLog.Info().Str("user", "alice").Str("action", "login").Msg("审计事件").Send()
	log.Warn().Msg("主 Logger Warn 仍然输出").Send()

	// ── 6. Post 异步写入 + Wait 排空 ────────────────────────────────────────
	for i := range 3 {
		log.Warn().Int("i", i).Msg("异步批量日志").Post()
	}
	yaklog.Wait() // 确保进程退出前所有 Post 落盘

	// ── 7. 级别未启用路径：nil-safe，零分配 ─────────────────────────────────
	log.Debug().Str("key", "这行不会分配任何内存").Msg("不会输出").Send()
}
