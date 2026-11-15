package store

import (
	"context"
	"errors"
	"time"
)

type keys struct{ d *DB }

const keyCols = `id, person_id, sha256, label, hint, connection_id, created_at, last_used_at`

func scanKey(r scanner) (Key, error) {
	var (
		k             Key
		created, used int64
	)
	if err := r.Scan(&k.ID, &k.PersonID, &k.SHA256, &k.Label, &k.Hint, &k.ConnectionID, &created, &used); err != nil {
		return Key{}, err
	}
	k.CreatedAt, k.LastUsedAt = fromMS(created), fromMS(used)
	return k, nil
}

func (r keys) Get(ctx context.Context, id string) (Key, error) {
	return one(ctx, r.d, scanKey, `SELECT `+keyCols+` FROM person_keys WHERE id = ?`, id)
}

func (r keys) List(ctx context.Context) ([]Key, error) {
	return collect(ctx, r.d, scanKey, `SELECT `+keyCols+` FROM person_keys ORDER BY created_at, rowid`)
}

func (r keys) ListByPerson(ctx context.Context, personID string) ([]Key, error) {
	return collect(ctx, r.d, scanKey, `SELECT `+keyCols+` FROM person_keys WHERE person_id = ?
		ORDER BY created_at DESC, rowid DESC`, personID)
}

func (r keys) Create(ctx context.Context, k Key) error {
	if k.ID == "" || k.SHA256 == "" {
		return errors.New("store: key id and sha256 are required")
	}
	_, err := r.d.exec(ctx, `INSERT INTO person_keys (`+keyCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.PersonID, k.SHA256, k.Label, k.Hint, k.ConnectionID, ms(r.d.stamp(k.CreatedAt)), ms(k.LastUsedAt))
	return err
}

func (r keys) Touch(ctx context.Context, id string, at time.Time) error {
	return r.d.execOne(ctx, `UPDATE person_keys SET last_used_at = ? WHERE id = ?`, ms(r.d.stamp(at)), id)
}

func (r keys) Delete(ctx context.Context, id string) error {
	return r.d.execOne(ctx, `DELETE FROM person_keys WHERE id = ?`, id)
}
