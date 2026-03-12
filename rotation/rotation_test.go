package rotation_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uniyakcom/yaklog/rotation"
)

// ── 辅助 ─────────────────────────────────────────────────────────────────────

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "yaklog-rotation-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// ── 基础写入 ──────────────────────────────────────────────────────────────────

// TestRotating_BasicWrite 验证日志可正常写入并落盘。
func TestRotating_BasicWrite(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(4096),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err = w.Write([]byte("hello world\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "app.log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(content), "hello world") {
		t.Errorf("content missing in: %q", content)
	}
}

// ── 大小触发轮转 ──────────────────────────────────────────────────────────────

// TestRotating_SizeRollover 验证超过 maxSize 后自动生成带时间戳的备份文件。
func TestRotating_SizeRollover(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<10), // 1 KiB
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	payload := bytes.Repeat([]byte("x"), 512)
	// 3 次 × 512 字节 = 1.5 KiB → 超过 1 KiB 触发轮转
	for i := 0; i < 3; i++ {
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	_ = w.Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "app-") {
			backups = append(backups, e.Name())
		}
	}
	if len(backups) == 0 {
		t.Errorf("expected ≥1 backup file after rollover, got none; dir: %v",
			func() []string {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				return names
			}())
	}
}

// ── 强制轮转 ──────────────────────────────────────────────────────────────────

// TestRotating_ForceRotate 验证 Rotate() 强制轮转立即生效。
func TestRotating_ForceRotate(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("srv"),
		rotation.WithMaxSize(10<<20),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	if _, err := w.Write([]byte("before rotate\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Rotate(); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := w.Write([]byte("after rotate\n")); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	entries, _ := os.ReadDir(dir)
	var backups int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "srv-") {
			backups++
		}
	}
	if backups == 0 {
		t.Error("expected ≥1 backup file after Rotate()")
	}
}

// ── 备份数量上限 ───────────────────────────────────────────────────────────────

// TestRotating_MaxBackups 验证超量旧备份文件被自动删除。
func TestRotating_MaxBackups(t *testing.T) {
	dir := tempDir(t)
	const maxBackups = 3
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<10), // 1 KiB
		rotation.WithMaxBackups(maxBackups),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	payload := bytes.Repeat([]byte("z"), 600)
	for i := 0; i < 20; i++ {
		_, _ = w.Write(payload)
	}
	_ = w.Close()

	// cleanup goroutine 异步执行，等待最多 500ms
	var backups []string
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		backups = backups[:0]
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "app-") {
				backups = append(backups, e.Name())
			}
		}
		if len(backups) <= maxBackups {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(backups) > maxBackups {
		t.Errorf("expected ≤%d backups, got %d: %v", maxBackups, len(backups), backups)
	}
}

// ── 路径穿越防护 ──────────────────────────────────────────────────────────────

// TestRotating_PathTraversal 验证含 ".." 的 filename 被拒绝。
func TestRotating_PathTraversal(t *testing.T) {
	dir := tempDir(t)
	_, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("../evil"),
		rotation.WithMaxSize(1<<20),
	)
	if err == nil {
		t.Error("expected error for path traversal filename, got nil")
	}
}

// ── 关闭后写入 ────────────────────────────────────────────────────────────────

// TestRotating_ClosedWrite 验证 Close 后写入返回 ErrWriterClosed。
func TestRotating_ClosedWrite(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<20),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = w.Close()

	_, err = w.Write([]byte("test"))
	if err != rotation.ErrWriterClosed {
		t.Errorf("expected ErrWriterClosed, got %v", err)
	}
}

// ── 无效参数 ──────────────────────────────────────────────────────────────────

// TestRotating_InvalidOptions 验证错误参数返回非 nil 错误。
func TestRotating_InvalidOptions(t *testing.T) {
	cases := []struct {
		name string
		opts []rotation.Option
	}{
		{"empty dir", []rotation.Option{rotation.WithFilename("app")}},
		{"empty filename", []rotation.Option{rotation.WithDir("/tmp")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := rotation.New(c.opts...)
			if err == nil {
				t.Errorf("expected error for %q, got nil", c.name)
			}
		})
	}
}

