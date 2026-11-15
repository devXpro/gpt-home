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

### Relay docker smoke test (`relay/test/run.sh`)

Covers the server side of PLAN §5.5 with real software: `install/server.sh` (files, token, connect code, rerun keeps the
token, `--rotate-token`, behind-proxy files), then the standalone compose stack (Caddy `tls internal` for the fake domain
`homie.test` + frps) and a real `frpc` v0.72.0 over `wss` on 443. It asserts, all through Caddy on one domain and one
frps port: GET and POST reach the echo backend; the channel survives 3 forced Caddy reloads (also with
`grace_period 3s`), while the same reload without `stream_close_delay` drops it (control); with frpc stopped the bilingual
offline page comes back with 404; frpc reconnects by itself; a wrong token and an untrusted CA are rejected. Everything is
named `gpthome-test-*` and removed on exit (about 80 s).

```sh
bash relay/test/run.sh
shellcheck -x install/server.sh relay/test/run.sh
```

Prerequisites: Docker with compose v2 and the images `ghcr.io/fatedier/frps:v0.72.0`, `ghcr.io/fatedier/frpc:v0.72.0`,
`caddy:2.11.4-alpine`, `alpine:3.21`, `curlimages/curl:8.11.1` (pulled on first run), Go (builds `relay/test/echo`),
openssl. No root, no network except image pulls, nothing on the host ports.

### Relay client (`internal/relay`)

Unit tests (no network, no Docker): connect code round trip and every parse error (tokens never in errors); the frp
client settings of PLAN §5.5 (wss on 443, TLS name = domain, system roots or only the CA file, `loginFailExit = false`,
one `http` proxy `homed` → `127.0.0.1:<port>`); the guard that refuses frp's default of not verifying the server
certificate; the frp log bridge; the real embedded client retrying against a closed port and stopping cleanly;
`SelfCheck` against an `httptest` TLS server (our id, another hub, not a hub, home offline, untrusted certificate).
(The phase 0 spike package and its tests were removed in T1.3; `homed relay connect` is covered by the homed suite.)

```sh
go test -race ./internal/relay/...
```

Integration test (Docker): the embedded client against the real `ghcr.io/fatedier/frps:v0.72.0` with the §5.5 config,
behind an in-process TLS terminator for `homie.test` with a throwaway CA. It asserts the offline 404 page before
connecting and after stopping, requests reaching the local hub, `SelfCheck`, an automatic reconnect after the channel is
cut (with `Status.Since` moving), and that an untrusted certificate and a wrong token never connect and show up in
`Status.LastError`. The frps container is named `gpthome-t03-frps-*` and removed at the end (about 3 s).

```sh
go test -race -tags integration -run Integration ./internal/relay/
```

The manual check behind real Caddy is in `docs/research/frp-embedding.md` ("Local acceptance run"); since T1.3 run
`homed run` without `--spike` there and call a plugin tool with a person key instead of `home_ping`.

### pluginkit and the plugin host (`pluginkit/`, `internal/plugins/`)

`pluginkit` is tested in memory against a go-sdk client: identity, instructions, tool `_meta`, the caller's request
`_meta`, the tool name rule, `DataDir` and `Serve` shutdown. The host tests build two real plugins in `TestMain`
(`internal/plugins/testdata/fakeplugin` and `pluginkit/example`) and run them as subprocesses: listing and audiences,
`_meta` parsing (timeouts, inventory, bad values), invalid names skipped, calls with caller `_meta`, `isError`, the call
timeout, a crash with last-known tools and `ErrPluginDown`, restart with backoff, the failing state, a missing binary,
`list_changed` → `Changed()` (on the 2026-07-28 protocol and on the legacy 2025-11-25 handshake), the stderr log (ring
and rotated file), the minimal env and data dir, and shutdown (SIGTERM, and SIGKILL for a plugin that ignores it).
About 5 s; no network.

```sh
go test -race ./pluginkit/... ./internal/plugins/...
```

### homed and its config (`cmd/homed`, `internal/config`)

`internal/config`: loading and validating `homed.yaml` (unknown keys, loopback-only listen, relay domain, plugin and
person ids, scopes, key hashes, duplicates), `Update` rewriting only changed top-level sections (comments and untouched
sections survive; failed updates write nothing; 0600), the relay token (0600, loose modes refused), the instance id
and the data dir resolution.

