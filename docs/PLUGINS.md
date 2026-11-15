# Writing a gpt-home plugin

A plugin gives the hub (`homed`) one family of devices: TVs, a kettle, a robot vacuum. It is a separate executable,
written in any language, that speaks [MCP](https://modelcontextprotocol.io) over stdio. The hub starts it, lists its
tools, and calls them on behalf of the family's assistants. Plugins are never reachable from the network: only the hub
talks to them.

Go authors get a small helper package, `github.com/devXpro/gpt-home/pluginkit`, built on the official
[go-sdk](https://github.com/modelcontextprotocol/go-sdk) v1.8.0. A complete example lives in
[`pluginkit/example`](../pluginkit/example/main.go).

## Process

- The hub runs `<command> [args…]`; the default argument list is `["serve"]`.
- The working directory is the plugin's data directory.
- The environment is minimal: `PATH`, `HOME`, `LANG` and `TZ` from the hub (when set), plus:

  | Variable | Meaning |
  |---|---|
  | `GPT_HOME_PLUGIN_ID` | the plugin id, e.g. `tv` |
  | `GPT_HOME_DATA_DIR` | persistent per-plugin directory, mode 0700; keep your state here |
  | `GPT_HOME_LOG_LEVEL` | `debug`, `info`, `warn` or `error` |
  | `GPT_HOME_HUB_VERSION` | the hub's version |

  Nothing else is passed through, so a plugin never sees the hub's secrets.
- stdin and stdout carry MCP: newline-delimited JSON-RPC. Use the protocol version your SDK negotiates with go-sdk
  v1.8.0; both the 2026-07-28 protocol (`server/discover`) and the older `initialize` handshake (2025-11-25 and
  earlier) work.
- **stderr is your log.** The hub keeps the last 1000 lines in memory and writes everything to
  `<hub data dir>/logs/<id>.log` (rotated at 5 MB, three files). Never write logs to stdout: it would break the protocol.

## Identity

- Plugin id: `^[a-z][a-z0-9]{1,15}$`, e.g. `tv`, `kettle`. The person installing the plugin chooses it; it must be the
  prefix of every tool name.
- In the `initialize` (or `server/discover`) result:
  - `serverInfo.name`: the repo or binary name, e.g. `gpt-tv`;
  - `serverInfo.title`: a human name, e.g. `TV`;
  - `serverInfo.version`: semver.
- Optional `instructions`: short English guidance for the model. The hub merges them into its own instructions.

## Tools

- Name: `^<id>_[a-z][a-z0-9_]*$`, e.g. `tv_power_on`. The hub skips any other name and logs a warning.
- `description`: English, written for an LLM, one or two sentences, with units and ranges stated
  ("Sets the volume, 0 to 100 percent.").
- `inputSchema` is a JSON Schema object; `outputSchema` is optional.
- Annotations follow MCP: `readOnlyHint: true` on every tool that changes nothing; `destructiveHint` and
  `idempotentHint` where they apply.
- Tool `_meta` keys the hub understands (all optional):

  | Key | Value | Effect |
  |---|---|---|
  | `gpt-home/audience` | `"admin"` | hidden from assistants; only the admin UI and the `homed` CLI see it. Default `"assistant"`. Any other value is treated as `"admin"`. |
  | `gpt-home/timeout` | seconds (number) | the hub-side call timeout. Default 30, maximum 300 (larger values are capped). Pairing flows use 180. |
  | `gpt-home/role` | `"inventory"` | marks the device list. Only on a `readOnlyHint` tool, at most one per plugin (the hub keeps the first by name). |
  | `gpt-home/i18n` | `{"<lang>": {"title", "description", "properties": {"<input property>": "<label>"}}}` | translated texts for the admin UI; see below. |

  The inventory tool returns `{"items":[{"id","name","subtitle"?,"state"?}]}` as `structuredContent`, where `state` is
  one of `on|off|offline|unknown|paired|unpaired`. The admin renders it as the device list.

### Translations (`gpt-home/i18n`)

The admin UI is in Russian and English. A tool can carry its title, description and input labels in other languages:

```json
"_meta": {"gpt-home/i18n": {"ru": {"title": "Громкость", "description": "Задаёт громкость динамиков, 0–100 %.",
                                   "properties": {"tv": "Телевизор", "percent": "Громкость, %"}}}}
```

- Keys are language tags (`ru`, `pt-BR`); every field is optional.
- Write for a person, not a model: short, plain words, like a phone's settings screen ("Подключить телевизор", not
  "Выполнить сопряжение устройства").
- Only the admin UI uses them. Assistants always get the English `title` and `description`, so keep those complete.
- The hub is lenient: a malformed language or field is skipped with a warning in the hub log, the rest is kept.

### Device arguments (`x-gpt-home-ref`)

An input property whose value is a device id from the plugin's inventory tool says so in its schema:

```json
"tv": {"type": "string", "description": "The TV's id or name, as tv_list shows it.", "x-gpt-home-ref": "inventory"}
```

The admin then shows a device picker for it instead of a text field, and opens the tool from that device's screen.
Without the keyword the hub guesses (a required string property named like the plugin id, `device` or `item`).

## Calls

- Every `tools/call` carries request `_meta` `{"gpt-home/caller":"<person name>","gpt-home/via":"site|mcp|admin|cli"}`.
  Log it if useful; **never trust it for security**.
- Results:
  - text content: short, English, human ("Living room TV is on.");
  - optional `structuredContent`;
  - a tool that ran but failed (device unreachable, not paired) returns `isError: true` with a text that says what to
    do ("The TV did not wake up. Turn on Mobile TV On in the TV settings.");
  - a JSON-RPC error means the call could not run at all: bad arguments, an internal error.
- When the hub's timeout expires it sends `notifications/cancelled`; stop the work when your context is cancelled.

### Progress

A long-running tool (pairing, a network search) can tell the person what is happening. When the admin runs a tool, the
hub sends a `progressToken` in the request `_meta`; the tool may then send MCP `notifications/progress` for it:

```json
{"method": "notifications/progress", "params": {"progressToken": "gpt-home-7", "progress": 1,
  "message": "Press OK on the TV remote", "_meta": {"gpt-home/i18n": {"ru": "Нажмите OK на пульте телевизора"}}}}
```

- `progress` grows with every report; `total` is optional (omit it when unknown).
- `message` is short English for a person; the notification `_meta` `gpt-home/i18n` carries its translations.
- Calls from assistants carry no token: send nothing then (`pluginkit.Progress` does that check for you).
- The admin shows the latest report until the result arrives. A report sent together with the result may be dropped,
  so put the outcome in the result, not in a last report.

## Lifecycle

- The hub calls `tools/list` after the handshake and again on `notifications/tools/list_changed`, so a plugin can add
  or remove tools at run time (go-sdk sends the notification by itself on `AddTool` and `RemoveTools`).
- A crashed plugin is restarted with backoff 1, 2, 4… up to 60 s. After 5 crashes within 5 minutes its state becomes
  `failing` and it is retried every 5 minutes. A run that stays up for a minute resets both.
- While a plugin is down, the hub keeps listing its last known tools; calls to them fail with "plugin unavailable"
  until it is back.
- Shutdown: the hub closes the session (your stdin reaches EOF), sends SIGTERM, and SIGKILL 5 s later. Exit promptly on
  either.

## A minimal Go plugin

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/devXpro/gpt-home/pluginkit"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type boilIn struct {
	Celsius int `json:"celsius" jsonschema:"target temperature, 40 to 100 °C"`
}

