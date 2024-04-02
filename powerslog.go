// Package powerslog provides a slog.Handler implementation.
package powerslog

import (
	"context"
	"log/slog"
)

// Handler is a [slog.Handler] that writes Records to an [io.Writer] as
// line-delimited JSON objects.
type Handler struct {
	parent slog.Handler
}

// HandlerOptions are the options for a [Handler].
// A zero HandlerOptions consists entirely of default values.
type HandlerOptions struct {
	// Level reports the minimum record level that will be logged.
	// The handler discards records with lower levels.
	// If Level is nil, the handler assumes slog.LevelInfo.
	Level slog.Leveler
}

// NewHandler creates a [Handler] that writes to w, using the given options.
func NewHandler(opts *HandlerOptions) *Handler {
	if opts == nil {
		opts = &HandlerOptions{}
	}
	return &Handler{}
}

// Enabled reports whether the handler handles records at the given level.
// The handler ignores records whose level is lower.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.parent.Enabled(ctx, level)
}

// Handle implements the slog.Handler interface and handles the Record.
// It will only be called when Enabled returns true.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	return h.parent.Handle(ctx, record)
}

// WithAttrs returns a new Handler whose attributes consist of both the
// receiver's attributes and the arguments.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.parent.WithAttrs(attrs)
}

// WithGroup returns a new Handler with the given group appended to
// the receiver's existing groups.
func (h *Handler) WithGroup(name string) slog.Handler {
	return h.parent.WithGroup(name)
}
