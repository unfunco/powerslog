# Powerslog

A slog handler that captures key fields from an AWS Lambda context and produces
structured logs with the same fields as the Powertools loggers for Python and
TypeScript.

## Getting started

### Requirements

- [Go] 1.27+

### Installation and usage

```go
package main

import (
    "context"
    "log/slog"
    "net/http"
    "os"

    "github.com/aws/aws-lambda-go/events"
    "github.com/aws/aws-lambda-go/lambda"
    "github.com/unfunco/powerslog"
)

func newHandler(logger *slog.Logger) func(context.Context, events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
    return func(ctx context.Context, event events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
        logger.InfoContext(ctx, "Request received",
            slog.String("method", event.HTTPMethod),
            slog.String("resource", event.Resource),
        )

        return events.APIGatewayProxyResponse{
            StatusCode: http.StatusNoContent,
        }, nil
    }
}

func main() {
    logger := slog.New(powerslog.NewHandler(os.Stdout, nil))

    lambda.Start(newHandler(logger))
}
```

The per-invocation fields are read from the context, so use the `*Context`
logging methods, such as `InfoContext`, with the context passed to the function
handler.

### Fields

| Key                    | Source                                    |
|------------------------|-------------------------------------------|
| `timestamp`            | The time of the record, in UTC            |
| `level`                | The level of the record                   |
| `message`              | The message of the record                 |
| `location`             | The log statement, with `AddSource`       |
| `service`              | `POWERTOOLS_SERVICE_NAME`                 |
| `function_name`        | `AWS_LAMBDA_FUNCTION_NAME`                |
| `function_memory_size` | `AWS_LAMBDA_FUNCTION_MEMORY_SIZE`         |
| `sampling_rate`        | The sample rate, when sampling is enabled |
| `function_request_id`  | The Lambda context                        |
| `function_arn`         | The Lambda context                        |
| `cold_start`           | The Lambda context                        |
| `xray_trace_id`        | The Lambda context, or `_X_AMZN_TRACE_ID` |

As with the handlers in `log/slog`, duplicate keys are not removed, so avoid
adding top-level attributes with these names, or add them to a group.

`cold_start` is true for the first invocation whose context reaches the
handler, so it is only reliable when the first invocation makes at least one
`*Context` logging call, at any level.

### Configuration

The handler is configured with `powerslog.Options` and the same environment
variables as the Powertools loggers.

| Environment variable            | Description                           |
|---------------------------------|---------------------------------------|
| `AWS_LAMBDA_LOG_LEVEL`          | Level, overrides `Options.Level`      |
| `POWERTOOLS_LOG_LEVEL`          | Level, when `Options.Level` is nil    |
| `POWERTOOLS_LOGGER_SAMPLE_RATE` | Sample rate, when `SampleRate` is 0   |
| `POWERTOOLS_SERVICE_NAME`       | Value of the `service` field          |

The level is one of `TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`, `FATAL` or
`SILENT`, and defaults to `INFO`. The sample rate is the probability, from 0 to
1, that an invocation also logs its `DEBUG` records.

### Development and testing

```bash
go test -race ./...
```

The `lambda` directory contains a function for testing in AWS.

```bash
cd lambda
GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bootstrap main.go
sam deploy --guided
```

## License

© 2024 [Daniel Morris]\
Made available under the terms of the [MIT License].

[daniel morris]: https://unfun.co
[go]: https://go.dev
[mit license]: LICENSE.md
