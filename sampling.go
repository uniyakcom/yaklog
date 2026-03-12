package yaklog

import (
	"hash/maphash"
	"math"
	"sync/atomic"

	"github.com/uniyakcom/yakutil/ratelimit"
)

// ─── Sampler 接口 ─────────────────────────────────────────────────────────────

// Sampler 采样器接口。
//
// Sample 返回 true 表示允许输出本条记录，false 表示丢弃。
// 实现须并发安全（多 goroutine 同时调用）。
type Sampler interface {
	Sample(level Level, msg string) bool
}

// ─── RateSampler 令牌桶采样 ───────────────────────────────────────────────────

// RateSampler 基于令牌桶算法的速率采样器（复用 yakutil/ratelimit.Limiter）。
//
// 每秒最多允许 rate 条记录通过，突发上限为 burst 条。
// 超出速率的记录被静默丢弃。
// 热路径：单次原子 CAS，无锁，无 goroutine，零分配。
type RateSampler struct {
	lim     *ratelimit.Limiter
	allowFn func() bool // nil → 使用 lim.Allow()；测试可替换
}

// NewRateSampler 创建令牌桶采样器。
//
//   - rate：每秒允许通过的最大记录数（> 0）
//   - burst：突发令牌桶容量（> 0）
func NewRateSampler(rate, burst int) *RateSampler {
	return &RateSampler{lim: ratelimit.New(rate, burst)}
}

// Sample 实现 Sampler 接口，消耗一个令牌；令牌耗尽时返回 false。
func (s *RateSampler) Sample(_ Level, _ string) bool {
	if s.allowFn != nil {
		return s.allowFn()
	}
	return s.lim.Allow()
}

// ─── HashSampler 哈希固定比例采样 ────────────────────────────────────────────

// HashSampler 基于消息哈希的固定比例采样器。
//
// 对相同 level+msg 组合哈希后与阈值比较，保证同一来源的记录以稳定比例输出，
// 避免令牌桶的时间窗口倾斜问题。
// 并发安全：热路径纯整数运算，无共享可变状态，零分配。
//
// 种子范围：maphash.MakeSeed 在进程启动时随机化（AES-NI 加速，防哈希碰撞预测）。
// 相同消息在同一进程内的采样结果稳定一致；不同进程间由于种子不同结果将不同，
// 不应依赖跨进程的采样确定性。
//
// 各级别独立限额：limits[uint8(level)&7] 存储每个日志级别的采样阈值。
// 级别到索引的映射： Trace(-2)→6、Debug(-1)→7、Info(0)→0、Warn(1)→1、Error(2)→2、Panic(3)→3、Fatal(4)→4。
// 采样率支持在运行时通过 [HashSampler.SetRate] 和 [HashSampler.SetRateForLevel] 更新；
// 新率平均在一次原子写后对所有 goroutine 可见。
//
// 注意：当作为 Logger.Sampler 使用时，采样发生在 Msg() 调用之前，
// msg 参数始终为空字符串。此时采样仅基于 level 粒度，而不是基于消息内容。
// 如需基于消息内容做稳定采样，应在应用层自行调用 Sample()。
type HashSampler struct {
	seed   maphash.Seed     // 随机种子，进程启动时初始化（AES-NI 加速，防哈希碰撞预测）
	limits [8]atomic.Uint64 // 每级别独立哈希阈值；索引 = uint8(level) & 7
}

// NewHashSampler 创建固定比例采样器，所有级别使用相同初始采样率。
//
//   - rate：采样率，范围 (0, 1.0]；1.0 表示全量输出，0.01 表示 1%。
func NewHashSampler(rate float64) *HashSampler {
	s := &HashSampler{seed: maphash.MakeSeed()}
	lim := rateToLimit(rate)
	for i := range s.limits {
		s.limits[i].Store(lim)
	}
	return s
}

// SetRate 修改所有级别的采样率。原子写入，对所有 goroutine 单次内存屏障后可见。
// rate 范围 (0, 1.0]；超出范围自动酆位。
func (s *HashSampler) SetRate(rate float64) {
	lim := rateToLimit(rate)
	for i := range s.limits {
		s.limits[i].Store(lim)
	}
}

// SetRateForLevel 修改指定级别的采样率。原子写入，对所有 goroutine 单次内存屏障后可见。
// 让不同级别使用不同的采样率，例如 Error/Warn 全量输出而 Debug 只输出 10%。
// rate 范围 (0, 1.0]；超出范围自动酆位。
func (s *HashSampler) SetRateForLevel(level Level, rate float64) {
	s.limits[uint8(level)&7].Store(rateToLimit(rate))
}

// rateToLimit 将采样率转换为 uint64 阈値。
func rateToLimit(rate float64) uint64 {
	if rate <= 0 {
		return 0
	}
	if rate >= 1 {
		return math.MaxUint64
	}
	return uint64(math.Round(float64(math.MaxUint64) * rate))
}

// Sample 实现 Sampler 接口，对 level+msg 哈希后与该级别的阈值比较。
// 热路径：maphash.String 单次调用（AES-NI 加速），level 以黄金比例常数混入，零分配。
//
// 当 Logger.Sampler 调用时 msg 为空字符串（采样在 Msg() 之前执行），
// 此时哈希结果仅由 level 决定——即对同一级别按固定比例采样。
func (s *HashSampler) Sample(level Level, msg string) bool {
	// maphash.String 比逐步写入 maphash.Hash 快：无需创建 128B 结构体、无 SetSeed 调用。
	// 黄金比例常数 (2^64 / φ) 将 level 均匀散布到哈希空间。
	h := maphash.String(s.seed, msg) ^ uint64(uint8(level))*0x9e3779b97f4a7c15
	return h <= s.limits[uint8(level)&7].Load()
}
