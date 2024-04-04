package powerslog_test

import (
	"log/slog"
	"os"

	"github.com/unfunco/powerslog"
)

func Example_withTextHandler() {
	_ = os.Setenv("AWS_LAMBDA_FUNCTION_NAME", "example-text-logging-function")
	_ = os.Setenv("AWS_LAMBDA_FUNCTION_MEMORY_SIZE", "128")
	_ = os.Setenv("POWERTOOLS_SERVICE_NAME", "example-text-logging-service")

	// Create a new TextHandler that writes to stdout but does not include the
	// time, this makes it easier to test the output.
	textHandler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: removeTimeAttr(),
	})

	powerslogHandler := powerslog.NewHandler(textHandler)
	logger := slog.New(powerslogHandler)

	logger.Debug("This will not appear since the default level is INFO")
	logger.Info("This is informational!")
	logger.Warn("This is a warning!")
	logger.Error("This is an error!")

	// Output:
	// level=INFO msg="This is informational!" service=example-text-logging-service function_name=example-text-logging-function function_memory_size=128
	// level=WARN msg="This is a warning!" service=example-text-logging-service function_name=example-text-logging-function function_memory_size=128
	// level=ERROR msg="This is an error!" service=example-text-logging-service function_name=example-text-logging-function function_memory_size=128
}

func Example_withJSONHandler() {
	_ = os.Setenv("AWS_LAMBDA_FUNCTION_NAME", "example-json-logging-function")
	_ = os.Setenv("AWS_LAMBDA_FUNCTION_MEMORY_SIZE", "256")
	_ = os.Setenv("POWERTOOLS_SERVICE_NAME", "example-json-logging-service")

	// Create a new JSONHandler that writes to stdout but does not include the
	// time, this makes it easier to test the output.
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: removeTimeAttr(),
	})

	powerslogHandler := powerslog.NewHandler(jsonHandler)
	logger := slog.New(powerslogHandler)

	logger.Debug("This is a debug message!")
	logger.Info("This is an informational message!")
	logger.Warn("This is a warning message!")
	logger.Error("This is an error message!")

	// Output:
	// {"level":"DEBUG","msg":"This is a debug message!","service":"example-json-logging-service","function_name":"example-json-logging-function","function_memory_size":256}
	// {"level":"INFO","msg":"This is an informational message!","service":"example-json-logging-service","function_name":"example-json-logging-function","function_memory_size":256}
	// {"level":"WARN","msg":"This is a warning message!","service":"example-json-logging-service","function_name":"example-json-logging-function","function_memory_size":256}
	// {"level":"ERROR","msg":"This is an error message!","service":"example-json-logging-service","function_name":"example-json-logging-function","function_memory_size":256}
}

func removeTimeAttr() func(groups []string, a slog.Attr) slog.Attr {
	return func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey && len(groups) == 0 {
			return slog.Attr{}
		}
		return a
	}
}
