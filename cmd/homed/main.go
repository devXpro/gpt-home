// Command homed is the GPT-Home hub daemon and its CLI.
//
// Phase 0 (spike) commands:
//
//	homed run --spike [--spike-authless-mcp] [--data-dir D] [--listen 127.0.0.1:8080]
//	homed relay connect [--data-dir D] <code | ->
//	homed version
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

Usage:
  homed run --spike [--spike-authless-mcp] [--data-dir D] [--listen 127.0.0.1:8080]
  homed relay connect [--data-dir D] <code | ->   ("-" reads the code from stdin)
  homed version

The data dir defaults to ~/Library/Application Support/gpt-home (macOS) or $XDG_DATA_HOME/gpt-home (Linux);
--data-dir or HOMED_DATA_DIR override it.
`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "homed:", err)
		}
		os.Exit(2)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return flag.ErrHelp
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:], stderr)
	case "relay":
		if len(args) < 2 || args[1] != "connect" {
			fmt.Fprint(stderr, usage)
			return errors.New("unknown relay command; try: homed relay connect <code>")
		}
		return cmdRelayConnect(args[2:], stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, "homed", version)
		return nil
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		fmt.Fprint(stderr, usage)
		return fmt.Errorf("unknown command %q", args[0])
	}
}
