package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/devXpro/gpt-home/internal/relay"
)

// cmdRelayConnect stores a connect code: relay.domain into homed.yaml and the token into relay-token (0600).
// It never prints the code or the token.
func cmdRelayConnect(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("relay connect", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dataDir := fl.String("data-dir", "", "data directory")
	if err := fl.Parse(args); err != nil {
		return err
	}
	// Allow flags after the code too: homed relay connect <code> --data-dir D.
	rest := fl.Args()
	if len(rest) == 0 {
		return errors.New("missing connect code; usage: homed relay connect [--data-dir D] <code | ->")
	}
	codeArg := rest[0]
	if err := fl.Parse(rest[1:]); err != nil {
		return err
	}
	if fl.NArg() > 0 {
		return errors.New("too many arguments")
	}

	if codeArg == "-" {
		line, err := bufio.NewReader(io.LimitReader(stdin, 4096)).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read code from stdin: %w", err)
		}
		codeArg = line
	}
	code, err := relay.ParseCode(codeArg)
	if err != nil {
		return err
	}

	dir, err := resolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	tokenPath := filepath.Join(dir, relayTokenFile)
	if err := writeFileAtomic(tokenPath, []byte(code.Token+"\n"), 0o600); err != nil {
		return fmt.Errorf("save relay token: %w", err)
	}
	if err := setRelayDomain(dir, code.Domain); err != nil {
		return fmt.Errorf("save relay domain: %w", err)
	}
	fmt.Fprintf(stdout, "Relay set to %s in %s.\nToken saved to %s (0600). Restart homed to connect.\n",
		code.Domain, filepath.Join(dir, configFile), tokenPath)
	return nil
}
