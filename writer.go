package yaklog

import (
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uniyakcom/yakutil/bufpool"
	"github.com/uniyakcom/yakutil/mpsc"
)

// ─── logEntry：单条日志投递单元 ──────────────────────────────────────────────

// logEntry 存放在 mpsc.Ring slot 中，生命周期由 Ring 管理。
// 生产者 Enqueue → 消费者 DrainRelease 写入后清零。
type logEntry struct {
	buf    []byte  // 已编码的日志行，来自 bufpool，写完后归还
	bufPtr *[]byte // bufpool.GetPtr 的包装器指针；非 nil 时用 PutPtr 归还（0-alloc boxing）
	// nil 表示 buf 由 Get（boxing）分配，降级用 Put 归还
	logWg *sync.WaitGroup // Logger 级等待组，写完后 Done；不需要时为 nil
	dst   io.Writer       // 写入目标
}

// ─── 包级全局 worker ──────────────────────────────────────────────────────────

// globalWorkerState 是包级单一后台写入 goroutine 的控制结构。
// 通过 initWorker 惰性初始化，所有 Logger 的 Post 任务共享此 worker。
type globalWorkerState struct {
	noCopy
	ring      *mpsc.Ring[logEntry] // 无锁 MPSC 环形缓冲区，替代 channel
	notify    chan struct{}        // buffer=1，唤醒消费者 goroutine
	flushReq  chan chan struct{}   // Wait() 屏障：worker 排空后关闭收到的 chan
	stopCh    chan struct{}
	gate      sync.RWMutex   // 协调 Post 与 Shutdown，防止关闭期间仍有新任务入队
	wg        sync.WaitGroup // worker goroutine 本身的生命周期
	dropped   atomic.Int64
	errCnt    atomic.Int64
	interval  time.Duration
	closed    bool
	closeOnce sync.Once
}

// gw 经由 atomic.Pointer 读写，保证并发安全。
var (
	gw     atomic.Pointer[globalWorkerState]
	gwOnce sync.Once
)

// initGlobalWorker 惰性启动包级 worker。
func initGlobalWorker(queueLen int, interval time.Duration) {
	gwOnce.Do(func() {
		if queueLen < 1 {
			queueLen = defaultQueueLen
		}
		if queueLen > maxQueueLen {
			queueLen = maxQueueLen
		}
		if interval <= 0 {
			interval = 100 * time.Millisecond
		}
		w := &globalWorkerState{
			ring:     mpsc.New[logEntry](queueLen),
			notify:   make(chan struct{}, 1),
			flushReq: make(chan chan struct{}, 1),
			stopCh:   make(chan struct{}),
			interval: interval,
		}
		w.wg.Add(1)
		go w.run()
		gw.Store(w)
	})
}

// getGlobalWorker 返回已初始化的全局 worker；若未初始化则用默认参数启动。
// 快路径（worker 已存在）仅执行一次原子读。
func getGlobalWorker() *globalWorkerState {
	if w := gw.Load(); w != nil {
		return w
	}
	initGlobalWorker(defaultQueueLen, 100*time.Millisecond)
	return gw.Load()
}

// ─── worker 主循环 ────────────────────────────────────────────────────────────

func (w *globalWorkerState) run() {
	defer w.wg.Done()
	t := time.NewTimer(w.interval)
	defer t.Stop()

	for {
		select {
		case <-w.notify:
			w.drainRing()
			resetTimer(t, w.interval)

		case done := <-w.flushReq:
			// Wait() 屏障：排空所有已入队条目后通知调用方
			w.drainRing()
			close(done)
			resetTimer(t, w.interval)

		case <-t.C:
			// 定时器兜底：即使通知信号丢失，也能周期性收割
			w.drainRing()
			resetTimer(t, w.interval)

		case <-w.stopCh:
			// 排空剩余后退出
			w.drainRing()
			// 排空未决的 flush 请求
			for {
				select {
				case done := <-w.flushReq:
					close(done)
				default:
					return
				}
			}
		}
	}
}

// drainRing 批量收割并处理 Ring 中所有就绪条目。
func (w *globalWorkerState) drainRing() {
	w.ring.DrainRelease(func(entry *logEntry) {
		if _, err := entry.dst.Write(entry.buf); err != nil {
			w.errCnt.Add(1)
			fireOnWriteError(err)
		}
		// 优先用 PutPtr（0-alloc boxing）归还 buf；
		// bufPtr 是独立堆分配对象（非 ring slot 内嵌字段），安全传入 ptrTiers pool。
		// 注意：须在 DrainRelease 标记 slot free 前清空 entry.bufPtr，
		// 防止 ring slot 复用后出现指针别名。
		bp := entry.bufPtr
		entry.bufPtr = nil // 清空：DrainRelease 返回后 slot 可被 producer 复用
		if bp != nil {
			bufpool.PutPtr(bp) // 0-alloc boxing（指针类型直接放入 iface.data）
		} else {
			bufpool.Put(entry.buf) // 降级：无包装器（首次冷启动或扩容路径）
		}
		if entry.logWg != nil {
			entry.logWg.Done()
		}
	})
}

