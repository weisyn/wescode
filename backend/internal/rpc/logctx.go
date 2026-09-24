package rpc

import (
	"context"
	"log/slog"
)

type ctxLoggerKey struct{}

// WithLogger 把带请求身份字段的 logger 存入 ctx，供下游 handler 取用。
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxLoggerKey{}, l)
}

// L 从 ctx 取 logger；缺失时返回 slog.Default()。
func L(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxLoggerKey{}).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
