package adapter

import (
	"context"
	"log/slog"

	"github.com/uniyakcom/yaklog"
)

// slogToYakLevel maps slog.Level to yaklog.Level.
func slogToYakLevel(lvl slog.Level) yaklog.Level {
	switch {
	case lvl >= slog.LevelError:
		return yaklog.Error
	case lvl >= slog.LevelWarn:
		return yaklog.Warn
	case lvl >= slog.LevelInfo:
		return yaklog.Info
	default:
		return yaklog.Debug
	}
}

// slogAdapter 将 *yaklog.Logger 适配为 slog.Handler 接口。
type slogAdapter struct {
	l           *yaklog.Logger
	groupPrefix string // 当前 slog.Group 命名空间前缀；空字符串表示无前缀
}

// Enabled 实现 slog.Handler。
func (a *slogAdapter) Enabled(_ context.Context, lvl slog.Level) bool {
	return slogToYakLevel(lvl) >= a.l.GetLevel()
}

// Handle 实现 slog.Handler，将 slog.Record 路由到 yaklog 的 Send 路径。
// ctx 中通过 [yaklog.WithTrace] / [yaklog.WithField] 注入的字段会自动追加到日志记录。
func (a *slogAdapter) Handle(ctx context.Context, r slog.Record) error {
	e := a.l.Event(slogToYakLevel(r.Level))
	if e == nil {
		return nil
	}
	// 将 ctx 绑定到 Event，使 trace_id 和 WithField 注入的字段在此条日志中生效。
	// e.Ctx() 仅设置 e.boundCtx（零分配），不克隆 Logger。
	e = e.Ctx(ctx)
	r.Attrs(func(attr slog.Attr) bool {
		e = appendSlogAttr(e, attr, a.groupPrefix, 0)
		return true
	})
	e.Msg(r.Message).Send()
	return nil
}

// maxSlogGroupDepth 限制 slog.Group 属性的递归展开深度，防止无限嵌套导致栈溢出。
const maxSlogGroupDepth = 16

// appendSlogAttr 将单个 slog.Attr 追加到 Event。
// 自动解析 KindLogValuer，将 KindGroup 属性展平为“前缀.键”字段（深度上限 maxSlogGroupDepth）。
func appendSlogAttr(e *yaklog.Event, attr slog.Attr, prefix string, depth int) *yaklog.Event {
	attr.Value = attr.Value.Resolve()
	key := attr.Key
	if prefix != "" {
		key = prefix + "." + attr.Key
	}
	switch attr.Value.Kind() {
	case slog.KindGroup:
		if depth >= maxSlogGroupDepth {
			return e.Str(key, "...")
		}
		for _, sub := range attr.Value.Group() {
			e = appendSlogAttr(e, sub, key, depth+1)
		}
		return e
	case slog.KindString:
		return e.Str(key, attr.Value.String())
	case slog.KindInt64:
		return e.Int64(key, attr.Value.Int64())
	case slog.KindUint64:
		return e.Uint64(key, attr.Value.Uint64())
	case slog.KindFloat64:
		return e.Float64(key, attr.Value.Float64())
	case slog.KindBool:
		return e.Bool(key, attr.Value.Bool())
	case slog.KindDuration:
		return e.Dur(key, attr.Value.Duration())
	case slog.KindTime:
		return e.Time(key, attr.Value.Time())
	default:
		return e.Str(key, attr.Value.String())
	}
}

// appendSlogAttrToBuilder 将已解析的 attr 以类型安全方式追加到 LabelBuilder。
// KindGroup 属性递归展平（深度上限 maxSlogGroupDepth），与 appendSlogAttr 语义一致。
func appendSlogAttrToBuilder(b yaklog.LabelBuilder, key string, attr slog.Attr, depth ...int) yaklog.LabelBuilder {
	d := 0
	if len(depth) > 0 {
		d = depth[0]
	}
	switch attr.Value.Kind() {
	case slog.KindGroup:
		if d >= maxSlogGroupDepth {
			return b.Str(key, "...")
		}
		for _, sub := range attr.Value.Group() {
			sub.Value = sub.Value.Resolve()
			subKey := sub.Key
			if key != "" {
				subKey = key + "." + sub.Key
			}
			b = appendSlogAttrToBuilder(b, subKey, sub, d+1)
		}
		return b
	case slog.KindString:
		return b.Str(key, attr.Value.String())
	case slog.KindInt64:
		return b.Int64(key, attr.Value.Int64())
	case slog.KindUint64:
		return b.Uint64(key, attr.Value.Uint64())
	case slog.KindFloat64:
		return b.Float64(key, attr.Value.Float64())
	case slog.KindBool:
		return b.Bool(key, attr.Value.Bool())
	default:
		return b.Any(key, attr.Value.Any())
	}
}

// WithAttrs 实现 slog.Handler，返回预追加属性的新 Handler。
// KindLogValuer 属性在存储前展开，避免延迟解析时的无限循环风险。
// 使用 Labels() 构建器一次 clone + 类型安全序列化，避免每条 attr 各自 clone Logger。
// 若当前设置了 groupPrefix，属性键自动加上前缀以符合 slog.WithGroup 语义。
func (a *slogAdapter) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return a
	}
	b := a.l.Labels()
	for _, attr := range attrs {
		attr.Value = attr.Value.Resolve()
		key := attr.Key
		if a.groupPrefix != "" {
			key = a.groupPrefix + "." + attr.Key
		}
		b = appendSlogAttrToBuilder(b, key, attr)
	}
	return &slogAdapter{l: b.Build(), groupPrefix: a.groupPrefix}
}

// WithGroup 实现 slog.Handler，设置命名空间前缀。
// 当前层及后续层的所有属性键均使用 "<group>." 前缀，符合 slog spec。
// name 为空字符串时返回自身（slog spec 要求）。
func (a *slogAdapter) WithGroup(name string) slog.Handler {
	if name == "" {
		return a
	}
	prefix := name
	if a.groupPrefix != "" {
		prefix = a.groupPrefix + "." + name
	}
	return &slogAdapter{l: a.l, groupPrefix: prefix}
}

// SetDefault 将 l 安装为程序的默认 [slog.Logger]，
// 使 [slog.Info]、[slog.Warn]、[slog.Error] 等包级函数直接输出到 l。
//
// 调用后 [slog.Default()] 返回包装了 l 的 [slog.Logger]。
//
// 注意：l 的生命周期必须不短于程序运行期，建议在 main 函数顶部调用一次。
func SetDefault(l *yaklog.Logger) {
	slog.SetDefault(slog.New(&slogAdapter{l: l}))
}

// RefreshDefault 重新将当前默认 slog 处理器注册为新的 slog.Logger，
// 使 [slog.Default()] 的 handler（通常是 slogAdapter）感知到底层 yaklog.Logger
// 级别变更等运行时修改。
//
// 适用场景：调用 [SetDefault] 后，通过 Logger.SetLevel 改变了日志级别，
// 调用 RefreshDefault 使 [slog.Logger.Enabled] 重新查询最新级别。
func RefreshDefault() {
	slog.SetDefault(slog.New(slog.Default().Handler()))
}

// NewHandler 返回将 l 适配为 [slog.Handler] 的处理器，可传入 [slog.New] 构造 slog logger。
//
// 适用于需要同时维护 *slog.Logger 和 *yaklog.Logger 的场景。
func NewHandler(l *yaklog.Logger) slog.Handler {
	return &slogAdapter{l: l}
}
