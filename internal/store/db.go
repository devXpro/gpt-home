package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// queryer is what *sql.DB and *sql.Tx share, so one repository implementation serves both.
type queryer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB is the SQLite implementation of Store. Outside InTx every statement runs on its own; inside InTx the Store handed
// to the callback is a DB bound to one transaction.
type DB struct {
	conn *sql.DB
	q    queryer
	inTx bool
	now  func() time.Time
}

var _ Store = (*DB)(nil)

// Open opens (creating it when missing) the database file at path, with mode 0600, WAL, foreign keys and a busy
// timeout, and applies the embedded migrations. It refuses a database written by a newer homed.
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	// Create the file ourselves so it is 0600 from the start; SQLite gives -wal and -shm the main file's mode.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("store: create database file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("store: restrict database file: %w", err)
	}

	// _txlock=immediate makes every transaction take the write lock at BEGIN, so a read-then-write transaction waits
	// for the busy timeout instead of failing with SQLITE_BUSY halfway.
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}).String()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open database: %w", err)
	}
	conn.SetMaxOpenConns(8)
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("store: open database: %w", err)
	}
	if err := migrate(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &DB{conn: conn, q: conn, now: time.Now}, nil
}

// People returns the people repository.
func (d *DB) People() People { return people{d} }

// Passkeys returns the passkeys repository.
func (d *DB) Passkeys() Passkeys { return passkeys{d} }

// Sessions returns the sessions repository.
func (d *DB) Sessions() Sessions { return sessions{d} }

// Keys returns the person keys repository.
func (d *DB) Keys() Keys { return keys{d} }

// Invites returns the invites repository.
func (d *DB) Invites() Invites { return invites{d} }

// Connections returns the connections repository.
func (d *DB) Connections() Connections { return connections{d} }

// Plugins returns the plugins repository.
func (d *DB) Plugins() Plugins { return plugins{d} }

// Audit returns the audit repository.
func (d *DB) Audit() Audit { return audit{d} }

// Settings returns the settings repository.
func (d *DB) Settings() Settings { return settings{d} }

// InTx implements Store.InTx.
func (d *DB) InTx(ctx context.Context, fn func(tx Store) error) error {
	if d.inTx {
		return fn(d)
	}
	tx, err := d.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin transaction: %w", err)
	}
	if err := fn(&DB{conn: d.conn, q: tx, inTx: true, now: d.now}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// Close implements Store.Close.
func (d *DB) Close() error {
	if d.inTx {
		return errors.New("store: Close called inside a transaction")
	}
	return d.conn.Close()
}

// NewID returns a fresh public id such as "k_7HdQ2vXr1nB0" for sessions ("s"), keys ("k"), invites ("i") and
// connections ("cn"): the prefix, an underscore and 12 random bytes in base64url.
func NewID(prefix string) string {
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(randomBytes(12))
}

// NewUserHandle returns a WebAuthn user handle: 32 random bytes, to be stored in Person.UserHandle at creation.
func NewUserHandle() []byte { return randomBytes(32) }

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("store: no randomness: " + err.Error())
	}
	return b
}

// ms converts a time to stored Unix milliseconds; the zero time becomes 0.
func ms(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// fromMS is the inverse of ms; 0 becomes the zero time.
func fromMS(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v).UTC()
}

// stamp returns t, or the current time when t is zero.
func (d *DB) stamp(t time.Time) time.Time {
	if t.IsZero() {
		return d.now().UTC()
	}
	return t
}

// mapErr turns driver errors into the package's sentinels.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
			return fmt.Errorf("%w (%s)", ErrConflict, se.Error())
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			// A row names an owner that does not exist.
			return fmt.Errorf("%w: referenced row (%s)", ErrNotFound, se.Error())
		}
	}
	return err
}

// exec runs a statement and maps its error.
func (d *DB) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	res, err := d.q.ExecContext(ctx, query, args...)
	return res, mapErr(err)
}

// execOne runs a statement that must touch exactly one row; no row means ErrNotFound.
func (d *DB) execOne(ctx context.Context, query string, args ...any) error {
	res, err := d.exec(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// execCount runs a statement and returns how many rows it touched.
func (d *DB) execCount(ctx context.Context, query string, args ...any) (int, error) {
	res, err := d.exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// scanner is *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

// collect runs a query and scans every row with scan.
func collect[T any](ctx context.Context, d *DB, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := d.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = rows.Close() }()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

// one runs a query for a single row.
func one[T any](ctx context.Context, d *DB, scan func(scanner) (T, error), query string, args ...any) (T, error) {
	v, err := scan(d.q.QueryRowContext(ctx, query, args...))
	if err != nil {
		var zero T
		return zero, mapErr(err)
	}
	return v, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
