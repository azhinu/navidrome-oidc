package ctxutil

import "context"

type ctxKey string

const requestIDKey ctxKey = "request_id"

// WithRequestID stuffs the uuid into context for later gossip.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID drags the id back out if someone actually cares.
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}
