# Powerslog

## Getting started

### Requirements

- [Go] 1.22+

### Installation and usage

```go
package main

import (
  "log/slog"

  "github.com/unfunco/powerslog"
)

func main() {
  logHandler := powerslog.NewHandler(nil)
  logger := slog.New(logHandler)
  logger.Info("Hello, world!")
}
```

## License

© 2024 [Daniel Morris]\
Made available under the terms of the [MIT License].

[daniel morris]: https://unfun.co
[go]: https://go.dev
[mit license]: LICENSE.md