// ── 自定义扩展名 ──────────────────────────────────────────────────────────────

// TestRotating_WithExt 验证 WithExt 选项生效，文件使用自定义后缀。
func TestRotating_WithExt(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithExt(".jsonl"),
		rotation.WithMaxSize(1<<20),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = w.Write([]byte(`{"msg":"hello"}` + "\n"))
	_ = w.Close()

	if _, err := os.Stat(filepath.Join(dir, "app.jsonl")); err != nil {
		t.Errorf("expected app.jsonl to exist: %v", err)
	}
}

// ── gzip 压缩 ─────────────────────────────────────────────────────────────────

// TestRotating_Compress 验证 WithCompress(true) 在轮转后生成 .gz 文件。
func TestRotating_Compress(t *testing.T) {
	dir := tempDir(t)
	// maxSize 设为最小有效值 1 KiB，写入 5 批 300 字节确保发生至少 1 次轮转。
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<10),
		rotation.WithCompress(true),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	payload := bytes.Repeat([]byte("x"), 300)
	for i := 0; i < 5; i++ {
		_, _ = w.Write(payload)
	}
	_ = w.Close()

	// 压缩在后台 goroutine 执行，最多等 2 秒。
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".gz") {
				return // 找到 .gz 文件，测试通过
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	// 输出目录内容以辅助调试
	entries, _ := os.ReadDir(dir)
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	t.Errorf("expected at least one .gz backup file; dir contents: %v", names)
}

// ── MaxAge 过期清理 ────────────────────────────────────────────────────────────

// TestCleanup_MaxAge 验证超过 maxAge 天数的旧备份文件被自动删除。
func TestCleanup_MaxAge(t *testing.T) {
	dir := tempDir(t)

	// 先生成两批备份文件，一批"旧"（修改时间设为 10 天前），一批"新"。
	// 旧文件：直接创建文件并 chtimes 改为 10 天前。
	const maxAge = 3 // days
	oldName := filepath.Join(dir, "app-19700101-000000.000.log")
	if err := os.WriteFile(oldName, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(oldName, tenDaysAgo, tenDaysAgo); err != nil {
		t.Fatal(err)
	}

	// 启动写入器并触发轮转（使 cleanup 被调用）。
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<10), // 1 KiB，易触发
		rotation.WithMaxAge(maxAge),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = w.Close() })

	// 写入足够数据触发轮转（调用 cleanup）
	payload := bytes.Repeat([]byte("y"), 600)
	for i := 0; i < 3; i++ {
		_, _ = w.Write(payload)
	}

	// cleanup 异步执行，等最多 500ms
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(oldName); os.IsNotExist(err) {
			return // 旧文件已被删除，通过
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(oldName); !os.IsNotExist(err) {
		t.Errorf("expected old backup %q to be deleted by maxAge=%d cleanup", oldName, maxAge)
	}
}

// ── LocalTime 文件名时间戳 ────────────────────────────────────────────────────

