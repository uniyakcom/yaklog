package rotation

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// cleanup 扫描日志目录，按 maxAge（天） 删除过期文件，再按 maxBackups 保留最新 N 个。
// 只处理 safeDir 下以 filename 为前缀、扩展名为 ext 或 ext+".gz" 的文件。
// 此方法在独立 goroutine 中运行；错误静默丢弃。
func (w *RotatingWriter) cleanup() {
	entries, err := os.ReadDir(w.opts.dir)
	if err != nil {
		return
	}

	prefix := w.opts.filename + "-"
	type backupEntry struct {
		path    string
		modTime time.Time
	}
	var backups []backupEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if !strings.HasSuffix(name, w.opts.ext) && !strings.HasSuffix(name, w.opts.ext+".gz") {
			continue
		}
		full := filepath.Join(w.opts.dir, name)
		// 路径安全验证：跳过任何超出 safeDir 的路径
		if !strings.HasPrefix(filepath.Clean(full), w.safeDir) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backupEntry{path: full, modTime: info.ModTime()})
	}

	// 按名称升序（名称含时间戳，升序即从旧到新）
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].path < backups[j].path
	})

	// 第一步：按年龄删除过期文件
	if w.opts.maxAge > 0 {
		cutoff := w.nowFn().Add(-time.Duration(w.opts.maxAge) * 24 * time.Hour)
		var remaining []backupEntry
		for _, b := range backups {
			if b.modTime.Before(cutoff) {
				_ = os.Remove(b.path)
			} else {
				remaining = append(remaining, b)
			}
		}
		backups = remaining
	}

	// 第二步：按数量保留最新 maxBackups 个
	if w.opts.maxBackups > 0 && len(backups) > w.opts.maxBackups {
		toDelete := backups[:len(backups)-w.opts.maxBackups]
		for _, b := range toDelete {
			_ = os.Remove(b.path)
		}
		backups = backups[len(backups)-w.opts.maxBackups:]
	}

	// 第三步：按总大小上限从最旧备份开始删除，直至总体积低于阈值
	if w.opts.maxTotalSize > 0 && len(backups) > 0 {
		// 重新获取最新 stat（前两步可能已删除部分文件）
		var total int64
		for i := range backups {
			info, err := os.Stat(backups[i].path)
			if err != nil {
				// os.Stat 失败时保留原始 modTime，后续跳过 size=0
				continue
			}
			total += info.Size()
		}
		for len(backups) > 0 && total > w.opts.maxTotalSize {
			oldest := backups[0]
			info, err := os.Stat(oldest.path)
			var sz int64
			if err == nil {
				sz = info.Size()
			}
			if removeErr := os.Remove(oldest.path); removeErr == nil {
				total -= sz
			}
			backups = backups[1:]
		}
	}
}
