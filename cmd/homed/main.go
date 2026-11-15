// Command homed is the GPT-Home hub daemon and its CLI. See `homed help` and docs/DEV.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// version is set at build time with -ldflags "-X main.version=…".
var version = "dev"

const usage = `homed — the GPT-Home hub

Daemon:
  homed run [--listen 127.0.0.1:8080]          serve the gateway, the plugins, the relay and the local socket

Relay:
  homed relay connect <- | code>               store a connect code; "-" reads it from stdin and keeps it
                                               out of shell history and the process list
  homed relay status                           connection and self-check of the running homed

Person keys (stored in homed.yaml as hashes; a running homed picks up changes at once):
  homed key new --person ID [--name NAME] [--label TEXT] [--plugins tv,…] [--keychain SERVICE]
                                               print a new key once, or save it to the macOS Keychain
  homed key list
  homed key revoke <key id prefix>

Plugins (a running homed needs a restart to apply):
  homed plugin add --id ID -- <command> [args…]
  homed plugin list
  homed plugin remove <id>

Tools (through the running homed; every tool, admin ones included):
  homed tool list
  homed tool call <name> ['<json arguments>']

  homed version

Every command takes --data-dir D. The data dir defaults to ~/Library/Application Support/gpt-home (macOS) or
$XDG_DATA_HOME/gpt-home (Linux); HOMED_DATA_DIR overrides the default.
`

// errToolFailed is returned when a tool ran and reported a failure; homed exits with status 1 for it.
var errToolFailed = errors.New("the tool reported a failure")

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	switch {
	case err == nil:
	case errors.Is(err, errToolFailed):
		os.Exit(1)
	case errors.Is(err, flag.ErrHelp):
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "homed:", err)
		os.Exit(2)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return flag.ErrHelp
	}
	cmd, rest := args[0], args[1:]
	sub := ""
	if len(rest) > 0 {
		sub = rest[0]
	}
	switch {
	case cmd == "run":
		return cmdRun(rest, stderr)
	case cmd == "relay" && sub == "connect":
		return cmdRelayConnect(rest[1:], stdin, stdout, stderr)
	case cmd == "relay" && sub == "status":
		return cmdRelayStatus(rest[1:], stdout, stderr)
	case cmd == "key" && sub == "new":
		return cmdKeyNew(rest[1:], stdout, stderr)
	case cmd == "key" && sub == "list":
		return cmdKeyList(rest[1:], stdout, stderr)
	case cmd == "key" && sub == "revoke":
		return cmdKeyRevoke(rest[1:], stdout, stderr)
	case cmd == "plugin" && sub == "add":
		return cmdPluginAdd(rest[1:], stdout, stderr)
	case cmd == "plugin" && sub == "list":
		return cmdPluginList(rest[1:], stdout, stderr)
	case cmd == "plugin" && sub == "remove":
		return cmdPluginRemove(rest[1:], stdout, stderr)
	case cmd == "tool" && sub == "list":
		return cmdToolList(rest[1:], stdout, stderr)
	case cmd == "tool" && sub == "call":
		return cmdToolCall(rest[1:], stdout, stderr)
	case cmd == "version" || cmd == "--version" || cmd == "-v":
		fmt.Fprintln(stdout, "homed", version)
		return nil
	case cmd == "help" || cmd == "-h" || cmd == "--help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", joinCmd(cmd, sub))
	}
}

func joinCmd(cmd, sub string) string {
	switch cmd {
	case "relay", "key", "plugin", "tool":
		return cmd + " " + sub
	}
	return cmd
}

// newFlags returns a flag set with the --data-dir flag every command takes.
func newFlags(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", "", "data directory (default: the platform data dir, or HOMED_DATA_DIR)")
	return fl, dataDir
}

// parseFlags parses fl over args and allows flags after positional arguments, as people type them
// (homed relay connect <code> --data-dir D). Everything after "--" is positional. It returns the positionals.
func parseFlags(fl *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fl.Parse(args); err != nil {
			return nil, err
		}
		rest := fl.Args()
		if consumed := len(args) - len(rest); consumed > 0 && args[consumed-1] == "--" {
			return append(pos, rest...), nil
		}
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}
