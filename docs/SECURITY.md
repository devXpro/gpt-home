# Security model

What each part of GPT-Home can see and do, what it trusts, and what follows from that. Phase 1 state (relay, hub,
person keys, plugins over stdio); the admin and passkeys arrive in phase 2 and are described as designed.

```
ChatGPT / a Site ──https──▶ relay server (Caddy: TLS ends here) ──▶ frps ◀──wss── homed (127.0.0.1) ──stdio──▶ plugins ──LAN──▶ devices
```

## Secrets and what they unlock

| Secret | Where it lives | Who holds it | What it unlocks |
|---|---|---|---|
| Person key (`ghk_…`) | Only its SHA-256 in `homed.db` (phase 1: `homed.yaml`); the key itself in the person's ChatGPT Site settings (or the macOS Keychain) | The person's Site | The assistant tools of the plugins that person may use, through the REST façade and `/mcp`. |
| Relay token | `relay-token` (0600) in the home data dir; `token` (0600) on the server; inside the connect code | The home box and the server | Registering the domain on frps, i.e. **being** the home as seen from the internet. |
| Passkeys (phase 2) | The admin's devices; public keys at home | The admin | Admin actions, with a fresh assertion (step-up) for dangerous ones. |
| TV client key | `tvs.json` in the gpt-tv plugin data dir | gpt-tv | Controlling that TV over the LAN. |

### The relay token is as powerful as the person keys

Anyone with the relay token (or the connect code, which contains it) can log in to frps and register the domain
whenever the home box is not connected — when it sleeps, reboots or loses its network. From then on:

- every request the Sites send, **bearer person keys included**, reaches them instead of the home;
- they can serve their own pages on the home's origin (`https://<domain>`), e.g. a fake admin login;
- the real home box cannot register the domain again until they disconnect (frps keeps the first registration).

So keep the token and the connect code as secret as the person keys: never paste them into a chat or a ticket, use
`homed relay connect -` (stdin) instead of putting the code on the command line, and **rotate** the token
(`install/server.sh --rotate-token`, then `homed relay connect -` at home) whenever it may have leaked. After a
suspected leak, also issue new person keys (`homed key new` / `homed key revoke`), since they may have been captured.

What a token holder still cannot do: open TCP or UDP listeners on the relay (`allowPorts` only offers frps's own,
taken port; see `docs/RELAY.md`), reach the home box (it accepts no inbound connections), or read anything stored at
home.

## The relay server

- **TLS ends at the server.** Caddy holds the certificate, so the server (and whoever runs it) can read every request
  and response: person keys in `Authorization` headers, tool arguments and results, admin pages. Run the relay on a
  server you control, and do not put a CDN that terminates TLS in front of it.
- **It holds no home secrets.** No person keys (only what passes through), no passkeys, no plugins, no database. Its
  only secret is the relay token.
- **Passkeys are verified at home** (phase 2). The server cannot forge an assertion, so it cannot perform admin actions
  on its own. It can, however, serve modified pages on the origin while an admin is using it, so a compromised relay
  server is a compromised admin session: the admin trusts the server operator as much as the domain.
- frps publishes no port; it is reachable only from Caddy (or your proxy) inside the docker network.

## The hub (`homed`)

- **Loopback only.** It listens on `127.0.0.1` (or `localhost`); other addresses are refused. The relay client forwards
  to `127.0.0.1:<port>`. A Host allowlist (the relay domain and the hub's own loopback address) answers anything else
  with 421, which is the DNS-rebinding guard.
- **Person keys.** 32 random bytes, stored as SHA-256 hex, compared in constant time. Each person sees only the
  assistant tools of the plugins they may use; a hidden tool and a missing one both answer 404. 120 calls per minute
  per key.
- **Audit.** Every call through every surface is logged. Argument values of keys matching
  `(?i)(token|key|secret|password|pin|code)` become `"[redacted]"`. Schema-validation errors quote the rejected values,
  so the audit keeps only "invalid arguments" for them; the caller still gets the full message.
- **A bad plugin tool cannot take the API down.** A tool whose schema the MCP server refuses is skipped with a warning
  when the plugin lists it and again when the gateway builds its tool set; the plugin's other tools keep working.
- **Local admin socket.** `<data_dir>/homed.sock`, 0600, and homed accepts only connections from its own OS user
  (peer credentials). Whoever can open it is the hub's owner: it serves the whole admin API (every tool, admin ones
  included) without a key, a cookie or step-up, and it makes setup links (`homed admin setup-link`).
- **Data dir.** Created 0700. Secret files in it are 0600, and `relay-token` is refused when group or others can read
  it. homed never changes the mode of an existing directory it did not create (a mistaken `--data-dir ~` must not lock
  up a home directory); it only tightens its platform default dir and warns about any other dir open to others.
- **One instance per data dir.** `homed run` holds an `flock` on `<data_dir>/.homed.lock` for its whole life; a second
  one is refused and never touches the first one's socket.

## Plugins

Plugins are separate executables that the hub starts with a minimal environment and talks to over stdio. What that
does and does not isolate:

- **A plugin runs as the same OS user as homed. It is fully trusted.** It can read everything that user can:
  `homed.db` (key hashes, sessions, the audit log), **`relay-token`**, the admin socket, other plugins' data dirs, the
  user's files and, on a Mac, Keychain items that user can open without a prompt. Install only plugins whose authors
  you trust as much as the hub itself. Their per-plugin data dir (`GPT_HOME_DATA_DIR`, 0700) is for tidiness, not a
  sandbox.
- Plugins never receive the callers' keys, and they are not reachable from the network through the hub: the hub talks
  to them over stdio only. They may make their own network connections (gpt-tv talks to TVs on the LAN).
- The request `_meta` `gpt-home/caller` and `gpt-home/via` are informational; plugins must not base security
  decisions on them.

## The home LAN (gpt-tv)

- LG webOS 3.x speaks SSAP over plain `ws://` on port 3000, so the TV client key travels **in clear** on the LAN;
  anyone who can sniff the LAN can capture it and control the TV. See `gpt-tv/docs/families/lg-webos.md`.
- Discovery fetches the UPnP description only from the IP that answered the SSDP search and follows no redirects; the
  SSAP websocket dial follows no redirects either.
- Wake-on-LAN packets are broadcast on the LAN; they carry only the TV's MAC address.

## Reporting a problem

Open a GitHub issue without details for anything that could be abused, and ask for a private channel; do not post
tokens, keys or connect codes anywhere.
