package store

import (
	"context"
	"errors"
	"time"
)

type sessions struct{ d *DB }

const sessionCols = `id, token_hash, person_id, passkey_id, device, created_at, last_seen_at, step_up_at`

func scanSession(r scanner) (Session, error) {
	var (
		s                 Session
		created, seen, up int64
	)
	if err := r.Scan(&s.ID, &s.TokenHash, &s.PersonID, &s.PasskeyID, &s.Device, &created, &seen, &up); err != nil {
		return Session{}, err
	}
	s.CreatedAt, s.LastSeenAt, s.StepUpAt = fromMS(created), fromMS(seen), fromMS(up)
	return s, nil
}

func (r sessions) GetByTokenHash(ctx context.Context, tokenHash string) (Session, error) {
	return one(ctx, r.d, scanSession, `SELECT `+sessionCols+` FROM sessions WHERE token_hash = ?`, tokenHash)
}

func (r sessions) ListByPerson(ctx context.Context, personID string) ([]Session, error) {
	return collect(ctx, r.d, scanSession, `SELECT `+sessionCols+` FROM sessions WHERE person_id = ?
		ORDER BY last_seen_at DESC, rowid DESC`, personID)
}

// Create stores a session. CreatedAt defaults to now and LastSeenAt to CreatedAt.
func (r sessions) Create(ctx context.Context, s Session) error {
	if s.ID == "" || s.TokenHash == "" {
		return errors.New("store: session id and token hash are required")
	}
	created := r.d.stamp(s.CreatedAt)
	seen := s.LastSeenAt
	if seen.IsZero() {
		seen = created
	}
	_, err := r.d.exec(ctx, `INSERT INTO sessions (`+sessionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.TokenHash, s.PersonID, s.PasskeyID, s.Device, ms(created), ms(seen), ms(s.StepUpAt))
	return err
}

func (r sessions) Touch(ctx context.Context, id string, at time.Time) error {
	return r.d.execOne(ctx, `UPDATE sessions SET last_seen_at = ? WHERE id = ?`, ms(r.d.stamp(at)), id)
}

func (r sessions) SetStepUp(ctx context.Context, id string, at time.Time) error {
	return r.d.execOne(ctx, `UPDATE sessions SET step_up_at = ? WHERE id = ?`, ms(r.d.stamp(at)), id)
}

func (r sessions) Delete(ctx context.Context, id string) error {
	return r.d.execOne(ctx, `DELETE FROM sessions WHERE id = ?`, id)
}

func (r sessions) DeleteByPerson(ctx context.Context, personID, keep string) (int, error) {
	return r.d.execCount(ctx, `DELETE FROM sessions WHERE person_id = ? AND id <> ?`, personID, keep)
}

func (r sessions) DeleteAll(ctx context.Context) (int, error) {
	return r.d.execCount(ctx, `DELETE FROM sessions`)
}

// DeleteExpired removes sessions last seen before idleBefore or created before createdBefore. A zero bound matches
// nothing.
func (r sessions) DeleteExpired(ctx context.Context, idleBefore, createdBefore time.Time) (int, error) {
	return r.d.execCount(ctx, `DELETE FROM sessions
		WHERE (? > 0 AND last_seen_at < ?) OR (? > 0 AND created_at < ?)`,
		ms(idleBefore), ms(idleBefore), ms(createdBefore), ms(createdBefore))
}
