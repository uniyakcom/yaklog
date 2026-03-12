package yaklog

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/uniyakcom/yaklog/rotation"
)

// ── openSave：路径校验 ────────────────────────────────────────────────────────

// TestOpenSave_EmptyPath 验证空字符串路径返回 ErrInvalidPath。
func TestOpenSave_EmptyPath(t *testing.T) {
	_, err := openSave("", 100, 0, 0, false, false)
	if !errors.Is(err, ErrInvalidPath) {
		t.Errorf("空路径应返回 ErrInvalidPath，got %v", err)
	}
}

// TestOpenSave_AbsPath 验证绝对路径能正常创建写入器。
func TestOpenSave_AbsPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abs.log")
	wc, err := openSave(path, 100, 0, 0, false, false)
	if err != nil {
		t.Fatalf("绝对路径应成功，got %v", err)
	}
	_ = wc.Close()
}

// TestOpenSave_RelPath 验证相对路径被自动转换为绝对路径（不再拒绝）。
func TestOpenSave_RelPath(t *testing.T) {
	// 注意：相对路径依赖当前工作目录（测试时为包目录）。
	// 使用 t.TempDir 无法直接得到相对路径，故在 os.TempDir 下手动构造。
	tmpBase := t.TempDir()
	// 获取 tmpBase 相对于当前工作目录的相对路径
	cwd, err := os.Getwd()
	if err != nil {
		t.Skip("无法获取工作目录，跳过相对路径测试")
	}
	rel, err := filepath.Rel(cwd, tmpBase)
	if err != nil || strings.HasPrefix(rel, "..") {
		// 无法构造有意义的相对路径（跨驱动器或上级目录），跳过
		t.Skip("无法构造相对路径，跳过测试")
	}
	relPath := filepath.Join(rel, "rel.log")
	wc, err := openSave(relPath, 100, 0, 0, false, false)
	if err != nil {
		t.Fatalf("相对路径应被自动解析为绝对路径，got %v", err)
	}
	_ = wc.Close()
	// 验证文件确实创建在正确位置
	absPath := filepath.Join(tmpBase, "rel.log")
	if _, statErr := os.Stat(absPath); statErr != nil {
		t.Errorf("期望文件存在于 %s，但 stat 失败: %v", absPath, statErr)
	}
}

// TestOpenSave_DotDotPath 验证包含 ".." 的路径被 Clean 消解，不能逃逸至意外目录。
func TestOpenSave_DotDotPath(t *testing.T) {
	dir := t.TempDir()
	// 构造形如 /tmp/xxx/sub/../app.log 的路径，经 Clean 消解后仍在 dir 内
	path := filepath.Join(dir, "sub", "..", "dotdot.log")
	wc, err := openSave(path, 100, 0, 0, false, false)
	if err != nil {
		t.Fatalf("含 '..' 的路径应被 Clean 消解后正常打开，got %v", err)
	}
	_ = wc.Close()
	// 最终文件应在 dir 下
	expected := filepath.Join(dir, "dotdot.log")
	if _, statErr := os.Stat(expected); statErr != nil {
		t.Errorf("期望文件存在于 %s，但 stat 失败: %v", expected, statErr)
	}
}

// ── checkExistingFile：普通文件检测 ──────────────────────────────────────────

// TestCheckExistingFile_NormalFile 验证普通文本文件（无可执行位）通过检测。
func TestCheckExistingFile_NormalFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "normal*.log")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("INFO starting\n")
	_ = f.Close()

	// 确保无可执行位
	_ = os.Chmod(f.Name(), 0600)

	info, err := os.Stat(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := checkExistingFile(f.Name(), info); err != nil {
		t.Errorf("普通日志文件应通过检测，got %v", err)
	}
}

// TestCheckExistingFile_Directory 验证目录返回 ErrNotLogFile。
func TestCheckExistingFile_Directory(t *testing.T) {
	dir := t.TempDir()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkExistingFile(dir, info); !errors.Is(err, ErrNotLogFile) {
		t.Errorf("目录应返回 ErrNotLogFile，got %v", err)
	}
}

