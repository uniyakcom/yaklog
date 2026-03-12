package rotation

import "github.com/uniyakcom/yakutil"

// 包级错误常量——零分配，类型安全，且可用 == 比较。

// ErrInvalidDir 目录路径为空或无效。
const ErrInvalidDir = yakutil.ErrStr("rotation: invalid directory path")

// ErrInvalidFilename 文件名为空。
const ErrInvalidFilename = yakutil.ErrStr("rotation: invalid filename")

// ErrInvalidMaxSize 最大文件大小不合法（必须 > 0）。
const ErrInvalidMaxSize = yakutil.ErrStr("rotation: maxSize must be > 0")

// ErrDirNotAbs 目录路径不是绝对路径（必须使用绝对路径以保证 checkPath 前缀验证的稳定性）。
const ErrDirNotAbs = yakutil.ErrStr("rotation: directory path must be absolute")

// ErrPathTraversal 生成的文件路径超出配置目录范围（路径穿越攻击防护）。
const ErrPathTraversal = yakutil.ErrStr("rotation: path traversal detected")

// ErrWriterClosed 写入器已关闭后再次写入。
const ErrWriterClosed = yakutil.ErrStr("rotation: writer closed")

// ErrSymlinkDetected 目标目录或文件为符号链接，拒绝跟随，避免日志写入非预期位置。
const ErrSymlinkDetected = yakutil.ErrStr("rotation: symlink detected")

// ErrInsufficientDiskSpace 磁盘可用空间低于 MinFreeBytes 阈值，拒绝打开日志文件。
const ErrInsufficientDiskSpace = yakutil.ErrStr("rotation: insufficient disk space")
