package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Files in the data dir (PLAN §5.6).
const (
	configFile     = "homed.yaml"
	relayTokenFile = "relay-token"
	instanceIDFile = "instance-id"
)

// fileConfig is the part of homed.yaml the spike reads. T1.3 moves this to internal/config.
type fileConfig struct {
	HomeName string `yaml:"home_name"`
	Listen   string `yaml:"listen"`
	Relay    struct {
		Domain string `yaml:"domain"`
	} `yaml:"relay"`
}

// resolveDataDir applies --data-dir, then HOMED_DATA_DIR, then the platform default, and makes the dir (0700).
func resolveDataDir(flagValue string) (string, error) {
	dir := flagValue
	if dir == "" {
		dir = os.Getenv("HOMED_DATA_DIR")
	}
	if dir == "" {
		var err error
		if dir, err = defaultDataDir(); err != nil {
			return "", err
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // a directory needs the x bit; 0700 is owner-only
		return "", fmt.Errorf("secure data dir: %w", err)
	}
	return dir, nil
}

func defaultDataDir() (string, error) {
	if runtime.GOOS == "darwin" {
		base, err := os.UserConfigDir() // ~/Library/Application Support
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "gpt-home"), nil
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "gpt-home"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "gpt-home"), nil
}

// loadConfig reads homed.yaml; a missing file is an empty config.
func loadConfig(dir string) (fileConfig, error) {
	var c fileConfig
	raw, err := os.ReadFile(filepath.Join(dir, configFile)) //nolint:gosec // the hub's own data dir
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("%s: %w", configFile, err)
	}
	return c, nil
}

// setRelayDomain writes relay.domain into homed.yaml and keeps everything else in the file, comments included.
func setRelayDomain(dir, domain string) error {
	path := filepath.Join(dir, configFile)
	raw, err := os.ReadFile(path) //nolint:gosec // the hub's own data dir
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("%s: %w", configFile, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: the top level must be a mapping", configFile)
	}
	relayNode := mappingChild(doc.Content[0], "relay", yaml.MappingNode)
	if relayNode.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: relay must be a mapping", configFile)
	}
	d := mappingChild(relayNode, "domain", yaml.ScalarNode)
	d.Kind, d.Tag, d.Value, d.Style = yaml.ScalarNode, "!!str", domain, 0

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes(), 0o600)
}

// mappingChild returns the value node of key in m, appending an empty node of the given kind when key is absent.
func mappingChild(m *yaml.Node, key string, kind yaml.Kind) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	v := &yaml.Node{Kind: kind}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	return v
}

// readSecretFile reads a 0600 secret file and refuses one readable by group or others.
func readSecretFile(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is readable by others (mode %o); run: chmod 600 %s", path, st.Mode().Perm(), path)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the hub's own data dir
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// instanceID returns the hub's persistent random id, creating it on first use.
func instanceID(dir string) (string, error) {
	path := filepath.Join(dir, instanceIDFile)
	raw, err := os.ReadFile(path) //nolint:gosec // the hub's own data dir
	if err == nil {
		if id := strings.TrimSpace(string(raw)); id != "" {
			return id, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	if err := writeFileAtomic(path, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// writeFileAtomic writes through a temp file in the same dir and renames it over path.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op after the rename
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
