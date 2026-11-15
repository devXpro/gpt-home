package store

import (
	"context"
	"database/sql"
	"errors"
)

type settings struct{ d *DB }

func (r settings) Get(ctx context.Context) (HomeSettings, error) {
	var (
		s       HomeSettings
		kind    string
		signOut int
		updated int64
	)
	err := r.d.q.QueryRowContext(ctx, `SELECT home_name, language, connection_kind, site_sign_out, updated_at
		FROM settings WHERE id = 1`).Scan(&s.HomeName, &s.Language, &kind, &signOut, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return HomeSettings{Language: "ru"}, nil
	}
	if err != nil {
		return HomeSettings{}, mapErr(err)
	}
	s.ConnectionKind, s.SiteSignOut, s.UpdatedAt = ConnectionKind(kind), signOut != 0, fromMS(updated)
	return s, nil
}

func (r settings) Put(ctx context.Context, s HomeSettings) error {
	_, err := r.d.exec(ctx, `INSERT INTO settings (id, home_name, language, connection_kind, site_sign_out, updated_at)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET home_name = excluded.home_name, language = excluded.language,
			connection_kind = excluded.connection_kind, site_sign_out = excluded.site_sign_out,
			updated_at = excluded.updated_at`,
		s.HomeName, s.Language, string(s.ConnectionKind), boolInt(s.SiteSignOut), ms(r.d.now()))
	return err
}