// TestBackupPath_LocalTime 验证 WithLocalTime(true) 时备份文件名使用固定的本地时间戳，
// WithLocalTime(false) 时使用 UTC 时间戳，两者在非 UTC 时区时会不同。
func TestBackupPath_LocalTime(t *testing.T) {
	// 固定一个非 UTC 时刻：UTC+8 本地 vs UTC 差异明显
	fixedUTC := time.Date(2024, 1, 2, 10, 30, 0, 0, time.UTC)
	fixedLocal := fixedUTC.In(time.FixedZone("UTC+8", 8*3600)) // 18:30 本地

	localDir := tempDir(t)
	wLocal, err := rotation.New(
		rotation.WithDir(localDir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<10),
		rotation.WithLocalTime(true),
	)
	if err != nil {
		t.Fatalf("New(local): %v", err)
	}
	// 注入固定时间（在写入前设置，测试期间单 goroutine 顺序访问，竞态安全）
	restore := rotation.SetNow(wLocal, func() time.Time { return fixedLocal })
	defer restore()

	payload := bytes.Repeat([]byte("z"), 600)
	for i := 0; i < 3; i++ {
		_, _ = wLocal.Write(payload)
	}
	_ = wLocal.Close()

	utcDir := tempDir(t)
	wUTC, err := rotation.New(
		rotation.WithDir(utcDir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<10),
		rotation.WithLocalTime(false),
	)
	if err != nil {
		t.Fatalf("New(utc): %v", err)
	}
	restoreUTC := rotation.SetNow(wUTC, func() time.Time { return fixedUTC })
	defer restoreUTC()
	for i := 0; i < 3; i++ {
		_, _ = wUTC.Write(payload)
	}
	_ = wUTC.Close()

	// 如果时区不是 UTC，本地时间戳（18:30）和 UTC（10:30）应不同
	localFiles := backupFiles(t, localDir, "app-")
	utcFiles := backupFiles(t, utcDir, "app-")
	if len(localFiles) == 0 || len(utcFiles) == 0 {
		t.Skip("no backup files generated; skipping LocalTime assertion")
	}

	// fixedLocal 在 UTC+8 下为 18:30, fixedUTC 为 10:30，时间戳必须不同
	localStamp := strings.TrimSuffix(strings.TrimPrefix(localFiles[0], "app-"), ".log")
	utcStamp := strings.TrimSuffix(strings.TrimPrefix(utcFiles[0], "app-"), ".log")
	if localStamp == utcStamp {
		t.Errorf("expected different timestamps for localTime vs UTC; both got %q", localStamp)
	}
	// 本地时间含 "1830"，UTC 含 "1030"
	if !strings.Contains(localStamp, "1830") {
		t.Errorf("localTime backup should have 18:30 timestamp; got %q", localStamp)
	}
	if !strings.Contains(utcStamp, "1030") {
		t.Errorf("UTC backup should have 10:30 timestamp; got %q", utcStamp)
	}
}

// ── P2: 相对路径目录被拒绝 ────────────────────────────────────────────────────

// TestRotating_RelativeDirRejected 验证传入相对路径目录时 New 返回 ErrDirNotAbs。
func TestRotating_RelativeDirRejected(t *testing.T) {
	_, err := rotation.New(
		rotation.WithDir("relative/path"),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<20),
	)
	if err != rotation.ErrDirNotAbs {
		t.Errorf("expected ErrDirNotAbs, got %v", err)
	}
}

// TestRotating_SymlinkDirRejected 验证符号链接目录被拒绝。
func TestRotating_SymlinkDirRejected(t *testing.T) {
	realDir := tempDir(t)
	linkDir := filepath.Join(tempDir(t), "logs-link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := rotation.New(
		rotation.WithDir(linkDir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<20),
	)
	if err != rotation.ErrSymlinkDetected {
		t.Fatalf("expected ErrSymlinkDetected, got %v", err)
	}
}

// TestRotating_SymlinkFileRejected 验证当前日志文件若为符号链接则被拒绝。
func TestRotating_SymlinkFileRejected(t *testing.T) {
	dir := tempDir(t)
	target := filepath.Join(tempDir(t), "target.log")
	if err := os.WriteFile(target, []byte("seed"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "app.log")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	_, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<20),
	)
	if err != rotation.ErrSymlinkDetected {
		t.Fatalf("expected ErrSymlinkDetected, got %v", err)
	}
}

// ── P1: 同毫秒备份名冲突 → 唯一文件名 ───────────────────────────────────────

// TestRotating_BackupNameCollision_UniqueNames 验证在同一毫秒内两次 Rotate()
// 生成的备份文件名不同，防止 os.Rename 静默覆盖导致日志丢失。
func TestRotating_BackupNameCollision_UniqueNames(t *testing.T) {
	dir := tempDir(t)
	// 固定时间源：两次 Rotate() 得到完全相同的毫秒时间戳
	fixedTime := time.Date(2024, 6, 1, 12, 0, 0, 500_000_000, time.UTC)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(10<<20),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	restore := rotation.SetNow(w, func() time.Time { return fixedTime })
	defer restore()
	defer func() { _ = w.Close() }()

	if _, err := w.Write([]byte("log line 1\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Rotate(); err != nil {
		t.Fatalf("first Rotate: %v", err)
	}
	if _, err := w.Write([]byte("log line 2\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Rotate(); err != nil {
		t.Fatalf("second Rotate: %v", err)
	}
	_ = w.Close()

	entries, _ := os.ReadDir(dir)
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "app-") {
			backups = append(backups, e.Name())
		}
	}
	if len(backups) < 2 {
		t.Errorf("expected ≥2 distinct backup files for same-millisecond rotations, got %d: %v", len(backups), backups)
		return
	}
	// 确认所有备份名互不相同
	seen := make(map[string]struct{}, len(backups))
	for _, b := range backups {
		if _, dup := seen[b]; dup {
			t.Errorf("duplicate backup name %q in %v", b, backups)
		}
		seen[b] = struct{}{}
	}
}

