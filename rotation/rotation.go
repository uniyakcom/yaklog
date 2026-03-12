package rotation

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RotatingWriter 是基于文件大小的日志轮转写入器，实现 [io.Writer] 和 [io.Closer]。
//
// 并发安全：Write/Rotate/Close 均持有同一把 sync.Mutex。
// 安全：所有文件路径经 [filepath.Clean] 规范化，并验证在 Dir 目录内；
// 默认拒绝符号链接目录和文件，减少日志重定向风险。
//
// 零值不可用；请使用 [New] 创建实例。
type RotatingWriter struct {
	mu             sync.Mutex
	opts           options
	safeDir        string   // filepath.Clean(opts.dir) + os.PathSeparator；用于路径验证
	file           *os.File // 当前打开的日志文件
	written        int64    // 当前文件已写字节数
	closed         bool
	nowFn          func() time.Time                  // 时间源；默认 time.Now，测试可替换
	availBytesFn   func(path string) (uint64, error) // 磁盘空间查询；默认 availableBytes，测试可替换
	compressFn     func(src string)                  // 压缩函数；默认 compressFile，测试可替换
	onWriteErrFn   func(err error, p []byte)         // 写入失败回调；nil 时回退至 os.Stderr
	writeOK        atomic.Bool                       // 最后一次 file.Write 是否成功；true＝健康
	cleanupRunning atomic.Bool                       // 防止 cleanup goroutine 并发重入
	compressSem    chan struct{}                     // 限制并发压缩 goroutine 数量（容量 = maxCompressConcurrent）
}

// New 创建并初始化 RotatingWriter。
// dir 和 filename 为必填项；其余字段有默认值。
func New(opts ...Option) (*RotatingWriter, error) {
	var o options
	for _, fn := range opts {
		fn(&o)
	}
	applyDefaults(&o)
	if err := validate(&o); err != nil {
		return nil, err
	}

	safeDir := filepath.Clean(o.dir) + string(os.PathSeparator)
	w := &RotatingWriter{
		opts:         o,
		safeDir:      safeDir,
		nowFn:        time.Now,
		availBytesFn: availableBytes,
		compressFn:   compressFile,
		onWriteErrFn: o.onWriteErrFn,
		compressSem:  make(chan struct{}, maxCompressConcurrent),
	}
	if err := w.openLocked(); err != nil {
		return nil, err
	}
	w.writeOK.Store(true)
	return w, nil
}

// Write 将 p 写入当前日志文件。若写入后超过 maxSize 则触发轮转。
// 实现 [io.Writer]。
func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return 0, ErrWriterClosed
	}
	// 单条消息体积达到 maxSize 且当前文件已有内容时，先轮转再写入，
	// 避免超大消息附加到已有内容后立即截断，使文件始终保持近似 maxSize 上限。
	if w.written > 0 && int64(len(p)) >= w.opts.maxSize {
		_ = w.rotateLocked()
	}
	n, err := w.file.Write(p)
	w.written += int64(n)
	if err != nil {
		w.writeOK.Store(false)
		// 文件写入失败时调用回调；未设置时回退至 os.Stderr 保证日志不丢失。
		if w.onWriteErrFn != nil {
			w.onWriteErrFn(err, p)
		} else {
			_, _ = os.Stderr.Write(p)
		}
		return n, err
	}
	w.writeOK.Store(true)
	if w.written >= w.opts.maxSize {
		// 清理完成失败不影响 Write 返回值；日志必须继续
		_ = w.rotateLocked()
	}
	return n, nil
}

// Rotate 强制触发一次轮转（不管当前文件大小）。
func (w *RotatingWriter) Rotate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrWriterClosed
	}
	return w.rotateLocked()
}

