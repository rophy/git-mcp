package audit

import (
	"context"
	"net/http"
	"strings"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	serviceKey   contextKey = "service"
)

func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

func ServiceFromContext(ctx context.Context) string {
	v, _ := ctx.Value(serviceKey).(string)
	return v
}

func ContextWithService(ctx context.Context, service string) context.Context {
	return context.WithValue(ctx, serviceKey, service)
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			tp := r.Header.Get("Traceparent")
			if tp != "" {
				parts := strings.Split(tp, "-")
				if len(parts) >= 3 {
					requestID = parts[1] + "-" + parts[2]
				}
			}
		}

		ctx := r.Context()
		if requestID != "" {
			ctx = context.WithValue(ctx, requestIDKey, requestID)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
