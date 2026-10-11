package audit

import (
	"log/slog"
	"time"
)

type Entry struct {
	Service     string
	RequestID   string
	Tool        string
	Repo        string
	Ref         string
	ResolvedSHA string
	Path        string
	Duration    time.Duration
	Error       string
	ErrorCode   string
}

type Logger struct {
	log *slog.Logger
}

func NewLogger(log *slog.Logger) *Logger {
	return &Logger{log: log}
}

func (l *Logger) Log(e Entry) {
	attrs := []slog.Attr{
		slog.String("service", e.Service),
		slog.String("tool", e.Tool),
		slog.String("repo", e.Repo),
	}

	if e.RequestID != "" {
		attrs = append(attrs, slog.String("request_id", e.RequestID))
	}
	if e.Ref != "" {
		attrs = append(attrs, slog.String("ref", e.Ref))
	}
	if e.ResolvedSHA != "" {
		attrs = append(attrs, slog.String("resolved_sha", e.ResolvedSHA))
	}
	if e.Path != "" {
		attrs = append(attrs, slog.String("path", e.Path))
	}

	attrs = append(attrs, slog.Float64("duration_ms", float64(e.Duration.Milliseconds())))

	if e.Error != "" {
		attrs = append(attrs, slog.String("error", e.Error))
	}
	if e.ErrorCode != "" {
		attrs = append(attrs, slog.String("error_code", e.ErrorCode))
	}

	args := make([]any, len(attrs))
	for i, a := range attrs {
		args[i] = a
	}
	l.log.Info("tool_call", args...)
}