// Close 关闭当前日志文件，并将写入器标记为关闭状态。
// 关闭后调用 Write/Rotate 将返回 [ErrWriterClosed]。
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	var closeErr error
	if w.file != nil {
		closeErr = w.file.Close()
		w.file = nil
	}
	// 关闭时做一次同步清理，确保 maxBackups/maxAge/maxTotalSize 策略最终生效。
	// 特别是快速轮转时（同一毫秒多次轮转），CAS 防重入可能导致部分备份
	// 在 goroutine 版 cleanup 的快照之外；同步路径保证最终一致性。
	if w.opts.maxBackups > 0 || w.opts.maxAge > 0 || w.opts.maxTotalSize > 0 {
		w.cleanup()
	}
	return closeErr
}

// Healthy 报告写入器当前是否健康：未关闭且最近一次文件写入成功。
// 并发安全，热路径纯原子读，无锁。
func (w *RotatingWriter) Healthy() bool {
	if w.closed {
		return false
	}
	return w.writeOK.Load()
}

// ── 内部方法 ─────────────────────────────────────────────────────────────────

// validate 验证 options 字段合法性（纯检查，不修改 o 的字段值）。
// 字段默认值和安全钳位由 applyDefaults 负责，validate 仅做拒绝性校验。
func validate(o *options) error {
	if o.dir == "" {
		return ErrInvalidDir
	}
	// 目录必须是绝对路径：checkPath 以 safeDir（绝对路径前缀）验证生成的文件路径；
	// 相对路径在进程 chdir 后会导致前缀比对失效，使路径穿越防护形同虚设。
	if !filepath.IsAbs(o.dir) {
		return ErrDirNotAbs
	}
	if o.filename == "" {
		return ErrInvalidFilename
	}
	// filename 须为单一文件名，不得含路径分隔符（防路径穿越首道防线）。
	// checkPath 在文件操作时提供第二道验证，此处提前拦截并给出明确错误。
	if filepath.Base(o.filename) != o.filename {
		return ErrInvalidFilename
	}
	if o.maxSize < minMaxSize {
		return ErrInvalidMaxSize
	}
	return nil
}

// currentPath 返回当前活跃日志文件路径（锁外不读）。
func (w *RotatingWriter) currentPath() string {
	return filepath.Join(w.opts.dir, w.opts.filename+w.opts.ext)
}

// backupPath 以当前时间生成轮转备份路径。
// 格式：<filename>-<YYYYMMDD-HHMMSS.mmm><ext>
// localTime=true 时使用本地时间，否则 UTC。
func (w *RotatingWriter) backupPath(t time.Time) string {
	if !w.opts.localTime {
		t = t.UTC()
	}
	stamp := t.Format("20060102-150405.000")
	name := fmt.Sprintf("%s-%s%s", w.opts.filename, stamp, w.opts.ext)
	return filepath.Join(w.opts.dir, name)
}

// uniqueBackupPath 生成不与已有备份冲突的唯一备份路径。
//
// 正常情况下直接返回 backupPath(t)；若同名文件已存在（同一毫秒内多次轮转），
// 则追加 -2、-3 … 序号后缀直至路径空闲，防止 os.Rename 静默覆盖已有备份导致日志丢失。
func (w *RotatingWriter) uniqueBackupPath(t time.Time) (string, error) {
	bak := w.backupPath(t)
	if err := w.checkPath(bak); err != nil {
		return "", err
	}
	if _, err := os.Stat(bak); errors.Is(err, os.ErrNotExist) {
		return bak, nil
	}
	// 同一毫秒内发生冲突，生成带序号的候选路径
	tc := t
	if !w.opts.localTime {
		tc = t.UTC()
	}
	stamp := tc.Format("20060102-150405.000")
	for i := 2; i <= 9999; i++ {
		name := fmt.Sprintf("%s-%s-%d%s", w.opts.filename, stamp, i, w.opts.ext)
		candidate := filepath.Join(w.opts.dir, name)
		if err := w.checkPath(candidate); err != nil {
			return "", err
		}
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
	}
	// 极端回退：9999 个同毫秒冲突后仍返回原路径（os.Rename 将覆盖，优先于永久卡死）
	return bak, nil
}

