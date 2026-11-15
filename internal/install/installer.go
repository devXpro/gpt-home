package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Job step names, as in ADMIN-API §3.
const (
	StepResolve  = "resolve"
	StepDownload = "download"
	StepVerify   = "verify"
	StepUnpack   = "unpack"
	StepSwitch   = "switch"
	StepStart    = "start"
	StepRollback = "rollback"
	StepReplace  = "replace"
	StepRestart  = "restart"
)

// Hooks connect the installer to the rest of homed (T2.6 supplies them over the store and the plugin host).
// Every field is optional.
type Hooks struct {
	// Step is called as each job step begins.
	Step func(step string)
	// Progress reports archive download progress.
	Progress Progress
	// Switch persists the new version as the plugin's current one (the store row: Command, Version,
	// PreviousVersion). It runs before Start and only for updates. An error aborts the update with nothing
	// changed on disk but the new, unused version directory, which is removed.
	Switch func(ctx context.Context, next *Installed) error
	// Start makes the new version live: for an install it creates the store row and starts the plugin; for an
	// update it restarts the plugin on the switched command. It must return nil only when the plugin is up
	// (initialize + tools/list answered). An error means the new version did not come up.
	Start func(ctx context.Context, next *Installed) error
	// Rollback puts the previous version back after a failed Start (store row and a restart). It runs on a
	// context that is not cancelled with the job's.
	Rollback func(ctx context.Context, prev *Installed) error
}

func (h *Hooks) step(s string) {
	if h != nil && h.Step != nil {
		h.Step(s)
	}
}

// Installed is one unpacked, verified plugin release on disk.
type Installed struct {
	ID       string
	Repo     string
	Version  string
	Manifest *Manifest
	Dir      string   // <data_dir>/plugins/<id>/bin/<version>
	Binary   string   // absolute path of the executable inside Dir
	Command  []string // Binary + manifest args: what plugins.Spec.Command and store.Plugin.Command hold
}

// Candidate is a resolved release that has not been downloaded yet. It is what GET /plugins/preview shows.
type Candidate struct {
	Release     *Release
	Manifest    *Manifest
	RawManifest []byte // the manifest exactly as released
	Asset       Asset
	AssetFound  bool
	Compatible  bool
}

// Err returns ErrNoAsset or ErrIncompatible when the candidate cannot be installed, else nil.
func (c *Candidate) Err() error {
	if !c.AssetFound {
		return fmt.Errorf("%w: %s %s", ErrNoAsset, c.Release.Repo, c.Release.Tag)
	}
	if !c.Compatible {
		return fmt.Errorf("%w: %s needs hub %s", ErrIncompatible, c.Manifest.ID, c.Manifest.MinHubVersion)
	}
	return nil
}

// Installer manages plugin releases under <DataDir>/plugins/<id>/bin/<version>/.
type Installer struct {
	Client     *Client
	DataDir    string
	HubVersion string // the running hub's version, for minHubVersion; a dev build is compatible with everything
	GOOS       string // default runtime.GOOS (tests override)
	GOARCH     string // default runtime.GOARCH
	Limits     Limits
}

func (i *Installer) pluginsDir() string { return filepath.Join(i.DataDir, "plugins") }

func (i *Installer) binDir(id string) string { return filepath.Join(i.pluginsDir(), id, "bin") }

// VersionDir returns <data_dir>/plugins/<id>/bin/<version>.
func (i *Installer) VersionDir(id, version string) string {
	return filepath.Join(i.binDir(id), version)
}

func (i *Installer) platform() (string, string) {
	goos, goarch := i.GOOS, i.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	return goos, goarch
}

