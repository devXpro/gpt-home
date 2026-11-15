package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type audit struct{ d *DB }

const (
	auditDefaultLimit = 50
	auditMaxLimit     = 200
	auditCols         = `id, at, kind, person_id, person, via, tool, plugin_id, action, target, args, ok, error, duration_ns`
)

func scanAudit(r scanner) (AuditRecord, error) {
	var (
		a        AuditRecord
		kind     string
		args     sql.NullString
		at, dur  int64
		okFlag   int
		personID string
	)
	if err := r.Scan(&a.ID, &at, &kind, &personID, &a.Person, &a.Via, &a.Tool, &a.PluginID, &a.Action, &a.Target,
		&args, &okFlag, &a.Error, &dur); err != nil {
		return AuditRecord{}, err
	}
	a.PersonID, a.Kind, a.At = personID, AuditKind(kind), fromMS(at)
	if args.Valid {
		a.Args = json.RawMessage(args.String)
	}
	a.OK, a.Duration = okFlag != 0, time.Duration(dur)
	return a, nil
}

// Append stores the records in order, in one transaction, and returns them with their ids.
func (r audit) Append(ctx context.Context, recs []AuditRecord) ([]AuditRecord, error) {
	if len(recs) == 0 {
		return []AuditRecord{}, nil
	}
	for i, rec := range recs {
		if rec.Kind != AuditTool && rec.Kind != AuditAdmin {
			return nil, fmt.Errorf("store: audit record %d: bad kind %q", i, rec.Kind)
		}
	}
	out := make([]AuditRecord, len(recs))
	err := r.d.InTx(ctx, func(tx Store) error {
		d := tx.(*DB)
		for i, rec := range recs {
			rec.At = d.stamp(rec.At)
			var args any
			if rec.Args != nil {
				args = string(rec.Args)
			}
			res, err := d.exec(ctx, `INSERT INTO audit (at, kind, person_id, person, via, tool, plugin_id, action, target,
				args, ok, error, duration_ns) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				ms(rec.At), string(rec.Kind), rec.PersonID, rec.Person, rec.Via, rec.Tool, rec.PluginID, rec.Action,
				rec.Target, args, boolInt(rec.OK), rec.Error, int64(rec.Duration))
			if err != nil {
				return err
			}
			if rec.ID, err = res.LastInsertId(); err != nil {
				return err
			}
			rec.At = rec.At.UTC().Truncate(time.Millisecond)
			out[i] = rec
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r audit) Query(ctx context.Context, f AuditFilter) ([]AuditRecord, error) {
	var (
		where []string
		args  []any
	)
	add := func(cond string, a ...any) {
		where = append(where, cond)
		args = append(args, a...)
	}
	if f.Kind != "" {
		add(`kind = ?`, string(f.Kind))
	}
	if f.PersonID != "" {
		add(`person_id = ?`, f.PersonID)
	}
	if f.PluginID != "" {
		add(`plugin_id = ?`, f.PluginID)
	}
	if f.Tool != "" {
		add(`tool = ?`, f.Tool)
	}
	if len(f.Via) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(f.Via)), ",")
		vias := make([]any, len(f.Via))
		for i, v := range f.Via {
			vias[i] = v
		}
		add(`via IN (`+marks+`)`, vias...)
	}
	switch f.Status {
	case AuditOK:
		add(`ok = 1`)
	case AuditFailed:
		add(`ok = 0`)
	}
	if !f.Since.IsZero() {
		add(`at >= ?`, ms(f.Since))
	}
	if !f.Until.IsZero() {
		add(`at < ?`, ms(f.Until))
	}
	if f.BeforeID > 0 {
		add(`id < ?`, f.BeforeID)
	}
	limit := f.Limit
	switch {
	case limit <= 0:
		limit = auditDefaultLimit
	case limit > auditMaxLimit:
		limit = auditMaxLimit
	}
	q := `SELECT ` + auditCols + ` FROM audit`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	return collect(ctx, r.d, scanAudit, q, args...)
}

func (r audit) Get(ctx context.Context, id int64) (AuditRecord, error) {
	return one(ctx, r.d, scanAudit, `SELECT `+auditCols+` FROM audit WHERE id = ?`, id)
}

func (r audit) Prune(ctx context.Context, before time.Time) (int64, error) {
	n, err := r.d.execCount(ctx, `DELETE FROM audit WHERE at < ?`, ms(before))
	return int64(n), err
}
