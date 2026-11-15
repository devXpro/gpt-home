# Testing

Every suite is listed here with the exact command to run it. Each task appends its own suite.

## Prerequisites

- Go (see `go.mod`), golangci-lint v2.12.2, Node 22+ (only for `web/` and `site/`), Docker (only for integration tests).

## Go unit tests and lint

```sh
make lint   # golangci-lint run ./...
make test   # go test -race ./...
```

## Integration tests

Tests that need Docker or the LAN are behind the build tag `integration`:

```sh
go test -race -tags integration ./...
```

## Site tests

```sh
make site-test   # node --test site/core (no-op until site/ exists)
```

## Web tests

```sh
make web   # no-op until web/ exists
```

## Suites

<!-- Append one section per suite below: what it covers, the command, prerequisites. -->
