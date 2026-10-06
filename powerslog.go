// Package powerslog provides a slog.Handler implementation designed for use
// with AWS Lambda functions and captures key fields from the Lambda context,
// it is intended to be functionally similar to the Powertools loggers for
// Python and TypeScript whilst remaining idiomatic for the Go programming
// language.
package powerslog

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
)

const (
	envVarLambdaFunctionName    = "AWS_LAMBDA_FUNCTION_NAME"
	envVarLambdaMemorySize      = "AWS_LAMBDA_FUNCTION_MEMORY_SIZE"
	envVarPowertoolsServiceName = "POWERTOOLS_SERVICE_NAME"

	attrKeyFunctionName = "function_name"
	attrKeyLocation     = "location"
	attrKeyMemorySize   = "function_memory_size"
	attrKeyMessage      = "message"
	attrKeyService      = "service"
	attrKeyTimestamp    = "timestamp"

	timestampFormat = "2006-01-02T15:04:05.000Z"
)

// Options configures a [Handler]. A nil *Options is equivalent to the zero
// value.
type Options struct {
	// Level is the minimum level of records to log. Defaults to [slog.LevelInfo].
	Level slog.Leveler

	// AddSource adds the source code position of the log statement to the output.
	AddSource bool

	// ReplaceAttr is called to rewrite each non-group attribute before it is
	// logged, see [slog.HandlerOptions]. It is called after the built-in
	// attributes have been renamed, so it sees the timestamp, message and
	// location keys rather than the time, msg and source keys.
	ReplaceAttr func(groups []string, a slog.Attr) slog.Attr
}

// Handler is a [slog.Handler] that writes JSON records and adds key fields from
// the AWS Lambda environment to every record.
type Handler struct {
	parent slog.Handler
}

// NewHandler creates a [Handler] that writes JSON records to w and adds the
// service name, function name and function memory size attributes, where set,
// since these values do not change during the lifetime of the handler.
func NewHandler(w io.Writer, opts *Options) *Handler {
	if opts == nil {
		opts = &Options{}
	}
	var handler slog.Handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
		AddSource:   opts.AddSource,
		Level:       opts.Level,
		ReplaceAttr: replaceAttr(opts.ReplaceAttr),
	})

	var attrs []slog.Attr
	for _, attr := range []slog.Attr{
		getServiceName(),
		getFunctionName(),
		getFunctionMemorySize(),
	} {
		if !attr.Equal(slog.Attr{}) {
			attrs = append(attrs, attr)
		}
	}
	if len(attrs) > 0 {
		handler = handler.WithAttrs(attrs)
	}
	return &Handler{parent: handler}
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
	return &Handler{
		parent: h.parent.WithAttrs(attrs),
	}
}

// WithGroup returns a new Handler with the given group appended to
// the receiver's existing groups.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{
		parent: h.parent.WithGroup(name),
	}
}

func replaceAttr(next func([]string, slog.Attr) slog.Attr) func([]string, slog.Attr) slog.Attr {
	return func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 {
			a = renameBuiltin(a)
		}
		if next != nil {
			return next(groups, a)
		}
		return a
	}
}

func renameBuiltin(a slog.Attr) slog.Attr {
	switch a.Key {
	case slog.TimeKey:
		if a.Value.Kind() == slog.KindTime {
			return slog.String(attrKeyTimestamp, a.Value.Time().UTC().Format(timestampFormat))
		}
	case slog.MessageKey:
		return slog.Attr{Key: attrKeyMessage, Value: a.Value}
	case slog.SourceKey:
		if src, ok := a.Value.Any().(*slog.Source); ok && src != nil {
			return slog.String(attrKeyLocation, src.Function+":"+strconv.Itoa(src.Line))
		}
	}
	return a
}

func getFunctionMemorySize() slog.Attr {
	memorySizeStr := os.Getenv(envVarLambdaMemorySize)
	if memorySizeStr == "" {
		return slog.Attr{}
	}
	memorySize, err := strconv.Atoi(memorySizeStr)
	if err != nil {
		return slog.Attr{}
	}
	return slog.Attr{
		Key:   attrKeyMemorySize,
		Value: slog.IntValue(memorySize),
	}
}

func getFunctionName() slog.Attr {
	functionName := os.Getenv(envVarLambdaFunctionName)
	if functionName == "" {
		return slog.Attr{}
	}
	return slog.Attr{
		Key:   attrKeyFunctionName,
		Value: slog.StringValue(functionName),
	}
}

func getServiceName() slog.Attr {
	service := os.Getenv(envVarPowertoolsServiceName)
	if service == "" {
		return slog.Attr{}
	}
	return slog.Attr{
		Key:   attrKeyService,
		Value: slog.StringValue(service),
	}
}
