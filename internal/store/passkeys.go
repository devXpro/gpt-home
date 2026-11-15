package store

import (
	"context"
	"errors"
	"time"
)

type passkeys struct{ d *DB }

const passkeyCols = `id, person_id, name, provider, credential, created_at, last_used_at` //nolint:gosec // column names, not credentials

func scanPasskey(r scanner) (Passkey, error) {
	var (
		k             Passkey
		created, used int64
	)
	if err := r.Scan(&k.ID, &k.PersonID, &k.Name, &k.Provider, &k.Credential, &created, &used); err != nil {
		return Passkey{}, err
	}
	k.CreatedAt, k.LastUsedAt = fromMS(created), fromMS(used)
	return k, nil
}

func (r passkeys) Get(ctx context.Context, id string) (Passkey, error) {
	return one(ctx, r.d, scanPasskey, `SELECT `+passkeyCols+` FROM passkeys WHERE id = ?`, id)
}

func (r passkeys) ListByPerson(ctx context.Context, personID string) ([]Passkey, error) {
	return collect(ctx, r.d, scanPasskey, `SELECT `+passkeyCols+` FROM passkeys WHERE person_id = ?
		ORDER BY created_at, rowid`, personID)
}

func (r passkeys) Count(ctx context.Context) (int, error) {
	var n int
	err := r.d.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM passkeys`).Scan(&n)
	return n, mapErr(err)
}

func (r passkeys) Create(ctx context.Context, pk Passkey) error {
	if pk.ID == "" {
		return errors.New("store: passkey id is empty")
	}
	_, err := r.d.exec(ctx, `INSERT INTO passkeys (`+passkeyCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		pk.ID, pk.PersonID, pk.Name, pk.Provider, pk.Credential, ms(r.d.stamp(pk.CreatedAt)), ms(pk.LastUsedAt))
	return err
}

func (r passkeys) Rename(ctx context.Context, id, name string) error {
	return r.d.execOne(ctx, `UPDATE passkeys SET name = ? WHERE id = ?`, name, id)
}

func (r passkeys) RecordUse(ctx context.Context, id string, credential []byte, at time.Time) error {
	return r.d.execOne(ctx, `UPDATE passkeys SET credential = ?, last_used_at = ? WHERE id = ?`,
		credential, ms(r.d.stamp(at)), id)
}

// Delete removes the passkey in one statement that also checks the person keeps another one, so two concurrent
// deletes of a person's last two passkeys cannot both succeed.
func (r passkeys) Delete(ctx context.Context, id string) error {
	n, err := r.d.execCount(ctx, `DELETE FROM passkeys WHERE id = ?
		AND (SELECT COUNT(*) FROM passkeys other WHERE other.person_id = passkeys.person_id) > 1`, id)
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	var exists int
	if err := r.d.q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM passkeys WHERE id = ?)`, id).Scan(&exists); err != nil {
		return mapErr(err)
	}
	if exists == 1 {
		return ErrLastPasskey
	}
	return ErrNotFound
}

func (r passkeys) DeleteAll(ctx context.Context) (int, error) {
	return r.d.execCount(ctx, `DELETE FROM passkeys`)
}
