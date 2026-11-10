package powerslog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/slogtest"
	"time"

	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/unfunco/powerslog"
)

type lambdaEnv struct {
	functionName       string
	memorySize         string
	serviceName        string
	traceHeader        string
	lambdaLogLevel     string
	powertoolsLogLevel string
	sampleRate         string
}

func (e lambdaEnv) set(tb testing.TB) {
	tb.Helper()
	tb.Setenv("AWS_LAMBDA_FUNCTION_NAME", e.functionName)
	tb.Setenv("AWS_LAMBDA_FUNCTION_MEMORY_SIZE", e.memorySize)
	tb.Setenv("POWERTOOLS_SERVICE_NAME", e.serviceName)
	tb.Setenv("_X_AMZN_TRACE_ID", e.traceHeader)
	tb.Setenv("AWS_LAMBDA_LOG_LEVEL", e.lambdaLogLevel)
	tb.Setenv("POWERTOOLS_LOG_LEVEL", e.powertoolsLogLevel)
	tb.Setenv("POWERTOOLS_LOGGER_SAMPLE_RATE", e.sampleRate)
}

var fullLambdaEnv = lambdaEnv{
	functionName: "test-function",
	memorySize:   "128",
	serviceName:  "test-service",
}

func newTestHandler(t *testing.T, opts *powerslog.Options) (slog.Handler, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return powerslog.NewHandler(&buf, opts), &buf
}

func decodeLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var records []map[string]any
	dec := json.NewDecoder(buf)
	for dec.More() {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decoding log output: %v", err)
		}
		records = append(records, m)
	}
	return records
}

// contextHandler handles every record with ctx in place of the context it was
// logged with, so that records logged without one carry the context fields.
type contextHandler struct {
	slog.Handler
	ctx context.Context
}

func (h contextHandler) Handle(_ context.Context, record slog.Record) error {
	return h.Handler.Handle(h.ctx, record)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{Handler: h.Handler.WithAttrs(attrs), ctx: h.ctx}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{Handler: h.Handler.WithGroup(name), ctx: h.ctx}
}

func TestHandlerConformance(t *testing.T) {
	sampledEnv := fullLambdaEnv
	sampledEnv.sampleRate = "1"

	tests := []struct {
		name          string
		env           lambdaEnv
		contextFields bool
	}{
		{name: "default", env: fullLambdaEnv},
		{name: "sampled", env: sampledEnv},
		{name: "context fields", env: fullLambdaEnv, contextFields: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env.set(t)
			powerslog.ResetColdStart()
			t.Cleanup(powerslog.ResetColdStart)

			var buf bytes.Buffer
			slogtest.Run(t, func(t *testing.T) slog.Handler {
				buf.Reset()
				handler := powerslog.NewHandler(&buf, nil)
				if !tt.contextFields {
					return handler
				}
				return contextHandler{
					Handler: handler,
					ctx:     lambdaContext(t.Context(), testRequestID, testTraceHeader),
				}
			}, func(t *testing.T) map[string]any {
				records := decodeLines(t, &buf)
				if len(records) != 1 {
					t.Fatalf("got %d records, want 1", len(records))
				}
				record := records[0]
				if _, ok := record["function_request_id"]; ok != tt.contextFields {
					t.Fatalf("function_request_id present = %v, want %v", ok, tt.contextFields)
				}
				// slogtest looks up the built-in slog keys.
				for from, to := range map[string]string{"timestamp": slog.TimeKey, "message": slog.MessageKey} {
					if v, ok := record[from]; ok {
						record[to] = v
						delete(record, from)
					}
				}
				return record
			})
		})
	}
}

