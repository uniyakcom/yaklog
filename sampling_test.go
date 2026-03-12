package yaklog

import (
	"sync"
	"testing"
	"time"
)

// ─── Sampler 接口 ─────────────────────────────────────────────────────────────

// TestRateSampler_AllowBurst 验证前 n 条令牌桶允许通过。
func TestRateSampler_AllowBurst(t *testing.T) {
	// 速率 1000/s，允许前 10 条突发
	s := NewRateSampler(1000, 10)
	passed := 0
	for i := 0; i < 10; i++ {
		if s.Sample(Info, "msg") {
			passed++
		}
	}
	if passed == 0 {
		t.Error("RateSampler 突发阶段全部拒绝，期望至少 1 条通过")
	}
}

// TestRateSampler_Throttle 验证令牌耗尽后 Sample 返回 false。
func TestRateSampler_Throttle(t *testing.T) {
	// 速率 1/s，突发 1；快速发出 100 条，大多数应被拒绝
	s := NewRateSampler(1, 1)
	blocked := 0
	for i := 0; i < 100; i++ {
		if !s.Sample(Info, "msg") {
			blocked++
		}
	}
	if blocked == 0 {
		t.Error("RateSampler 未节流，期望大部分被拒绝")
	}
}

// TestRateSampler_NilSafe 验证 nil Sampler 在 Logger 中被跳过（全量输出）。
func TestRateSampler_NilSafe(t *testing.T) {
	// sampler=nil 表示全量；验证逻辑由 Handler 保证，此处仅测接口
	var s Sampler = nil
	_ = s // nil 是合法的 Sampler 值
}

// ─── HashSampler ──────────────────────────────────────────────────────────────

// TestHashSampler_AllRate 验证 rate=1.0 时全量通过。
func TestHashSampler_AllRate(t *testing.T) {
	s := NewHashSampler(1.0)
	for i := 0; i < 1000; i++ {
		if !s.Sample(Info, "msg") {
			t.Error("rate=1.0 时应全量通过")
		}
	}
}

// TestHashSampler_ZeroRate 验证 rate=0 时无记录通过（limit=0，所有哈希>0）。
func TestHashSampler_ZeroRate(t *testing.T) {
	s := NewHashSampler(0)
	passed := 0
	for i := 0; i < 1000; i++ {
		if s.Sample(Info, "msg") {
			passed++
		}
	}
	// 哈希=0 概率极低，允许极少通过
	if passed > 5 {
		t.Errorf("rate=0 时期望接近 0 通过，得 %d", passed)
	}
}

// TestHashSampler_ApproximateRate 验证采样率接近目标（允许 ±15% 误差）。
func TestHashSampler_ApproximateRate(t *testing.T) {
	const rate = 0.3
	const total = 10000
	s := NewHashSampler(rate)

	passed := 0
	for i := 0; i < total; i++ {
		// 使用不同 msg 保证哈希分布
		if s.Sample(Info, string(rune('A'+i%26))+string(rune('a'+i/26%26))) {
			passed++
		}
	}

	got := float64(passed) / total
	lo, hi := rate-0.15, rate+0.15
	if got < lo || got > hi {
		t.Errorf("采样率期望约 %.2f，得 %.3f（允许 ±0.15）", rate, got)
	}
}

// TestHashSampler_Deterministic 验证相同 level+msg 组合的采样结果稳定。
func TestHashSampler_Deterministic(t *testing.T) {
	s := NewHashSampler(0.5)
	first := s.Sample(Info, "deterministic-key")
	for i := 0; i < 20; i++ {
		got := s.Sample(Info, "deterministic-key")
		if got != first {
			t.Error("相同 msg 的采样结果应稳定（哈希固定）")
			return
		}
	}
}

// TestHashSampler_SetRate 验证 SetRate 后采样率立即生效（无需重建采样器）。
func TestHashSampler_SetRate(t *testing.T) {
	s := NewHashSampler(1.0) // 全量通过

	// rate=1.0：全量通过
	for i := 0; i < 100; i++ {
		if !s.Sample(Info, "r") {
			t.Fatal("rate=1.0 时应全量通过")
		}
	}

	// 热更新至 rate=0：全量拒绝
	s.SetRate(0)
	passed := 0
	for i := 0; i < 1000; i++ {
		if s.Sample(Info, "r") {
			passed++
		}
	}
	if passed > 5 {
		t.Errorf("SetRate(0) 后期望接近 0 通过，得 %d", passed)
	}

	// 热更新回 rate=1.0：再次全量通过
	s.SetRate(1.0)
	for i := 0; i < 100; i++ {
		if !s.Sample(Info, "r") {
			t.Fatal("SetRate(1.0) 后应全量通过")
		}
	}
}

// TestHashSampler_SetRate_Concurrent 验证 SetRate 并发调用无竞态。
func TestHashSampler_SetRate_Concurrent(t *testing.T) {
	s := NewHashSampler(0.5)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rate := float64(i+1) / 8.0
			s.SetRate(rate)
			_ = s.Sample(Info, "concurrent")
		}(i)
	}
	wg.Wait()
	// 不 panic / 无竞态即通过
}

// ─── RateLimiter 时间推进 ────────────────────────────────────────────────────

