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
