# Powerslog

[![CI](https://github.com/unfunco/powerslog/actions/workflows/ci.yaml/badge.svg)](https://github.com/unfunco/powerslog/actions/workflows/ci.yaml)
[![License: MIT](https://img.shields.io/badge/License-MIT-purple.svg)](https://opensource.org/licenses/MIT)

A [slog] handler that enriches structured logs with key fields captured from an
AWS Lambda context, to produce logs equivalent to those produced by the
AWS Lambda Powertools packages for [Python], [TypeScript], and [.NET].

## Getting started

### Requirements

- [AWS Command Line Interface] 2+
- [Go] 1.22+
- [SAM Command Line Interface] 1.115+

### Installation and usage

Powerslog is compatible with modern Go releases in module mode.
With Go installed, the following command will resolve and add the package to the
current development module, along with its dependencies.

```bash
go get github.com/unfunco/powerslog
```

Alternatively, the same can be achieved using import in a package and running
the `go get` command without arguments.

```go
import "github.com/unfunco/powerslog"
```

```go
powerslogHandler := powerslog.NewHandler(os.Stdout, nil)
logger := slog.New(powerslogHandler)
```

```go
ctx := context.WithValue(context.Background(), "logger", logger)
lambda.StartWithOptions(handler, lambda.WithContext(ctx))
```

```go
logger := ctx.Value("logger").(*slog.Logger)
logger.Info("Hello, world!")
```

```json
{
  "level": "INFO",
  "timestamp": "2024-04-20T16:20:56.666902747Z",
  "message": "Hello, world!",
  "function_name": "powerslog-test-TestFunction-yjKUabwI3fNE",
  "function_memory_size": 128
}
```

#### AWS Lambda Advanced Logging Controls

With [AWS Lambda Advanced Logging Controls], the output format can be set to
either TEXT or JSON and the minimum accepted log level can be specified.
Regardless of the output format setting in Lambda, log messages will always be
emitted as JSON.

#### Environment variables

### Development and testing

```bash
cd lambda
GOOS=linux GOARCH=arm64 go build -tags lambda.norpc -o bootstrap main.go
sam deploy --guided
```

## License

© 2024 [Daniel Morris]\
Made available under the terms of the [MIT License].

[.net]: https://docs.powertools.aws.dev/lambda/dotnet/
[aws command line interface]: https://aws.amazon.com/cli/
[aws lambda advanced logging controls]: https://docs.aws.amazon.com/lambda/latest/dg/monitoring-cloudwatchlogs.html#monitoring-cloudwatchlogs-advanced
[daniel morris]: https://unfun.co
[go]: https://go.dev
[mit license]: LICENSE.md
[python]: https://docs.powertools.aws.dev/lambda/python/latest/
[sam command line interface]: https://aws.amazon.com/serverless/sam/
[slog]: https://pkg.go.dev/log/slog
[typescript]: https://docs.powertools.aws.dev/lambda/typescript/latest/