// TestRateSampler_Refill 验证等待后令牌桶补充，后续调用再次通过。
func TestRateSampler_Refill(t *testing.T) {
	// 速率 100/s，突发 1
	s := NewRateSampler(100, 1)
	// 耗光令牌
	for i := 0; i < 10; i++ {
		s.Sample(Info, "drain")
	}
	// 等待令牌补充（100/s → 10ms 补充 1 个）
	time.Sleep(20 * time.Millisecond)
	if !s.Sample(Info, "after wait") {
		t.Error("令牌补充后应允许通过")
	}
}

// TestRateSampler_InjectableAllowFn 验证 allowFn 被注入后，Sample 使用注入行为而非真实令牌桶。
func TestRateSampler_InjectableAllowFn(t *testing.T) {
	s := NewRateSampler(1, 1)
	var callCount int

	// 注入始终拒绝的 allowFn
	s.allowFn = func() bool {
		callCount++
		return false
	}

	for i := 0; i < 5; i++ {
		if s.Sample(Info, "msg") {
			t.Error("注入 allowFn=false 时 Sample 应返回 false")
		}
	}
	if callCount != 5 {
		t.Errorf("allowFn 应被调用 5 次，实际 %d 次", callCount)
	}

	// 切换为始终允许的 allowFn
	s.allowFn = func() bool { return true }
	for i := 0; i < 5; i++ {
		if !s.Sample(Info, "msg") {
			t.Error("注入 allowFn=true 时 Sample 应返回 true")
		}
	}
}

// TestHashSampler_SetRateForLevel_IndependentLimits 验证 SetRateForLevel 只影响指定级别。
func TestHashSampler_SetRateForLevel_IndependentLimits(t *testing.T) {
	s := NewHashSampler(1.0) // 所有级别初始全量通过

	// 将 Debug 级别设为 rate=0（全拒绝）
	s.SetRateForLevel(Debug, 0)

	// Debug 应被拒绝
	passed := 0
	for i := 0; i < 1000; i++ {
		if s.Sample(Debug, "d") {
			passed++
		}
	}
	if passed > 5 {
		t.Errorf("SetRateForLevel(Debug, 0) 后 Debug 应接近 0 通过，得 %d", passed)
	}

	// Info、Warn、Error 应仍然全量通过（初始 rate=1.0 未被修改）
	for _, lvl := range []Level{Info, Warn, Error} {
		for i := 0; i < 50; i++ {
			if !s.Sample(lvl, "x") {
				t.Errorf("SetRateForLevel 不应影响 level %v，得 false", lvl)
			}
		}
	}
}

// TestHashSampler_SetRateForLevel_AllLevels 验证对每个已定义级别均可独立设置采样率。
func TestHashSampler_SetRateForLevel_AllLevels(t *testing.T) {
	s := NewHashSampler(0.0) // 全部初始拒绝

	// 逐级开放，仅验证 Error 级别
	s.SetRateForLevel(Error, 1.0)

	for i := 0; i < 100; i++ {
		if !s.Sample(Error, "err") {
			t.Error("SetRateForLevel(Error, 1.0) 后应全量通过")
		}
	}
	// 其他级别应仍被拒绝
	for i := 0; i < 1000; i++ {
		if s.Sample(Info, "info") {
			t.Error("未设置 Info 级别时应被拒绝")
			break
		}
	}
}

// TestHashSampler_SetRateForLevel_Concurrent 验证 SetRateForLevel 并发调用无竞态。
func TestHashSampler_SetRateForLevel_Concurrent(t *testing.T) {
	s := NewHashSampler(0.5)
	var wg sync.WaitGroup
	levels := []Level{Trace, Debug, Info, Warn, Error, Panic, Fatal}
	for _, lvl := range levels {
		lvl := lvl
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.SetRateForLevel(lvl, 0.3)
			_ = s.Sample(lvl, "concurrent")
		}()
	}
	wg.Wait()
	// 无 panic / 无竞态即通过
}

// ─── 基准测试 ─────────────────────────────────────────────────────────────────

// BenchmarkRateSampler_UnderLimit 令牌充足时的并发采样吞吐量。
func BenchmarkRateSampler_UnderLimit(b *testing.B) {
	s := NewRateSampler(1<<30, 1<<30)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Sample(Info, "bench")
		}
	})
}

// BenchmarkRateSampler_OverLimit 令牌耗尽时的并发采样吞吐量。
func BenchmarkRateSampler_OverLimit(b *testing.B) {
	s := NewRateSampler(1, 1)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			s.Sample(Info, "bench")
		}
	})
}

// BenchmarkHashSampler_Allow 50% 采样率下的并发哈希采样吞吐量，
// 使用 16 条不同消息防止哈希缓存效应干扰基准结果。
func BenchmarkHashSampler_Allow(b *testing.B) {
	s := NewHashSampler(0.5)
	msgs := [16]string{
		"alpha", "beta", "gamma", "delta",
		"epsilon", "zeta", "eta", "theta",
		"iota", "kappa", "lambda", "mu",
		"nu", "xi", "omicron", "pi",
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			s.Sample(Info, msgs[i&15])
			i++
		}
	})
}
