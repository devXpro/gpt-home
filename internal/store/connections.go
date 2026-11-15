package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type connections struct{ d *DB }

const connectionCols = `id, person_id, kind, state, title, key_id, detail, fingerprint, published_at, last_error, created_at, updated_at`

func scanConnection(r scanner) (Connection, error) {
	var (
		c                          Connection
		kind, state, detail        string
		published, created, update int64
	)
	if err := r.Scan(&c.ID, &c.PersonID, &kind, &state, &c.Title, &c.KeyID, &detail, &c.Fingerprint,
		&published, &c.LastError, &created, &update); err != nil {
		return Connection{}, err
	}
	c.Kind, c.State = ConnectionKind(kind), ConnectionState(state)
	if detail != "" {
		c.Detail = json.RawMessage(detail)
	}
	c.PublishedAt, c.CreatedAt, c.UpdatedAt = fromMS(published), fromMS(created), fromMS(update)
	return c, nil
}

func checkDetail(c Connection) error {
	if len(c.Detail) > 0 && !json.Valid(c.Detail) {
		return fmt.Errorf("store: connection %s: detail is not valid JSON", c.ID)
	}
	return nil
}

func (r connections) Get(ctx context.Context, id string) (Connection, error) {
	return one(ctx, r.d, scanConnection, `SELECT `+connectionCols+` FROM connections WHERE id = ?`, id)
}

func (r connections) List(ctx context.Context) ([]Connection, error) {
	return collect(ctx, r.d, scanConnection, `SELECT `+connectionCols+` FROM connections
		ORDER BY person_id, created_at DESC, rowid DESC`)
}

func (r connections) ListByPerson(ctx context.Context, personID string) ([]Connection, error) {
	return collect(ctx, r.d, scanConnection, `SELECT `+connectionCols+` FROM connections WHERE person_id = ?
		ORDER BY created_at DESC, rowid DESC`, personID)
}

func (r connections) Create(ctx context.Context, c Connection) error {
	if c.ID == "" {
		return errors.New("store: connection id is empty")
	}
	if err := checkDetail(c); err != nil {
		return err
	}
	created := r.d.stamp(c.CreatedAt)
	updated := r.d.stamp(c.UpdatedAt)
	_, err := r.d.exec(ctx, `INSERT INTO connections (`+connectionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.PersonID, string(c.Kind), string(c.State), c.Title, c.KeyID, string(c.Detail), c.Fingerprint,
		ms(c.PublishedAt), c.LastError, ms(created), ms(updated))
	return err
}

func (r connections) Update(ctx context.Context, c Connection) error {
	if err := checkDetail(c); err != nil {
		return err
	}
	return r.d.execOne(ctx, `UPDATE connections SET state = ?, title = ?, key_id = ?, detail = ?, fingerprint = ?,
		published_at = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		string(c.State), c.Title, c.KeyID, string(c.Detail), c.Fingerprint, ms(c.PublishedAt), c.LastError,
		ms(r.d.now()), c.ID)
}

func (r connections) Delete(ctx context.Context, id string) error {
	return r.d.execOne(ctx, `DELETE FROM connections WHERE id = ?`, id)
}
