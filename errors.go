package yaklog

import "github.com/uniyakcom/yakutil"

// ─── 包级错误常量 ────────────────────────────────────────────────────────────

const (
	// ErrWriterClosed 包级 worker 已关闭或已执行 Shutdown，不再接受新 Post 任务。
	ErrWriterClosed = yakutil.ErrStr("yaklog: worker closed")

	// ErrInvalidPath 日志文件路径不合法（如空字符串或无法解析为绝对路径）。
	ErrInvalidPath = yakutil.ErrStr("yaklog: invalid file path")

	// ErrNotLogFile 目标路径指向的已有文件疑似二进制/可执行文件，拒绝覆盖写入。
	// 触发条件：文件已存在且（含可执行权限位 0111，或前缀匹配已知二进制 magic）。
	ErrNotLogFile = yakutil.ErrStr("yaklog: target file is not a log file")

	// ErrInvalidOpts 配置参数不合法（如队列容量超限）。
	ErrInvalidOpts = yakutil.ErrStr("yaklog: invalid options")
)
