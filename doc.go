// Package yaklog 提供高性能零分配结构化日志库。
//
// # 设计目标
//
//   - 零分配热路径：级别未启用或采样丢弃全路径零分配；
//     启用路径通过 sync.Pool 复用 Event 对象 + bufpool 复用缓冲。
//   - Send / Post 二元模型：Send 同步写入（无延迟），Post 异步投递包级 worker。
//   - Shutdown：进程退出阶段可调用 Shutdown() 停止接受新的 Post 并排空队列。
//   - JSON / Text 自动选择：Out 为 Console(...) 时自动切换 Text 编码器，其余使用 JSON。
//   - 全局 Config：yaklog.Config(opts) 热更新全局参数；New() 零参使用全局配置。
//   - 可插拔采样：内置 RateSampler（令牌桶）和 HashSampler（哈希固定采样）。
//   - ctx 注入：通过 WithTrace / WithField / WithLogger / WithEventSink 将上下文信息注入日志。
//
// # 快速开始
//
//	// 使用默认配置（JSON 输出到 stderr）
//	l := yaklog.New()
//	l.Info().Str("service", "gateway").Int("port", 8080).Msg("启动").Send()
//
// # 自定义配置
//
//	l := yaklog.New(yaklog.Options{
//		Out:   yaklog.Console(os.Stdout),
//		Level: yaklog.Debug,
//	})
//	l.Debug().Str("key", "value").Send()
//
// # 异步写入 + 等待排空
//
//	defer yaklog.Wait() // 进程退出前等待 Post 队列排空
//	l.Info().Msg("异步日志").Post()
//
// # 派生子 Logger（Label 追加固定字段）
//
//	sub := l.Label("module", "auth")
//	sub.Info().Msg("鉴权通过").Send()
//
// # 独立 Logger（Fork 独立级别）
//
//	dbLog := l.Fork()
//	dbLog.SetLevel(yaklog.Debug) // 不影响 l
package yaklog