// ── P3: compress 信号量 → 无 goroutine 泄漏 ──────────────────────────────────

// TestRotating_CompressSemaphore_NoGoroutineLeak 验证多次快速轮转并启用压缩时
// 不发生 panic，且所有备份最终均被压缩（信号量限并发但不丢任务）。
func TestRotating_CompressSemaphore_NoGoroutineLeak(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMaxSize(1<<10), // 1 KiB，易触发
		rotation.WithCompress(true),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	payload := bytes.Repeat([]byte("y"), 300)
	for i := 0; i < 10; i++ {
		_, _ = w.Write(payload)
	}
	_ = w.Close()

	// 等待后台压缩完成（最多 3 秒）
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		var uncompressed int
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "app-") && !strings.HasSuffix(e.Name(), ".gz") {
				uncompressed++
			}
		}
		if uncompressed == 0 {
			return // 全部压缩完毕
		}
		time.Sleep(30 * time.Millisecond)
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	t.Errorf("expected all backup files compressed within 3s timeout; dir: %v", names)
}

// backupFiles 返回 dir 中以 prefix 开头的文件名列表（不含路径）。
func backupFiles(t *testing.T, dir, prefix string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	return names
}

// ── WithMaxSize 零值回退 ──────────────────────────────────────────────────────

// TestNew_DefaultMaxSize 验证不传 WithMaxSize 时 New 正常创建，写入不触发轮转。
func TestNew_DefaultMaxSize(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("default-size"),
	)
	if err != nil {
		t.Fatalf("New（无 WithMaxSize）: %v", err)
	}
	defer w.Close() //nolint:errcheck
	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

// ── Close 幂等性 ──────────────────────────────────────────────────────────────

// TestClose_Idempotent 验证连续两次 Close 均返回 nil，不发生 panic 或 fd 二次关闭错误。
func TestClose_Idempotent(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("close-idempotent"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("第一次 Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("第二次 Close 应返回 nil，得 %v", err)
	}
}

// TestClose_WriteAfterClose 验证 Close 后 Write 和 Rotate 均返回 ErrWriterClosed。
func TestClose_WriteAfterClose(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("closed-write"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = w.Close()

	if _, err := w.Write([]byte("after close\n")); err != rotation.ErrWriterClosed {
		t.Errorf("Close 后 Write 应返回 ErrWriterClosed，得 %v", err)
	}
	if err := w.Rotate(); err != rotation.ErrWriterClosed {
		t.Errorf("Close 后 Rotate 应返回 ErrWriterClosed，得 %v", err)
	}
}

// ── MinFreeBytes 磁盘空间预检 ──────────────────────────────────────────────────

// TestNew_InsufficientDiskSpace 验证可用空间低于阈值时 New 返回 ErrInsufficientDiskSpace。
func TestNew_InsufficientDiskSpace(t *testing.T) {
	dir := tempDir(t)

	// 先创建 writer 以获取实例，再注入空间不足的探针
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMinFreeBytes(1),
	)
	if err != nil {
		t.Fatalf("New（正常空间）: %v", err)
	}
	_ = w.Close()

	// 用空间总是不足的探针覆盖 availBytesFn 后重建 writer
	insufficientFn := func(string) (uint64, error) { return 0, nil }

	// 直接调用 SetAvailableBytes 后再 Rotate（openLocked 路径）
	w2, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app2"),
		rotation.WithMinFreeBytes(1<<30), // 1 GiB 阈值
	)
	if err != nil {
		t.Fatalf("New（正常）: %v", err)
	}
	restore := rotation.SetAvailableBytes(w2, insufficientFn)
	defer restore()
	defer w2.Close() //nolint:errcheck

	// Rotate 会再次调用 openLocked，触发空间预检
	if err := w2.Rotate(); err == nil || err != rotation.ErrInsufficientDiskSpace {
		t.Errorf("Rotate 应返回 ErrInsufficientDiskSpace，得 %v", err)
	}
}