// ─── 外部接口 ─────────────────────────────────────────────────────────────────

// postTask 将编码完毕的 buf 投入包级 worker 异步写入。
// bufPtr 为 bufpool.GetPtr 返回的包装器（nil 表示 buf 由 Get boxing 分配）。
// logWg 为 Logger 级等待组，写完后 Done；不需要 Logger.Wait() 时可传 nil。
// Ring 满时 buf 立即归还 bufpool 并计入 dropped 计数，调用方不阻塞。
func postTask(buf []byte, bufPtr *[]byte, logWg *sync.WaitGroup, dst io.Writer) {
	w := getGlobalWorker()
	w.gate.RLock()
	if w.closed {
		w.gate.RUnlock()
		if bufPtr != nil {
			bufpool.PutPtr(bufPtr)
		} else {
			bufpool.Put(buf)
		}
		fireOnWriteError(ErrWriterClosed)
		return
	}

	if logWg != nil {
		logWg.Add(1)
	}

	_, ok := w.ring.Enqueue(logEntry{buf: buf, bufPtr: bufPtr, logWg: logWg, dst: dst})
	if !ok {
		w.gate.RUnlock()
		// Ring 满：丢弃，避免 logWg 永不归零。
		if logWg != nil {
			logWg.Done()
		}
		if bufPtr != nil {
			bufpool.PutPtr(bufPtr) // 丢弃路径同样用 PutPtr 归还（0-alloc）
		} else {
			bufpool.Put(buf)
		}
		w.dropped.Add(1)
		fireOnDrop()
		return
	}

	// 通知消费者（buffer=1，非阻塞；已有信号时跳过，消费者会批量收割）
	select {
	case w.notify <- struct{}{}:
	default:
	}
	w.gate.RUnlock()
}

// sendErrCnt 计数 Send 同步路径的写入错误（与 Post 异步路径的 errCnt 对称）。
var sendErrCnt atomic.Int64

// ErrCount 返回全部写入错误次数（Post 异步路径 + Send 同步路径）。
// 可配合 Dropped() 监控日志流健康。
func ErrCount() int64 {
	w := gw.Load()
	postCnt := int64(0)
	if w != nil {
		postCnt = w.errCnt.Load()
	}
	return postCnt + sendErrCnt.Load()
}

// Wait 等待所有已提交的 Post 任务写入完成。通常在 main 函数最后调用。
//
// 原理：向 worker 发送屏障请求，worker 排空所有已入队条目后应答。
// 安全性：Wait 期间不应有并发的 Post 调用；若有，这些后续投递不保证被本次 Wait
// 覆盖，但不会引发崩溃或数据损坏。调用前应确保所有 Post 生产方已停止提交。
func Wait() {
	w := gw.Load()
	if w == nil {
		return
	}
	// stopCh 关闭时 worker 已停止——优先检测，避免与 flushReq 的非确定性竞争。
	select {
	case <-w.stopCh:
		return
	default:
	}
	done := make(chan struct{})
	select {
	case w.flushReq <- done:
		<-done
	case <-w.stopCh:
		// worker 已关闭
	}
}

func shutdownGlobalWorker() error {
	w := gw.Load()
	if w == nil {
		return nil
	}

	w.gate.Lock()
	defer w.gate.Unlock()
	if w.closed {
		return ErrWriterClosed
	}
	w.closed = true

	done := make(chan struct{})
	select {
	case w.flushReq <- done:
		<-done
	case <-w.stopCh:
		return ErrWriterClosed
	}

	w.closeOnce.Do(func() {
		close(w.stopCh)
		w.wg.Wait()
	})
	return nil
}

// Shutdown 停止接受新的 Post 任务，排空当前队列并关闭包级 worker。
//
// 它适合进程退出阶段使用：比 Wait 更强，会在排空后拒绝后续异步投递。
// 调用成功后，后续 Post 会被忽略，并通过 OnWriteError 回调报告 ErrWriterClosed。
func Shutdown() error {
	return shutdownGlobalWorker()
}

// Dropped 返回因队列满而丢弃的 Post 任务总数（可用于监控）。
func Dropped() int64 {
	w := gw.Load()
	if w == nil {
		return 0
	}
	return w.dropped.Load()
}

// ─── timer 辅助 ───────────────────────────────────────────────────────────────

// resetTimer 安全重置 Timer，防止 channel 中残留旧 tick 导致 race。
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
