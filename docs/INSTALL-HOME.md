# Install the home hub

`homed` is the hub: one binary that runs at home, keeps a single outbound connection to your relay, serves the admin,
holds your passkeys, people, keys and audit log, and runs the device plugins. This page covers where it can run, how
to install it, and the first five minutes after that.

You need a relay first ([INSTALL-SERVER.md](INSTALL-SERVER.md)) and its **connect code** (`ghr1.…`).

- [Where the hub can run](#where-the-hub-can-run)
- [Install](#install): [macOS](#macos) · [Raspberry Pi and Linux](#raspberry-pi-and-linux) · [Docker (NAS)](#docker-nas) · [from source](#from-source)
- [Connect the relay](#connect-the-relay)
- [First run: create your passkey](#first-run-create-your-passkey)
- [Add your first device](#add-your-first-device)
- [Running as a service](#running-as-a-service)
- [Updates](#updates)
- [Uninstall](#uninstall)
- [Backups](#backups)

## Where the hub can run

The hub must be **always on** (ChatGPT cannot reach a sleeping home) and **on the same network as your devices**
(plugins find them with multicast and wake them with broadcast packets). It needs outbound HTTPS and nothing inbound.

| Box | Notes |
|---|---|
| **A Mac that stays on** | Apple Silicon or Intel, recent macOS. A Mac mini or an iMac is ideal; a laptop works while it is awake and online. Runs as a LaunchAgent in your login session. |
| **Raspberry Pi 5, 4, 3 or Zero 2 W** | the best 24/7 home box: silent, a few watts. Use **Raspberry Pi OS Lite (64-bit)**; the 32-bit OS works too (armv7). The original Pi Zero and Pi 1 (ARMv6) are not supported. Prefer Ethernet for the TV's sake. |
| **Any Linux box** | amd64, arm64 or armv7 with systemd: a mini PC, an old laptop, a home server. |
| **A NAS with Docker** | Synology, QNAP, Unraid, TrueNAS and the like, with **host networking**. |

Not supported: Docker Desktop on macOS or Windows (its "host network" is a Linux VM, not your LAN, so devices cannot
be found or woken), and Windows itself.

| How | For | Updates |
|---|---|---|
| [**One-command installer**](#macos) `install/home.sh` | macOS, Raspberry Pi, Linux | the admin, or rerun the installer |
| [**Homebrew**](#homebrew) | macOS (and Linux with Homebrew) | `brew upgrade` |
| [**Docker image**](#docker-nas) | NAS, any Docker host on Linux | `docker pull` |
| [**From source**](#from-source) | development | `git pull` and rebuild |

## Install

### macOS

```sh
curl -fsSL https://raw.githubusercontent.com/devXpro/gpt-home/main/install/home.sh | bash
```

Run it as yourself, not with `sudo`. It downloads the latest release for your Mac, checks its SHA-256 against the
release's `checksums.txt`, and installs:

| What | Where |
|---|---|
| the program | `~/Library/Application Support/gpt-home/bin/homed` |
| the data dir | `~/Library/Application Support/gpt-home` |
| the LaunchAgent | `~/Library/LaunchAgents/com.github.devxpro.gpt-home.homed.plist` (starts at login, restarts after a crash or an update) |
| the log | `~/Library/Logs/gpt-home/homed.log` |

It starts the hub and prints the first-run page's address and PIN. `homed` is not put on your `PATH`; the commands on
this page use a variable for it:

```sh
H="$HOME/Library/Application Support/gpt-home/bin/homed"
"$H" version
```

Options (after `bash -s --`): `--version vX.Y.Z`, `--prefix DIR`, `--no-start`, `--dry-run`, `--uninstall`,
`--purge`, `--yes`. Example: `curl -fsSL …/home.sh | bash -s -- --dry-run`.

**Keep the Mac awake.** System Settings → Energy (or Battery → Options on a laptop) → *Prevent automatic sleeping when
the display is off*. A sleeping Mac means "home is offline" for everyone.

**Local Network permission.** The first time a plugin talks to your LAN (finding or waking the TV), macOS asks
whether *homed* may find devices on your local network. Click **Allow**. Missed it? System Settings → Privacy &
Security → Local Network → turn on homed, then
`launchctl kickstart -k gui/$(id -u)/com.github.devxpro.gpt-home.homed`.

#### Homebrew

An alternative to the installer — use one or the other, not both:

```sh
brew tap devxpro/gpt-home https://github.com/devXpro/gpt-home
brew install gpt-home
brew services start gpt-home
homed version
```

`homed` is on your `PATH`, the data dir is the same, the log is `$(brew --prefix)/var/log/gpt-home/homed.log`.
Update with `brew upgrade gpt-home`: the admin's self-update would replace the file inside the Cellar and the next
`brew upgrade` would put the brew version back.

### Raspberry Pi and Linux

**Preparing a Pi** (skip for any other Linux box): flash **Raspberry Pi OS Lite (64-bit)** with
[Raspberry Pi Imager](https://www.raspberrypi.com/software/). In its settings set a hostname (`homepi`), your user,
your Wi-Fi if there is no cable, and enable SSH. Boot, then `ssh you@homepi.local`.

```sh
curl -fsSL https://raw.githubusercontent.com/devXpro/gpt-home/main/install/home.sh | sudo bash
```

It detects the CPU (amd64, arm64 or armv7), verifies the release checksum and installs:

| What | Where |
|---|---|
| the program | `/usr/local/lib/gpt-home/homed` |
| the `homed` command | `/usr/local/bin/homed` — a small wrapper that runs the CLI as the service user, with `sudo` when needed |
| the system user | `gpt-home` (no login shell) |
| the data dir | `/var/lib/gpt-home` (mode 0700, owned by `gpt-home`) |
| the service | `/etc/systemd/system/gpt-home.service`: starts at boot, `Restart=always`, sandboxed (`ProtectSystem=strict`, no new privileges, no capabilities), LAN access kept for discovery and Wake-on-LAN |

It starts the service and prints the first-run page's address and PIN.

```sh
systemctl status gpt-home
homed version
```

Local changes to the unit go in `sudo systemctl edit gpt-home`; the installed file is rewritten on every update.

### Docker (NAS)

```sh
docker run -d --name gpt-home --restart unless-stopped --network host \
  -v gpt-home:/data ghcr.io/devxpro/gpt-home:latest
```

Or with Compose:

```yaml
# docker-compose.yml
services:
  gpt-home:
    image: ghcr.io/devxpro/gpt-home:latest
    container_name: gpt-home
    restart: unless-stopped
    network_mode: host
    volumes:
      - gpt-home:/data

volumes:
  gpt-home:
```

```sh
docker compose up -d
docker exec gpt-home homed admin firstrun      # the first-run page's address and PIN
```

Images exist for `linux/amd64`, `linux/arm64` and `linux/arm/v7`.

- **`--network host` is required.** Devices are found with SSDP multicast and woken with UDP broadcasts; neither
  crosses Docker's bridge network, so a bridged container sees no TV and cannot wake one.
- **Data** lives in the `/data` volume, owned by uid `10001`. With a bind mount instead of a named volume, give the
  directory to that uid: `sudo chown 10001:10001 /volume1/docker/gpt-home`.
- **Ports.** The hub listens on `127.0.0.1:8080` of the host and the first-run page on `:8090`. If the NAS already
  uses 8080, move the hub, and its health check with it:

  ```yaml
      command: ["run", "--listen", "127.0.0.1:18080"]
      healthcheck:
        test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:18080/healthz"]
  ```
- **CLI:** every `homed` command on this page runs as `docker exec gpt-home homed …`; the ones that read stdin need
  `-i`: `docker exec -i gpt-home homed relay connect -`.
- **Updates** come from a new image (`docker compose pull && docker compose up -d`); the admin's self-update cannot
  replace the read-only binary inside the image.

### From source

For development, or a platform without a release. Needs Go 1.26 (the `go` command fetches go1.26.11 by itself) and
Node 22+ for the admin.

```sh
git clone https://github.com/devXpro/gpt-home.git
make -C gpt-home build          # builds the admin, then gpt-home/bin/homed with it embedded
gpt-home/bin/homed version
gpt-home/bin/homed run          # foreground; Ctrl-C stops it
```

To run it as a service, use the LaunchAgent in [DEV.md](DEV.md#10-run-at-login-with-launchd) or the systemd unit in
[`install/systemd/gpt-home.service`](../install/systemd/gpt-home.service). Running homed locally for development,
without a relay: [DEV.md](DEV.md).

## Connect the relay

Give the hub the connect code your server printed. It goes through **stdin**, never as an argument (an argument would
end up in the shell history and the process list; homed refuses it):

| Platform | Command |
|---|---|
| macOS installer | `"$HOME/Library/Application Support/gpt-home/bin/homed" relay connect -` |
| Homebrew, Linux, Raspberry Pi | `homed relay connect -` |
| Docker | `docker exec -i gpt-home homed relay connect -` |

Paste the code, press Enter. A running hub connects at once, no restart:

```sh
homed relay status
```

```
relay:      home.example.com
state:      connected
self-check: ok at 2026-11-15 18:42:07
```

The self-check fetches `https://<domain>/healthz` every 30 seconds and confirms it reaches *this* hub. You can also
skip this step and paste the code into the first-run page below.

## First run: create your passkey

The admin has no password. Its first passkey can only be created from the home box itself — so nobody who merely
reaches your domain can claim your home. Two ways:

**A. The first-run page** (easiest, from any phone or laptop on your home network). The installer printed its address
and an 8-digit PIN; to see them again (it also reopens a closed page):

```sh
homed admin firstrun
```

1. Open the address, e.g. `http://192.168.1.20:8090/firstrun`, and type the PIN.
2. Paste the connect code if the hub has none yet, and wait for *connected*.
3. Scan the QR code with your phone, or choose *Open on this computer*.
4. Name your home (the name your family will see in ChatGPT), enter your name, and create the passkey with Face ID or
   Touch ID.

The page listens on the whole home network (`0.0.0.0:8090`) only while no relay is connected; once one is, only on
the box itself (`127.0.0.1:8090`). It closes 30 minutes after the hub starts, and for good once you have a passkey.

**B. A setup link from the terminal** (best over SSH):

```sh
homed admin setup-link
```

It prints `https://<your domain>/admin/setup#t=…`: a one-time link valid for 15 minutes. Open it on your phone or
computer and follow step 4 above. The link is a secret until it is used.

<p>
  <img src="images/08-lan-pin-phone-dark.webp" width="260" alt="The first-run page asks for the PIN">
  <img src="images/05-setup-passkey-phone-dark.webp" width="260" alt="Creating the passkey">
</p>

Your passkey syncs through iCloud Keychain (or your password manager), so it works on your other devices too. Add one
for another device in **Settings → Passkeys**. Lost them all? `homed admin reset-access` on the home box removes every
passkey and session and prints a new setup link.

## Add your first device

Open `https://<your domain>/admin` → **Devices → Add devices** → the TV module → **Install**. Then **Find TVs**,
**Add a TV**, **Pair a TV** (press OK on the remote). The full walk-through, and connecting ChatGPT:
[USING.md](USING.md).

## Running as a service

| | macOS (installer) | macOS (Homebrew) | Linux / Raspberry Pi | Docker |
|---|---|---|---|---|
| status | `launchctl print gui/$(id -u)/com.github.devxpro.gpt-home.homed` | `brew services info gpt-home` | `systemctl status gpt-home` | `docker ps -f name=gpt-home` |
| restart | `launchctl kickstart -k gui/$(id -u)/com.github.devxpro.gpt-home.homed` | `brew services restart gpt-home` | `sudo systemctl restart gpt-home` | `docker restart gpt-home` |
| stop | `launchctl bootout gui/$(id -u)/com.github.devxpro.gpt-home.homed` | `brew services stop gpt-home` | `sudo systemctl stop gpt-home` | `docker stop gpt-home` |
| logs | `tail -f ~/Library/Logs/gpt-home/homed.log` | `tail -f "$(brew --prefix)/var/log/gpt-home/homed.log"` | `journalctl -u gpt-home -f` | `docker logs -f gpt-home` |

Only one `homed run` may use a data dir; stop the service before running one by hand. Every service restarts the hub
when it exits — including on purpose, with status 75, after an update from the admin.

## Updates

- **From the admin:** **Settings → About → Check for updates → Update**, confirmed with Face ID or Touch ID. The hub
  downloads the release, verifies it, checks that the new binary runs, swaps it, and exits so the service starts the
  new version; the page reloads by itself. Works for the installer and from-source installs.
- **Installer:** rerun the same one-liner. It keeps your data and restarts only if something changed.
- **Homebrew:** `brew upgrade gpt-home`.
- **Docker:** `docker compose pull && docker compose up -d`.

Plugins update from **Devices → the module → Maintenance**; a version that does not start is rolled back by itself.

## Uninstall

| Platform | Command |
|---|---|
| macOS (installer) | `curl -fsSL https://raw.githubusercontent.com/devXpro/gpt-home/main/install/home.sh \| bash -s -- --uninstall` |
| Linux / Raspberry Pi | `curl -fsSL https://raw.githubusercontent.com/devXpro/gpt-home/main/install/home.sh \| sudo bash -s -- --uninstall` |
| Homebrew | `brew services stop gpt-home && brew uninstall gpt-home` |
| Docker | `docker rm -f gpt-home` (the `gpt-home` volume keeps the data: `docker volume rm gpt-home`) |

The installer keeps the data dir; add `--purge` to delete it too (on Linux also the `gpt-home` user). macOS Keychain
items such as `gpt-home-key` are never touched. Removing the hub does not touch your relay server, and your ChatGPT
Sites keep pointing at the domain until you delete them.

## Backups

Everything is in the data dir: `~/Library/Application Support/gpt-home`, `/var/lib/gpt-home`, or the Docker volume.
Stop the hub, copy the directory, start it again. Treat the copy as a secret: it holds the relay token, the TV pairing
keys and the hashes of your access keys. Restoring it on another box moves the whole home there.
