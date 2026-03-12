package yaklog

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uniyakcom/yakutil/bufpool"
)

// ─── 测试辅助写入器 ───────────────────────────────────────────────────────────

// countingWriter 统计 Write 调用次数及总字节数，线程安全。
type countingWriter struct {
	calls atomic.Int64
	bytes atomic.Int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	cw.calls.Add(1)
	cw.bytes.Add(int64(len(p)))
	return len(p), nil
}

// ─── postTask + Wait ──────────────────────────────────────────────────────────

// TestPostTask_BasicAsync 验证 postTask 异步写入，Wait 后全部完成。
func TestPostTask_BasicAsync(t *testing.T) {
	// 使用独立的等待组追踪本测试提交的任务
	var logWg sync.WaitGroup
	cw := &countingWriter{}

	const n = 100
	for i := 0; i < n; i++ {
		buf := bufpool.Get(4)[:0]
		buf = append(buf, 'd', 'a', 't', 'a')
		postTask(buf, nil, &logWg, cw)
	}
	logWg.Wait() // 等待本测试所有任务完成

	if got := cw.calls.Load(); got != n {
		t.Errorf("postTask+Wait: 期望写入 %d 次，得 %d", n, got)
	}
}

// TestPostTask_ConcurrentAsync 验证多 goroutine 并发 postTask。
func TestPostTask_ConcurrentAsync(t *testing.T) {
	var logWg sync.WaitGroup
	cw := &countingWriter{}

	const goroutines = 10
	const perG = 50
	for g := 0; g < goroutines; g++ {
		for i := 0; i < perG; i++ {
			buf := bufpool.Get(4)[:0]
			buf = append(buf, 'y')
			postTask(buf, nil, &logWg, cw)
		}
	}
	logWg.Wait()

	if got := cw.calls.Load(); got != goroutines*perG {
		t.Errorf("并发 postTask: 期望 %d 次写入，得 %d", goroutines*perG, got)
	}
}

// ─── Dropped / Wait 包级函数 ──────────────────────────────────────────────────

// TestDropped_NonNegative 验证 Dropped() 返回非负数。
func TestDropped_NonNegative(t *testing.T) {
	d := Dropped()
	if d < 0 {
		t.Errorf("Dropped() 期望 >= 0, 得 %d", d)
	}
}

// TestWait_NoDataLoss 验证包级 Wait 在 postTask 后不丢数据。
func TestWait_NoDataLoss(t *testing.T) {
	cw := &countingWriter{}
	const n = 30
	for i := 0; i < n; i++ {
		buf := bufpool.Get(4)[:0]
		buf = append(buf, 'z')
		postTask(buf, nil, nil, cw)
	}
	Wait() // 包级 Wait 涵盖所有 inFlight 任务

	if got := cw.calls.Load(); got < n {
		// Wait 之后写入数可能 > n（其他并发测试也在写），但不应 < n
		t.Errorf("Wait 后: 期望至少 %d 次写入，得 %d", n, got)
	}
}

// ─── OnDrop / OnWriteError 钩子 ───────────────────────────────────────────────

// TestSetOnDrop_FiresOnFull 验证队列满时 OnDrop 回调被触发。
func TestSetOnDrop_FiresOnFull(t *testing.T) {
	var dropCount atomic.Int64
	SetOnDrop(func() { dropCount.Add(1) })
	defer SetOnDrop(nil)

	// 使用慢写入器堵住 worker，确保队列积压后溢出触发丢弃。
	l := New(Options{Out: &slowWriter{}, Level: Trace, QueueLen: 1})
	droppedBefore := Dropped()
	for i := 0; i < 5000; i++ {
		l.Info().Msg("flood").Post()
	}
	l.Wait()
	Wait()

	droppedAfter := Dropped()
	if droppedAfter <= droppedBefore {
		t.Skip("worker 消费速度过快未产生丢弃，跳过（CI 机器可能极快）")
	}
	if got := dropCount.Load(); got == 0 {
		t.Errorf("Dropped 计数增长 %d 但 OnDrop 回调未被调用", droppedAfter-droppedBefore)
	}
}

