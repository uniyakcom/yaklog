package yaklog

// ─── 编译期接口实现断言 ───────────────────────────────────────────────────────

// 确保 RateSampler 实现 Sampler 接口。
var _ Sampler = (*RateSampler)(nil)

// 确保 HashSampler 实现 Sampler 接口。
var _ Sampler = (*HashSampler)(nil)

// 确保 consoleSink 实现 io.Writer 接口（通过 Console() 构造）。
var _ interface{ Write([]byte) (int, error) } = (*consoleSink)(nil)
