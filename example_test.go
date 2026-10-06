package powerslog_test

import (
	"log/slog"
	"os"

	"github.com/unfunco/powerslog"
)

func Example() {
	_ = os.Setenv("AWS_LAMBDA_FUNCTION_NAME", "example-logging-function")
	_ = os.Setenv("AWS_LAMBDA_FUNCTION_MEMORY_SIZE", "256")
	_ = os.Setenv("POWERTOOLS_SERVICE_NAME", "example-logging-service")

	// Remove the time from the output, this makes it easier to test.
	logger := slog.New(powerslog.NewHandler(os.Stdout, &powerslog.Options{
		Level:       slog.LevelDebug,
		ReplaceAttr: removeTimeAttr(),
	}))

	logger.Debug("This is a debug message!")
	logger.Info("This is an informational message!")
	logger.Warn("This is a warning message!")
	logger.Error("This is an error message!")

	// Output:
	// {"level":"DEBUG","message":"This is a debug message!","service":"example-logging-service","function_name":"example-logging-function","function_memory_size":256}
	// {"level":"INFO","message":"This is an informational message!","service":"example-logging-service","function_name":"example-logging-function","function_memory_size":256}
	// {"level":"WARN","message":"This is a warning message!","service":"example-logging-service","function_name":"example-logging-function","function_memory_size":256}
	// {"level":"ERROR","message":"This is an error message!","service":"example-logging-service","function_name":"example-logging-function","function_memory_size":256}
}

func removeTimeAttr() func(groups []string, a slog.Attr) slog.Attr {
	return func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == "timestamp" && len(groups) == 0 {
			return slog.Attr{}
		}
		return a
	}
}
