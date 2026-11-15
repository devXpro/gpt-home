package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/devXpro/gpt-home/internal/config"
	"github.com/devXpro/gpt-home/internal/keys"
)

// keyIDLen is how many hex characters of a key's sha256 `key list` shows as the key id.
const keyIDLen = 12

// minRevokePrefix keeps `key revoke` from matching by accident.
const minRevokePrefix = 6

// cmdKeyNew issues a person key: the hash goes into homed.yaml, the key itself is printed once to stdout (and
// nothing else is), or with --keychain saved to the macOS Keychain without ever being shown.
func cmdKeyNew(args []string, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("key new", stderr)
	personID := fl.String("person", "", "person id, e.g. roman (created when new)")
	name := fl.String("name", "", "display name (default: the existing name, or the id capitalized)")
	label := fl.String("label", "", "what the key is for, e.g. \"ChatGPT Site\"")
	pluginList := fl.String("plugins", "", "comma-separated plugin ids the person may use, or * (default: unchanged, * for a new person)")
	keychain := fl.String("keychain", "", "macOS only: save the key to this Keychain item (account = person id) instead of printing it")
	if pos, err := parseFlags(fl, args); err != nil {
		return err
	} else if len(pos) > 0 {
		return fmt.Errorf("unexpected arguments: %v", pos)
	}
	if *personID == "" {
		return errors.New("--person is required")
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}

	key, hash := keys.Generate()
	var displayName string
	err = config.Update(dir, func(c *config.Config) error {
		p := c.Person(*personID)
		if p == nil {
			c.People = append(c.People, config.Person{ID: *personID, Name: capitalize(*personID), Plugins: []string{"*"}})
			p = &c.People[len(c.People)-1]
		}
		if *name != "" {
			p.Name = *name
		}
		if *pluginList != "" {
			p.Plugins = splitList(*pluginList)
		}
		p.Keys = append(p.Keys, config.Key{SHA256: hash, Label: *label, Created: time.Now().UTC().Truncate(time.Second)})
		displayName = p.Name
		if err := c.Validate(); err != nil {
			return err
		}
		// Into the Keychain before homed.yaml is written: a failure there leaves nothing behind.
		if *keychain != "" {
			return keychainStore(*keychain, *personID, key)
		}
		return nil
	})
	if err != nil {
		return err
	}

	id := hash[:keyIDLen]
	if *keychain != "" {
		fmt.Fprintf(stdout, "New key %s for %s saved to Keychain item %q (account %s).\n", id, displayName, *keychain, *personID)
	} else {
		fmt.Fprintf(stderr, "New key %s for %s. It is shown only this once; homed keeps only its hash:\n", id, displayName)
		fmt.Fprintln(stdout, key)
	}
	notifyReload(dir, stderr, "the key works at its next start")
	return nil
}

// cmdKeyList prints every key by its id (the start of its hash); keys themselves are never stored.
func cmdKeyList(args []string, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("key list", stderr)
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
	if len(cfg.People) == 0 {
		fmt.Fprintln(stdout, "No people yet; add one with: homed key new --person ID --name NAME")
		return nil
	}
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PERSON\tNAME\tPLUGINS\tKEY ID\tLABEL\tCREATED")
	for _, p := range cfg.People {
		plugins := strings.Join(p.Plugins, ",")
		if len(p.Keys) == 0 {
			fmt.Fprintf(tw, "%s\t%s\t%s\t-\t\t\n", p.ID, p.Name, plugins)
		}
		for _, k := range p.Keys {
			created := ""
			if !k.Created.IsZero() {
				created = k.Created.Local().Format(time.DateTime)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Name, plugins, k.SHA256[:keyIDLen], k.Label, created)
		}
	}
	return tw.Flush()
}

// cmdKeyRevoke removes the one key whose hash starts with the given key id prefix.
func cmdKeyRevoke(args []string, stdout, stderr io.Writer) error {
	fl, dataDir := newFlags("key revoke", stderr)
	pos, err := parseFlags(fl, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: homed key revoke <key id prefix> (see homed key list)")
	}
	prefix := strings.ToLower(pos[0])
	if len(prefix) < minRevokePrefix {
		return fmt.Errorf("give at least %d characters of the key id", minRevokePrefix)
	}
	dir, err := config.ResolveDataDir(*dataDir)
	if err != nil {
		return err
	}
	var revoked string
	err = config.Update(dir, func(c *config.Config) error {
		matches := 0
		for i := range c.People {
			for _, k := range c.People[i].Keys {
				if strings.HasPrefix(k.SHA256, prefix) {
					matches++
				}
			}
		}
		switch {
		case matches == 0:
			return fmt.Errorf("no key id starts with %q (see homed key list)", prefix)
		case matches > 1:
			return fmt.Errorf("%d keys start with %q; give more characters", matches, prefix)
		}
		for i := range c.People {
			p := &c.People[i]
			for j, k := range p.Keys {
				if strings.HasPrefix(k.SHA256, prefix) {
					p.Keys = append(p.Keys[:j], p.Keys[j+1:]...)
					revoked = fmt.Sprintf("%s of %s", k.SHA256[:keyIDLen], p.Name)
					return nil
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Revoked key %s.\n", revoked)
	notifyReload(dir, stderr, "the change applies at its next start")
	return nil
}

// notifyReload asks a running homed to re-read people and keys.
func notifyReload(dir string, stderr io.Writer, notRunning string) {
	var res apiReload
	err := apiCall(dir, "POST", "/v1/reload", nil, &res)
	switch {
	case err == nil:
		fmt.Fprintln(stderr, "The running homed picked up the change.")
		if len(res.RestartNeeded) > 0 {
			fmt.Fprintf(stderr, "Restart homed to apply the other changes in homed.yaml: %s.\n", strings.Join(res.RestartNeeded, ", "))
		}
	case errors.Is(err, errNotRunning):
		fmt.Fprintf(stderr, "homed is not running; %s.\n", notRunning)
	default:
		fmt.Fprintf(stderr, "Warning: the running homed did not reload (%v); restart it to apply the change.\n", err)
	}
}

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