// Resolve finds the release (version "" or "latest" means the latest one), reads its manifest and looks for the
// archive for this OS and CPU. It downloads only the small manifest.
func (i *Installer) Resolve(ctx context.Context, repo, version string) (*Candidate, error) {
	var rel *Release
	var err error
	if version == "" || version == "latest" {
		rel, err = i.Client.Latest(ctx, repo)
	} else {
		rel, err = i.Client.ReleaseByVersion(ctx, repo, version)
	}
	if err != nil {
		return nil, err
	}
	m, raw, err := i.Client.FetchManifest(ctx, rel)
	if err != nil {
		return nil, err
	}
	goos, goarch := i.platform()
	c := &Candidate{Release: rel, Manifest: m, RawManifest: raw, Compatible: m.Compatible(i.HubVersion)}
	if a, err := FindAsset(rel, m.Binary, goos, goarch); err == nil {
		c.Asset, c.AssetFound = a, true
	} else if !errors.Is(err, ErrNoAsset) {
		return nil, err
	}
	return c, nil
}

// Install resolves repo at version, downloads, verifies and unpacks it, then calls hooks.Start. wantID, when not
// empty, must equal the manifest's id (the plugin's tools are prefixed with it, so it cannot be renamed). If
// Start fails the unpacked files are removed and the error wraps ErrStartFailed. The caller checks that the id
// is free in the store first.
func (i *Installer) Install(ctx context.Context, repo, version, wantID string, hooks *Hooks) (*Installed, error) {
	hooks.step(StepResolve)
	cand, err := i.Resolve(ctx, repo, version)
	if err != nil {
		return nil, err
	}
	if wantID != "" && wantID != cand.Manifest.ID {
		return nil, fmt.Errorf("%w: asked for %q, the release is %q", ErrIDMismatch, wantID, cand.Manifest.ID)
	}
	if err := cand.Err(); err != nil {
		return nil, err
	}
	_, statErr := os.Stat(filepath.Join(i.pluginsDir(), cand.Manifest.ID))
	fresh := errors.Is(statErr, os.ErrNotExist)

	next, err := i.Fetch(ctx, cand, hooks)
	if err != nil {
		return nil, err
	}
	hooks.step(StepStart)
	if hooks != nil && hooks.Start != nil {
		if err := hooks.Start(ctx, next); err != nil {
			_ = i.removeVersion(next.ID, next.Version)
			if fresh {
				_ = i.removePluginDir(next.ID)
			}
			return nil, fmt.Errorf("%w: %w", ErrStartFailed, err)
		}
	}
	return next, nil
}

// Update moves an installed plugin from current to version ("" or "latest" is the latest release): download,
// verify, unpack, hooks.Switch, hooks.Start. If Start fails, hooks.Rollback restores current and the new files
// are removed; the error then wraps ErrStartFailed and ErrRolledBack. ErrUpToDate when version is current. After
// a success only the new and the previous version stay on disk.
func (i *Installer) Update(ctx context.Context, id, repo, current, version string, hooks *Hooks) (*Installed, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	hooks.step(StepResolve)
	cand, err := i.Resolve(ctx, repo, version)
	if err != nil {
		return nil, err
	}
	if cand.Manifest.ID != id {
		return nil, fmt.Errorf("%w: installed %q, the release is %q", ErrIDMismatch, id, cand.Manifest.ID)
	}
	if cand.Release.Version == current {
		return nil, fmt.Errorf("%w: %s %s", ErrUpToDate, id, current)
	}
	if err := cand.Err(); err != nil {
		return nil, err
	}
	var prev *Installed
	if current != "" {
		if p, err := i.Locate(id, current); err == nil {
			prev = p
		}
	}
	next, err := i.Fetch(ctx, cand, hooks)
	if err != nil {
		return nil, err
	}
	hooks.step(StepSwitch)
	if hooks != nil && hooks.Switch != nil {
		if err := hooks.Switch(ctx, next); err != nil {
			_ = i.removeVersion(id, next.Version)
			return nil, err
		}
	}
	hooks.step(StepStart)
	if hooks != nil && hooks.Start != nil {
		if err := hooks.Start(ctx, next); err != nil {
			return nil, i.rollback(ctx, hooks, prev, next, err)
		}
	}
	_ = i.Prune(id, next.Version, current)
	return next, nil
}

