package yaklog

import (
	"os"
	"sync/atomic"
)

// ─── Fatal / Panic 钩子 ───────────────────────────────────────────────────────
//
// yaklog 将 Fatal 退出动作和 Panic 动作抽象为可替换函数，
// 方便在测试中捕获这两种行为，同时也支持在生产环境中添加自定义清理逻辑。
//
// 替换函数在进程生命周期内通常只发生一次（init 或 TestMain 中），
// 内部使用 atomic.Pointer 保证并发安全。

var fatalFuncPtr atomic.Pointer[func(int)]
var panicFuncPtr atomic.Pointer[func(string)]

// fatalFuncCustom 标记 SetFatalFunc 是否被调用过。
// true 时 drainAndExit 跳过关闭全局 worker，避免测试污染全局工作线程池。
var fatalFuncCustom atomic.Bool

func init() {
	f := func(code int) { os.Exit(code) }
	fatalFuncPtr.Store(&f)
	g := func(msg string) { panic(msg) }
	panicFuncPtr.Store(&g)
}

// SetFatalFunc 替换 Fatal 级别触发的退出函数，默认为 os.Exit。
//
// 典型用法：在 TestMain 或具体测试函数开头替换，测试结束后通过 defer 还原。
//
//	old := yaklog.GetFatalFunc()
//	defer yaklog.SetFatalFunc(old)
//	yaklog.SetFatalFunc(func(code int) { /* 捕获退出码，不真正退出 */ })
func SetFatalFunc(fn func(int)) {
	fatalFuncPtr.Store(&fn)
	fatalFuncCustom.Store(true)
}

// GetFatalFunc 返回当前 Fatal 退出函数，用于保存和还原。
func GetFatalFunc() func(int) {
	return *fatalFuncPtr.Load()
}

// SetPanicFunc 替换 Panic 级别触发的 panic 函数，默认为内置 panic。
//
// 典型用法：在 TestMain 或具体测试函数开头替换，测试结束后通过 defer 还原。
//
//	old := yaklog.GetPanicFunc()
//	defer yaklog.SetPanicFunc(old)
//	yaklog.SetPanicFunc(func(msg string) { /* 捕获消息，不真正 panic */ })
func SetPanicFunc(fn func(string)) {
	panicFuncPtr.Store(&fn)
}

// GetPanicFunc 返回当前 Panic 函数，用于保存和还原。
func GetPanicFunc() func(string) {
	return *panicFuncPtr.Load()
}

// ─── Worker 可观测性钩子 ──────────────────────────────────────────────────────
//
// 以下钩子允许用户监控 Post 异步路径的健康状态，
// 可搭配 yakevent 事件总线或其他告警系统使用。
// 底层使用 atomic.Pointer，并发安全，零分配热路径（未设置时为 nil 跳过）。

var onDropHook atomic.Pointer[func()]
var onWriteErrorHook atomic.Pointer[func(error)]

// SetOnDrop 注册 Post 队列满丢弃日志时的回调。
// 回调在 postTask 调用方 goroutine 中同步执行，须快速返回，不可阻塞。
// 传 nil 可清除回调。可搭配 yakevent 使用：
//
//	bus, _ := yakevent.ForAsync()
//	yaklog.SetOnDrop(func() { bus.Emit(&yakevent.Event{Type: "log.drop"}) })
func SetOnDrop(fn func()) {
	if fn == nil {
		onDropHook.Store(nil)
	} else {
		onDropHook.Store(&fn)
	}
}

// SetOnWriteError 注册异步写入失败时的回调，参数为底层 io.Writer 返回的 error。
// 回调在 worker goroutine 中同步执行，须快速返回，不可阻塞或 panic。
// 传 nil 可清除回调。
//
//	yaklog.SetOnWriteError(func(err error) { metrics.LogWriteErrors.Inc() })
func SetOnWriteError(fn func(error)) {
	if fn == nil {
		onWriteErrorHook.Store(nil)
	} else {
		onWriteErrorHook.Store(&fn)
	}
}

func recoverUserCallbackPanic() {
	_ = recover()
}

func safeEmitEventSink(sink EventSink, level Level, msg string, fields []byte) {
	defer recoverUserCallbackPanic()
	sink.Emit(level, msg, fields)
}

// fireOnDrop 内部调用，触发丢弃回调。
func fireOnDrop() {
	if p := onDropHook.Load(); p != nil {
		defer recoverUserCallbackPanic()
		(*p)()
	}
}

// fireOnWriteError 内部调用，触发写入错误回调。
func fireOnWriteError(err error) {
	if p := onWriteErrorHook.Load(); p != nil {
		defer recoverUserCallbackPanic()
		(*p)(err)
	}
}
