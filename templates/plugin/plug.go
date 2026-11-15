package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Plug is one smart plug as the plugin remembers it.
type Plug struct {
	ID    string `json:"id"`    // stable, e.g. "plug", "plug-2"
	Name  string `json:"name"`  // what people call it, e.g. "Desk lamp"
	Host  string `json:"host"`  // IPv4 address or host name on the LAN
	Model string `json:"model"` // from the device, e.g. "SNPL-00112EU"
}

// Store keeps the plugs in plugs.json inside the plugin's data dir (pluginkit.DataDir).
type Store struct {
	mu   sync.Mutex
	path string
}

// NewStore returns a store in dir.
func NewStore(dir string) *Store {
	return &Store{path: filepath.Join(dir, "plugs.json")}
}

// List returns every plug; a missing file is an empty list.
func (s *Store) List() ([]Plug, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

// Add stores p. A plug at the same host is replaced, so adding again refreshes the model.
func (s *Store) Add(p Plug) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plugs, err := s.read()
	if err != nil {
		return err
	}
	kept := plugs[:0]
	for _, old := range plugs {
		if old.Host != p.Host {
			kept = append(kept, old)
		}
	}
	return s.write(append(kept, p))
}

func (s *Store) read() ([]Plug, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var plugs []Plug
	if err := json.Unmarshal(data, &plugs); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return plugs, nil
}

// write replaces the file atomically: a crash leaves either the old or the new list.
func (s *Store) write(plugs []Plug) error {
	data, err := json.MarshalIndent(plugs, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".plugs-*.json")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Device talks to one kind of plug. Swap this for your own device family.
type Device interface {
	// Info reads the device's model; it fails when nothing answers at host.
	Info(ctx context.Context, host string) (model string, err error)
	// State reports whether the relay is on.
	State(ctx context.Context, host string) (on bool, err error)
	// Set switches the relay.
	Set(ctx context.Context, host string, on bool) error
}

// Shelly speaks the local RPC API of Shelly Gen2+ plugs over plain HTTP (no cloud): /rpc/<Method>?<params>.
// Devices with authentication turned on are not supported by this template.
type Shelly struct {
	Client *http.Client
	// Base builds the device URL from its host; nil means "http://" + host. Tests point it at an httptest server.
	Base func(host string) string
}

// NewShelly returns a client with a short timeout and no redirects, so a request never leaves the device's address.
func NewShelly() *Shelly {
	return &Shelly{Client: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Info implements Device with Shelly.GetDeviceInfo.
func (s *Shelly) Info(ctx context.Context, host string) (string, error) {
	var info struct {
		Model string `json:"model"`
		Gen   int    `json:"gen"`
	}
	if err := s.rpc(ctx, host, "Shelly.GetDeviceInfo", nil, &info); err != nil {
		return "", err
	}
	if info.Gen < 2 {
		return "", errors.New("not a Shelly Gen2+ device")
	}
	return info.Model, nil
}

// State implements Device with Switch.GetStatus.
func (s *Shelly) State(ctx context.Context, host string) (bool, error) {
	var st struct {
		Output bool `json:"output"`
	}
	err := s.rpc(ctx, host, "Switch.GetStatus", url.Values{"id": {"0"}}, &st)
	return st.Output, err
}

// Set implements Device with Switch.Set.
func (s *Shelly) Set(ctx context.Context, host string, on bool) error {
	return s.rpc(ctx, host, "Switch.Set", url.Values{"id": {"0"}, "on": {fmt.Sprint(on)}}, nil)
}

func (s *Shelly) rpc(ctx context.Context, host, method string, params url.Values, out any) error {
	base := "http://" + host
	if s.Base != nil {
		base = s.Base(host)
	}
	u := base + "/rpc/" + method
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return err
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(body, out)
}