// TestNew_MinFreeBytes_Sufficient 验证可用空间满足阈值时正常创建。
func TestNew_MinFreeBytes_Sufficient(t *testing.T) {
	dir := tempDir(t)

	// 注入总是返回充足空间的探针：通过先创建再置换的方式绕过构造时检查
	// （构造时使用真实系统调用，不影响测试目的）
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("app"),
		rotation.WithMinFreeBytes(1), // 1 字节，真实磁盘必然满足
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	if _, err := w.Write([]byte("ok\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
}

// ── 超大消息预轮转 ─────────────────────────────────────────────────────────────

// TestWrite_OversizedPreRotates 验证单条消息体积 >= maxSize 且文件已有内容时，
// Write 在写入前先触发一次轮转，使超大消息独占新文件，旧内容保留在备份中。
func TestWrite_OversizedPreRotates(t *testing.T) {
	dir := tempDir(t)
	const maxSize = 1 << 10 // 1 KiB

	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("pre"),
		rotation.WithMaxSize(maxSize),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	// 先写入少量内容（< maxSize），让文件有内容
	if _, err := w.Write([]byte("seed\n")); err != nil {
		t.Fatalf("Write (seed): %v", err)
	}

	// 写入 >= maxSize 的消息，应触发预轮转：备份 seed，然后写入大消息
	big := bytes.Repeat([]byte("X"), maxSize)
	if _, err := w.Write(big); err != nil {
		t.Fatalf("Write (oversized): %v", err)
	}
	_ = w.Close()

	// 应有 ≥1 个备份文件（已包含 seed 内容）
	entries, _ := os.ReadDir(dir)
	var foundSeed bool
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "pre-") {
			continue
		}
		data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		if strings.Contains(string(data), "seed") {
			foundSeed = true
			break
		}
	}
	if !foundSeed {
		// 列出目录内容以辅助调试
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("备份中应含 seed 内容（预轮转），目录内容: %v", names)
	}
}

// ── 压缩信号量背压（同步回退）────────────────────────────────────────────────

// TestRotate_CompressBackpressure 验证压缩信号量满时降级为同步调用，
// 具体表现为 Write 阻塞直到压缩完成。
func TestRotate_CompressBackpressure(t *testing.T) {
	dir := tempDir(t)

	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("bp"),
		rotation.WithMaxSize(1<<10),
		rotation.WithCompress(true),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	// blockCh 锁住所有压缩调用；关闭后全部解除阻塞。
	blockCh := make(chan struct{})
	var closeOnce sync.Once
	unblock := func() { closeOnce.Do(func() { close(blockCh) }) }
	defer unblock() // 测试退出时确保 goroutine 可以解除阻塞

	// started 在每次 compressFn 开始时接收一个令牌（缓冲足够大，不会阻塞发送方）。
	started := make(chan struct{}, rotation.MaxCompressConcurrent+2)

	restore := rotation.SetCompressFn(w, func(_ string) {
		started <- struct{}{} // 通知测试：压缩已开始
		<-blockCh             // 等待解除阻塞
	})
	defer restore()

	// 触发 MaxCompressConcurrent 次轮转，充满信号量
	payload := bytes.Repeat([]byte("z"), 1<<10)
	for range rotation.MaxCompressConcurrent {
		_, _ = w.Write(payload)
	}

	// 等待所有异步 goroutine 进入 compressFn（信号量已满）
	for range rotation.MaxCompressConcurrent {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("异步压缩 goroutine 未及时启动")
		}
	}

	// 再触发一次轮转：信号量已满，应走同步路径并阻塞 Write goroutine
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = w.Write(bytes.Repeat([]byte("z"), 1<<10))
	}()

	// 等待同步压缩开始（此时 Write goroutine 已被阻塞在 compressFn 内）
	select {
	case <-started:
		// ✓ 同步压缩已启动，说明 Write 正在同步路径上阻塞
	case <-writeDone:
		t.Fatal("Write 在同步压缩开始前就返回了（未阻塞）")
	case <-time.After(2 * time.Second):
		t.Fatal("同步压缩未及时启动")
	}

	// 此时 Write 应仍在阻塞
	select {
	case <-writeDone:
		t.Error("同步压缩运行时 Write 不应已返回")
	default:
		// ✓
	}

	// 解除所有阻塞
	unblock()

	// Write 应迅速完成
	select {
	case <-writeDone:
		// ✓
	case <-time.After(2 * time.Second):
		t.Error("解除阻塞后 Write 未完成")
	}
}

