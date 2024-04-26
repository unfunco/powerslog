# Powerslog

## Getting started

### Requirements

- [Go] 1.22+

### Installation and usage

```go
package main

import (
	"log/slog"
	"os"

	"github.com/unfunco/powerslog"
)

func main() {
	jsonHandler := slog.NewJSONHandler(os.Stdout, nil)
	powerslogHandler := powerslog.NewHandler(jsonHandler, nil)
	logger := slog.New(powerslogHandler)

	logger.Info("Hello, world!")
}
```

## License

© 2024 [Daniel Morris]\
Made available under the terms of the [MIT License].

[daniel morris]: https://unfun.co
[go]: https://go.dev
[mit license]: LICENSE.md
