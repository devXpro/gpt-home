# Embedding the frp client in homed (T0.3)

Verdict: **embedding works.** `github.com/fatedier/frp/client` v0.72.0 runs in-process with the settings of PLAN §5.5:
wss on 443, the outer TLS verified, token auth, endless retries and one HTTP proxy. It was tested against the real
`ghcr.io/fatedier/frps:v0.72.0` image, behind an in-process TLS terminator and behind real Caddy 2.11.4 with
`tls internal`. No external `frpc` binary is needed. `homed` is 23 MB (frp brings quic-go, kcp, wireguard and netlink
code even when unused).

The code is in `internal/relay`. All source references are to `/Users/devxpro/go/pkg/mod/github.com/fatedier/frp@v0.72.0`.

## How the client is started

```go
src := source.NewConfigSource()
src.ReplaceAll([]v1.ProxyConfigurer{httpProxy}, nil)
svc, _ := client.NewService(client.ServiceOptions{
	Common:                 common,                         // *v1.ClientCommonConfig
	ConfigSourceAggregator: source.NewAggregator(src),      // required since the config-source refactor
	UnsafeFeatures:         security.NewUnsafeFeatures(nil),
	ConnectorCreator:       wrapDefaultConnector,           // optional; used to observe sessions
})
err := svc.Run(ctx) // blocks until ctx is done
```

This mirrors `cmd/frpc/sub/root.go` (`runClientWithAggregator`, `startServiceWithAggregator`), minus the TOML loading and
`log.InitLogger`. No admin web server (`webServer.port = 0`) and no metrics server are started.

`Service.Run` details (`client/service.go`):
- The first login loops with backoff 1 s × 2 up to 10 s. With `loginFailExit = false` it never gives up.
- If ctx is cancelled before the first login succeeds, `Run` returns `login to the server failed: …` and not nil.
  `relay.Client.Run` treats any return after ctx is done as a clean stop.
- After a session ends, `keepControllerWorking` logs in again: three fast retries within a minute (200 ms), then
  exponential backoff up to 20 s. The same run id is reused.
- `Service.Close`/`GracefulClose` exist, but cancelling the ctx given to `Run` is enough.

Process-wide side effects of importing `frp/client`: `init()` sets `golib/crypto.DefaultSalt = "frp"` and two QUIC env
vars. frp also logs through one global logger. Hence one `relay.Client` per process at a time.

## Settings (PLAN §5.5 → frp)

| §5.5 | frp field | Note |
|---|---|---|
| `serverAddr = <domain>`, `serverPort = 443` | `ServerAddr`, `ServerPort` | `Config.ServerAddr` (tests only) replaces only the dial address |
| `transport.protocol = "wss"` | `Transport.Protocol` | outer TLS + websocket to `wss://<tls server name>/~!frp` |
| outer TLS verified | `Transport.TLS.UseSystemCa = true`, or `Transport.TLS.TrustedCaFile` | see the trap below |
| TLS name | `Transport.TLS.ServerName = <domain>` | also the websocket Host (`DialHookWebsocket(…, tlsConfig.ServerName)`) |
| token auth | `Auth.Method = "token"`, `Auth.Token` | the token is read by homed from `relay-token` (0600) |
| `loginFailExit = false` | `LoginFailExit = &false` | the default is true |
| one proxy | `v1.HTTPProxyConfig{Name:"homed", Type:"http", LocalIP:"127.0.0.1", LocalPort, CustomDomains:[domain]}` | |

Defaults worth knowing (`pkg/config/v1/client.go`, `Complete`): `tcpMux = true` (one yamux session over the websocket;
yamux keepalive every 30 s; app-level heartbeats are off), `poolCount = 1`, `dialServerTimeout = 10 s`, and
`proxyURL` defaults to `$http_proxy`. With wss, `transport.tls.enable` only means "TLS on"; there is no second, inner
TLS layer and the custom first byte is not sent (`client/connector.go`, `realConnect`, `case "wss"`).

Settings are checked with `validation.ValidateAllClientConfig` before starting, so a bad config fails fast.

## The TLS trap: frp does not verify by default

`pkg/transport/tls.go`, `newClientTLSConfig`:

