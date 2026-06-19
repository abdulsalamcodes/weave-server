package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorGray   = "\033[90m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

type prettyHandler struct {
	opts slog.HandlerOptions
	mu   sync.Mutex
	out  io.Writer
}

func newPrettyHandler(out io.Writer, opts *slog.HandlerOptions) *prettyHandler {
	h := &prettyHandler{out: out}
	if opts != nil {
		h.opts = *opts
	}
	return h
}

func (h *prettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *prettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler  { return h }
func (h *prettyHandler) WithGroup(name string) slog.Handler         { return h }

func (h *prettyHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	// Timestamp
	fmt.Fprintf(&buf, "%s%s%s ", colorGray, r.Time.UTC().Format(time.TimeOnly), colorReset)

	// Level
	switch r.Level {
	case slog.LevelDebug:
		fmt.Fprintf(&buf, "%sDEBUG%s ", colorGray, colorReset)
	case slog.LevelInfo:
		fmt.Fprintf(&buf, "%s INFO%s ", colorGreen, colorReset)
	case slog.LevelWarn:
		fmt.Fprintf(&buf, "%s WARN%s ", colorYellow, colorReset)
	case slog.LevelError:
		fmt.Fprintf(&buf, "%sERROR%s ", colorRed, colorReset)
	}

	// Source (filename:line only)
	if h.opts.AddSource && r.PC != 0 {
		frames := slog.Source{}
		_ = frames
		// extract from attrs added by ReplaceAttr — skip, just print message
	}

	// Message
	fmt.Fprintf(&buf, "%s%s%s", colorBold, r.Message, colorReset)

	// Attrs
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == slog.SourceKey {
			if src, ok := a.Value.Any().(*slog.Source); ok {
				// trim to filename:line
				file := src.File
				for i := len(file) - 1; i > 0; i-- {
					if file[i] == '/' {
						file = file[i+1:]
						break
					}
				}
				fmt.Fprintf(&buf, " %s%s:%d%s", colorGray, file, src.Line, colorReset)
			}
			return true
		}
		key := a.Key
		val := a.Value.String()
		if key == "error" {
			fmt.Fprintf(&buf, " %s%s=%s%s", colorRed, key, val, colorReset)
		} else {
			fmt.Fprintf(&buf, " %s%s%s=%s", colorCyan, key, colorReset, strings.TrimSpace(val))
		}
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.out.Write(buf.Bytes())
	return err
}
