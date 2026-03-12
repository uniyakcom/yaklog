// Package adapter 提供 yaklog 与 Go 标准库日志系统的适配层。
//
// # 功能
//
//  1. [ToStdLogWriter] — 将 *yaklog.Logger 包装为 [io.Writer]，
//     可传递给 [log.SetOutput]，将旧代码的 log.Printf 输出路由到 yaklog。
//
//  2. [SetDefault] — 将 *yaklog.Logger 安装为程序的默认 [slog.Logger]，
//     使 [slog.Info]、[slog.Error] 等包级函数直接使用 yaklog 输出。
//
// # 采样注意事项
//
// [SetDefault] 安装的 slog.Handler 会绕过 slog 内置的采样机制：
// slog.Record 本身不携带采样决策，上层 slog handler 链中的采样规则对本适配器不可见。
// 若需采样，请在 yaklog.Logger 一侧配置 [yaklog.Options.Sampler]；
// 不要同时在 slog 和 yaklog 两侧配置采样，否则采样率会叠加（实际通过率 = 双方各自采样率之积）。
//
// # 用法
//
//	logger := yaklog.New(yaklog.Options{Level: yaklog.Info})
//
//	// 将 slog 默认 logger 替换为 yaklog
//	adapter.SetDefault(logger)
//
// # 依赖
//
// 本包仅依赖 [github.com/uniyakcom/yaklog] 和 Go 标准库，无其他第三方依赖。
package adapter
