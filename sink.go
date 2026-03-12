package yaklog

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"

	"github.com/uniyakcom/yaklog/rotation"
)

// ─── consoleSink：彩色文本输出 ────────────────────────────────────────────────

// consoleSink 是 Console() 返回的内部类型。
// Logger 在 New() 时检测到此类型，自动选择 textEncoder（无需 JSON → Text 解析）。
type consoleSink struct {
	w io.Writer
}

// Write 实现 io.Writer，直接透传给底层 writer。
func (c *consoleSink) Write(p []byte) (int, error) {
	return c.w.Write(p)
}

// Console 创建彩色文本 Sink，输出到指定 writer（默认 os.Stderr）。
//
// Logger 检测到此 Sink 后自动使用 textEncoder，热路径零分配无需 JSON 解析。
// ConsoleTimeFormat 由 Options.ConsoleTimeFormat 控制（零值使用 ConsoleTimeMilli）。
func Console(w ...io.Writer) io.Writer {
	out := io.Writer(os.Stderr)
	if len(w) > 0 && w[0] != nil {
		out = w[0]
	}
	return &consoleSink{w: out}
}

// ─── Save：JSON 文件轮转输出 ──────────────────────────────────────────────────

// Save 创建基于文件的 JSON Sink，支持按大小自动轮转。
//
// 使用形式：
//   - Save("./logs/app.log")        — 显式指定路径
//   - Save()                        — 使用 Options.FilePath（在 New 时解析）
//
// 轮转参数（MaxSize/MaxAge/MaxBackups/Compress/LocalTime）来自 Logger 的 Options。
// 返回的 io.Writer 同时实现 io.Closer；调用方可通过 Logger.Closer() 取回并在退出时关闭。
func Save(path ...string) io.Writer {
	if len(path) == 0 || path[0] == "" {
		// 占位符：路径将在 New() 中从 Options.FilePath 填入
		return &lazySave{}
	}
	return &lazySave{path: path[0]}
}

// lazySave 是 Save() 返回的占位类型。
// New() 接收到 Options 后，若检测到此类型，会调用 openSave 展开为真实写入器。
type lazySave struct {
	path string // 空 = 使用 Options.FilePath
}

// Write 永远不应被直接调用；展开后由 rotation.RotatingWriter 接管。
func (l *lazySave) Write(p []byte) (int, error) { return 0, ErrInvalidOpts }

// openSave 根据路径和选项创建 rotation.RotatingWriter。
// dir 和 filename 从路径拆解，其余参数来自 Options。
//
// 路径处理：
//   - 相对路径在调用时刻通过 filepath.Abs 锁定为绝对路径（使用进程当前工作目录），
//     不会随后续工作目录变动而漂移。
//   - 空字符串或 Abs 失败时返回 ErrInvalidPath。
//
// 安全检测（仅当目标文件已存在时执行）：
//  1. 必须是普通文件（非目录、设备、socket 等），否则返回 ErrNotLogFile。
//  2. 若文件带有可执行权限位（mode & 0111 != 0），返回 ErrNotLogFile。
//  3. 读取前 4 字节检查已知二进制 magic（ELF、Mach-O、PE），命中则返回 ErrNotLogFile。
//  4. 若目标是符号链接，直接拒绝，避免日志被导向非预期位置。
func openSave(path string, maxSizeMB, maxBackups, maxAge int, compress, localTime bool) (io.WriteCloser, error) {
	if path == "" {
		return nil, ErrInvalidPath
	}
	// 将相对路径在此刻（配置时刻）锁定为绝对路径，避免工作目录漂移。
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrInvalidPath
	}
	// Clean 消解多余分隔符和 ".."，防止路径穿越。
	clean := filepath.Clean(abs)
	if !filepath.IsAbs(clean) {
		// filepath.Abs 本应保证绝对路径，此处为防御性断言。
		return nil, ErrInvalidPath
	}
	// ── 目标文件已存在时的安全检测 ──────────────────────────────────────────
	if info, statErr := os.Lstat(clean); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, rotation.ErrSymlinkDetected
		}
		if err := checkExistingFile(clean, info); err != nil {
			return nil, err
		}
	}

	dir := filepath.Dir(clean)
	base := filepath.Base(clean)
	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]
	if ext == "" {
		ext = ".log"
	}

	rw, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename(name),
		rotation.WithExt(ext),
		rotation.WithMaxSize(int64(maxSizeMB)*1024*1024),
		rotation.WithMaxBackups(maxBackups),
		rotation.WithMaxAge(maxAge),
		rotation.WithCompress(compress),
		rotation.WithLocalTime(localTime),
	)
	if err != nil {
		return nil, err
	}
	return rw, nil
}

// checkExistingFile 对已存在的文件执行安全检测，确认其适合作为日志文件写入目标。
// info 由 os.Stat(path) 返回。
//
// 安全注意（TOCTOU）：此函数与轮转库实际打开文件之间存在经典的
// “检查时刻/使用时刻”（TOCTOU）竞争窗口：攻击者可在查询与打开之间替换文件。
// 该风险在典型日志配置场景（属于原调方信任边界）中级别低；
// 如属安全敏感环境，应将日志目录以严格文件系统权限保护（第三方无写权限）。
func checkExistingFile(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return rotation.ErrSymlinkDetected
	}
	// 1. 必须是普通文件。
	if !info.Mode().IsRegular() {
		return ErrNotLogFile
	}
	// 2. 含可执行权限位 → 疑似二进制/脚本，拒绝。
	if info.Mode().Perm()&0111 != 0 {
		return ErrNotLogFile
	}
	// 3. 检查已知二进制 magic（前 4 字节）。
	if isBinaryMagic(path) {
		return ErrNotLogFile
	}
	return nil
}

// isBinaryMagic 读取文件前 4 字节，检查是否为已知可执行/二进制格式的 magic number。
// 无法读取（空文件、权限不足等）时保守返回 false（不拦截）。
//
// 支持的 magic：
//   - ELF:              0x7f 'E' 'L' 'F'
//   - Mach-O 32-bit BE: 0xfe 0xed 0xfa 0xce
//   - Mach-O 32-bit LE: 0xce 0xfa 0xed 0xfe
//   - Mach-O 64-bit BE: 0xfe 0xed 0xfa 0xcf
//   - Mach-O 64-bit LE: 0xcf 0xfa 0xed 0xfe
//   - Mach-O fat:       0xca 0xfe 0xba 0xbe
//   - PE (Windows):     'M' 'Z'
func isBinaryMagic(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck

	var buf [4]byte
	n, err := f.Read(buf[:])
	if err != nil || n < 2 {
		return false
	}
	// PE: 仅需前 2 字节。
	if buf[0] == 'M' && buf[1] == 'Z' {
		return true
	}
	if n < 4 {
		return false
	}
	magic := binary.BigEndian.Uint32(buf[:])
	switch magic {
	case 0x7f454c46, // ELF
		0xfeedface, // Mach-O 32-bit BE
		0xcefaedfe, // Mach-O 32-bit LE
		0xfeedfacf, // Mach-O 64-bit BE
		0xcffaedfe, // Mach-O 64-bit LE
		0xcafebabe: // Mach-O fat / Java class（同值）
		return true
	}
	return false
}

// ─── Discard：丢弃所有输出 ────────────────────────────────────────────────────

// Discard 返回丢弃所有输出的 Sink，通常用于测试场景。
func Discard() io.Writer {
	return io.Discard
}