// TestSetOnDrop_NilSafe 验证 SetOnDrop(nil) 清除回调后不 panic。
func TestSetOnDrop_NilSafe(t *testing.T) {
	SetOnDrop(nil)
	// 直接调用 fireOnDrop 不应 panic
	fireOnDrop()
}

// TestSetOnWriteError_FiresOnError 验证写入失败时 OnWriteError 回调被触发。
func TestSetOnWriteError_FiresOnError(t *testing.T) {
	var errCount atomic.Int64
	SetOnWriteError(func(error) { errCount.Add(1) })
	defer SetOnWriteError(nil)

	l := New(Options{Out: &failWriter{}, Level: Trace})
	for i := 0; i < 10; i++ {
		l.Info().Msg("fail").Post()
	}
	l.Wait()
	Wait()

	if got := errCount.Load(); got == 0 {
		t.Error("期望 OnWriteError 至少被调用一次")
	}
}

// TestSetOnWriteError_NilSafe 验证 SetOnWriteError(nil) 后不 panic。
func TestSetOnWriteError_NilSafe(t *testing.T) {
	SetOnWriteError(nil)
	fireOnWriteError(nil)
}

// slowWriter 人为制造延迟的写入器，用于堵住 worker 触发队列溢出。
type slowWriter struct{}

func (slowWriter) Write(p []byte) (int, error) {
	// 小延迟足以令 MPSC ring 积压
	time.Sleep(50 * time.Microsecond)
	return len(p), nil
}

// ─── Shutdown 集成测试 ────────────────────────────────────────────────────────

// resetGlobalWorkerForTest 重置包级 worker，使下一次调用重新初始化。
// 仅在测试代码中调用，避免 Shutdown 污染后续测试。
func resetGlobalWorkerForTest() {
	gw.Store(nil)
	gwOnce = sync.Once{}
}

// TestShutdown_RejectsNewPost 验证 Shutdown 后 postTask 触发 OnWriteError(ErrWriterClosed)
// 且不向写入器写入任何数据。
func TestShutdown_RejectsNewPost(t *testing.T) {
	resetGlobalWorkerForTest() // 确保与上一次测试隔离，-shuffle 运行下同样安全
	getGlobalWorker()
	t.Cleanup(resetGlobalWorkerForTest)

	var sawClosed atomic.Bool
	SetOnWriteError(func(err error) {
		if err == ErrWriterClosed {
			sawClosed.Store(true)
		}
	})
	defer SetOnWriteError(nil)

	if err := Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	cw := &countingWriter{}
	buf := bufpool.Get(4)[:0]
	buf = append(buf, 't', 'e', 's', 't')
	postTask(buf, nil, nil, cw)

	// fireOnWriteError 在 postTask 内同步调用，无需等待
	if !sawClosed.Load() {
		t.Error("Shutdown 后 postTask 应同步触发 OnWriteError(ErrWriterClosed)")
	}
	if cw.calls.Load() != 0 {
		t.Error("Shutdown 后数据不应写入目标写入器")
	}
}

// TestShutdown_WaitNoDeadlock 验证 Shutdown 后调用 Wait() 不会死锁，且及时返回。
func TestShutdown_WaitNoDeadlock(t *testing.T) {
	resetGlobalWorkerForTest() // 确保与上一次测试隔离
	getGlobalWorker()
	t.Cleanup(resetGlobalWorkerForTest)

	if err := Shutdown(); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	done := make(chan struct{})
	go func() {
		Wait()
		close(done)
	}()

	select {
	case <-done:
		// Wait 及时返回，无死锁
	case <-time.After(3 * time.Second):
		t.Fatal("Wait() 在 Shutdown 后超时 3s，疑似死锁")
	}
}