| `UseSystemCa` | `TrustedCaFile` | Result |
|---|---|---|
| false | empty | **`InsecureSkipVerify = true`** (frp's default) |
| true | empty | system roots (platform verifier on macOS) |
| false | set | **only** the CA file |
| true | set | system roots plus the CA file |

`relay` always sets exactly one trust source: the CA file when `Config.CAFile` is set (test harnesses), else the system
roots. `checkVerifies` then rebuilds the TLS config with frp's own `transport.NewClientTLSConfigWithSystemCA`, the call
`realConnect` makes, and refuses to start if `InsecureSkipVerify` would be true or the server name is empty.
`TestCheckVerifiesRejectsFrpDefault` pins the trap. The integration test proves that a wrong CA never connects:
`connect to server error: tls: failed to verify certificate: x509: certificate signed by unknown authority`.

## Observing login, reconnects and errors

frp has no callbacks for these. Three hooks are combined:

1. **Proxy state (polled every 500 ms).** `svc.StatusExporter().GetProxyStatus("homed")` returns the proxy phase. It is
   `running` only after a successful login and NewProxy exchange. When the control connection ends, frp's proxy manager
   drops every proxy (`client/proxy/proxy_manager.go`, `Close`), so the lookup fails. A proxy start error (for example a
   domain already taken on the server) shows in `WorkingStatus.Err`.
2. **Session start and end (pushed).** `ServiceOptions.ConnectorCreator` wraps `client.NewConnector`. frp makes one
   connector per login attempt, opens it, and closes it when the session ends (`Control.closeSession`). Generation
   counters ("the newest opened connector is not closed") mark a disconnect at once. Polling alone missed a reconnect
   that took 5 ms.
3. **Errors (log bridge).** `frplog.Logger` (`pkg/util/log`) is replaced once in `init` with a logger whose output
   implements golib's `log.Writer` (`WriteLog(p, level, time)`). Every frp line goes to homed's slog
   (`component=frp`, `src=<file:line>`). Warn and error lines from `client/…` files become `Status.LastError`, which
   keeps login failures such as `connect to server error: token in login doesn't match token from configuration`, TLS
   failures and `bad status` (the proxy answered the websocket upgrade with an error). frps' own lines, when frps runs
   in the same process, are not taken.

`Status{Connected, Since, LastError}`: `Connected` is "session alive and proxy running"; `Since` moves on every change of `Connected`; `LastError` is kept after a later success. No line seen contains the token, and the acceptance run
grepped the logs to confirm it.

frps logs the client's `os.Hostname()` on every login (for example `hostname [MacBook-Pro-Roman]`); frp sends it in the
login message and has no setting to hide it.

## Other findings for the next tasks

- **go-sdk DNS-rebinding guard.** `mcp.StreamableHTTPHandler` answers 403 when the local address is loopback and the
  Host is not (`streamable.go`, `ServeHTTP`). Every relayed request is exactly that: frp dials `127.0.0.1:8080` with
  Host `homie.jobbyjobs.work`. The spike sets `DisableLocalhostProtection: true`. **T1.2:** do the same, and instead
  accept only Host = the relay domain or loopback in the gateway itself.
- **Caddy reload and upstream pooling.** With real Caddy, `caddy reload --force` did not drop the channel
  (`stream_close_delay 30m`). Restarting frps made frpc see `bad status` (Caddy 502) once, and it logged in again 2 s
  later. With Caddy's upstream pool warmed by vhost requests first, the frpc websocket still connected at once: the
  `/~!frp` upgrade did not land on a pooled vhost connection.
- **Offline detection.** With no frpc connected, frps answers every request with `custom404Page` and status 404,
  without `X-GPT-Home-Instance`. `relay.SelfCheck` reports this as "home is offline".

## API of `internal/relay` (for T1.3)

```go
type Config struct { Domain, Token string; LocalPort int; CAFile, ServerAddr string }
type Status struct { Connected bool; Since time.Time; LastError string }
type Client struct{ … }                                         // zero value ready; one Run at a time
func (c *Client) Run(ctx context.Context, cfg Config, log *slog.Logger) error // blocks, reconnects; nil after ctx done
func (c *Client) Status() Status
func SelfCheck(ctx context.Context, domain, instanceID string) error          // system roots
func (c Config) SelfCheck(ctx context.Context, instanceID string) error       // honours CAFile and ServerAddr
type Code struct { Domain, Token string }
func ParseCode(s string) (Code, error)
func FormatCode(c Code) (string, error)
const ProxyName = "homed"; const InstanceHeader = "X-GPT-Home-Instance"
```

## Local acceptance run (what T0.3 ran on this Mac)

Real Caddy (`tls internal`) and frps with T0.1's `relay/frps.toml`, `relay/offline.html` and `relay/Caddyfile.tmpl`;
homed on the host. Docker objects are named `gpthome-t03-*`.

```sh
W=/tmp/gpthome-t03-acc; R=/Users/devxpro/projects/family/gpt-home/relay
mkdir -p $W/frp $W/data; cp $R/frps.toml $R/offline.html $W/frp/
TOK=$(openssl rand -hex 32); printf '%s\n' "$TOK" > $W/frp/token; chmod 644 $W/frp/*
sed 's/__DOMAIN__/homie.test/; s/gpt-home-frps:7000/gpthome-t03-frps:7000/' $R/Caddyfile.tmpl \
  | sed 's/^homie.test {/homie.test {\n\ttls internal/' > $W/Caddyfile
docker network create gpthome-t03-net
docker run -d --name gpthome-t03-frps --network gpthome-t03-net -v $W/frp:/etc/frp:ro \
  ghcr.io/fatedier/frps:v0.72.0 -c /etc/frp/frps.toml
docker run -d --name gpthome-t03-caddy --network gpthome-t03-net -p 127.0.0.1:18443:443 \
  -v $W/Caddyfile:/etc/caddy/Caddyfile:ro caddy:2.11.4-alpine
docker cp gpthome-t03-caddy:/data/caddy/pki/authorities/local/root.crt $W/root.crt   # once Caddy made it

go -C /Users/devxpro/projects/family/gpt-home build -o $W/homed ./cmd/homed
printf 'ghr1.%s\n' "$(printf '{"v":1,"domain":"homie.test","token":"%s"}' "$TOK" | base64 | tr '+/' '-_' | tr -d '=\n')" \
  | $W/homed relay connect --data-dir $W/data -
HOMED_SPIKE_KEY=spike-test-key $W/homed run --spike --data-dir $W/data --listen 127.0.0.1:18080 \
  --relay-ca-file $W/root.crt --relay-server-addr 127.0.0.1:18443 &

c() { curl -sS --resolve homie.test:18443:127.0.0.1 --cacert $W/root.crt "$@"; }
c -i https://homie.test:18443/healthz                                     # 200 {"ok":true} + x-gpt-home-instance
c -H 'Authorization: Bearer spike-test-key' https://homie.test:18443/api/v1/tools                 # 200, home_ping
c -X POST -H 'Authorization: Bearer spike-test-key' -d '{}' https://homie.test:18443/api/v1/tools/home_ping  # pong
# cleanup: kill homed; docker rm -f gpthome-t03-caddy gpthome-t03-frps; docker network rm gpthome-t03-net; rm -rf /tmp/gpthome-t03-acc
```

Results on 2026-11-15: connected in 0.5 s; `/healthz` 200 with the instance header; `/api/v1/tools` 401 without the key
and 200 with it; `home_ping` 200 `{"message":"pong","at":…}`; `/mcp` 404 without the flag; with
`--spike-authless-mcp`, MCP `initialize` and `tools/call home_ping` worked through Caddy; the Caddy reload kept the
channel; restarting frps reconnected in 2 s; SIGTERM stopped homed cleanly; the token appeared in no log.

## Commands for the orchestrator against the real domain (T0.5)

Prerequisites: T0.2 done (relay on jobby, connect code in Keychain item `gpt-home-relay-code`), and the spike key in
Keychain item `gpt-home-spike-key`. Nothing below prints a secret.

```sh
G=/Users/devxpro/projects/family/gpt-home
go -C $G build -o $G/bin/homed ./cmd/homed

# 1. Store the relay (domain → homed.yaml, token → relay-token 0600). The code goes through stdin, not argv.
security find-generic-password -s gpt-home-relay-code -w | $G/bin/homed relay connect -

# 2. Run the spike (path B). Keep this terminal open; logs go to stderr.
HOMED_SPIKE_KEY="$(security find-generic-password -s gpt-home-spike-key -w)" $G/bin/homed run --spike
#    expect within seconds: msg="relay connected"; within 30 s: msg="relay self-check passed"

# 3. From outside (or the Mac itself): health and instance id, then the façade.
curl -sS -i https://homie.jobbyjobs.work/healthz | grep -i '^HTTP\|x-gpt-home-instance'
cat "$HOME/Library/Application Support/gpt-home/instance-id"          # must equal the header above
curl -sS -o /dev/null -w '%{http_code}\n' https://homie.jobbyjobs.work/api/v1/tools   # 401 without a key
curl -sS -H "Authorization: Bearer $(security find-generic-password -s gpt-home-spike-key -w)" \
  -X POST -d '{}' https://homie.jobbyjobs.work/api/v1/tools/home_ping                  # {"message":"pong",…}

# 4. Offline check: stop homed (Ctrl-C), then
curl -sS -o /dev/null -w '%{http_code}\n' https://homie.jobbyjobs.work/healthz          # 404 (offline page)

# 5. Path A test only: restart with the authless MCP endpoint, and remove the flag right after the test.
HOMED_SPIKE_KEY="$(security find-generic-password -s gpt-home-spike-key -w)" $G/bin/homed run --spike --spike-authless-mcp
curl -sS -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -X POST https://homie.jobbyjobs.work/mcp \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"home_ping","arguments":{}}}'
```

If step 2 logs `connect to server error: …`, `Status.LastError` holds the same text. `x509` means certificate trouble
(check that Caddy serves a Let's Encrypt certificate for the domain), `token in login doesn't match` means a wrong or
rotated code (run step 1 again), and `bad status` means Caddy could not reach frps.
