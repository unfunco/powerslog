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
//
// The cold start is attributed to the first invocation whose context reaches
// the handler, whether or not the record is logged. If the first invocation
// makes no such logging call, the next invocation that does is reported as
// the cold start instead.
//
// The handler writes the timestamp, level, message, location, service,
// function_name, function_memory_size, sampling_rate, function_request_id,
// function_arn, cold_start and xray_trace_id keys at the top level of each
// record. Like the handlers in [log/slog], it does not remove duplicate keys,
// so an attribute added at the top level under one of these names is written
// in addition to the handler's own.
package powerslog

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
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
	envVarPowertoolsSampleRate  = "POWERTOOLS_LOGGER_SAMPLE_RATE"
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
	attrKeySampleRate   = "sampling_rate"
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

// levelSilent is above every level that is logged, matching the SILENT level
// of the Powertools logger for TypeScript.
const levelSilent = slog.Level(math.MaxInt)

// Options configures a [Handler]. A nil *Options is equivalent to the zero
// value.
type Options struct {
	// Level is the minimum level of records to log. The AWS_LAMBDA_LOG_LEVEL
	// environment variable takes precedence over Level, and the
	// POWERTOOLS_LOG_LEVEL environment variable is used when Level is nil.
	// Defaults to [slog.LevelInfo].
	//
	// The environment variables accept TRACE, DEBUG, INFO, WARN or WARNING,
	// ERROR, FATAL or CRITICAL, and SILENT, in any case. Any other value is
	// ignored.
	Level slog.Leveler

	// SampleRate is the probability, from 0 to 1, that an invocation is
	// sampled, which logs its debug records regardless of the level. The
	// decision is made from the request ID, so it is the same for every
	// record in an invocation. When SampleRate is 0 or outside [0, 1], the
	// POWERTOOLS_LOGGER_SAMPLE_RATE environment variable is used.
	SampleRate float64

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
	// grouped is set once a group has been opened, after which attributes
	// are no longer added at the top level.
	grouped bool
	// cache holds the handlers most recently built by withContextFields, one
	// for records logged without a request ID and one for records logged with
	// one, so that mixing the two does not rebuild the handler every time.
	cache      [2]atomic.Pointer[cachedHandler]
	level      slog.Leveler
	sampleRate float64
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
//
// When the sample rate is greater than 0, it is added to every record as the
// sampling_rate attribute.
func NewHandler(w io.Writer, opts *Options) *Handler {
	if opts == nil {
		opts = &Options{}
	}
	// Level gating is done by Handler.Enabled, so the JSON handler accepts
	// records at every level.
	var handler slog.Handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
		AddSource:   opts.AddSource,
		Level:       slog.Level(math.MinInt),
		ReplaceAttr: replaceAttr(opts.ReplaceAttr),
	})

	sampleRate := resolveSampleRate(opts.SampleRate)
	var attrs []slog.Attr
	for _, attr := range []slog.Attr{
		getServiceName(),
		getFunctionName(),
		getFunctionMemorySize(),
		getSampleRate(sampleRate),
	} {
		if !attr.Equal(slog.Attr{}) {
			attrs = append(attrs, attr)
		}
	}
	if len(attrs) > 0 {
		handler = handler.WithAttrs(attrs)
	}
	return &Handler{
		root:       handler,
		handler:    handler,
		level:      resolveLevel(opts.Level),
		sampleRate: sampleRate,
	}
}

// Enabled reports whether the handler handles records at the given level.
// The handler ignores records whose level is lower, except for debug records
// in a sampled invocation.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	// Enabled sees every logging call, including those that are not logged,
	// so the cold start is not misattributed to a later invocation when the
	// first one logs nothing at an enabled level.
	observeInvocation(ctx)
	if level >= h.level.Level() {
		return true
	}
	return level >= slog.LevelDebug && h.sampleRate > 0 && h.sampled(ctx)
}

func (h *Handler) sampled(ctx context.Context) bool {
	lc := lambdaContext(ctx)
	if lc == nil || lc.AwsRequestID == "" {
		return false
	}
	return sampleValue(lc.AwsRequestID) < h.sampleRate
}

// lambdaContext returns the Lambda context in ctx, or nil if there is none.
func lambdaContext(ctx context.Context) *lambdacontext.LambdaContext {
	if ctx == nil {
		return nil
	}
	lc, _ := lambdacontext.FromContext(ctx)
	return lc
}

// sampleValue maps a request ID to a value in [0, 1).
func sampleValue(requestID string) float64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(requestID))
	// The FNV-1a high bits are poorly distributed for similar inputs, so
	// they are mixed with the MurmurHash3 finaliser.
	h := hash.Sum64()
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return float64(h>>11) / (1 << 53)
}

// Handle implements the slog.Handler interface and handles the Record.
// It will only be called when Enabled returns true.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	if !h.grouped && hasUserAttrToKeep(&record) {
		record = keepUserAttrs(record)
	}
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
	attrs = slices.Clone(attrs)
	if !h.grouped {
		for i, attr := range attrs {
			attrs[i] = keepUserAttr(attr)
		}
	}
	return h.with(handlerOp{attrs: attrs})
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
		root:       h.root,
		handler:    op.apply(h.handler),
		ops:        append(slices.Clip(h.ops), op),
		grouped:    h.grouped || op.group != "",
		level:      h.level,
		sampleRate: h.sampleRate,
	}
}

