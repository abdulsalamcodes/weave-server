package logger

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"time"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

func New(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "info":
		l = slog.LevelInfo
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       l,
		AddSource:   true,
		ReplaceAttr: replaceAttr,
	})

	return slog.New(handler)
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Key == slog.SourceKey {
		if source, ok := a.Value.Any().(*slog.Source); ok {
			source.Function = ""
		}
	}
	if a.Key == slog.TimeKey {
		a.Value = slog.StringValue(time.Now().UTC().Format(time.RFC3339Nano))
	}
	return a
}

func ErrAttr(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	return slog.String("error", err.Error())
}

func SrcAttr() slog.Attr {
	_, file, line, ok := runtime.Caller(1)
	if !ok {
		return slog.Attr{}
	}
	return slog.String("source", formatSource(file, line))
}

func formatSource(file string, line int) string {
	short := file
	for i := len(file) - 1; i > 0; i-- {
		if file[i] == '/' {
			short = file[i+1:]
			break
		}
	}
	return short + ":" + itoa(line)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [10]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