func (i *Installer) rollback(ctx context.Context, hooks *Hooks, prev, next *Installed, cause error) error {
	err := fmt.Errorf("%w: %w", ErrStartFailed, cause)
	if prev == nil || hooks == nil || hooks.Rollback == nil {
		return err
	}
	hooks.step(StepRollback)
	if rbErr := hooks.Rollback(context.WithoutCancel(ctx), prev); rbErr != nil {
		return fmt.Errorf("%w; rollback to %s failed: %w", err, prev.Version, rbErr)
	}
	_ = i.removeVersion(next.ID, next.Version)
	return fmt.Errorf("%w: %w", ErrRolledBack, err)
}

// Fetch downloads, verifies and unpacks a candidate into <data_dir>/plugins/<id>/bin/<version>/ and returns it.
// The directory appears atomically (one rename from a staging directory on the same filesystem); an existing
// directory of the same version is replaced. Nothing is unpacked before the sha256 matches checksums.txt.
func (i *Installer) Fetch(ctx context.Context, cand *Candidate, hooks *Hooks) (*Installed, error) {
	if err := cand.Err(); err != nil {
		return nil, err
	}
	m, rel := cand.Manifest, cand.Release
	if err := checkID(m.ID); err != nil {
		return nil, err
	}
	lim := i.Limits.withDefaults()
	if err := os.MkdirAll(i.pluginsDir(), 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(i.pluginsDir(), ".stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	hooks.step(StepDownload)
	var pr Progress
	if hooks != nil {
		pr = hooks.Progress
	}
	archive, err := i.Client.Download(ctx, cand.Asset, stage, lim.MaxArchiveBytes, pr)
	if err != nil {
		return nil, err
	}
	hooks.step(StepVerify)
	if err := i.Client.Verify(ctx, rel, cand.Asset, archive); err != nil {
		return nil, err
	}
	hooks.step(StepUnpack)
	unpacked := filepath.Join(stage, "unpacked")
	if err := os.Mkdir(unpacked, 0o750); err != nil {
		return nil, err
	}
	if err := Unpack(archive, unpacked, lim); err != nil {
		return nil, err
	}
	if err := flattenSingleDir(unpacked, m.Binary); err != nil {
		return nil, err
	}
	bin := filepath.Join(unpacked, m.Binary)
	if err := checkExecutable(bin); err != nil {
		return nil, err
	}
	// The verified-over-https manifest is stored next to the binary so Locate works offline.
	if err := os.WriteFile(filepath.Join(unpacked, ManifestAsset), cand.RawManifest, 0o644); err != nil { //nolint:gosec // not a secret
		return nil, err
	}

	final := i.VersionDir(m.ID, rel.Version)
	if err := os.MkdirAll(i.binDir(m.ID), 0o755); err != nil { //nolint:gosec // the data dir above is 0700
		return nil, err
	}
	if err := commitDir(unpacked, final, filepath.Join(stage, "old")); err != nil {
		return nil, err
	}
	return installedFrom(m, cand.Release.Repo, rel.Version, final), nil
}

func installedFrom(m *Manifest, repo, version, dir string) *Installed {
	bin := filepath.Join(dir, m.Binary)
	return &Installed{
		ID: m.ID, Repo: repo, Version: version, Manifest: m, Dir: dir, Binary: bin,
		Command: append([]string{bin}, m.Args...),
	}
}

// Locate reads an installed version back from disk (its stored manifest) without any network access.
func (i *Installer) Locate(id, version string) (*Installed, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	v, err := NormalizeVersion(version)
	if err != nil {
		return nil, err
	}
	dir := i.VersionDir(id, v)
	data, err := os.ReadFile(filepath.Join(dir, ManifestAsset)) //nolint:gosec // id and version were validated
	if err != nil {
		return nil, fmt.Errorf("install: %s %s is not installed: %w", id, v, err)
	}
	m, err := ParseManifest(data)
	if err != nil {
		return nil, err
	}
	if m.ID != id {
		return nil, fmt.Errorf("%w: directory of %q holds %q", ErrIDMismatch, id, m.ID)
	}
	if err := checkExecutable(filepath.Join(dir, m.Binary)); err != nil {
		return nil, err
	}
	return installedFrom(m, "", v, dir), nil
}

// Prune deletes every installed version of id except those in keep. Empty names in keep are ignored.
func (i *Installer) Prune(id string, keep ...string) error {
	if err := checkID(id); err != nil {
		return err
	}
	entries, err := os.ReadDir(i.binDir(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var errs []error
	for _, e := range entries {
		name := e.Name()
		if isKept(name, keep) || !e.IsDir() {
			continue
		}
		if _, err := NormalizeVersion(name); err != nil { // not one of ours
			continue
		}
		errs = append(errs, i.removeVersion(id, name))
	}
	return errors.Join(errs...)
}

func isKept(name string, keep []string) bool {
	for _, k := range keep {
		if k != "" && k == name {
			return true
		}
	}
	return false
}

// Remove deletes a plugin's files: the whole plugins/<id> directory, or only its bin/ when keepData is true.
// The caller has stopped the plugin and deletes the store row.
func (i *Installer) Remove(id string, keepData bool) error {
	if err := checkID(id); err != nil {
		return err
	}
	if keepData {
		return os.RemoveAll(i.binDir(id))
	}
	return i.removePluginDir(id)
}

func (i *Installer) removePluginDir(id string) error {
	if err := checkID(id); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(i.pluginsDir(), id))
}

func (i *Installer) removeVersion(id, version string) error {
	if err := checkID(id); err != nil {
		return err
	}
	if _, err := NormalizeVersion(version); err != nil {
		return err
	}
	return os.RemoveAll(i.VersionDir(id, version))
}

func checkID(id string) error {
	if !pluginIDRE.MatchString(id) {
		return fmt.Errorf("install: invalid plugin id %q", id)
	}
	return nil
}

// checkExecutable requires a regular file (not a symlink) with an exec bit.
func checkExecutable(p string) error {
	fi, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("%w: the archive has no %s: %w", ErrBadManifest, filepath.Base(p), err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrBadManifest, filepath.Base(p))
	}
	if fi.Mode().Perm()&0o111 == 0 {
		if err := os.Chmod(p, 0o755); err != nil { //nolint:gosec // an installed plugin binary must be executable
			return err
		}
	}
	return nil
}

// flattenSingleDir handles archives wrapped in one top-level directory (GoReleaser wrap_in_directory): when the
// binary is not at the root but the root holds exactly one directory that has it, its children move up.
func flattenSingleDir(dir, binary string) error {
	if _, err := os.Lstat(filepath.Join(dir, binary)); err == nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].IsDir() {
		return nil //nolint:nilerr // the executable check reports the real problem
	}
	inner := filepath.Join(dir, entries[0].Name())
	if _, err := os.Lstat(filepath.Join(inner, binary)); err != nil {
		return nil //nolint:nilerr // same
	}
	children, err := os.ReadDir(inner)
	if err != nil {
		return err
	}
	for _, c := range children {
		if err := os.Rename(filepath.Join(inner, c.Name()), filepath.Join(dir, c.Name())); err != nil {
			return err
		}
	}
	return os.Remove(inner)
}

// commitDir renames staged into final. An existing final moves to aside first and is restored if the rename fails.
func commitDir(staged, final, aside string) error {
	moved := false
	if _, err := os.Lstat(final); err == nil {
		if err := os.Rename(final, aside); err != nil {
			return err
		}
		moved = true
	}
	if err := os.Rename(staged, final); err != nil {
		if moved {
			_ = os.Rename(aside, final)
		}
		return err
	}
	return nil
}