func TestHandler(t *testing.T) {
	type logFunc func(slog.Handler) slog.Handler

	tests := []struct {
		name  string
		env   lambdaEnv
		apply logFunc
		want  map[string]any
	}{
		{
			name: "all environment variables set",
			env:  fullLambdaEnv,
			want: map[string]any{
				"level":                "INFO",
				"message":              "hello",
				"service":              "test-service",
				"function_name":        "test-function",
				"function_memory_size": float64(128),
			},
		},
		{
			name: "no environment variables set",
			env:  lambdaEnv{},
			want: map[string]any{
				"level":   "INFO",
				"message": "hello",
			},
		},
		{
			name: "invalid memory size is omitted",
			env: lambdaEnv{
				functionName: "test-function",
				memorySize:   "lots",
				serviceName:  "test-service",
			},
			want: map[string]any{
				"level":         "INFO",
				"message":       "hello",
				"service":       "test-service",
				"function_name": "test-function",
			},
		},
		{
			name: "WithGroup keeps Lambda attributes at the top level",
			env:  fullLambdaEnv,
			apply: func(h slog.Handler) slog.Handler {
				return h.WithGroup("request").WithAttrs([]slog.Attr{slog.String("id", "abc")})
			},
			want: map[string]any{
				"level":                "INFO",
				"message":              "hello",
				"service":              "test-service",
				"function_name":        "test-function",
				"function_memory_size": float64(128),
				"request":              map[string]any{"id": "abc"},
			},
		},
		{
			name: "WithAttrs preserves Lambda attributes",
			env:  fullLambdaEnv,
			apply: func(h slog.Handler) slog.Handler {
				return h.WithAttrs([]slog.Attr{slog.String("user", "alice")})
			},
			want: map[string]any{
				"level":                "INFO",
				"message":              "hello",
				"service":              "test-service",
				"function_name":        "test-function",
				"function_memory_size": float64(128),
				"user":                 "alice",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env.set(t)

			handler, buf := newTestHandler(t, &powerslog.Options{
				ReplaceAttr: removeTimeAttr(),
			})
			if tt.apply != nil {
				handler = tt.apply(handler)
			}
			slog.New(handler).Info("hello")

			records := decodeLines(t, buf)
			if len(records) != 1 {
				t.Fatalf("got %d records, want 1", len(records))
			}
			got := records[0]
			for k, v := range got {
				if k == "" || v == nil {
					t.Errorf("unexpected attribute %q=%v", k, v)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %v\nwant %v", got, tt.want)
			}
		})
	}
}

func TestHandlerEnabled(t *testing.T) {
	tests := []struct {
		name    string
		level   slog.Leveler
		enabled map[slog.Level]bool
	}{
		{
			name:  "default level",
			level: nil,
			enabled: map[slog.Level]bool{
				slog.LevelDebug: false,
				slog.LevelInfo:  true,
				slog.LevelWarn:  true,
				slog.LevelError: true,
			},
		},
		{
			name:  "debug level",
			level: slog.LevelDebug,
			enabled: map[slog.Level]bool{
				slog.LevelDebug: true,
				slog.LevelInfo:  true,
				slog.LevelWarn:  true,
				slog.LevelError: true,
			},
		},
		{
			name:  "error level",
			level: slog.LevelError,
			enabled: map[slog.Level]bool{
				slog.LevelDebug: false,
				slog.LevelInfo:  false,
				slog.LevelWarn:  false,
				slog.LevelError: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fullLambdaEnv.set(t)

			handler, _ := newTestHandler(t, &powerslog.Options{Level: tt.level})
			handlers := map[string]slog.Handler{
				"handler":   handler,
				"WithAttrs": handler.WithAttrs([]slog.Attr{slog.String("k", "v")}),
				"WithGroup": handler.WithGroup("g"),
			}
			for _, name := range slices.Sorted(maps.Keys(handlers)) {
				for level, want := range tt.enabled {
					if got := handlers[name].Enabled(t.Context(), level); got != want {
						t.Errorf("%s: Enabled(%v) = %v, want %v", name, level, got, want)
					}
				}
			}
		})
	}
}

func TestHandlerLevel(t *testing.T) {
	tests := []struct {
		name  string
		env   lambdaEnv
		level slog.Leveler
		want  slog.Level
	}{
		{
			name: "defaults to INFO",
			want: slog.LevelInfo,
		},
		{
			name: "POWERTOOLS_LOG_LEVEL is used without Options.Level",
			env:  lambdaEnv{powertoolsLogLevel: "DEBUG"},
			want: slog.LevelDebug,
		},
		{
			name:  "Options.Level takes precedence over POWERTOOLS_LOG_LEVEL",
			env:   lambdaEnv{powertoolsLogLevel: "DEBUG"},
			level: slog.LevelError,
			want:  slog.LevelError,
		},
		{
			name:  "AWS_LAMBDA_LOG_LEVEL takes precedence over Options.Level",
			env:   lambdaEnv{lambdaLogLevel: "WARN", powertoolsLogLevel: "DEBUG"},
			level: slog.LevelDebug,
			want:  slog.LevelWarn,
		},
		{
			name: "AWS_LAMBDA_LOG_LEVEL takes precedence over POWERTOOLS_LOG_LEVEL",
			env:  lambdaEnv{lambdaLogLevel: "ERROR", powertoolsLogLevel: "DEBUG"},
			want: slog.LevelError,
		},
		{
			name: "level names are case-insensitive and trimmed",
			env:  lambdaEnv{powertoolsLogLevel: " dEbUg "},
			want: slog.LevelDebug,
		},
		{
			name: "WARNING is an alias for WARN",
			env:  lambdaEnv{lambdaLogLevel: "warning"},
			want: slog.LevelWarn,
		},
		{
			name: "TRACE",
			env:  lambdaEnv{lambdaLogLevel: "TRACE"},
			want: powerslog.LevelTrace,
		},
		{
			name: "FATAL",
			env:  lambdaEnv{powertoolsLogLevel: "FATAL"},
			want: powerslog.LevelFatal,
		},
		{
			name: "CRITICAL is an alias for FATAL",
			env:  lambdaEnv{powertoolsLogLevel: "CRITICAL"},
			want: powerslog.LevelFatal,
		},
		{
			name: "SILENT is above every level",
			env:  lambdaEnv{powertoolsLogLevel: "SILENT"},
			want: slog.Level(math.MaxInt),
		},
		{
			name:  "unknown AWS_LAMBDA_LOG_LEVEL falls through to Options.Level",
			env:   lambdaEnv{lambdaLogLevel: "VERBOSE"},
			level: slog.LevelError,
			want:  slog.LevelError,
		},
		{
			name: "unknown AWS_LAMBDA_LOG_LEVEL falls through to POWERTOOLS_LOG_LEVEL",
			env:  lambdaEnv{lambdaLogLevel: "VERBOSE", powertoolsLogLevel: "WARN"},
			want: slog.LevelWarn,
		},
		{
			name: "unknown POWERTOOLS_LOG_LEVEL falls back to INFO",
			env:  lambdaEnv{powertoolsLogLevel: "VERBOSE"},
			want: slog.LevelInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env.set(t)

			handler, _ := newTestHandler(t, &powerslog.Options{Level: tt.level})
			if !handler.Enabled(t.Context(), tt.want) {
				t.Errorf("Enabled(%v) = false, want true", tt.want)
			}
			if handler.Enabled(t.Context(), tt.want-1) {
				t.Errorf("Enabled(%v) = true, want false", tt.want-1)
			}
		})
	}
}