// checkPath 验证 path 是否在 safeDir 内（防路径穿越）。
func (w *RotatingWriter) checkPath(path string) error {
	clean := filepath.Clean(path)
	if !strings.HasPrefix(clean, w.safeDir) {
		return ErrPathTraversal
	}
	return nil
}

// openLocked 创建或打开当前日志文件（调用方持锁）。
func (w *RotatingWriter) openLocked() error {
	path := w.currentPath()
	if err := w.checkPath(path); err != nil {
		return err
	}
	// 确保目录存在
	if err := os.MkdirAll(w.opts.dir, 0o750); err != nil {
		return err
	}
	if err := w.ensureDirNotSymlink(); err != nil {
		return err
	}
	// 磁盘空间预检（Unix 平台；minFreeBytes=0 跳过）
	if w.opts.minFreeBytes > 0 {
		avail, err := w.availBytesFn(w.opts.dir)
		if err != nil {
			return err
		}
		if avail < uint64(w.opts.minFreeBytes) {
			return ErrInsufficientDiskSpace
		}
	}
	f, err := openFileAppendNoFollow(path, 0o640)
	if err != nil {
		return err
	}
	// 获取已有大小，避免超出 maxSize 的文件不触发轮转
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.written = info.Size()
	return nil
}

func (w *RotatingWriter) ensureDirNotSymlink() error {
	clean := filepath.Clean(w.opts.dir)
	// 仅检查日志目录自身（最后一个路径分量）是否是符号链接。
	// 刻意不使用 filepath.EvalSymlinks 做路径字符串比较：EvalSymlinks 会展开路径中
	// 所有中间符号链接（如 macOS 的 /var→/private/var）和系统别名（如 Windows 的
	// 8.3 短文件名），导致合法目录被误判为 ErrSymlinkDetected。
	// os.Lstat 只检查最后一个分量，能够准确捕获目录本身被替换为符号链接的攻击，
	// 同时不受系统路径规范化的干扰。
	info, err := os.Lstat(clean)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ErrSymlinkDetected
	}
	return nil
}

// rotateLocked 执行轮转（调用方持锁）：
//  1. 关闭当前文件
//  2. 重命名当前文件为带时间戳的备份文件
//  3. 打开新的当前文件
//  4. 异步压缩旧文件（若 compress=true）
//  5. 清理超量旧文件
func (w *RotatingWriter) rotateLocked() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}

	cur := w.currentPath()
	bak, err := w.uniqueBackupPath(w.nowFn())
	if err != nil {
		return err
	}

	// 重命名（若当前文件存在）
	if _, err := os.Stat(cur); err == nil {
		if err := os.Rename(cur, bak); err != nil {
			return err
		}
		// 可选异步压缩（不阻塞写入路径）。
		// 信号量限制并发 goroutine 数量（上限 maxCompressConcurrent）；
		// 容量耗尽时降级为同步压缩，防止慢速存储场景下 goroutine 无限累积。
		if w.opts.compress {
			fn := w.compressFn // 在锁内捕获，避免 goroutine 延迟调度时产生数据竞争
			select {
			case w.compressSem <- struct{}{}:
				go func() {
					defer func() { <-w.compressSem }()
					fn(bak)
				}()
			default:
				// 信号量已满，同步压缩
				fn(bak)
			}
		}
	}

	// 打开新文件
	if err := w.openLocked(); err != nil {
		return err
	}

	// 清理旧文件（maxBackups > 0、maxAge > 0 或 maxTotalSize > 0 时均需触发）。
	// CAS 防重入：上一次 cleanup 若尚未结束则跳过，避免两个 goroutine
	// 并发扫描同一目录、竞争删除相同文件以及 maxBackups 计数使用不一致快照。
	if w.opts.maxBackups > 0 || w.opts.maxAge > 0 || w.opts.maxTotalSize > 0 {
		if w.cleanupRunning.CompareAndSwap(false, true) {
			go func() {
				defer w.cleanupRunning.Store(false)
				w.cleanup()
			}()
		}
	}
	return nil
}
