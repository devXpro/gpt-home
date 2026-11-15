package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/devXpro/gpt-home/internal/admin"
	"github.com/devXpro/gpt-home/internal/hub"
	"github.com/devXpro/gpt-home/internal/keys"
	"github.com/devXpro/gpt-home/internal/plugins"
	"github.com/devXpro/gpt-home/internal/store"
)

// pluginRuntime keeps the running plugins equal to store.Plugins (admin.PluginRuntime). Before the host has started
// (the CLI's in-process admin) Apply only updates the host's list.
type pluginRuntime struct {
	st   store.Store
	host *plugins.Host
	mu   sync.Mutex // one reconciliation at a time, so an older read of the store never wins
}

var _ admin.PluginRuntime = (*pluginRuntime)(nil)

// enabledSpecs returns the enabled plugins of the store, as the host runs them.
func enabledSpecs(ctx context.Context, st store.Store) ([]plugins.Spec, error) {
	rows, err := st.Plugins().List(ctx)
	if err != nil {
		return nil, err
	}
	var specs []plugins.Spec
	for _, p := range rows {
		if p.Enabled {
			specs = append(specs, plugins.Spec{ID: p.ID, Command: p.Command})
		}
	}
	return specs, nil
}

// Apply starts new and enabled plugins, stops disabled and removed ones, and restarts those whose command changed.
func (r *pluginRuntime) Apply(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	specs, err := enabledSpecs(ctx, r.st)
	if err != nil {
		return err
	}
	return r.host.Sync(specs)
}

// Restart stops and starts one plugin; a disabled plugin stays stopped.
func (r *pluginRuntime) Restart(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	row, err := r.st.Plugins().Get(ctx, id)
	if err != nil {
		return fmt.Errorf("plugin %s: %w", id, err)
	}
	if !row.Enabled {
		return nil
	}
	specs, err := enabledSpecs(ctx, r.st)
	if err != nil {
		return err
	}
	if err := r.host.Sync(specs); err != nil {
		return err
	}
	return r.host.Restart(id)
}

// keyResolver is the gateway's hub.KeyResolver over the store's person keys: keys.Resolver does the constant-time
// match, and a used key's LastUsedAt is stored at most once a minute. It is also the admin's KeyUpdater, which hands
// it the full set after every key change.
type keyResolver struct {
	st  store.Store
	res *keys.Resolver
	log *slog.Logger

	mu      sync.Mutex
	ids     map[string]string    // sha256 → key id
	touched map[string]time.Time // key id → when its use was last stored
}

var (
	_ hub.KeyResolver  = (*keyResolver)(nil)
	_ admin.KeyUpdater = (*keyResolver)(nil)
)

const touchKeyEvery = time.Minute

func newKeyResolver(ctx context.Context, st store.Store, log *slog.Logger) (*keyResolver, error) {
	res, err := keys.New(nil)
	if err != nil {
		return nil, err
	}
	k := &keyResolver{st: st, res: res, log: log, ids: map[string]string{}, touched: map[string]time.Time{}}
	return k, k.reload(ctx)
}

// reload rebuilds the key set from the store: every key with its person's current name and plugin scope.
func (k *keyResolver) reload(ctx context.Context) error {
	people, err := k.st.People().List(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]hub.Person, len(people))
	for _, p := range people {
		byID[p.ID] = hub.Person{ID: p.ID, Name: p.Name, Plugins: p.Plugins}
	}
	list, err := k.st.Keys().List(ctx)
	if err != nil {
		return err
	}
	entries := make([]keys.Entry, 0, len(list))
	for _, key := range list {
		if p, ok := byID[key.PersonID]; ok {
			entries = append(entries, keys.Entry{Person: p, SHA256: key.SHA256})
		}
	}
	return k.Update(entries)
}

// Update implements admin.KeyUpdater: it swaps the key set and reads the key ids for LastUsedAt.
func (k *keyResolver) Update(entries []keys.Entry) error {
	if err := k.res.Update(entries); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := k.st.Keys().List(ctx)
	if err != nil {
		return err
	}
	ids := make(map[string]string, len(list))
	for _, key := range list {
		ids[key.SHA256] = key.ID
	}
	k.mu.Lock()
	k.ids = ids
	k.mu.Unlock()
	return nil
}

// ResolveKey implements hub.KeyResolver.
func (k *keyResolver) ResolveKey(ctx context.Context, key string) (hub.Person, error) {
	p, err := k.res.ResolveKey(ctx, key)
	if err == nil {
		k.touch(ctx, keys.Hash(key))
	}
	return p, err
}

// touch stores that a key was used, at most once a minute per key. A failure only costs the timestamp.
func (k *keyResolver) touch(ctx context.Context, hash string) {
	now := time.Now()
	k.mu.Lock()
	id := k.ids[hash]
	if id == "" || now.Sub(k.touched[id]) < touchKeyEvery {
		k.mu.Unlock()
		return
	}
	k.touched[id] = now
	k.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := k.st.Keys().Touch(ctx, id, now); err != nil && !errors.Is(err, store.ErrNotFound) {
		k.log.Warn("cannot store the key's last use", "key", id, "err", err)
	}
}