// TestCheckExistingFile_ExecutableBit 验证含可执行权限位的文件返回 ErrNotLogFile。
func TestCheckExistingFile_ExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 可执行权限位，chmod +x 无效")
	}
	f, err := os.CreateTemp(t.TempDir(), "exec*.bin")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	_ = os.Chmod(f.Name(), 0755)

	info, err := os.Stat(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := checkExistingFile(f.Name(), info); !errors.Is(err, ErrNotLogFile) {
		t.Errorf("可执行文件应返回 ErrNotLogFile，got %v", err)
	}
}

// ── isBinaryMagic ─────────────────────────────────────────────────────────────

// TestIsBinaryMagic_ELF 验证 ELF magic 命中。
func TestIsBinaryMagic_ELF(t *testing.T) {
	path := writeTempFile(t, []byte{0x7f, 'E', 'L', 'F', 0x00})
	if !isBinaryMagic(path) {
		t.Error("ELF magic 应被识别为二进制")
	}
}

// TestIsBinaryMagic_PE 验证 PE（Windows 可执行）magic 命中。
func TestIsBinaryMagic_PE(t *testing.T) {
	path := writeTempFile(t, []byte{'M', 'Z', 0x00, 0x00})
	if !isBinaryMagic(path) {
		t.Error("PE magic 应被识别为二进制")
	}
}

// TestIsBinaryMagic_MachO_FECF 验证 Mach-O 64-bit BE magic 命中。
func TestIsBinaryMagic_MachO_FECF(t *testing.T) {
	path := writeTempFile(t, []byte{0xfe, 0xed, 0xfa, 0xcf})
	if !isBinaryMagic(path) {
		t.Error("Mach-O 64-bit BE magic 应被识别为二进制")
	}
}

// TestIsBinaryMagic_MachO_CFFE 验证 Mach-O 64-bit LE magic 命中。
func TestIsBinaryMagic_MachO_CFFE(t *testing.T) {
	path := writeTempFile(t, []byte{0xcf, 0xfa, 0xed, 0xfe})
	if !isBinaryMagic(path) {
		t.Error("Mach-O 64-bit LE magic 应被识别为二进制")
	}
}

// TestIsBinaryMagic_PlainText 验证普通文本文件不触发 magic 检测。
func TestIsBinaryMagic_PlainText(t *testing.T) {
	path := writeTempFile(t, []byte("INFO 2026-01-01T00:00:00Z msg=\"hello\"\n"))
	if isBinaryMagic(path) {
		t.Error("纯文本日志不应被识别为二进制")
	}
}

// TestIsBinaryMagic_ShortFile 验证不足 2 字节的文件不触发 magic 检测。
func TestIsBinaryMagic_ShortFile(t *testing.T) {
	path := writeTempFile(t, []byte{0x7f})
	if isBinaryMagic(path) {
		t.Error("单字节文件不应被识别为二进制")
	}
}

// TestIsBinaryMagic_EmptyFile 验证空文件不触发 magic 检测。
func TestIsBinaryMagic_EmptyFile(t *testing.T) {
	path := writeTempFile(t, []byte{})
	if isBinaryMagic(path) {
		t.Error("空文件不应被识别为二进制")
	}
}

// TestIsBinaryMagic_NonExistent 验证不存在的文件返回 false（保守不拦截）。
func TestIsBinaryMagic_NonExistent(t *testing.T) {
	if isBinaryMagic("/nonexistent/path/file.bin") {
		t.Error("不存在的文件应返回 false")
	}
}

// ── openSave：已存在文件的安全拦截 ───────────────────────────────────────────

// TestOpenSave_ExistingBinaryFile 验证已存在的 ELF 二进制文件被 openSave 拒绝。
func TestOpenSave_ExistingBinaryFile(t *testing.T) {
	// 写入 ELF magic
	path := writeTempFile(t, []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00})
	// chmod 无可执行位，使其仅靠 magic 触发
	_ = os.Chmod(path, 0600)

	_, err := openSave(path, 100, 0, 0, false, false)
	if !errors.Is(err, ErrNotLogFile) {
		t.Errorf("ELF 文件应返回 ErrNotLogFile，got %v", err)
	}
}

