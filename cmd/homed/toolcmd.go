package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"text/tabwriter"

	"github.com/devXpro/gpt-home/internal/config"
)

// cmdToolList prints every tool of the running homed, admin tools included.
func cmdToolList(args []string, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("tool list", stderr)
	if pos, err := parseFlags(fl, args); err != nil {
		return err
	} else if len(pos) > 0 {
		return fmt.Errorf("unexpected arguments: %v", pos)
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	var tools []apiTool
	if err := apiCall(dir, "GET", "/v1/tools", nil, &tools); err != nil {
		return err
	}
	if len(tools) == 0 {
		fmt.Fprintln(stdout, "No tools (yet): see homed plugin list.")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tAUDIENCE\tDESCRIPTION")
	for _, t := range tools {
		desc, _, _ := strings.Cut(t.Description, "\n")
		if r := []rune(desc); len(r) > 90 {
			desc = string(r[:89]) + "…"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name, t.Audience, desc)
	}
	return tw.Flush()
}

// cmdToolCall calls one tool through the running homed (caller "admin", via "cli") and prints its text, then its
// structured result as indented JSON. A tool that reports a failure makes homed exit with status 1.
func cmdToolCall(args []string, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("tool call", stderr)
	pos, err := parseFlags(fl, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 || len(pos) > 2 {
		return errors.New("usage: homed tool call <name> ['<json arguments>']")
	}
	body := []byte("{}")
	if len(pos) == 2 {
		body = []byte(pos[1])
		if !json.Valid(body) {
			return errors.New("the arguments are not valid JSON")
		}
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	var res apiResult
	if err := apiCall(dir, "POST", "/v1/tools/"+url.PathEscape(pos[0]), body, &res); err != nil {
		return err
	}
	out := stdout
	if res.IsError {
		out = stderr
	}
	if res.Text != "" {
		fmt.Fprintln(out, res.Text)
	}
	if len(res.Structured) > 0 {
		var buf bytes.Buffer
		if json.Indent(&buf, res.Structured, "", "  ") == nil {
			fmt.Fprintln(stdout, buf.String())
		}
	}
	if res.IsError {
		return errToolFailed
	}
	return nil
}