// withContextFields returns the handler for records carrying the given
// fields. The result is cached because the fields rarely change between
// records within an invocation.
func (h *Handler) withContextFields(fields contextFields) slog.Handler {
	slot := &h.cache[0]
	if fields.requestID != "" {
		slot = &h.cache[1]
	}
	if c := slot.Load(); c != nil && c.fields == fields {
		return c.handler
	}
	handler := h.root.WithAttrs(fields.attrs())
	for _, op := range h.ops {
		handler = op.apply(handler)
	}
	slot.Store(&cachedHandler{fields: fields, handler: handler})
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
	if lc := lambdaContext(ctx); lc != nil {
		fields.requestID = lc.AwsRequestID
		fields.functionARN = lc.InvokedFunctionArn
	}
	if ctx != nil {
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

// coldStartRequestID is the request ID of the first invocation seen in this
// execution environment.
var coldStartRequestID atomic.Pointer[string]

// observeInvocation records the invocation in ctx, if there is one, as the
// cold start when no invocation has been seen before.
func observeInvocation(ctx context.Context) {
	if coldStartRequestID.Load() != nil {
		return
	}
	if lc := lambdaContext(ctx); lc != nil && lc.AwsRequestID != "" {
		isColdStart(lc.AwsRequestID)
	}
}

func isColdStart(requestID string) bool {
	first := coldStartRequestID.Load()
	if first == nil {
		// The request ID is copied so that only the first call allocates.
		id := requestID
		if coldStartRequestID.CompareAndSwap(nil, &id) {
			return true
		}
		first = coldStartRequestID.Load()
	}
	return *first == requestID
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

func resolveSampleRate(rate float64) float64 {
	if rate > 0 && rate <= 1 {
		return rate
	}
	rate, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(envVarPowertoolsSampleRate)), 64)
	if err != nil || math.IsNaN(rate) || rate < 0 || rate > 1 {
		return 0
	}
	return rate
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
	case "FATAL", "CRITICAL":
		return LevelFatal, true
	case "SILENT":
		return levelSilent, true
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
		// Only the built-in keys are renamed or wrapped by keepUserAttr.
		if len(groups) == 0 && isBuiltinKey(a.Key) {
			if v, ok := userValue(a.Value); ok {
				a.Value = v
			} else {
				a = renameBuiltin(a)
			}
		}
		if next != nil {
			return next(groups, a)
		}
		return a
	}
}

// verbatim wraps the value of a top-level attribute that shares its key with
// one of slog's built-in attributes. ReplaceAttr is given no other way to
// tell the two apart, and without it an attribute such as
// slog.String("msg", ...) would be renamed as though it were the message.
type verbatim struct{ value slog.Value }

// userValue returns the value wrapped by keepUserAttr, if v is one.
func userValue(v slog.Value) (slog.Value, bool) {
	if v.Kind() != slog.KindAny {
		return slog.Value{}, false
	}
	wrapped, ok := v.Any().(verbatim)
	return wrapped.value, ok
}

func isBuiltinKey(key string) bool {
	switch key {
	case slog.TimeKey, slog.LevelKey, slog.MessageKey, slog.SourceKey:
		return true
	}
	return false
}

// keepUserAttr returns a top-level attribute in a form that is written as it
// was given, rather than being renamed as a built-in attribute.
func keepUserAttr(a slog.Attr) slog.Attr {
	switch {
	case isBuiltinKey(a.Key):
		// Groups are never passed to ReplaceAttr, so they are not renamed.
		a.Value = a.Value.Resolve()
		if a.Value.Kind() != slog.KindGroup {
			a.Value = slog.AnyValue(verbatim{a.Value})
		}
	case a.Key == "":
		// A group without a key is inlined, so its attributes are top-level.
		a.Value = a.Value.Resolve()
		if a.Value.Kind() == slog.KindGroup {
			attrs := slices.Clone(a.Value.Group())
			for i, attr := range attrs {
				attrs[i] = keepUserAttr(attr)
			}
			a.Value = slog.GroupValue(attrs...)
		}
	}
	return a
}

// hasUserAttrToKeep reports whether keepUserAttr could change any of the
// attributes of the record, which is rarely the case.
func hasUserAttrToKeep(record *slog.Record) bool {
	found := false
	record.Attrs(func(a slog.Attr) bool {
		found = a.Key == "" || isBuiltinKey(a.Key)
		return !found
	})
	return found
}

// keepUserAttrs returns a copy of the record with keepUserAttr applied to
// each of its attributes.
func keepUserAttrs(record slog.Record) slog.Record {
	kept := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(a slog.Attr) bool {
		kept.AddAttrs(keepUserAttr(a))
		return true
	})
	return kept
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

func getSampleRate(rate float64) slog.Attr {
	if rate == 0 {
		return slog.Attr{}
	}
	return slog.Float64(attrKeySampleRate, rate)
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
