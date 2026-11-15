# Running homed locally

How to build and run the hub (`homed`) and the TV plugin (`gpt-tv`) on a Mac, connect the relay, issue a person key
into the Keychain, call tools by hand, and start homed at login with launchd. Nothing here prints a secret.

Paths below assume the working copies in `/Users/devxpro/projects/family/`.

```sh
G=/Users/devxpro/projects/family/gpt-home
T=/Users/devxpro/projects/family/gpt-tv
D="$HOME/Library/Application Support/gpt-home"   # the default data dir on macOS
```

## 1. Build

```sh
make -C "$G" build    # → $G/bin/homed
make -C "$T" build    # → $T/bin/gpt-tv
"$G/bin/homed" version
```

## 2. The data dir

`homed` keeps everything in one directory (mode 0700). It defaults to `~/Library/Application Support/gpt-home` on macOS
and `$XDG_DATA_HOME/gpt-home` on Linux; every command takes `--data-dir D`, and `HOMED_DATA_DIR` overrides the default.

| File | What |
|---|---|
| `homed.yaml` | config: home name, listen address, relay domain, plugins, people and their key hashes (0600) |
| `relay-token` | the frps token (0600) |
| `instance-id` | random id sent as `X-GPT-Home-Instance`; the relay self-check looks for it |
| `homed.sock` | local admin socket of the running homed (0600); the CLI talks to it |
| `audit/audit-YYYY-MM.jsonl` | every tool call: who, via what, arguments with secrets redacted, outcome |
| `logs/<plugin>.log` | each plugin's stderr (rotated at 5 MB) |
| `plugins/<id>/data/` | each plugin's own data (`GPT_HOME_DATA_DIR`), e.g. gpt-tv's `tvs.json` with TV pairing keys |

`homed.yaml` is written by the CLI; hand edits are fine (comments survive CLI edits). Unknown keys are an error, so a
typo is caught at start. The home name has no command yet; set it by hand once:

```sh
mkdir -p "$D" && chmod 700 "$D"
grep -q '^home_name:' "$D/homed.yaml" 2>/dev/null || printf 'home_name: "Мой шалаш"\n' >> "$D/homed.yaml"
```

## 3. Connect the relay

The connect code goes through stdin, never through argv:

```sh
security find-generic-password -s gpt-home-relay-code -a roman -w | "$G/bin/homed" relay connect -
```

It writes `relay.domain` into `homed.yaml` and the token into `relay-token` (0600). A running homed needs a restart to
use a new relay.

## 4. Add the TV plugin

Flags go **before** `--`; everything after it is the plugin's command (a `--data-dir` there would be passed to the
plugin):

```sh
"$G/bin/homed" plugin add --id tv -- "$T/bin/gpt-tv" serve
"$G/bin/homed" plugin list
```

The executable is stored as an absolute path. Plugin changes apply when homed (re)starts. `homed plugin remove tv`
takes it out again (its data dir stays).

## 5. Run homed

In a terminal, in the foreground (logs go to stderr; Ctrl-C stops it cleanly):

```sh
"$G/bin/homed" run
```

Expect `homed listening addr=127.0.0.1:8080`, `plugin up plugin=tv`, `relay connected` within seconds and
`relay self-check passed` within 30 s. `HOMED_LOG_LEVEL=debug` adds detail. Only one homed can run per data dir: a
second one exits with "homed is already running".

In another terminal:

```sh
"$G/bin/homed" relay status     # connected since …, self-check ok at …
"$G/bin/homed" plugin list      # tv  up  <version>
"$G/bin/homed" tool list        # all tools, admin ones included
curl -sS -i https://homie.jobbyjobs.work/healthz | grep -i '^HTTP\|x-gpt-home-instance'
cat "$D/instance-id"            # equals the header above
```

## 6. Set up the TV by hand (admin tools through the socket)

`homed tool call <name> ['<json>']` calls any tool through the running homed as caller `admin`, via `cli`, and
writes the audit entry. It prints the tool's text, then its structured result. A tool that ran and failed prints its
message to stderr and exits with status 1.

```sh
"$G/bin/homed" tool call tv_discover
"$G/bin/homed" tool call tv_add '{"host":"192.168.68.66","name":"Living room TV"}'
"$G/bin/homed" tool call tv_pair '{"tv":"<id from tv_add>"}'    # someone presses OK on the TV within 3 minutes
"$G/bin/homed" tool call tv_list
```

## 7. Issue Roman's key straight into the Keychain

```sh
"$G/bin/homed" key new --person roman --name Roman --label "ChatGPT Site" --keychain gpt-home-key
```

This saves the key as Keychain item `gpt-home-key`, account `roman` (an existing item is replaced), and stores only its
sha256 in `homed.yaml`. The key reaches `/usr/bin/security` on stdin, so it is in no process list and never on the
terminal. Without `--keychain` the key is printed once to stdout (and only the key, so it can be piped).