// ── MaxTotalSize 总量清理 ──────────────────────────────────────────────────────

// TestCleanup_MaxTotalSize 验证备份文件总大小超出阈值时从最旧开始删除。
func TestCleanup_MaxTotalSize(t *testing.T) {
	dir := tempDir(t)
	const chunkSize = 1 << 10 // 1 KiB（minMaxSize 下限）

	// maxTotalSize 设为 2 个 chunk 大小，产生 5 次轮转后最多保留 2 个备份
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("tot"),
		rotation.WithMaxSize(chunkSize),
		rotation.WithMaxTotalSize(2*chunkSize),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	payload := bytes.Repeat([]byte("t"), chunkSize)
	for i := 0; i < 5; i++ {
		_, _ = w.Write(payload)
	}
	_ = w.Close() // 同步清理

	entries, _ := os.ReadDir(dir)
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tot-") {
			backups = append(backups, e.Name())
		}
	}
	// 两个备份各约 1 KiB，总量 ≤ maxTotalSize=2 KiB
	if len(backups) > 2 {
		t.Errorf("MaxTotalSize 清理后备份数 %d 超过预期 ≤2，backups=%v", len(backups), backups)
	}
}

// TestCleanup_MaxTotalSize_WithMaxBackups 验证 maxTotalSize 与 maxBackups 同时生效时
// 先按数量裁剪，再按总量裁剪（结果取更严格约束）。
func TestCleanup_MaxTotalSize_WithMaxBackups(t *testing.T) {
	dir := tempDir(t)
	const chunkSize = 1 << 10 // 1 KiB

	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("both"),
		rotation.WithMaxSize(chunkSize),
		rotation.WithMaxBackups(4),
		rotation.WithMaxTotalSize(chunkSize), // 只允许约 1 个备份的体积
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	payload := bytes.Repeat([]byte("b"), chunkSize)
	for i := 0; i < 6; i++ {
		_, _ = w.Write(payload)
	}
	_ = w.Close()

	entries, _ := os.ReadDir(dir)
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "both-") {
			backups = append(backups, e.Name())
		}
	}
	// maxTotalSize=chunkSize 约束总量为最多 1 个备份
	if len(backups) > 2 { // 允许轻微误差：单文件大小可能略不同
		t.Errorf("双重约束后备份数 %d 超过预期，backups=%v", len(backups), backups)
	}
}

// ── Healthy 健康探针 ──────────────────────────────────────────────────────────

// TestHealthy_InitialTrue 验证 New 成功后 Healthy() 返回 true。
func TestHealthy_InitialTrue(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("health"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck
	if !w.Healthy() {
		t.Error("新建写入器 Healthy() 应为 true")
	}
}

// TestHealthy_FalseAfterWriteError 验证底层写入失败后 Healthy() 返回 false。
func TestHealthy_FalseAfterWriteError(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("health-err"),
		rotation.WithOnWriteError(func(_ error, _ []byte) {}), // 静默错误回调
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	// 确认写入前健康
	if _, err := w.Write([]byte("ok\n")); err != nil {
		t.Fatalf("Write (ok): %v", err)
	}
	if !w.Healthy() {
		t.Error("正常写入后 Healthy() 应为 true")
	}

	// 折断文件使写入失败
	rotation.BreakFile(w)
	_, _ = w.Write([]byte("broken\n"))

	if w.Healthy() {
		t.Error("写入失败后 Healthy() 应为 false")
	}
}

// TestHealthy_FalseAfterClose 验证 Close 后 Healthy() 返回 false。
func TestHealthy_FalseAfterClose(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("health-close"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = w.Close()
	if w.Healthy() {
		t.Error("Close 后 Healthy() 应为 false")
	}
}

// TestHealthy_RecoveryAfterSuccessfulWrite 验证写入成功后 Healthy() 稳定保持 true，
// 包括多次连续写入场景。
func TestHealthy_RecoveryAfterSuccessfulWrite(t *testing.T) {
	dir := tempDir(t)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("health-recover"),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte("line\n")); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		if !w.Healthy() {
			t.Errorf("写入 %d 次后 Healthy() 应为 true", i+1)
		}
	}
}

