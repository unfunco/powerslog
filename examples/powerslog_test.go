package powerslog_test

import (
	"log/slog"
	"os"

	"github.com/unfunco/powerslog"
)

func Example_withJSONHandler() {
	_ = os.Setenv("AWS_LAMBDA_FUNCTION_NAME", "example-json-logging-function")
	_ = os.Setenv("AWS_LAMBDA_FUNCTION_MEMORY_SIZE", "256")
	_ = os.Setenv("POWERTOOLS_LOG_LEVEL", "DEBUG")
	_ = os.Setenv("POWERTOOLS_SERVICE_NAME", "example-json-logging-service")

	powerslogHandler := powerslog.NewHandler(os.Stdout, nil)
	logger := slog.New(powerslogHandler)

	logger.Debug("This is a debug message!")
	logger.Info("This is an informational message!")
	logger.Warn("This is a warning message!")
	logger.Error("This is an error message!")

	// Output:
	// {"level":"DEBUG","message":"This is a debug message!","service":"example-json-logging-service","function_name":"example-json-logging-function","function_memory_size":256}
	// {"level":"INFO","message":"This is an informational message!","service":"example-json-logging-service","function_name":"example-json-logging-function","function_memory_size":256}
	// {"level":"WARN","message":"This is a warning message!","service":"example-json-logging-service","function_name":"example-json-logging-function","function_memory_size":256}
	// {"level":"ERROR","message":"This is an error message!","service":"example-json-logging-service","function_name":"example-json-logging-function","function_memory_size":256}
}