// TestOpenSave_ExistingExecutableFile 验证已存在的带可执行位文件被 openSave 拒绝。
func TestOpenSave_ExistingExecutableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持 Unix 可执行权限位，chmod +x 无效")
	}
	path := writeTempFile(t, []byte("#!/bin/sh\necho hello\n"))
	_ = os.Chmod(path, 0755)

	_, err := openSave(path, 100, 0, 0, false, false)
	if !errors.Is(err, ErrNotLogFile) {
		t.Errorf("可执行 shell 脚本应返回 ErrNotLogFile，got %v", err)
	}
}

// TestOpenSave_ExistingLogFile 验证已存在的合法日志文件可被续写。
func TestOpenSave_ExistingLogFile(t *testing.T) {
	path := writeTempFile(t, []byte("INFO starting\n"))
	_ = os.Chmod(path, 0600)

	wc, err := openSave(path, 100, 0, 0, false, false)
	if err != nil {
		t.Fatalf("已存在的合法日志文件应可续写，got %v", err)
	}
	_ = wc.Close()
}

// ── New()：FilePath 相对路径端到端 ───────────────────────────────────────────

// TestNew_FilePath_Relative 验证通过 Options.FilePath 传入相对路径时，
// Logger 能正常创建并写入日志（端到端）。
func TestNew_FilePath_Relative(t *testing.T) {
	tmpBase := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Skip("无法获取工作目录")
	}
	rel, err := filepath.Rel(cwd, tmpBase)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Skip("无法构造相对路径，跳过测试")
	}

	logPath := filepath.Join(rel, "e2e.log")
	l := New(Options{
		Level:    Info,
		FilePath: logPath,
	})
	l.Info().Str("test", "relative-path").Msg("e2e ok").Send()
	drainLogger(l)
	if c := l.Closer(); c != nil {
		_ = c.Close()
	}

	absLog := filepath.Join(tmpBase, "e2e.log")
	raw, readErr := os.ReadFile(absLog)
	if readErr != nil {
		t.Fatalf("期望日志文件存在于 %s，但读取失败: %v", absLog, readErr)
	}
	if !strings.Contains(string(raw), "e2e ok") {
		t.Errorf("日志内容缺失「e2e ok」，got: %s", raw)
	}
}

// TestOpenSave_SymlinkRejected 验证符号链接目标被拒绝，避免日志写入非预期位置。
func TestOpenSave_SymlinkRejected(t *testing.T) {
	target := writeTempFile(t, []byte("INFO starting\n"))
	link := filepath.Join(t.TempDir(), "link.log")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("当前平台不支持 symlink: %v", err)
	}

	_, err := openSave(link, 100, 0, 0, false, false)
	if !errors.Is(err, rotation.ErrSymlinkDetected) {
		t.Fatalf("符号链接应返回 ErrSymlinkDetected, got %v", err)
	}
}

// ── 辅助 ─────────────────────────────────────────────────────────────────────

// ── RotatingWriter 并发安全 ─────────────────────────────────────────────────

// TestSave_Concurrent 验证多 goroutine 并发写入 RotatingWriter 无 panic 和 data race。
func TestSave_Concurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "concurrent.log")

	// maxSize=1 MiB，触发少量轮转以覆盖 rotateLocked 并发路径
	wc, err := openSave(path, 1, 5, 0, false, false)
	if err != nil {
		t.Fatalf("openSave: %v", err)
	}
	t.Cleanup(func() { _ = wc.Close() })

	const goroutines = 8
	const writesEach = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	line := []byte("{\"level\":\"INFO\",\"msg\":\"concurrent\"}\n")
	for range goroutines {
		go func() {
			defer wg.Done()
			for range writesEach {
				_, _ = wc.Write(line)
			}
		}()
	}
	wg.Wait()
}

// writeTempFile 在临时目录创建含指定内容的文件，返回其绝对路径。
func writeTempFile(t *testing.T, content []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "yaklog_test_*")
	if err != nil {
		t.Fatal(err)
	}
	if len(content) > 0 {
		if _, err := f.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	_ = f.Close()
	return f.Name()
}