type pairIn struct {
	Kettle string `json:"kettle"`
}

func main() {
	s := pluginkit.New(pluginkit.Plugin{
		ID: "kettle", Name: "gpt-kettle", Title: "Kettle", Version: "0.1.0",
		Instructions: "Boil water only when the user asks for it.",
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "kettle_boil",
		Description: "Heats the kettle to the given temperature, 40 to 100 °C.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in boilIn) (*mcp.CallToolResult, any, error) {
		person, via := pluginkit.Caller(req) // for the log only
		slog.Info("boil", "celsius", in.Celsius, "caller", person, "via", via)
		// ... talk to the kettle; on failure return IsError with a hint:
		// return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{
		// 	Text: "The kettle is offline. Check that it is plugged in."}}}, nil, nil
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "The kettle is heating."}}}, nil, nil
	})

	// Admin-only, with a longer timeout, Russian texts and a device argument; the helpers chain.
	mcp.AddTool(s, pluginkit.I18n(pluginkit.Admin(pluginkit.Timeout(&mcp.Tool{
		Name:        "kettle_pair",
		Description: "Pairs a kettle. Hold its button for 5 seconds first.",
		InputSchema: map[string]any{"type": "object", "required": []string{"kettle"}, "properties": map[string]any{
			"kettle": pluginkit.InventoryRef(map[string]any{"type": "string", "description": "The kettle's id."}),
		}},
	}, 3*time.Minute)), "ru", pluginkit.Text{
		Title:       "Подключить чайник",
		Description: "Зажмите кнопку на чайнике на 5 секунд.",
		Properties:  map[string]string{"kettle": "Чайник"},
	}), func(ctx context.Context, req *mcp.CallToolRequest, in pairIn) (*mcp.CallToolResult, any, error) {
		_ = pluginkit.Progress(ctx, req, 1, 0, "Hold the kettle button", map[string]string{"ru": "Зажмите кнопку на чайнике"})
		dir, err := pluginkit.DataDir() // GPT_HOME_DATA_DIR, or ./data in development
		if err != nil {
			return nil, nil, err
		}
		_ = dir // ... pair in.Kettle and store the pairing here
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Kettle paired."}}}, nil, nil
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := pluginkit.Serve(ctx, s) // returns nil on SIGTERM or when the hub closes stdin
	stop()
	if err != nil {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
```

### pluginkit reference

| Function | Does |
|---|---|
| `New(Plugin) *mcp.Server` | a go-sdk server with `serverInfo` and `instructions` set and a stderr logger at `GPT_HOME_LOG_LEVEL`; panics on an invalid id |
| `Admin(t)` | sets `_meta "gpt-home/audience": "admin"` |
| `Timeout(t, d)` | sets `_meta "gpt-home/timeout"` to `d` in whole seconds, rounded up |
| `Inventory(t)` | sets `_meta "gpt-home/role": "inventory"` (add `ReadOnlyHint: true` yourself) |
| `I18n(t, lang, Text{Title, Description, Properties})` | adds one language to `_meta "gpt-home/i18n"`; call once per language |
| `InventoryRef(prop)` | sets `"x-gpt-home-ref": "inventory"` on a `map[string]any` property schema (for a `*jsonschema.Schema`: `Extra[pluginkit.SchemaRef] = pluginkit.RefInventory`) |
| `Progress(ctx, req, progress, total, message, i18n)` | sends `notifications/progress` when the hub asked for it; a no-op otherwise |
| `DataDir()` | `GPT_HOME_DATA_DIR`, or `./data` in development; created with mode 0700; absolute |
| `Caller(req)` | the person and surface from the request `_meta`; empty outside the hub |
| `Serve(ctx, s)` | MCP over stdio until ctx is done or stdin closes; both return nil |
| `CheckToolName(id, tool)` | the tool name rule; the hub uses the same function |

The `Meta*`, `Env*`, `SchemaRef` and `RefInventory` constants name the protocol keys and variables above.

On the hub side, `plugins.WithProgress(ctx, func(plugins.Progress))` makes `Host.Call` send the progress token and
pass every report (progress, total, message and its translations) to the callback while the call runs.

## Trying a plugin

```sh
go build -o bin/gpt-echo ./pluginkit/example
homed plugin add --id echo -- "$PWD/bin/gpt-echo" serve
homed tool list
homed tool call echo_say '{"text":"hello"}'
```

## Release assets (phase 2 installs)

- GoReleaser archives `<binary>_<version>_<os>_<arch>.tar.gz` plus `checksums.txt`.
- `gpt-home-plugin.json`: `{"id","title","description","binary","args":["serve"],"minHubVersion","homepage"}`.
