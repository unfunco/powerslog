// Package powerslog provides a slog.Handler implementation designed for use
// with AWS Lambda functions and captures key fields from the Lambda context,
// it is intended to be functionally similar to the Powertools loggers for
// Python and TypeScript whilst remaining idiomatic for the Go programming
// language.
//
// The per-invocation fields (function_request_id, function_arn, cold_start
// and xray_trace_id) are read from the context passed to the handler, so the
// *Context logging methods, such as [slog.Logger.InfoContext], must be used
// with the context passed to the Lambda function handler to include them.
package powerslog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/aws/aws-lambda-go/lambdacontext"
)

const (
	envVarLambdaFunctionName    = "AWS_LAMBDA_FUNCTION_NAME"
	envVarLambdaLogLevel        = "AWS_LAMBDA_LOG_LEVEL"
	envVarLambdaMemorySize      = "AWS_LAMBDA_FUNCTION_MEMORY_SIZE"
	envVarPowertoolsLogLevel    = "POWERTOOLS_LOG_LEVEL"
	envVarPowertoolsServiceName = "POWERTOOLS_SERVICE_NAME"
	envVarTraceID               = "_X_AMZN_TRACE_ID"

	// ctxKeyTraceID is the plain string key under which aws-lambda-go stores
	// the X-Ray trace header in the invocation context.
	ctxKeyTraceID = "x-amzn-trace-id"

	attrKeyColdStart    = "cold_start"
	attrKeyFunctionARN  = "function_arn"
	attrKeyFunctionName = "function_name"
	attrKeyLocation     = "location"
	attrKeyMemorySize   = "function_memory_size"
	attrKeyMessage      = "message"
	attrKeyRequestID    = "function_request_id"
	attrKeyService      = "service"
	attrKeyTimestamp    = "timestamp"
	attrKeyTraceID      = "xray_trace_id"

	timestampFormat = "2006-01-02T15:04:05.000Z"
)

// Levels in addition to those defined by slog, matching the TRACE and FATAL
// levels supported by Lambda and the Powertools loggers.
const (
	LevelTrace slog.Level = slog.LevelDebug - 4
	LevelFatal slog.Level = slog.LevelError + 4
)

// Options configures a [Handler]. A nil *Options is equivalent to the zero
// value.
type Options struct {
	// Level is the minimum level of records to log. The AWS_LAMBDA_LOG_LEVEL
	// environment variable takes precedence over Level, and the
	// POWERTOOLS_LOG_LEVEL environment variable is used when Level is nil.
	// Defaults to [slog.LevelInfo].
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
// the AWS Lambda environment and invocation context to every record.
type Handler struct {
	// root is the JSON handler with the environment attributes, handler is
	// root with ops applied. When a record carries context fields, they are
	// added to root and ops are replayed so that the fields are always at the
	// top level, regardless of any groups.
	root    slog.Handler
	handler slog.Handler
	ops     []handlerOp
	cache   atomic.Pointer[cachedHandler]
}

type handlerOp struct {
	group string
	attrs []slog.Attr
}

func (op handlerOp) apply(h slog.Handler) slog.Handler {
	if op.group != "" {
		return h.WithGroup(op.group)
	}
	return h.WithAttrs(op.attrs)
}

type cachedHandler struct {
	fields  contextFields
	handler slog.Handler
}

// NewHandler creates a [Handler] that writes JSON records to w and adds the
// service name, function name and function memory size attributes, where set,
// since these values do not change during the lifetime of the handler.
//
// The function request ID, function ARN, cold start and X-Ray trace ID are
// added to each record from the context passed to the *Context logging
// methods, such as [slog.Logger.InfoContext]. The X-Ray trace ID falls back
// to the _X_AMZN_TRACE_ID environment variable when it is not in the context.
func NewHandler(w io.Writer, opts *Options) *Handler {
	if opts == nil {
		opts = &Options{}
	}
	var handler slog.Handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
		AddSource:   opts.AddSource,
		Level:       resolveLevel(opts.Level),
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
	return &Handler{root: handler, handler: handler}
}

// Enabled reports whether the handler handles records at the given level.
// The handler ignores records whose level is lower.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// Handle implements the slog.Handler interface and handles the Record.
// It will only be called when Enabled returns true.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	fields := fieldsFromContext(ctx)
	if fields == (contextFields{}) {
		return h.handler.Handle(ctx, record)
	}
	return h.withContextFields(fields).Handle(ctx, record)
}

// WithAttrs returns a new Handler whose attributes consist of both the
// receiver's attributes and the arguments.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.with(handlerOp{attrs: slices.Clone(attrs)})
}

