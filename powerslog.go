// Package powerslog provides a slog.Handler implementation designed for use
// with AWS Lambda functions and captures key fields from the Lambda context,
// it is intended to be functionally similar to the Powertools loggers for
// Python and TypeScript whilst remaining idiomatic for the Go programming
// language.
package powerslog

import (
	"context"
	"log/slog"
	"os"
	"strconv"
)

const (
	envVarLambdaFunctionName    = "AWS_LAMBDA_FUNCTION_NAME"
	envVarLambdaMemorySize      = "AWS_LAMBDA_FUNCTION_MEMORY_SIZE"
	envVarPowertoolsServiceName = "POWERTOOLS_SERVICE_NAME"

	attrKeyFunctionName = "function_name"
	attrKeyMemorySize   = "function_memory_size"
	attrKeyService      = "service"
)

// Handler is a [slog.Handler] that wraps a parent handler and adds key fields
// from the AWS Lambda environment to every record.
type Handler struct {
	parent slog.Handler
}

// NewHandler creates a [Handler] that wraps handler and adds the service name,
// function name and function memory size attributes, where set, since these
// values do not change during the lifetime of the handler.
func NewHandler(handler slog.Handler) *Handler {
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
