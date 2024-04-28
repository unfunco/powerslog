# Powerslog

A slog handler that captures key fields from an AWS Lambda context and produces
structured logs with the same fields as the Powertools loggers for Python and
TypeScript.

## Getting started

### Requirements

- [Go] 1.22+

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

func handler(ctx context.Context, event events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
    logger := ctx.Value("logger").(*slog.Logger)
    logger.Info("Request received", slog.Any("event", event))

    return events.APIGatewayProxyResponse{
        StatusCode: http.StatusNoContent,
    }, nil
}

func main() {
    jsonHandler := slog.NewJSONHandler(os.Stdout, nil)
    powerslogHandler := powerslog.NewHandler(jsonHandler)
    logger := slog.New(powerslogHandler)

    ctx := context.WithValue(context.Background(), "logger", logger)
    lambda.StartWithOptions(handler, lambda.WithContext(ctx))
}
```

### Development and testing

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