`cmd/homed`: the CLI (`relay connect` incl. stdin, `key new/list/revoke`, `key new --keychain` against a fake
`security` that must receive the key on stdin and never in argv, `plugin add/list/remove`, flags after positionals,
commands needing a running homed), and the Host allowlist (relay domain with any port, `127.0.0.1`/`localhost`/`[::1]`
with our port, 421 otherwise). `TestDaemon` is the T1.3 acceptance: it builds the fake plugin, runs `serve` in-process
with a relay pointed at a closed local port, then: `key new` on the running homed (picked up at once over the socket),
`GET /api/v1/tools` with that key (assistant tools of the person's scope only), the relay Host passing and a foreign
Host getting 421, `tool list`/`tool call` through the socket (admin tools included, caller `admin` via `cli`, a failing
tool exits 1, unknown tool, non-object arguments), `plugin list`, `relay status`, a second homed refused on the same data
dir, `key revoke` taking effect at once, a clean shutdown removing the socket, and the audit lines with redaction. The
tests set `HOMED_DATA_DIR` to a temp dir, so nothing touches the real data dir; data dirs live under `/tmp` because a
unix socket path must fit in 103 bytes. About 2 s; no network, no Docker.

```sh
go test -race ./cmd/homed/... ./internal/config/...
```

### SQLite store (`internal/store`)

Covers the T2.1 implementation of the `store.Store` contract on throw-away databases in `t.TempDir()` with a fake clock:
file mode 0600 (also `-wal` and `-shm`), WAL, foreign keys and busy timeout, embedded migrations (run once, newer schema
refused), every repository (people, passkeys, sessions, person keys, invites, connections, plugins, audit, settings) with
its errors (`ErrNotFound`, `ErrConflict`, `ErrLastPasskey`, `ErrTokenExpired`, `ErrTokenUsed`), cascade delete of a
person, audit filters, cursor paging, limit caps and prune, `InTx` (commit, rollback, nesting, the first-run flow), the
races that must have one winner (the last two passkeys, one setup token), concurrent writers, and `ImportPhase1`
(owner first, once per database, merge into an existing home, bad input changes nothing). No network, no Docker.

```sh
go test -race ./internal/store/...
```

### Plugin installer and homed self-update (`internal/install`)

Runs against an httptest TLS fake of the GitHub Releases API and the asset host (no real network, no Docker): release
resolving (latest, by version, `v` and bare tags, prerelease and draft handling, rate limit and 404 mapping), asset
choice by the GoReleaser name (incl. armv7), checksum verification (mismatch, missing file, missing line), the full
install and update flows with their job steps, start failure with rollback, same-version replacement, pruning and
removal, the size limits (advertised and streamed), https-only incl. a redirect to plain http, the token going only to
the API host and never into errors, and the archive guard (`..`, absolute and backslash names, escaping symlinks, hard
links, devices, duplicates, entry and size caps, a gzip bomb, mode normalization). The homed self-update replaces a
fake executable in a temp dir (swap, `.prev` backup, rollback, failing self-test, read-only directory, symlinked
executable). About 2 s.

```sh
go test -race ./internal/install/...
```

- The plugin host tests also cover translated tool texts and progress (`TestToolI18n`, `TestCallProgress`, `TestParseI18n`, `TestDeliverProgress`; T2.7).

### Admin API backend (`internal/admin`)

Runs the admin JSON API (T2.3) on `httptest` servers for the web and the socket handler, with in-memory fakes of the
store, the plugin host, auth (cookie → principal), the relay, the installer and the key resolver; no network, no
Docker. Covers the error shape and common headers, JSON 404/405, auth levels (public, session, owner, member 403, socket
principal), the CSRF rule (header, Origin, Sec-Fetch-Site; socket exempt), content type and size limits, step-up
(missing, expired, per session; socket exempt), key create/revoke with the resolver updated before the answer and no
secret in lists or audit, passkeys and sessions of "me", tools/plugins/inventory cache/logs, admin tool calls (schema
validation into `fields`, 409 `call_running`, progress with `message_i18n`, `wait=1`, cancel, audit with redaction),
audit paging with filter-bound cursors, relay state and the domain-change rule, jobs (409 `job_running`, step reports,
coded failures), and SSE (hello, replay after `Last-Event-ID`, `resync` for another run or an id that left the
500-event ring, member filtering, `plugin.log`, `plugin`, `tools`, `audit`, `session.revoked` on revoke and on expiry).
About 2 s.

```sh
go test -race ./internal/admin/...
```

### Passkeys, sessions and the first-run page (`internal/auth`, `internal/firstrun`)

Runs real WebAuthn ceremonies (go-webauthn) against a virtual authenticator (`github.com/descope/virtualwebauthn`,
EC2 passkeys, synced and hardware-key flags) on an in-memory store (`internal/auth/authtest`) and a fake clock; no
network, no Docker, no browser. `internal/auth`: setup link → registration → Me and the `__Host-gh_session` cookie →
discoverable sign-in → logout; an imported owner, a family invite, renames and the home name; registration and sign-in
rejected for a wrong origin (incl. http), a wrong or parent RP ID, missing user verification or presence, bad backup
flags, unknown passkeys and user handles; replayed and expired ceremonies, an answer to another challenge, a ceremony
finished at the wrong endpoint; setup tokens single use (also two registrations racing), expired (also between begin
and finish, with rollback), revoked by a newer link, unknown; step-up (5-minute window, renewal, another person's
passkey, another session's ceremony), add passkey behind step-up (`excludeCredentials`, `passkey_exists`), clone
detection by the sign counter, idle and absolute session expiry, touch throttling and event streams, the hourly sweep,
the CSRF rule, request format errors, the rate limits (per client from the Caddy hop of `X-Forwarded-For`, in total,
`Retry-After`), `homed admin setup-link` and `reset-access` over the local handler, and the name, slug, device-label
and credential-storage helpers. `internal/firstrun`: the PIN gate (cookie only hashed, 30 minutes), five wrong PINs →
a new PIN printed and the lock with `Retry-After`, the relay code step (bad codes, no token in errors, `Connect`),
`relay_not_ready` until connected with a passing self-check, link → passkey registration end to end → every path
`410 setup_done`, the Host guard against DNS rebinding, CSRF, routes, the SSE `relay` stream, and `Serve` printing
the URL and PIN. About 3 s.

```sh
go test -race ./internal/auth/... ./internal/firstrun/...
```
