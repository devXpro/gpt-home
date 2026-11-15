package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type people struct{ d *DB }

const personCols = `id, name, role, plugins, language, user_handle, created_at, updated_at`

func scanPerson(r scanner) (Person, error) {
	var (
		p                Person
		role, plugs      string
		created, updated int64
	)
	if err := r.Scan(&p.ID, &p.Name, &role, &plugs, &p.Language, &p.UserHandle, &created, &updated); err != nil {
		return Person{}, err
	}
	p.Role = Role(role)
	if err := json.Unmarshal([]byte(plugs), &p.Plugins); err != nil {
		return Person{}, fmt.Errorf("store: person %s: bad plugins column: %w", p.ID, err)
	}
	p.CreatedAt, p.UpdatedAt = fromMS(created), fromMS(updated)
	return p, nil
}

func checkPerson(p Person) error {
	switch {
	case p.ID == "":
		return errors.New("store: person id is empty")
	case p.Role != RoleOwner && p.Role != RoleMember:
		return fmt.Errorf("store: person %s: bad role %q", p.ID, p.Role)
	}
	return nil
}

func stringsJSON(list []string) string {
	if list == nil {
		list = []string{}
	}
	b, _ := json.Marshal(list) // a string slice always marshals
	return string(b)
}

func (r people) Get(ctx context.Context, id string) (Person, error) {
	return one(ctx, r.d, scanPerson, `SELECT `+personCols+` FROM people WHERE id = ?`, id)
}

func (r people) GetByUserHandle(ctx context.Context, handle []byte) (Person, error) {
	return one(ctx, r.d, scanPerson, `SELECT `+personCols+` FROM people WHERE user_handle = ?`, handle)
}

func (r people) List(ctx context.Context) ([]Person, error) {
	return collect(ctx, r.d, scanPerson, `SELECT `+personCols+` FROM people
		ORDER BY CASE role WHEN 'owner' THEN 0 ELSE 1 END, name COLLATE NOCASE, id`)
}

func (r people) HasOwner(ctx context.Context) (bool, error) {
	var n int
	err := r.d.q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM people WHERE role = 'owner')`).Scan(&n)
	return n == 1, mapErr(err)
}

func (r people) Create(ctx context.Context, p Person) error {
	if err := checkPerson(p); err != nil {
		return err
	}
	if len(p.UserHandle) == 0 {
		return fmt.Errorf("store: person %s: user handle is empty", p.ID)
	}
	created := r.d.stamp(p.CreatedAt)
	updated := r.d.stamp(p.UpdatedAt)
	_, err := r.d.exec(ctx, `INSERT INTO people (`+personCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, string(p.Role), stringsJSON(p.Plugins), p.Language, p.UserHandle, ms(created), ms(updated))
	return err
}

func (r people) Update(ctx context.Context, p Person) error {
	if err := checkPerson(p); err != nil {
		return err
	}
	return r.d.execOne(ctx, `UPDATE people SET name = ?, role = ?, plugins = ?, language = ?, updated_at = ? WHERE id = ?`,
		p.Name, string(p.Role), stringsJSON(p.Plugins), p.Language, ms(r.d.now()), p.ID)
}

func (r people) Delete(ctx context.Context, id string) error {
	// Passkeys, sessions, keys, invites and connections go with the person through ON DELETE CASCADE.
	return r.d.execOne(ctx, `DELETE FROM people WHERE id = ?`, id)
}
