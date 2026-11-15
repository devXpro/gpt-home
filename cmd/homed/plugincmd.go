package main

import (
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/devXpro/gpt-home/internal/config"
)

// cmdPluginAdd adds a plugin to homed.yaml. The executable is resolved to an absolute path now, since homed may
// run from launchd with another working directory and a minimal PATH.
func cmdPluginAdd(args []string, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("plugin add", stderr)
	id := fl.String("id", "", "plugin id, e.g. tv")
	command, err := parseFlags(fl, args)
	if err != nil {
		return err
	}
	if *id == "" || len(command) == 0 {
		return errors.New("usage: homed plugin add --id ID -- <command> [args…]")
	}
	bin, err := exec.LookPath(command[0])
	if err != nil {
		return fmt.Errorf("plugin executable: %w", err)
	}
	if command[0], err = filepath.Abs(bin); err != nil {
		return err
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	err = config.Update(dir, func(c *config.Config) error {
		if c.Plugin(*id) != nil {
			return fmt.Errorf("plugin %q already exists; remove it first", *id)
		}
		c.Plugins = append(c.Plugins, config.Plugin{ID: *id, Command: command})
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Added plugin %s: %s\n", *id, strings.Join(command, " "))
	notifyRestart(dir, stderr)
	return nil
}

// cmdPluginRemove removes a plugin from homed.yaml. Its data dir stays.
func cmdPluginRemove(args []string, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("plugin remove", stderr)
	pos, err := parseFlags(fl, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: homed plugin remove <id>")
	}
	id := pos[0]
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	err = config.Update(dir, func(c *config.Config) error {
		for i, p := range c.Plugins {
			if p.ID == id {
				c.Plugins = append(c.Plugins[:i], c.Plugins[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("no plugin %q", id)
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Removed plugin %s. Its data stays in %s.\n", id, filepath.Join(dir, "plugins", id))
	notifyRestart(dir, stderr)
	return nil
}

// cmdPluginList prints the configured plugins and, when homed runs, their state.
func cmdPluginList(args []string, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("plugin list", stderr)
	if pos, err := parseFlags(fl, args); err != nil {
		return err
	} else if len(pos) > 0 {
		return fmt.Errorf("unexpected arguments: %v", pos)
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return err
	}
	if len(cfg.Plugins) == 0 {
		fmt.Fprintln(stdout, "No plugins yet; add one with: homed plugin add --id ID -- <command> [args…]")
		return nil
	}
	var live []apiPlugin
	liveErr := apiCall(dir, "GET", "/v1/plugins", nil, &live)
	byID := map[string]apiPlugin{}
	for _, p := range live {
		byID[p.ID] = p
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tVERSION\tCOMMAND")
	for _, p := range cfg.Plugins {
		state, ver := "-", ""
		if s, ok := byID[p.ID]; ok {
			state, ver = string(s.State), s.Version
			if s.LastError != "" {
				state += " (" + s.LastError + ")"
			}
		} else if liveErr == nil {
			state = "not loaded (restart homed)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.ID, state, ver, strings.Join(p.Command, " "))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if liveErr != nil {
		fmt.Fprintf(stderr, "No live state: %v\n", liveErr)
	}
	return nil
}

// notifyRestart tells the user a running homed needs a restart to apply plugin changes.
func notifyRestart(dir string, stderr io.Writer) {
	if err := apiCall(dir, "GET", "/v1/plugins", nil, nil); err == nil {
		fmt.Fprintln(stderr, "homed is running: restart it to apply the change.")
	}
}
