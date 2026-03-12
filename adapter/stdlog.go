package adapter

import (
	"bytes"
	"log/slog"

	"github.com/uniyakcom/yaklog"
)

// stdLogWriter 将 [io.Writer] 实现与 *yaklog.Logger 连接，
// 每次写入的内容作为一条日志消息转发给 Logger。
// 通常用于传递给 [log.SetOutput]。
type stdLogWriter struct {
	l     *yaklog.Logger
	level slog.Level
}

// ToStdLogWriter 以 *yaklog.Logger 和日志级别 level 创建一个 [io.Writer]。
//
// 每次调用 Write(p) 时，会去掉尾部的换行符，
// 然后以对应级别将内容作为消息发送给 l（同步写入）。
// 若 l 在该 level 未启用，Write 直接返回（零开销）。
//
// 典型用法：
//
//	log.SetOutput(bridge.ToStdLogWriter(logger, slog.LevelInfo))
//	log.SetFlags(0)
func ToStdLogWriter(l *yaklog.Logger, level slog.Level) *stdLogWriter {
	return &stdLogWriter{l: l, level: level}
}

// Write 实现 [io.Writer]。
// 将 p（去掉尾部换行符后）作为一条日志消息发送给底层 Logger（同步）。
// 始终返回 len(p), nil，避免触发 log 包的异常重试。
func (w *stdLogWriter) Write(p []byte) (int, error) {
	n := len(p)
	yakLvl := slogToYakLevel(w.level)
	// 去掉尾部 `\n`（以及可能的 `\r\n`）
	msg := bytes.TrimRight(p, "\r\n")

	e := w.l.Event(yakLvl)
	if e == nil {
		return n, nil
	}
	e.Msg(string(msg)).Send()
	return n, nil
}
