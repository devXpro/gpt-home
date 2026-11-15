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
make site-test   # node --test "site/core/*.test.mjs" (no-op until site/ exists)
```

The quotes matter: Node 22+ takes a glob after `--test`, and a bare directory such as `site/core` is not run.

Typecheck, lint and the release dry run (the Site's own checks; they need `npm ci` first):

```sh
npm --prefix site ci
npm --prefix site run check   # unit tests + tsc --noEmit
npm --prefix site run lint
GPT_HOME_SITE_HOME=/tmp/gpt-home-site-dryrun node site/release/release.mjs --dry-run --title Test --slug test
```

The dry run stages the source, builds it and packs the archive in `$GPT_HOME_SITE_HOME` (default `~/.gpt-home-site`); it
makes no ChatGPT or Codex call. Never run `release.mjs` without `--dry-run` as a test: it publishes to a real account.

## Web tests

```sh
make web   # no-op until web/ exists
```

## Suites

<!-- Append one section per suite below: what it covers, the command, prerequisites. -->

### Site template (`site/core`)

Covers `site/core/mcp.mjs`, the thin MCP proxy between ChatGPT and the hub's REST façade (PLAN §5.4, §5.7): protocol
negotiation, the tool-list fingerprint, forwarding with the bearer key, and the error mapping (401, 404 unknown tool,
422 tool failure, non-JSON 404 = home offline, 429, 503, 504, timeouts, network errors). No network, no account.

```sh
make site-test
```

### Gateway, keys and audit (`internal/gateway`, `internal/keys`, `internal/audit`)

Covers PLAN §5.4 with `httptest` and a fake `ToolHost`: bearer auth (missing, wrong, right), per-person plugin scopes on
the list and on calls, every row of the REST status table, the `/api/v1/tools` golden manifest
(`internal/gateway/testdata/tools.golden.json`, checked against the keys `librus-gpt/site/core/mcp.mjs` reads), MCP
list and call through a go-sdk client over HTTP, the `serverInfo.version` fingerprint following tool changes, the
per-key rate limit (fake clock), audit entries and redaction, key generation and constant-time matching, and the JSONL
audit files (monthly names, 0600, drop-on-overflow). No network.

```sh
go test -race ./internal/gateway/... ./internal/keys/... ./internal/audit/...
go test ./internal/gateway/ -run TestManifestGolden -update   # rewrite the golden after an intended change
```
