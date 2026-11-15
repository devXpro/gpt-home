package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Phase1Import is the phase 1 config (PLAN §5.6 homed.yaml) as plain values. The caller reads the YAML with
// internal/config and fills this; the store knows nothing about the file format.
type Phase1Import struct {
	HomeName string
	People   []Phase1Person
	Plugins  []Phase1Plugin
}

// Phase1Person is one entry of `people:`.
type Phase1Person struct {
	ID      string
	Name    string
	Plugins []string // plugin ids, or ["*"]
	Keys    []Phase1Key
}

// Phase1Key is one entry of a person's `keys:`: only the hash was ever stored.
type Phase1Key struct {
	SHA256  string // 64 lowercase hex characters
	Label   string
	Created time.Time // zero means "now"
}

// Phase1Plugin is one entry of `plugins:`.
type Phase1Plugin struct {
	ID      string
	Command []string
}

// ImportResult says what ImportPhase1 did.
type ImportResult struct {
	// Done is true when this call imported, false when the import had already run or there was nothing to import.
	Done     bool
	People   int
	Keys     int
	Plugins  int
	HomeName bool // the home name was copied into the settings
}

const importMarker = "phase1_import"

var (
	personIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	pluginIDRe = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)
	sha256Re   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ImportPhase1 copies the phase 1 people, keys, plugins and home name into the database. It runs once per database:
// the marker is stored in the same transaction, so a later call, and every later edit of homed.yaml, is a no-op. An
// empty import sets no marker.
//
// The first person becomes the owner, unless an owner already exists; everybody else is a member. Rows that already
// exist (same person id, plugin id or key hash) are left alone; a person who already exists still receives their keys.
// An existing home name is not overwritten. Any invalid entry fails the whole import and changes nothing.
func (d *DB) ImportPhase1(ctx context.Context, in Phase1Import) (ImportResult, error) {
	var res ImportResult
	if in.HomeName == "" && len(in.People) == 0 && len(in.Plugins) == 0 {
		return res, nil
	}
	if err := validateImport(in); err != nil {
		return res, err
	}
	err := d.InTx(ctx, func(tx Store) error {
		t := tx.(*DB)
		res = ImportResult{}
		var done int
		if err := t.q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM meta WHERE key = ?)`, importMarker).Scan(&done); err != nil {
			return mapErr(err)
		}
		if done == 1 {
			return nil
		}
		now := t.now().UTC()
		for _, p := range in.People {
			hasOwner, err := tx.People().HasOwner(ctx)
			if err != nil {
				return err
			}
			role := RoleMember
			if !hasOwner {
				role = RoleOwner
			}
			err = tx.People().Create(ctx, Person{
				ID: p.ID, Name: p.Name, Role: role, Plugins: p.Plugins, UserHandle: NewUserHandle(),
				CreatedAt: now, UpdatedAt: now,
			})
			switch {
			case err == nil:
				res.People++
			case !errors.Is(err, ErrConflict):
				return err
			}
			for _, k := range p.Keys {
				err := tx.Keys().Create(ctx, Key{
					ID: NewID("k"), PersonID: p.ID, SHA256: k.SHA256, Label: k.Label, CreatedAt: t.stamp(k.Created),
				})
				switch {
				case err == nil:
					res.Keys++
				case !errors.Is(err, ErrConflict):
					return err
				}
			}
		}
		for _, p := range in.Plugins {
			err := tx.Plugins().Create(ctx, Plugin{
				ID: p.ID, Source: PluginLocal, Command: p.Command, Enabled: true, InstalledAt: now, UpdatedAt: now,
			})
			switch {
			case err == nil:
				res.Plugins++
			case !errors.Is(err, ErrConflict):
				return err
			}
		}
		if in.HomeName != "" {
			cur, err := tx.Settings().Get(ctx)
			if err != nil {
				return err
			}
			if cur.HomeName == "" {
				cur.HomeName = in.HomeName
				if err := tx.Settings().Put(ctx, cur); err != nil {
					return err
				}
				res.HomeName = true
			}
		}
		if _, err := t.exec(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)`, importMarker, now.Format(time.RFC3339)); err != nil {
			return err
		}
		res.Done = true
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	return res, nil
}

func validateImport(in Phase1Import) error {
	for _, p := range in.People {
		if !personIDRe.MatchString(p.ID) {
			return fmt.Errorf("store: import: bad person id %q", p.ID)
		}
		for _, k := range p.Keys {
			if !sha256Re.MatchString(k.SHA256) {
				return fmt.Errorf("store: import: person %s: key hash is not 64 lowercase hex characters", p.ID)
			}
		}
	}
	for _, p := range in.Plugins {
		if !pluginIDRe.MatchString(p.ID) {
			return fmt.Errorf("store: import: bad plugin id %q", p.ID)
		}
		if len(p.Command) == 0 || p.Command[0] == "" {
			return fmt.Errorf("store: import: plugin %s: empty command", p.ID)
		}
	}
	return nil
}
