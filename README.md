<p align="center">
  <img src="docs/assets/logo.svg" width="280" alt="di — a Go gopher joining two coral cable connectors" />
</p>

# di

[![CI](https://github.com/yandex/di/actions/workflows/ci.yml/badge.svg)](https://github.com/yandex/di/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/golang.yandex/di.svg)](https://pkg.go.dev/golang.yandex/di)
[![coverage](https://img.shields.io/badge/coverage-%E2%89%A5%2098%25-brightgreen)](https://yandex.github.io/di/coverage.html)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Awesome Go](https://raw.githubusercontent.com/floatdrop/awesome-go/main/badges/yandex--di.svg)](https://floatdrop.github.io/awesome-go/#yandex--di)

A dependency-injection container for Go 1.27+. Constructors are plain
functions, keys are Go types, and services are built, started and stopped
in dependency order. No code generation or dependencies outside the standard
library.

## Installation

```sh
go get golang.yandex/di
```

Requires Go 1.27 or newer. Editor support for generic methods needs gopls
v0.23 or newer.

## Quick start

A complete program using stand-in database and server types:

[embedmd]:# (examples/quickstart/main.go go)
```go
// Quick start: register a few services, start and stop the application.
package main

import (
	"context"
	"fmt"
	"log"

	"golang.yandex/di"
)

type Config struct{ DSN string }
type DB struct{ dsn string }
type Repo struct{ db *DB }
type Server struct{ repo *Repo }

// Plain constructors: their parameters are their dependencies.
func NewDB(cfg Config) *DB         { return &DB{dsn: cfg.DSN} }
func NewRepo(db *DB) *Repo         { return &Repo{db: db} }
func NewServer(repo *Repo) *Server { return &Server{repo: repo} }

func main() {
	app := di.New()

	app.Value(Config{DSN: "postgres://localhost/app"})

	app.Wire[*DB](NewDB).
		OnStop(func(ctx context.Context, db *DB) error { fmt.Println("db closed"); return nil })

	app.Wire[*Repo](NewRepo)

	app.Wire[*Server](NewServer).
		Eager().
		OnStart(func(ctx context.Context, srv *Server) error { fmt.Println("listening"); return nil }).
		OnStop(func(ctx context.Context, srv *Server) error { fmt.Println("server stopped"); return nil })

	ctx := context.Background()
	if err := app.Start(ctx); err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := app.Stop(ctx); err != nil {
			log.Println(err)
		}
	}()

	fmt.Println("serving", app.Get[*Server]().repo.db.dsn)
}
```

`Value` registers an existing value; `Wire` registers a constructor whose
parameters are its dependencies. Registrations are lazy singletons by default.
`Eager()` builds the server during `Start`, resolving its dependencies first.
`Stop` runs the stop hooks in reverse build order: server before database.

The source is [`examples/quickstart/main.go`](examples/quickstart/main.go).
For a long-running application, `Run` starts, waits for a signal or shutdown
request, then stops with a deadline; see the [HTTP server](examples/server/main.go).

## Capabilities

| | API |
|---|---|
| [Registration](docs/GUIDE.md#registering-and-resolving) | Plain constructors with `Wire`, scope-aware closures with `Provide`, existing values with `Value`. |
| [Scopes](docs/GUIDE.md#scopes) | Shared singletons or one instance per resolving scope with `Scoped()`. |
| [Lifecycle](docs/GUIDE.md#lifecycle) | Start, drain and stop hooks; background workers; signal handling with `Run`. |
| [Optional dependencies](docs/GUIDE.md#optional-dependencies) and [groups](docs/GUIDE.md#groups) | Declare optional parameters and group slices with `Needs`. |
| [Modules](docs/GUIDE.md#composing-modules) and [wrappers](docs/GUIDE.md#wrapping-a-service) | Compose registration functions with `Use`; decorate services with `Wrap`. |
| [Testing](docs/GUIDE.md#testing) | Fresh scopes with `di.Test` and explicit `Override()` registrations. |
| [Validation](docs/GUIDE.md#validate) and [inspection](docs/GUIDE.md#explain-graph-and-modules) | Check declared dependencies before construction; render dependency trees, Graphviz DOT and module reports. |
| [HTTP and gRPC](docs/GUIDE.md#request-scopes) | Request scopes, handler resolution and server lifecycle with [`dihttp`](dihttp/) and [`digrpc`](digrpc/). |
| [Observability](docs/GUIDE.md#observability) | Constructor and hook events with `Observe`; `log/slog` integration with [`dislog`](dislog/). |

`Wire` reads constructor signatures once with reflection, so `Validate` can
check their dependencies without building anything. `Provide` closures are
checked only when they run and are reported as unchecked by `Validate`.

## Documentation

- [Application guide](https://yandex.github.io/di/) — one application, file by file.
- [API guide](docs/GUIDE.md) — registration, lifetimes, lifecycle and edge cases.
- [Go reference](https://pkg.go.dev/golang.yandex/di) — types and methods.
- [How it works](docs/DESIGN.md) — resolution and teardown, with diagrams.
- [Examples](examples/) — compiled programs, including [HTTP](examples/server/),
  [gRPC](examples/grpc/) and the [guide application](examples/guide/).

## Performance

The [`benchmarks/`](benchmarks/) module compares the same four-service graph
on an Apple M3 Max:

| | Warm resolve | Cold register and build |
|---|---|---|
| `di`, `Provide` closure | 21 ns, 0 B, 0 allocs | 4.0 µs, 4.4 kB, 68 allocs |
| `di`, `Wire` | 21 ns, 0 B, 0 allocs | 5.1 µs, 5.4 kB, 86 allocs |
| `do` v2.1 | 134 ns, 160 B, 6 allocs | 7.0 µs, 11.2 kB, 120 allocs |
| `dig` v1.19 | 486 ns, 768 B, 24 allocs | 19.9 µs, 24.3 kB, 302 allocs |

**The `dig` warm figure measures reflective `Invoke`, not a typed accessor.**
An fx application normally invokes once at startup; the cold comparison is
the fairer one.

```sh
cd benchmarks && go test -bench . -benchmem
```

## Versioning

While the major version is 0, a minor bump may change behaviour.
[CHANGELOG.md](CHANGELOG.md) and the
[releases](https://github.com/yandex/di/releases) state whether an upgrade
can break a caller.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the CLA, tests and example-sync
instructions.

## License

[MIT](LICENSE)