A running homed picks up new and revoked keys at once: the CLI writes `homed.yaml` and asks the daemon over the socket
to reload people and keys. Hand edits of people or keys apply with `kill -HUP <pid>` (or the launchd line in §8).

```sh
"$G/bin/homed" key list                   # PERSON NAME PLUGINS KEY ID LABEL CREATED (the id is the start of the hash)
"$G/bin/homed" key revoke <key id prefix> # at least 6 characters, must match one key
```

`--plugins tv,kettle` limits what the person may use (default `*` for a new person; unchanged for an existing one).

Check the key end to end without printing it (curl reads the header from stdin):

```sh
security find-generic-password -s gpt-home-key -w | sed 's/^/Authorization: Bearer /' \
  | curl -sS -H @- https://homie.jobbyjobs.work/api/v1/tools | head -c 300; echo
```

The Site release (`site/release/release.mjs`) reads the same Keychain item.

## 8. Run at login with launchd

A LaunchAgent runs homed as Roman whenever he is logged in and restarts it if it dies. Save as
`~/Library/LaunchAgents/com.github.devxpro.gpt-home.homed.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.github.devxpro.gpt-home.homed</string>
	<key>ProgramArguments</key>
	<array>
		<string>/Users/devxpro/projects/family/gpt-home/bin/homed</string>
		<string>run</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>StandardOutPath</key>
	<string>/Users/devxpro/Library/Logs/gpt-home/homed.log</string>
	<key>StandardErrorPath</key>
	<string>/Users/devxpro/Library/Logs/gpt-home/homed.log</string>
</dict>
</plist>
```

```sh
L=com.github.devxpro.gpt-home.homed
mkdir -p ~/Library/Logs/gpt-home
plutil -lint ~/Library/LaunchAgents/$L.plist
# Stop a homed running in a terminal first: only one may run per data dir.
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/$L.plist   # start now and at every login
launchctl print gui/$(id -u)/$L | grep -E 'state|pid'               # running, pid …
launchctl kickstart -k gui/$(id -u)/$L                               # restart (after plugin or relay changes, or a rebuild)
launchctl kill HUP gui/$(id -u)/$L                                   # reload people and keys after hand edits
launchctl bootout gui/$(id -u)/$L                                    # stop and unload
tail -f ~/Library/Logs/gpt-home/homed.log
```

launchd gives the agent `HOME`, so the default data dir is the same as in a terminal. The log file is not rotated; it
stays small at the info level.

**Local Network permission.** macOS asks once whether homed may find devices on the local network, the first time the
TV plugin (a child of homed) talks to the LAN: discovery, wake, or any TV command. Click **Allow**. A homed started from
a terminal runs under the terminal's permission, so the prompt comes again the first time homed runs from launchd; a
rebuilt binary may also be asked again. If the prompt was missed or denied, LAN calls fail at once (tools report the TV
as offline): turn homed on in System Settings → Privacy & Security → Local Network, then `launchctl kickstart -k`.

## 9. When something is wrong

| Symptom | Look at |
|---|---|
| a tool says the plugin is not running | `homed plugin list` (state and last error), `$D/logs/tv.log` |
| `relay status` disconnected, `last error: … token …` | the code was rotated: run §3 again, restart |
| `last error: … x509 …` | the relay's certificate (Caddy and Let's Encrypt on the server) |
| `self-check: FAILED … another gpt-home hub` | a second homed is connected to the same relay |
| the Site says the home is offline | homed is not running, the Mac sleeps, or the relay is down |
| `421 misdirected request` | the request's Host is neither the relay domain nor `127.0.0.1:<port>`/`localhost:<port>` |
| `homed is not running` from a CLI command | start it (§5 or §8); the CLI needs the socket for tool, relay status and live plugin state |

## Development without a relay or a TV

Without `relay` in `homed.yaml` homed serves only on `127.0.0.1`. The test plugins work as plugins:

```sh
W=/tmp/homed-dev
go -C "$G" build -o $W/homed ./cmd/homed
go -C "$G" build -o $W/echo ./pluginkit/example            # plugin id "echo"
go -C "$G" build -o $W/fake ./internal/plugins/testdata/fakeplugin   # plugin id "fake"
$W/homed plugin add --data-dir $W/data --id echo -- $W/echo serve
$W/homed run --data-dir $W/data --listen 127.0.0.1:18080 &
KEY=$($W/homed key new --data-dir $W/data --person dev)
printf 'Authorization: Bearer %s\n' "$KEY" | curl -sS -H @- http://127.0.0.1:18080/api/v1/tools
$W/homed tool list --data-dir $W/data
```

Keep `--data-dir` paths short: the socket path must fit in 103 bytes.

`--relay-server-addr host:port` and `--relay-ca-file ca.pem` point `homed run` at a local relay test rig (see
`docs/research/frp-embedding.md`, "Local acceptance run"); never use them against the real relay.
