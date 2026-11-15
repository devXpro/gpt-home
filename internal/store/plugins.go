package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type plugins struct{ d *DB }

const pluginCols = `id, source, repo, version, previous_version, command, enabled, installed_at, updated_at`

func scanPlugin(r scanner) (Plugin, error) {
	var (
		p                  Plugin
		source, command    string
		enabled            int
		installed, updated int64
	)
	if err := r.Scan(&p.ID, &source, &p.Repo, &p.Version, &p.PreviousVersion, &command, &enabled, &installed, &updated); err != nil {
		return Plugin{}, err
	}
	p.Source, p.Enabled = PluginSource(source), enabled != 0
	if err := json.Unmarshal([]byte(command), &p.Command); err != nil {
		return Plugin{}, fmt.Errorf("store: plugin %s: bad command column: %w", p.ID, err)
	}
	p.InstalledAt, p.UpdatedAt = fromMS(installed), fromMS(updated)
	return p, nil
}

func (r plugins) Get(ctx context.Context, id string) (Plugin, error) {
	return one(ctx, r.d, scanPlugin, `SELECT `+pluginCols+` FROM plugins WHERE id = ?`, id)
}

func (r plugins) List(ctx context.Context) ([]Plugin, error) {
	return collect(ctx, r.d, scanPlugin, `SELECT `+pluginCols+` FROM plugins ORDER BY id`)
}

func (r plugins) Create(ctx context.Context, p Plugin) error {
	if p.ID == "" {
		return errors.New("store: plugin id is empty")
	}
	installed := r.d.stamp(p.InstalledAt)
	updated := r.d.stamp(p.UpdatedAt)
	_, err := r.d.exec(ctx, `INSERT INTO plugins (`+pluginCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, string(p.Source), p.Repo, p.Version, p.PreviousVersion, stringsJSON(p.Command), boolInt(p.Enabled),
		ms(installed), ms(updated))
	return err
}

func (r plugins) Update(ctx context.Context, p Plugin) error {
	return r.d.execOne(ctx, `UPDATE plugins SET source = ?, repo = ?, version = ?, previous_version = ?, command = ?,
		enabled = ?, updated_at = ? WHERE id = ?`,
		string(p.Source), p.Repo, p.Version, p.PreviousVersion, stringsJSON(p.Command), boolInt(p.Enabled),
		ms(r.d.now()), p.ID)
}

func (r plugins) Delete(ctx context.Context, id string) error {
	return r.d.execOne(ctx, `DELETE FROM plugins WHERE id = ?`, id)
}