// WithGroup returns a new Handler with the given group appended to
// the receiver's existing groups.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.with(handlerOp{group: name})
}

func (h *Handler) with(op handlerOp) *Handler {
	return &Handler{
		root:    h.root,
		handler: op.apply(h.handler),
		ops:     append(slices.Clip(h.ops), op),
	}
}

// withContextFields returns the handler for records carrying the given
// fields. The result is cached because the fields rarely change between
// records within an invocation.
func (h *Handler) withContextFields(fields contextFields) slog.Handler {
	if c := h.cache.Load(); c != nil && c.fields == fields {
		return c.handler
	}
	handler := h.root.WithAttrs(fields.attrs())
	for _, op := range h.ops {
		handler = op.apply(handler)
	}
	h.cache.Store(&cachedHandler{fields: fields, handler: handler})
	return handler
}

type contextFields struct {
	requestID   string
	functionARN string
	traceID     string
	coldStart   bool
}

func (f contextFields) attrs() []slog.Attr {
	attrs := make([]slog.Attr, 0, 4)
	if f.requestID != "" {
		attrs = append(attrs, slog.String(attrKeyRequestID, f.requestID))
	}
	if f.functionARN != "" {
		attrs = append(attrs, slog.String(attrKeyFunctionARN, f.functionARN))
	}
	if f.requestID != "" {
		attrs = append(attrs, slog.Bool(attrKeyColdStart, f.coldStart))
	}
	if f.traceID != "" {
		attrs = append(attrs, slog.String(attrKeyTraceID, f.traceID))
	}
	return attrs
}

func fieldsFromContext(ctx context.Context) contextFields {
	var fields contextFields
	var header string
	if ctx != nil {
		if lc, ok := lambdacontext.FromContext(ctx); ok && lc != nil {
			fields.requestID = lc.AwsRequestID
			fields.functionARN = lc.InvokedFunctionArn
		}
		header, _ = ctx.Value(ctxKeyTraceID).(string)
	}
	if fields.requestID != "" {
		fields.coldStart = isColdStart(fields.requestID)
	}
	if header == "" {
		header = os.Getenv(envVarTraceID)
	}
	fields.traceID = traceRoot(header)
	return fields
}

// traceRoot returns the Root value of an X-Ray trace header in the form
// Root=1-5759e988-bd862e3fe1be46a994272793;Parent=53995c3f42cd8ad8;Sampled=1.
func traceRoot(header string) string {
	for part := range strings.SplitSeq(header, ";") {
		if root, ok := strings.CutPrefix(strings.TrimSpace(part), "Root="); ok {
			return root
		}
	}
	return ""
}

// coldStartRequestID is the request ID of the first invocation handled in
// this execution environment.
var coldStartRequestID atomic.Pointer[string]

func isColdStart(requestID string) bool {
	if coldStartRequestID.CompareAndSwap(nil, &requestID) {
		return true
	}
	return *coldStartRequestID.Load() == requestID
}

func resetColdStart() {
	coldStartRequestID.Store(nil)
}

func resolveLevel(level slog.Leveler) slog.Leveler {
	if l, ok := parseLevel(os.Getenv(envVarLambdaLogLevel)); ok {
		return l
	}
	if level != nil {
		return level
	}
	if l, ok := parseLevel(os.Getenv(envVarPowertoolsLogLevel)); ok {
		return l
	}
	return slog.LevelInfo
}

func parseLevel(s string) (slog.Level, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "TRACE":
		return LevelTrace, true
	case "DEBUG":
		return slog.LevelDebug, true
	case "INFO":
		return slog.LevelInfo, true
	case "WARN", "WARNING":
		return slog.LevelWarn, true
	case "ERROR":
		return slog.LevelError, true
	case "FATAL":
		return LevelFatal, true
	}
	return 0, false
}

// levelName names the levels that slog would otherwise name relative to
// DEBUG or ERROR, such as DEBUG-4 and ERROR+4.
func levelName(l slog.Level) (string, bool) {
	name := func(base string, offset slog.Level) string {
		if offset == 0 {
			return base
		}
		return fmt.Sprintf("%s%+d", base, int(offset))
	}
	switch {
	case l < slog.LevelDebug:
		return name("TRACE", l-LevelTrace), true
	case l >= LevelFatal:
		return name("FATAL", l-LevelFatal), true
	}
	return "", false
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
	case slog.LevelKey:
		if level, ok := a.Value.Any().(slog.Level); ok {
			if name, ok := levelName(level); ok {
				return slog.String(slog.LevelKey, name)
			}
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