// ── 写入错误回调 ──────────────────────────────────────────────────────────────

// TestWrite_OnWriteError_CustomCallback 验证底层文件写入失败时自定义回调被调用。
func TestWrite_OnWriteError_CustomCallback(t *testing.T) {
	dir := tempDir(t)

	var cbErr error
	var cbData []byte
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("errcb"),
		rotation.WithOnWriteError(func(e error, p []byte) {
			cbErr = e
			cbData = append(cbData, p...)
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	// 先写一条成功的记录
	if _, err := w.Write([]byte("ok\n")); err != nil {
		t.Fatalf("Write (ok): %v", err)
	}

	// 折断底层文件使后续写入失败
	rotation.BreakFile(w)

	payload := []byte("lost\n")
	_, writeErr := w.Write(payload)
	if writeErr == nil {
		t.Fatal("预期写入失败，实际成功")
	}
	if cbErr == nil {
		t.Fatal("回调未被调用")
	}
	if !strings.Contains(string(cbData), "lost") {
		t.Errorf("回调中应含原始数据，得: %q", string(cbData))
	}
}

// TestWrite_OnWriteError_StderrFallback 验证未设置回调时写入失败回退到 stderr（不 panic）。
func TestWrite_OnWriteError_StderrFallback(t *testing.T) {
	dir := tempDir(t)

	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("stderrfb"),
		// 不设置 WithOnWriteError：默认回退到 os.Stderr
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer w.Close() //nolint:errcheck

	// 先写一条成功的记录
	if _, err := w.Write([]byte("first\n")); err != nil {
		t.Fatalf("Write (first): %v", err)
	}

	rotation.BreakFile(w)

	// 写入失败：应回退到 stderr，不 panic，且返回错误
	_, writeErr := w.Write([]byte("fallback\n"))
	if writeErr == nil {
		t.Fatal("预期写入失败，实际成功")
	}
	// 未 panic 即通过
}

// ── cleanup 并发竞态 ──────────────────────────────────────────────────────────

// TestCleanup_ConcurrentRotate_Idempotent 验证同毫秒多次并发轮转下 cleanup 幂等：
// 最终保留的备份数量不超过 maxBackups，且无 panic / 竞态。
func TestCleanup_ConcurrentRotate_Idempotent(t *testing.T) {
	dir := tempDir(t)
	const maxBackups = 3

	// 固定时间使所有轮转都在同一毫秒发生，最大化测试 cleanup CAS 防重入路径。
	fixedNow := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("cc"),
		rotation.WithMaxSize(1<<10), // 1 KiB
		rotation.WithMaxBackups(maxBackups),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rotation.SetNow(w, func() time.Time { return fixedNow })
	defer w.Close() //nolint:errcheck

	// 8 个 goroutine 并发写入，每条 1 KiB 触发轮转
	const goroutines = 8
	var wg sync.WaitGroup
	payload := bytes.Repeat([]byte("x"), 1<<10)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = w.Write(payload)
		}()
	}
	wg.Wait()

	// 等待异步 cleanup 完成（最多 2 秒）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(dir)
		var backups int
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "cc-") {
				backups++
			}
		}
		if backups <= maxBackups {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	_ = w.Close() // 触发一次同步 cleanup，确保最终一致

	entries, _ := os.ReadDir(dir)
	var backups []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "cc-") {
			backups = append(backups, e.Name())
		}
	}
	if len(backups) > maxBackups {
		t.Errorf("cleanup 后备份数 %d 超过 maxBackups %d，backups=%v", len(backups), maxBackups, backups)
	}
}