func TestHandlerLevelVar(t *testing.T) {
	tests := []struct {
		name string
		env  lambdaEnv
		want map[slog.Level]bool
	}{
		{
			name: "LevelVar remains dynamic",
			env:  lambdaEnv{powertoolsLogLevel: "INFO"},
			want: map[slog.Level]bool{slog.LevelError: true, slog.LevelDebug: true},
		},
		{
			name: "AWS_LAMBDA_LOG_LEVEL takes precedence over a LevelVar",
			env:  lambdaEnv{lambdaLogLevel: "WARN"},
			want: map[slog.Level]bool{slog.LevelError: true, slog.LevelDebug: false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env.set(t)

			var level slog.LevelVar
			level.Set(slog.LevelError)
			handler, _ := newTestHandler(t, &powerslog.Options{Level: &level})
			handler = handler.WithGroup("g")
			for _, l := range slices.Sorted(maps.Keys(tt.want)) {
				level.Set(l)
				if got := handler.Enabled(t.Context(), l); got != tt.want[l] {
					t.Errorf("Enabled(%v) after Set(%v) = %v, want %v", l, l, got, tt.want[l])
				}
			}
		})
	}
}

func TestHandlerLevelNames(t *testing.T) {
	tests := []struct {
		level slog.Level
		want  string
	}{
		{powerslog.LevelTrace - 1, "TRACE-1"},
		{powerslog.LevelTrace, "TRACE"},
		{powerslog.LevelTrace + 1, "TRACE+1"},
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARN"},
		{slog.LevelError, "ERROR"},
		{slog.LevelError + 1, "ERROR+1"},
		{powerslog.LevelFatal, "FATAL"},
		{powerslog.LevelFatal + 2, "FATAL+2"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			lambdaEnv{}.set(t)

			var replaced any
			handler, buf := newTestHandler(t, &powerslog.Options{
				Level: slog.Level(-100),
				ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
					if a.Key == slog.LevelKey && len(groups) == 0 {
						replaced = a.Value.Any()
					}
					return a
				},
			})
			slog.New(handler).Log(t.Context(), tt.level, "hello")

			records := decodeLines(t, buf)
			if len(records) != 1 {
				t.Fatalf("got %d records, want 1", len(records))
			}
			if got := records[0]["level"]; got != tt.want {
				t.Errorf("level = %v, want %v", got, tt.want)
			}
			if got := fmt.Sprint(replaced); got != tt.want {
				t.Errorf("ReplaceAttr saw level %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandlerOptions(t *testing.T) {
	tests := []struct {
		name  string
		opts  *powerslog.Options
		check func(t *testing.T, records []map[string]any)
	}{
		{
			name: "nil options default to INFO",
			opts: nil,
			check: func(t *testing.T, records []map[string]any) {
				if len(records) != 1 {
					t.Fatalf("got %d records, want 1", len(records))
				}
				if got := records[0]["level"]; got != "INFO" {
					t.Errorf("level = %v, want INFO", got)
				}
				if _, ok := records[0]["timestamp"]; !ok {
					t.Error("timestamp attribute missing")
				}
				if _, ok := records[0]["location"]; ok {
					t.Error("unexpected location attribute")
				}
			},
		},
		{
			name: "Level is honoured",
			opts: &powerslog.Options{Level: slog.LevelDebug},
			check: func(t *testing.T, records []map[string]any) {
				if len(records) != 2 {
					t.Fatalf("got %d records, want 2", len(records))
				}
				if got := records[0]["level"]; got != "DEBUG" {
					t.Errorf("level = %v, want DEBUG", got)
				}
			},
		},
		{
			name: "AddSource is honoured",
			opts: &powerslog.Options{AddSource: true},
			check: func(t *testing.T, records []map[string]any) {
				if len(records) != 1 {
					t.Fatalf("got %d records, want 1", len(records))
				}
				location, ok := records[0]["location"].(string)
				if !ok {
					t.Fatalf("location = %v, want a string", records[0]["location"])
				}
				if !strings.HasPrefix(location, "github.com/unfunco/powerslog_test.TestHandlerOptions") {
					t.Errorf("location = %q, want prefix github.com/unfunco/powerslog_test.TestHandlerOptions", location)
				}
			},
		},
		{
			name: "ReplaceAttr is honoured",
			opts: &powerslog.Options{
				ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
					switch a.Key {
					case "timestamp":
						return slog.Attr{}
					case "service":
						return slog.String("service", "replaced")
					}
					return a
				},
			},
			check: func(t *testing.T, records []map[string]any) {
				if len(records) != 1 {
					t.Fatalf("got %d records, want 1", len(records))
				}
				if _, ok := records[0]["timestamp"]; ok {
					t.Error("timestamp attribute not removed")
				}
				if got := records[0]["service"]; got != "replaced" {
					t.Errorf("service = %v, want replaced", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fullLambdaEnv.set(t)

			handler, buf := newTestHandler(t, tt.opts)
			logger := slog.New(handler)
			logger.Debug("debug")
			logger.Info("info")

			tt.check(t, decodeLines(t, buf))
		})
	}
}

func TestHandlerTimestamp(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{
			name: "UTC time",
			time: time.Date(2021, 12, 12, 21, 21, 8, 921_000_000, time.UTC),
			want: "2021-12-12T21:21:08.921Z",
		},
		{
			name: "non-UTC time is converted to UTC",
			time: time.Date(2021, 12, 12, 22, 21, 8, 921_000_000, time.FixedZone("CET", 3600)),
			want: "2021-12-12T21:21:08.921Z",
		},
		{
			name: "sub-millisecond precision is truncated",
			time: time.Date(2021, 12, 12, 21, 21, 8, 921_999_999, time.UTC),
			want: "2021-12-12T21:21:08.921Z",
		},
		{
			name: "whole seconds keep millisecond precision",
			time: time.Date(2021, 12, 12, 21, 21, 8, 0, time.UTC),
			want: "2021-12-12T21:21:08.000Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, buf := newTestHandler(t, nil)
			if err := handler.Handle(t.Context(), slog.NewRecord(tt.time, slog.LevelInfo, "hello", 0)); err != nil {
				t.Fatal(err)
			}

			records := decodeLines(t, buf)
			if len(records) != 1 {
				t.Fatalf("got %d records, want 1", len(records))
			}
			if got := records[0]["timestamp"]; got != tt.want {
				t.Errorf("timestamp = %v, want %v", got, tt.want)
			}
			if _, ok := records[0]["time"]; ok {
				t.Error("unexpected time attribute")
			}
		})
	}
}

func TestHandlerZeroTimeIsOmitted(t *testing.T) {
	handler, buf := newTestHandler(t, nil)
	if err := handler.Handle(t.Context(), slog.NewRecord(time.Time{}, slog.LevelInfo, "hello", 0)); err != nil {
		t.Fatal(err)
	}

	records := decodeLines(t, buf)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	for _, key := range []string{"timestamp", "time"} {
		if _, ok := records[0][key]; ok {
			t.Errorf("unexpected %s attribute", key)
		}
	}
}

func TestHandlerMessageAndLevel(t *testing.T) {
	handler, buf := newTestHandler(t, nil)
	slog.New(handler).Warn("hello")

	records := decodeLines(t, buf)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if got := records[0]["message"]; got != "hello" {
		t.Errorf("message = %v, want hello", got)
	}
	if got := records[0]["level"]; got != "WARN" {
		t.Errorf("level = %v, want WARN", got)
	}
	if _, ok := records[0]["msg"]; ok {
		t.Error("unexpected msg attribute")
	}
}

func logWithLocation(logger *slog.Logger) int {
	_, _, line, _ := runtime.Caller(0)
	logger.Info("hello")
	return line + 1
}

func TestHandlerLocation(t *testing.T) {
	handler, buf := newTestHandler(t, &powerslog.Options{AddSource: true})
	line := logWithLocation(slog.New(handler))

	records := decodeLines(t, buf)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	want := "github.com/unfunco/powerslog_test.logWithLocation:" + strconv.Itoa(line)
	if got := records[0]["location"]; got != want {
		t.Errorf("location = %v, want %v", got, want)
	}
	if _, ok := records[0]["source"]; ok {
		t.Error("unexpected source attribute")
	}
}

func TestHandlerDoesNotRenameInsideGroups(t *testing.T) {
	ts := time.Date(2021, 12, 12, 21, 21, 8, 921_000_000, time.UTC)
	src := &slog.Source{Function: "main.handler", File: "main.go", Line: 12}

	handler, buf := newTestHandler(t, &powerslog.Options{ReplaceAttr: removeTimeAttr()})
	slog.New(handler.WithGroup("outer")).Info("hello",
		slog.Group("inner",
			slog.Time(slog.TimeKey, ts),
			slog.String(slog.MessageKey, "nested"),
			slog.Any(slog.SourceKey, src),
		),
	)

	records := decodeLines(t, buf)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	got := records[0]
	if got["message"] != "hello" {
		t.Errorf("message = %v, want hello", got["message"])
	}
	outer, _ := got["outer"].(map[string]any)
	inner, ok := outer["inner"].(map[string]any)
	if !ok {
		t.Fatalf("outer.inner = %v, want an object", outer["inner"])
	}
	want := map[string]any{
		slog.TimeKey:    ts.Format(time.RFC3339Nano),
		slog.MessageKey: "nested",
		slog.SourceKey: map[string]any{
			"function": "main.handler",
			"file":     "main.go",
			"line":     float64(12),
		},
	}
	if !reflect.DeepEqual(inner, want) {
		t.Errorf("outer.inner = %v\nwant %v", inner, want)
	}
}

// resolvedValue is a slog.LogValuer that counts how often it is resolved.
type resolvedValue struct {
	value string
	calls *int
}

func (v resolvedValue) LogValue() slog.Value {
	if v.calls != nil {
		*v.calls++
	}
	return slog.StringValue(v.value)
}

func TestHandlerDoesNotRenameUserAttrs(t *testing.T) {
	ts := time.Date(2021, 12, 12, 21, 21, 8, 921_000_000, time.UTC)
	src := &slog.Source{Function: "main.handler", File: "main.go", Line: 12}

	tests := []struct {
		name  string
		apply func(slog.Handler) slog.Handler
		ctx   func(context.Context) context.Context
		attrs []slog.Attr
		want  string
	}{
		{
			name: "record attributes",
			attrs: []slog.Attr{
				slog.String(slog.MessageKey, "user"),
				slog.Time(slog.TimeKey, ts),
				slog.Any(slog.SourceKey, src),
				slog.Any(slog.LevelKey, powerslog.LevelTrace),
			},
			want: `{"level":"INFO","message":"hello","msg":"user","time":"2021-12-12T21:21:08.921Z",` +
				`"source":{"function":"main.handler","file":"main.go","line":12},"level":"DEBUG-4"}`,
		},
		{
			name: "WithAttrs",
			apply: func(h slog.Handler) slog.Handler {
				return h.WithAttrs([]slog.Attr{slog.String(slog.MessageKey, "user")})
			},
			want: `{"level":"INFO","message":"hello","msg":"user"}`,
		},
		{
			name: "WithAttrs and context fields",
			apply: func(h slog.Handler) slog.Handler {
				return h.WithAttrs([]slog.Attr{slog.String(slog.MessageKey, "user")})
			},
			ctx: func(ctx context.Context) context.Context {
				return lambdaContext(ctx, testRequestID, "")
			},
			want: `{"level":"INFO","message":"hello","function_request_id":"` + testRequestID + `",` +
				`"function_arn":"` + testFunctionARN + `","cold_start":true,"msg":"user"}`,
		},
		{
			name: "inlined group",
			attrs: []slog.Attr{
				slog.Group("", slog.String(slog.MessageKey, "user"), slog.Int("n", 1)),
			},
			want: `{"level":"INFO","message":"hello","msg":"user","n":1}`,
		},
		{
			name:  "named group",
			attrs: []slog.Attr{slog.Group(slog.MessageKey, slog.String(slog.MessageKey, "user"))},
			want:  `{"level":"INFO","message":"hello","msg":{"msg":"user"}}`,
		},
		{
			name:  "LogValuer",
			attrs: []slog.Attr{slog.Any(slog.MessageKey, resolvedValue{value: "user"})},
			want:  `{"level":"INFO","message":"hello","msg":"user"}`,
		},
		{
			name: "after WithGroup",
			apply: func(h slog.Handler) slog.Handler {
				return h.WithGroup("g")
			},
			attrs: []slog.Attr{slog.String(slog.MessageKey, "user")},
			want:  `{"level":"INFO","message":"hello","g":{"msg":"user"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lambdaEnv{}.set(t)
			powerslog.ResetColdStart()
			t.Cleanup(powerslog.ResetColdStart)

			handler, buf := newTestHandler(t, &powerslog.Options{ReplaceAttr: removeTimeAttr()})
			if tt.apply != nil {
				handler = tt.apply(handler)
			}
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(ctx)
			}
			slog.New(handler).LogAttrs(ctx, slog.LevelInfo, "hello", tt.attrs...)

			// The output is compared as text because decoding it would hide
			// duplicate keys.
			if got := strings.TrimSpace(buf.String()); got != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func TestHandlerReplaceAttrSeesUserAttrs(t *testing.T) {
	lambdaEnv{}.set(t)

	var got []slog.Attr
	handler, _ := newTestHandler(t, &powerslog.Options{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.MessageKey && len(groups) == 0 {
				got = append(got, a)
			}
			return a
		},
	})
	slog.New(handler).Info("hello", slog.Any(slog.MessageKey, resolvedValue{value: "user"}))

	want := []slog.Attr{slog.String(slog.MessageKey, "user")}
	if !slices.EqualFunc(got, want, slog.Attr.Equal) {
		t.Errorf("ReplaceAttr saw %v, want %v", got, want)
	}
}

func TestHandlerReplaceAttrSeesPowertoolsNames(t *testing.T) {
	lambdaEnv{}.set(t)

	var keys []string
	handler, buf := newTestHandler(t, &powerslog.Options{
		AddSource: true,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 {
				keys = append(keys, a.Key)
			}
			if a.Key == "message" {
				return slog.String("message", "replaced")
			}
			return a
		},
	})
	slog.New(handler).Info("hello")

	want := []string{"timestamp", "level", "location", "message"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("ReplaceAttr keys = %v, want %v", keys, want)
	}
	records := decodeLines(t, buf)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if got := records[0]["message"]; got != "replaced" {
		t.Errorf("message = %v, want replaced", got)
	}
}

const (
	testRequestID   = "c6af9ac6-7b61-11e6-9a41-93e812345678"
	testFunctionARN = "arn:aws:lambda:eu-west-2:123456789012:function:test-function"
	testTraceHeader = "Root=1-5759e988-bd862e3fe1be46a994272793;Parent=53995c3f42cd8ad8;Sampled=1"
	testTraceID     = "1-5759e988-bd862e3fe1be46a994272793"
)

func lambdaContext(ctx context.Context, requestID, traceHeader string) context.Context {
	ctx = lambdacontext.NewContext(ctx, &lambdacontext.LambdaContext{
		AwsRequestID:       requestID,
		InvokedFunctionArn: testFunctionARN,
	})
	if traceHeader != "" {
		// aws-lambda-go stores the trace header under a plain string key.
		ctx = context.WithValue(ctx, "x-amzn-trace-id", traceHeader) //nolint:staticcheck
	}
	return ctx
}

func TestHandlerContextFields(t *testing.T) {
	tests := []struct {
		name  string
		env   lambdaEnv
		ctx   func(context.Context) context.Context
		apply func(slog.Handler) slog.Handler
		want  map[string]any
	}{
		{
			name: "Lambda context and trace header",
			ctx: func(ctx context.Context) context.Context {
				return lambdaContext(ctx, testRequestID, testTraceHeader)
			},
			want: map[string]any{
				"level":               "INFO",
				"message":             "hello",
				"function_request_id": testRequestID,
				"function_arn":        testFunctionARN,
				"cold_start":          true,
				"xray_trace_id":       testTraceID,
			},
		},
		{
			name: "trace header falls back to the environment",
			env:  lambdaEnv{traceHeader: testTraceHeader},
			ctx: func(ctx context.Context) context.Context {
				return lambdaContext(ctx, testRequestID, "")
			},
			want: map[string]any{
				"level":               "INFO",
				"message":             "hello",
				"function_request_id": testRequestID,
				"function_arn":        testFunctionARN,
				"cold_start":          true,
				"xray_trace_id":       testTraceID,
			},
		},
		{
			name: "trace header in the context takes precedence",
			env:  lambdaEnv{traceHeader: "Root=1-00000000-000000000000000000000000"},
			ctx: func(ctx context.Context) context.Context {
				return lambdaContext(ctx, testRequestID, testTraceHeader)
			},
			want: map[string]any{
				"level":               "INFO",
				"message":             "hello",
				"function_request_id": testRequestID,
				"function_arn":        testFunctionARN,
				"cold_start":          true,
				"xray_trace_id":       testTraceID,
			},
		},
		{
			name: "trace header from the environment without a Lambda context",
			env:  lambdaEnv{traceHeader: testTraceHeader},
			want: map[string]any{
				"level":         "INFO",
				"message":       "hello",
				"xray_trace_id": testTraceID,
			},
		},
		{
			name: "fields are absent without a Lambda context",
			want: map[string]any{
				"level":   "INFO",
				"message": "hello",
			},
		},
		{
			name: "empty request ID omits the request ID and cold start",
			ctx: func(ctx context.Context) context.Context {
				return lambdaContext(ctx, "", "Parent=53995c3f42cd8ad8;Sampled=1")
			},
			want: map[string]any{
				"level":        "INFO",
				"message":      "hello",
				"function_arn": testFunctionARN,
			},
		},
		{
			name: "WithGroup keeps context fields at the top level",
			env:  fullLambdaEnv,
			ctx: func(ctx context.Context) context.Context {
				return lambdaContext(ctx, testRequestID, testTraceHeader)
			},
			apply: func(h slog.Handler) slog.Handler {
				return h.WithAttrs([]slog.Attr{slog.String("user", "alice")}).
					WithGroup("request").
					WithAttrs([]slog.Attr{slog.String("id", "abc")}).
					WithGroup("empty")
			},
			want: map[string]any{
				"level":                "INFO",
				"message":              "hello",
				"service":              "test-service",
				"function_name":        "test-function",
				"function_memory_size": float64(128),
				"function_request_id":  testRequestID,
				"function_arn":         testFunctionARN,
				"cold_start":           true,
				"xray_trace_id":        testTraceID,
				"user":                 "alice",
				"request":              map[string]any{"id": "abc"},
			},
		},
		{
			name: "WithAttrs preserves context fields",
			ctx: func(ctx context.Context) context.Context {
				return lambdaContext(ctx, testRequestID, testTraceHeader)
			},
			apply: func(h slog.Handler) slog.Handler {
				return h.WithAttrs([]slog.Attr{slog.String("user", "alice")})
			},
			want: map[string]any{
				"level":               "INFO",
				"message":             "hello",
				"function_request_id": testRequestID,
				"function_arn":        testFunctionARN,
				"cold_start":          true,
				"xray_trace_id":       testTraceID,
				"user":                "alice",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env.set(t)
			powerslog.ResetColdStart()
			t.Cleanup(powerslog.ResetColdStart)

			handler, buf := newTestHandler(t, &powerslog.Options{
				ReplaceAttr: removeTimeAttr(),
			})
			if tt.apply != nil {
				handler = tt.apply(handler)
			}
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(ctx)
			}
			slog.New(handler).InfoContext(ctx, "hello")

			records := decodeLines(t, buf)
			if len(records) != 1 {
				t.Fatalf("got %d records, want 1", len(records))
			}
			if !reflect.DeepEqual(records[0], tt.want) {
				t.Errorf("got  %v\nwant %v", records[0], tt.want)
			}
		})
	}
}

func TestHandlerColdStart(t *testing.T) {
	lambdaEnv{}.set(t)
	powerslog.ResetColdStart()
	t.Cleanup(powerslog.ResetColdStart)

	handler, buf := newTestHandler(t, nil)
	logger := slog.New(handler).With(slog.String("user", "alice"))

	first := lambdaContext(t.Context(), "first-request", testTraceHeader)
	second := lambdaContext(t.Context(), "second-request", testTraceHeader)
	logger.InfoContext(first, "hello")
	logger.InfoContext(first, "hello")
	logger.InfoContext(second, "hello")
	logger.InfoContext(first, "hello")
	logger.Info("hello")

	records := decodeLines(t, buf)
	want := []struct {
		requestID any
		coldStart any
	}{
		{"first-request", true},
		{"first-request", true},
		{"second-request", false},
		{"first-request", true},
		{nil, nil},
	}
	if len(records) != len(want) {
		t.Fatalf("got %d records, want %d", len(records), len(want))
	}
	for i, w := range want {
		if got := records[i]["function_request_id"]; got != w.requestID {
			t.Errorf("record %d: function_request_id = %v, want %v", i, got, w.requestID)
		}
		if got := records[i]["cold_start"]; got != w.coldStart {
			t.Errorf("record %d: cold_start = %v, want %v", i, got, w.coldStart)
		}
		if got := records[i]["user"]; got != "alice" {
			t.Errorf("record %d: user = %v, want alice", i, got)
		}
	}
}

func TestHandlerColdStartCountsRecordsThatAreNotLogged(t *testing.T) {
	lambdaEnv{}.set(t)
	powerslog.ResetColdStart()
	t.Cleanup(powerslog.ResetColdStart)

	handler, buf := newTestHandler(t, &powerslog.Options{Level: slog.LevelWarn})
	logger := slog.New(handler)

	logger.InfoContext(lambdaContext(t.Context(), "first-request", ""), "not logged")
	logger.WarnContext(lambdaContext(t.Context(), "second-request", ""), "hello")

	records := decodeLines(t, buf)
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if got := records[0]["function_request_id"]; got != "second-request" {
		t.Errorf("function_request_id = %v, want second-request", got)
	}
	if got := records[0]["cold_start"]; got != false {
		t.Errorf("cold_start = %v, want false", got)
	}
}

func TestHandlerMixesRecordsWithAndWithoutContext(t *testing.T) {
	// Lambda sets the trace header in the environment, so records logged
	// without a context carry a trace ID but no request ID.
	lambdaEnv{traceHeader: testTraceHeader}.set(t)
	powerslog.ResetColdStart()
	t.Cleanup(powerslog.ResetColdStart)

	handler, buf := newTestHandler(t, nil)
	var resolved int
	logger := slog.New(handler).With(slog.Any("user", resolvedValue{value: "alice", calls: &resolved}))
	ctx := lambdaContext(t.Context(), testRequestID, testTraceHeader)

	logger.InfoContext(ctx, "hello")
	logger.Info("hello")
	built := resolved

	const n = 5
	for range n {
		logger.InfoContext(ctx, "hello")
		logger.Info("hello")
	}
	// The attributes are resolved each time the handler is rebuilt, which
	// must not happen again once it has been built for both kinds of record.
	if resolved != built {
		t.Errorf("handler was rebuilt %d times for %d more records", resolved-built, 2*n)
	}

	records := decodeLines(t, buf)
	if len(records) != 2*(n+1) {
		t.Fatalf("got %d records, want %d", len(records), 2*(n+1))
	}
	for i, record := range records {
		var wantRequestID any
		if i%2 == 0 {
			wantRequestID = testRequestID
		}
		if got := record["function_request_id"]; got != wantRequestID {
			t.Errorf("record %d: function_request_id = %v, want %v", i, got, wantRequestID)
		}
		if got := record["xray_trace_id"]; got != testTraceID {
			t.Errorf("record %d: xray_trace_id = %v, want %v", i, got, testTraceID)
		}
		if got := record["user"]; got != "alice" {
			t.Errorf("record %d: user = %v, want alice", i, got)
		}
	}
}

func TestHandlerWithNothingReturnsReceiver(t *testing.T) {
	handler, _ := newTestHandler(t, nil)
	if got := handler.WithAttrs(nil); got != handler {
		t.Error("WithAttrs(nil) returned a new handler")
	}
	if got := handler.WithGroup(""); got != handler {
		t.Error(`WithGroup("") returned a new handler`)
	}
}

func TestHandlerColdStartIsConcurrencySafe(t *testing.T) {
	lambdaEnv{}.set(t)
	powerslog.ResetColdStart()
	t.Cleanup(powerslog.ResetColdStart)

	handler := powerslog.NewHandler(io.Discard, nil)
	logger := slog.New(handler.WithGroup("g"))

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			ctx := lambdaContext(context.Background(), "request-"+strconv.Itoa(i%4), testTraceHeader)
			logger.InfoContext(ctx, "hello", slog.Int("i", i))
		})
	}
	wg.Wait()
}

func TestHandlerSampleRate(t *testing.T) {
	tests := []struct {
		name       string
		env        lambdaEnv
		sampleRate float64
		want       any
	}{
		{
			name: "no sample rate",
			want: nil,
		},
		{
			name:       "Options.SampleRate",
			sampleRate: 0.5,
			want:       0.5,
		},
		{
			name:       "Options.SampleRate takes precedence over POWERTOOLS_LOGGER_SAMPLE_RATE",
			env:        lambdaEnv{sampleRate: "0.25"},
			sampleRate: 1,
			want:       float64(1),
		},
		{
			name: "POWERTOOLS_LOGGER_SAMPLE_RATE is used without Options.SampleRate",
			env:  lambdaEnv{sampleRate: " 0.25 "},
			want: 0.25,
		},
		{
			name:       "out of range Options.SampleRate falls back to POWERTOOLS_LOGGER_SAMPLE_RATE",
			env:        lambdaEnv{sampleRate: "0.25"},
			sampleRate: 1.5,
			want:       0.25,
		},
		{
			name:       "negative Options.SampleRate falls back to POWERTOOLS_LOGGER_SAMPLE_RATE",
			env:        lambdaEnv{sampleRate: "0.25"},
			sampleRate: -0.5,
			want:       0.25,
		},
		{
			name: "invalid POWERTOOLS_LOGGER_SAMPLE_RATE is ignored",
			env:  lambdaEnv{sampleRate: "often"},
			want: nil,
		},
		{
			name: "out of range POWERTOOLS_LOGGER_SAMPLE_RATE is ignored",
			env:  lambdaEnv{sampleRate: "2"},
			want: nil,
		},
		{
			name: "NaN POWERTOOLS_LOGGER_SAMPLE_RATE is ignored",
			env:  lambdaEnv{sampleRate: "NaN"},
			want: nil,
		},
		{
			name: "zero POWERTOOLS_LOGGER_SAMPLE_RATE is omitted",
			env:  lambdaEnv{sampleRate: "0"},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env.set(t)

			handler, buf := newTestHandler(t, &powerslog.Options{SampleRate: tt.sampleRate})
			slog.New(handler.WithGroup("g")).Info("hello")

			records := decodeLines(t, buf)
			if len(records) != 1 {
				t.Fatalf("got %d records, want 1", len(records))
			}
			got, ok := records[0]["sampling_rate"]
			if tt.want == nil {
				if ok {
					t.Errorf("unexpected sampling_rate attribute %v", got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("sampling_rate = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandlerSampling(t *testing.T) {
	tests := []struct {
		name       string
		env        lambdaEnv
		level      slog.Leveler
		sampleRate float64
		requestID  string
		enabled    map[slog.Level]bool
	}{
		{
			name:       "sample rate 1 enables debug",
			sampleRate: 1,
			requestID:  testRequestID,
			enabled: map[slog.Level]bool{
				powerslog.LevelTrace: false,
				slog.LevelDebug:      true,
				slog.LevelInfo:       true,
			},
		},
		{
			name:       "sample rate 1 enables debug above the minimum level",
			level:      slog.LevelError,
			sampleRate: 1,
			requestID:  testRequestID,
			enabled: map[slog.Level]bool{
				powerslog.LevelTrace: false,
				slog.LevelDebug:      true,
				slog.LevelInfo:       true,
				slog.LevelWarn:       true,
				slog.LevelError:      true,
			},
		},
		{
			name:      "POWERTOOLS_LOGGER_SAMPLE_RATE enables debug",
			env:       lambdaEnv{sampleRate: "1"},
			requestID: testRequestID,
			enabled: map[slog.Level]bool{
				slog.LevelDebug: true,
			},
		},
		{
			name:      "sample rate 0 does not enable debug",
			requestID: testRequestID,
			enabled: map[slog.Level]bool{
				slog.LevelDebug: false,
				slog.LevelInfo:  true,
			},
		},
		{
			name:       "no request ID is not sampled",
			sampleRate: 1,
			enabled: map[slog.Level]bool{
				slog.LevelDebug: false,
				slog.LevelInfo:  true,
			},
		},
		{
			name:       "TRACE level is unchanged by sampling",
			env:        lambdaEnv{lambdaLogLevel: "TRACE"},
			sampleRate: 1,
			requestID:  testRequestID,
			enabled: map[slog.Level]bool{
				powerslog.LevelTrace - 1: false,
				powerslog.LevelTrace:     true,
				slog.LevelDebug:          true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.env.set(t)

			handler, _ := newTestHandler(t, &powerslog.Options{
				Level:      tt.level,
				SampleRate: tt.sampleRate,
			})
			ctx := lambdaContext(t.Context(), tt.requestID, "")
			handlers := map[string]slog.Handler{
				"handler":   handler,
				"WithAttrs": handler.WithAttrs([]slog.Attr{slog.String("k", "v")}),
				"WithGroup": handler.WithGroup("g"),
			}
			for _, name := range slices.Sorted(maps.Keys(handlers)) {
				for level, want := range tt.enabled {
					if got := handlers[name].Enabled(ctx, level); got != want {
						t.Errorf("%s: Enabled(%v) = %v, want %v", name, level, got, want)
					}
				}
			}
		})
	}
}

func TestHandlerSamplingLogsDebug(t *testing.T) {
	lambdaEnv{}.set(t)
	powerslog.ResetColdStart()
	t.Cleanup(powerslog.ResetColdStart)

	handler, buf := newTestHandler(t, &powerslog.Options{
		SampleRate:  1,
		ReplaceAttr: removeTimeAttr(),
	})
	logger := slog.New(handler).WithGroup("g")
	ctx := lambdaContext(t.Context(), testRequestID, "")
	logger.DebugContext(ctx, "sampled")
	logger.Debug("not sampled")

	records := decodeLines(t, buf)
	want := []map[string]any{{
		"level":               "DEBUG",
		"message":             "sampled",
		"sampling_rate":       float64(1),
		"function_request_id": testRequestID,
		"function_arn":        testFunctionARN,
		"cold_start":          true,
	}}
	if !reflect.DeepEqual(records, want) {
		t.Errorf("got  %v\nwant %v", records, want)
	}
}

func TestHandlerSamplingIsDeterministic(t *testing.T) {
	lambdaEnv{}.set(t)

	opts := &powerslog.Options{SampleRate: 0.5}
	first := powerslog.NewHandler(io.Discard, opts)
	second := powerslog.NewHandler(io.Discard, opts).WithGroup("g")

	const n = 1000
	sampled := 0
	for i := range n {
		ctx := lambdaContext(t.Context(), fmt.Sprintf("request-%d", i), "")
		got := first.Enabled(ctx, slog.LevelDebug)
		if again := first.Enabled(ctx, slog.LevelDebug); again != got {
			t.Fatalf("request-%d: Enabled = %v then %v", i, got, again)
		}
		if other := second.Enabled(ctx, slog.LevelDebug); other != got {
			t.Fatalf("request-%d: Enabled = %v, another handler = %v", i, got, other)
		}
		if got {
			sampled++
		}
	}
	if sampled < n*4/10 || sampled > n*6/10 {
		t.Errorf("sampled %d of %d invocations at rate 0.5", sampled, n)
	}
}

func TestHandlerSamplingLevelVar(t *testing.T) {
	lambdaEnv{}.set(t)

	var level slog.LevelVar
	level.Set(slog.LevelError)
	handler, _ := newTestHandler(t, &powerslog.Options{Level: &level, SampleRate: 1})
	handler = handler.WithGroup("g")

	sampled := lambdaContext(t.Context(), testRequestID, "")
	if !handler.Enabled(sampled, slog.LevelDebug) {
		t.Error("sampled: Enabled(DEBUG) = false, want true")
	}
	if handler.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("Enabled(INFO) at ERROR = true, want false")
	}
	level.Set(slog.LevelInfo)
	if !handler.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("Enabled(INFO) after Set(INFO) = false, want true")
	}
	level.Set(powerslog.LevelTrace)
	if !handler.Enabled(t.Context(), powerslog.LevelTrace) {
		t.Error("Enabled(TRACE) after Set(TRACE) = false, want true")
	}
}

func BenchmarkHandler(b *testing.B) {
	// Lambda sets the trace header in the environment for every invocation.
	lambdaEnv{traceHeader: testTraceHeader}.set(b)
	powerslog.ResetColdStart()
	b.Cleanup(powerslog.ResetColdStart)

	logger := slog.New(powerslog.NewHandler(io.Discard, nil)).With(slog.String("user", "alice"))
	noCtx := b.Context()
	ctx := lambdaContext(noCtx, testRequestID, testTraceHeader)

	b.Run("with context", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			logger.LogAttrs(ctx, slog.LevelInfo, "hello", slog.Int("n", 1))
		}
	})
	b.Run("without context", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			logger.LogAttrs(noCtx, slog.LevelInfo, "hello", slog.Int("n", 1))
		}
	})
	b.Run("alternating", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			logger.LogAttrs(ctx, slog.LevelInfo, "hello", slog.Int("n", 1))
			logger.LogAttrs(noCtx, slog.LevelInfo, "hello", slog.Int("n", 1))
		}
	})
	b.Run("not logged", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			logger.LogAttrs(ctx, slog.LevelDebug, "hello", slog.Int("n", 1))
		}
	})
}
