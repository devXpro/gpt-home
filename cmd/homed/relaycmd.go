package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/devXpro/gpt-home/internal/config"
	"github.com/devXpro/gpt-home/internal/relay"
)

// cmdRelayConnect stores a connect code: relay.domain into homed.yaml and the token into relay-token (0600).
// It never prints the code or the token.
func cmdRelayConnect(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("relay connect", stderr)
	pos, err := parseFlags(fl, args)
	if err != nil {
		return err
	}
	switch {
	case len(pos) == 0:
		return errors.New("missing connect code; usage: homed relay connect [--data-dir D] <code | ->")
	case len(pos) > 1:
		return errors.New("too many arguments")
	}
	codeArg := pos[0]
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

	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	if w := config.DataDirWarning(dir); w != "" {
		fmt.Fprintln(stderr, "warning:", w)
	}
	if err := config.WriteRelayToken(dir, code.Token); err != nil {
		return fmt.Errorf("save relay token: %w", err)
	}
	if err := config.Update(dir, func(c *config.Config) error {
		c.Relay.Domain = code.Domain
		return nil
	}); err != nil {
		return fmt.Errorf("save relay domain: %w", err)
	}
	fmt.Fprintf(stdout, "Relay set to %s in %s.\nToken saved to %s (0600). Restart homed to connect.\n",
		code.Domain, filepath.Join(dir, config.FileName), filepath.Join(dir, config.RelayTokenFile))
	return nil
}

// cmdRelayStatus prints the relay state of the running homed.
func cmdRelayStatus(args []string, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("relay status", stderr)
	if pos, err := parseFlags(fl, args); err != nil {
		return err
	} else if len(pos) > 0 {
		return errors.New("too many arguments")
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	var st apiRelay
	if err := apiCall(dir, "GET", "/v1/relay", nil, &st); err != nil {
		if errors.Is(err, errNotRunning) {
			if cfg, cerr := config.Load(dir); cerr == nil && cfg.Relay.Domain != "" {
				fmt.Fprintf(stdout, "relay:      %s (configured)\n", cfg.Relay.Domain)
			}
		}
		return err
	}
	if st.Domain == "" {
		fmt.Fprintln(stdout, "relay:      not configured (run: homed relay connect - and paste the code)")
		return nil
	}
	state := "disconnected"
	if st.Connected {
		state = "connected"
	}
	if st.Since != nil {
		state += " since " + st.Since.Local().Format(time.DateTime)
	}
	fmt.Fprintf(stdout, "relay:      %s\nstate:      %s\n", st.Domain, state)
	if st.LastError != "" {
		fmt.Fprintf(stdout, "last error: %s\n", st.LastError)
	}
	switch {
	case st.SelfCheckAt == nil:
		fmt.Fprintln(stdout, "self-check: not run yet (every 30 s while connected)")
	case st.SelfCheckError == "":
		fmt.Fprintf(stdout, "self-check: ok at %s\n", st.SelfCheckAt.Local().Format(time.DateTime))
	default:
		fmt.Fprintf(stdout, "self-check: FAILED at %s: %s\n", st.SelfCheckAt.Local().Format(time.DateTime), st.SelfCheckError)
	}
	return nil
}
