package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type invites struct{ d *DB }

const inviteCols = `id, token_hash, kind, person_id, created_by, created_at, expires_at, used_at`

func scanInvite(r scanner) (Invite, error) {
	var (
		inv                      Invite
		kind                     string
		person                   sql.NullString
		created, expires, usedAt int64
	)
	if err := r.Scan(&inv.ID, &inv.TokenHash, &kind, &person, &inv.CreatedBy, &created, &expires, &usedAt); err != nil {
		return Invite{}, err
	}
	inv.Kind, inv.PersonID = InviteKind(kind), person.String
	inv.CreatedAt, inv.ExpiresAt, inv.UsedAt = fromMS(created), fromMS(expires), fromMS(usedAt)
	return inv, nil
}

// personRef stores an empty person id as NULL, so the foreign key only binds real people.
func personRef(id string) sql.NullString { return sql.NullString{String: id, Valid: id != ""} }

func (r invites) GetByTokenHash(ctx context.Context, tokenHash string) (Invite, error) {
	return one(ctx, r.d, scanInvite, `SELECT `+inviteCols+` FROM invites WHERE token_hash = ?`, tokenHash)
}

func (r invites) ListActive(ctx context.Context, kind InviteKind, now time.Time) ([]Invite, error) {
	return collect(ctx, r.d, scanInvite, `SELECT `+inviteCols+` FROM invites
		WHERE kind = ? AND used_at = 0 AND expires_at > ? ORDER BY created_at DESC, rowid DESC`,
		string(kind), ms(r.d.stamp(now)))
}

func (r invites) Create(ctx context.Context, inv Invite) error {
	if inv.ID == "" || inv.TokenHash == "" {
		return errors.New("store: invite id and token hash are required")
	}
	if inv.Kind != InviteSetup && inv.Kind != InviteMember {
		return fmt.Errorf("store: invite %s: bad kind %q", inv.ID, inv.Kind)
	}
	if inv.ExpiresAt.IsZero() {
		return fmt.Errorf("store: invite %s: expiry is required", inv.ID)
	}
	_, err := r.d.exec(ctx, `INSERT INTO invites (`+inviteCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.ID, inv.TokenHash, string(inv.Kind), personRef(inv.PersonID), inv.CreatedBy,
		ms(r.d.stamp(inv.CreatedAt)), ms(inv.ExpiresAt), ms(inv.UsedAt))
	return err
}

// Consume marks the token used in one statement, so two concurrent registrations cannot both win.
func (r invites) Consume(ctx context.Context, tokenHash string, now time.Time) (Invite, error) {
	now = r.d.stamp(now)
	inv, err := one(ctx, r.d, scanInvite, `UPDATE invites SET used_at = ?
		WHERE token_hash = ? AND used_at = 0 AND expires_at > ? RETURNING `+inviteCols,
		ms(now), tokenHash, ms(now))
	if err == nil {
		return inv, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Invite{}, err
	}
	// Nothing was updated: tell the three reasons apart.
	cur, err := r.GetByTokenHash(ctx, tokenHash)
	if err != nil {
		return Invite{}, err
	}
	if !cur.UsedAt.IsZero() {
		return Invite{}, ErrTokenUsed
	}
	return Invite{}, ErrTokenExpired
}

func (r invites) Revoke(ctx context.Context, id string, at time.Time) error {
	n, err := r.d.execCount(ctx, `UPDATE invites SET used_at = ? WHERE id = ? AND used_at = 0`, ms(r.d.stamp(at)), id)
	if err != nil || n == 1 {
		return err
	}
	// Already used or revoked is fine and keeps its time; a missing id is not.
	var exists int
	if err := r.d.q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM invites WHERE id = ?)`, id).Scan(&exists); err != nil {
		return mapErr(err)
	}
	if exists == 0 {
		return ErrNotFound
	}
	return nil
}

func (r invites) RevokeAll(ctx context.Context, kind InviteKind, personID string, at time.Time) (int, error) {
	q := `UPDATE invites SET used_at = ? WHERE kind = ? AND used_at = 0`
	args := []any{ms(r.d.stamp(at)), string(kind)}
	if personID != "" {
		q += ` AND person_id = ?`
		args = append(args, personID)
	}
	return r.d.execCount(ctx, q, args...)
}
