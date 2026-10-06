package powerslog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
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
	functionName string
	memorySize   string
	serviceName  string
	traceHeader  string
}

func (e lambdaEnv) set(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", e.functionName)
	t.Setenv("AWS_LAMBDA_FUNCTION_MEMORY_SIZE", e.memorySize)
	t.Setenv("POWERTOOLS_SERVICE_NAME", e.serviceName)
	t.Setenv("_X_AMZN_TRACE_ID", e.traceHeader)
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

func TestHandlerConformance(t *testing.T) {
	fullLambdaEnv.set(t)

	var buf bytes.Buffer
	slogtest.Run(t, func(*testing.T) slog.Handler {
		buf.Reset()
		return powerslog.NewHandler(&buf, nil)
	}, func(t *testing.T) map[string]any {
		records := decodeLines(t, &buf)
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
		// slogtest looks up the built-in slog keys.
		record := records[0]
		for from, to := range map[string]string{"timestamp": slog.TimeKey, "message": slog.MessageKey} {
			if v, ok := record[from]; ok {
				record[to] = v
				delete(record, from)
			}
		}
		return record
	})
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
