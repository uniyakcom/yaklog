// file 演示文件 Sink 与自动轮转：按大小轮转、备份保留策略、gzip 压缩。
//
// 运行：
//
//	cd _examples && go run ./file
//
// 运行后观察 /tmp/yaklog-example/ 下生成的文件。
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/uniyakcom/yaklog"
)

func main() {
	dir := "/tmp/yaklog-example"

	// ── 方式 A：Options.FilePath（路径可点对绝对路径或相对路径） ───────────────────
	logA := yaklog.New(yaklog.Options{
		Level:          yaklog.Info,
		FilePath:       dir + "/app.log", // 绝对路径或相对路径均可；相对路径在此锁定为绝对路径
		FileMaxSize:    1,                // 1 MB 触发轮转（演示用，生产建议 100）
		FileMaxBackups: 3,                // 最多保留 3 个旧文件
		FileMaxAge:     7,                // 旧文件 7 天后自动删除
		FileCompress:   true,             // 轮转后 gzip 压缩（go routine 异步执行）
	})
	// Logger Closer 由调用方管理
	defer func() {
		if c := logA.Closer(); c != nil {
			_ = c.Close()
		}
	}()

	logA.Info().Str("mode", "FilePath").Msg("方式 A：通过 Options.FilePath 自动打开文件").Send()

	// ── 方式 B：Save() 占位符 + FilePath 解析（语义等价方式 A） ─────────────
	logB := yaklog.New(yaklog.Options{
		Level:       yaklog.Info,
		Out:         yaklog.Save(), // 占位符，路径由 FilePath 填入
		FilePath:    dir + "/app-b.log",
		FileMaxSize: 1,
	})
	defer func() {
		if c := logB.Closer(); c != nil {
			_ = c.Close()
		}
	}()

	logB.Info().Str("mode", "Save()").Msg("方式 B：Save() 占位符 + FilePath").Send()

	// ── 方式 C：Save(path) 显式路径，轮转参数走 Options ─────────────────────
	logC := yaklog.New(yaklog.Options{
		Level:       yaklog.Debug,
		Out:         yaklog.Save(dir + "/app-c.log"), // 路径在此明确指定
		FileMaxSize: 1,
	})
	defer func() {
		if c := logC.Closer(); c != nil {
			_ = c.Close()
		}
	}()

	logC.Debug().Str("mode", "Save(path)").Msg("方式 C：Save(path) 显式路径").Send()

	// ── 方式 D：相对路径（在调用时刻自动解析为绝对路径） ───────────────────────
	// 相对路径会在 New() 时将1刻使用 os.Getwd() 解析为绝对路径并锁定，
	// 后续即使工作目录改变也不会漂移。
	logD := yaklog.New(yaklog.Options{
		Level:       yaklog.Info,
		FilePath:    "./tmp-example/app-d.log", // 相对路径，行为与绝对路径等价
		FileMaxSize: 1,
	})
	defer func() {
		if c := logD.Closer(); c != nil {
			_ = c.Close()
		}
	}()

	logD.Info().Str("mode", "relative").Msg("方式 D：相对路径自动解析为绝对路径").Send()

	// ── 写入足量日志演示轮转计数器 ───────────────────────────────────────────
	payload := strings.Repeat("x", 512) // 512 字节 payload
	for i := range 200 {
		logA.Info().Int("i", i).Str("payload", payload).Msg("批量写入触发轮转").Post()
	}
	yaklog.Wait()

	// ── 列出生成的文件 ───────────────────────────────────────────────────────
	entries, _ := os.ReadDir(dir)
	fmt.Printf("\n生成文件（%s）：\n", dir)
	for _, e := range entries {
		info, _ := e.Info()
		if info != nil {
			fmt.Printf("  %-40s  %7d bytes\n", e.Name(), info.Size())
		}
	}
}
