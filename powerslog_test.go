package powerslog_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"testing"
	"testing/slogtest"

	"github.com/unfunco/powerslog"
)

type lambdaEnv struct {
	functionName string
	memorySize   string
	serviceName  string
}

func (e lambdaEnv) set(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_LAMBDA_FUNCTION_NAME", e.functionName)
	t.Setenv("AWS_LAMBDA_FUNCTION_MEMORY_SIZE", e.memorySize)
	t.Setenv("POWERTOOLS_SERVICE_NAME", e.serviceName)
}

var fullLambdaEnv = lambdaEnv{
	functionName: "test-function",
	memorySize:   "128",
	serviceName:  "test-service",
}

func newTestHandler(t *testing.T, opts *slog.HandlerOptions) (slog.Handler, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return powerslog.NewHandler(slog.NewJSONHandler(&buf, opts)), &buf
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
		return powerslog.NewHandler(slog.NewJSONHandler(&buf, nil))
	}, func(t *testing.T) map[string]any {
		records := decodeLines(t, &buf)
		if len(records) != 1 {
			t.Fatalf("got %d records, want 1", len(records))
		}
		return records[0]
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
				"msg":                  "hello",
				"service":              "test-service",
				"function_name":        "test-function",
				"function_memory_size": float64(128),
			},
		},
		{
			name: "no environment variables set",
			env:  lambdaEnv{},
			want: map[string]any{
				"level": "INFO",
				"msg":   "hello",
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
				"msg":           "hello",
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
				"msg":                  "hello",
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
				"msg":                  "hello",
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

			handler, buf := newTestHandler(t, &slog.HandlerOptions{
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

			handler, _ := newTestHandler(t, &slog.HandlerOptions{Level: tt.level})
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
