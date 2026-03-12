// sampling 演示两种采样器：令牌桶速率限制（RateSampler）和哈希固定比例（HashSampler）。
//
// 运行：
//
//	cd _examples && go run ./sampling
package main

import (
	"fmt"
	"os"

	"github.com/uniyakcom/yaklog"
)

func main() {
	// ── 1. RateSampler：每秒最多 N 条（令牌桶，突发 burst 条） ───────────────
	//
	// 场景：接入层高频路径，限制 access log 不超过 1000 qps，
	//       突发允许 50 条先过，既保留样本又防止 I/O 暴涨。
	rateLog := yaklog.New(yaklog.Options{
		Out:     os.Stdout,
		Level:   yaklog.Debug,
		Sampler: yaklog.NewRateSampler(10, 3), // 演示：每秒 10 条，突发 3
	})

	passed := 0
	for i := range 20 {
		e := rateLog.Info()
		if e != nil {
			e.Int("req", i).Msg("请求日志（令牌桶采样）").Send()
			passed++
		}
	}
	fmt.Printf("RateSampler：20 条中通过 %d 条（突发 3 + 速率限制）\n\n", passed)

	// ── 2. HashSampler：对相同 msg 哈希，固定比例输出 ─────────────────────────
	//
	// 场景：调试采样，对相同来源的日志以稳定的 50% 比例输出，
	//       避免令牌桶的时间窗口偏斜（时间一开始全放，后续全丢）。
	hashLog := yaklog.New(yaklog.Options{
		Out:     os.Stdout,
		Level:   yaklog.Debug,
		Sampler: yaklog.NewHashSampler(0.5), // 50% 比例
	})

	hashPassed := 0
	for i := range 100 {
		msg := fmt.Sprintf("事件类型-%d", i%5) // 只有 5 种消息，哈希稳定
		e := hashLog.Debug()
		if e != nil {
			e.Str("msg_type", msg).Int("i", i).Msg(msg).Send()
			hashPassed++
		}
	}
	fmt.Printf("HashSampler 0.5：100 条中通过约 %d 条（期望 ~50）\n\n", hashPassed)

	// ── 3. 全量输出（nil Sampler）与采样 Logger 并存 ──────────────────────────
	//
	// 场景：Error 及以上全量输出，其余采样。实现：Fork 独立 Logger + 不同 Sampler。
	fullLog := yaklog.New(yaklog.Options{
		Out:   os.Stdout,
		Level: yaklog.Error, // 只关注 Error+
	})
	debugSampled := yaklog.New(yaklog.Options{
		Out:     os.Stdout,
		Level:   yaklog.Debug,
		Sampler: yaklog.NewRateSampler(2, 1),
	})

	fullLog.Error().Str("reason", "panic recovered").Msg("Error 全量输出").Send()
	for i := range 10 {
		debugSampled.Debug().Int("i", i).Msg("Debug 采样输出").Send()
	}
}
