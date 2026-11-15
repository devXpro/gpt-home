package install

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Limits bounds what the installer downloads and unpacks. Zero fields take the defaults.
type Limits struct {
	MaxArchiveBytes  int64 // compressed download, default 256 MiB
	MaxUnpackedBytes int64 // all regular files together, default 512 MiB
	MaxEntries       int   // archive entries, default 2000
}

func (l Limits) withDefaults() Limits {
	if l.MaxArchiveBytes <= 0 {
		l.MaxArchiveBytes = 256 << 20
	}
	if l.MaxUnpackedBytes <= 0 {
		l.MaxUnpackedBytes = 512 << 20
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = 2000
	}
	return l
}

// Unpack extracts the tar.gz at archive into dest, which must exist and should be empty. It refuses absolute and
// ".." paths, duplicate entries, hard links, devices and FIFOs, symlinks that are absolute or point outside dest,
// and anything over the limits (counted on real decompressed bytes, so a gzip bomb stops at the cap). Every
// filesystem call goes through os.Root, which independently refuses to leave dest. Permissions are normalized to
// 0755 (any exec bit) or 0644; setuid and friends are dropped.
func Unpack(archive, dest string, lim Limits) error {
	lim = lim.withDefaults()
	f, err := os.Open(archive) //nolint:gosec // the caller passes its own verified download
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%w: not a gzip file: %w", ErrUnsafeArchive, err)
	}
	defer gz.Close()
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()

	tr := tar.NewReader(gz)
	remaining := lim.MaxUnpackedBytes
	entries := 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrUnsafeArchive, err)
		}
		entries++
		if entries > lim.MaxEntries {
			return fmt.Errorf("%w: more than %d entries", ErrTooLarge, lim.MaxEntries)
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name, err := cleanEntryName(hdr.Name)
		if err != nil {
			return err
		}
		if name == "" { // the "./" root entry
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o755); err != nil {
				return fmt.Errorf("%w: %s: %w", ErrUnsafeArchive, name, err)
			}
		case tar.TypeReg:
			if hdr.Size > remaining {
				return fmt.Errorf("%w: unpacked size over %d bytes", ErrTooLarge, lim.MaxUnpackedBytes)
			}
			n, err := writeFile(root, name, tr, hdr.FileInfo().Mode(), remaining)
			if err != nil {
				return err
			}
			remaining -= n
		case tar.TypeSymlink:
			if err := checkSymlink(name, hdr.Linkname); err != nil {
				return err
			}
			if err := mkParent(root, name); err != nil {
				return err
			}
			if err := root.Symlink(hdr.Linkname, name); err != nil {
				return fmt.Errorf("%w: symlink %s: %w", ErrUnsafeArchive, name, err)
			}
		default:
			return fmt.Errorf("%w: %s has forbidden entry type %q", ErrUnsafeArchive, name, hdr.Typeflag)
		}
	}
}

// cleanEntryName returns the slash-separated, local, cleaned name; "" for the archive root.
func cleanEntryName(raw string) (string, error) {
	if strings.ContainsAny(raw, "\x00\\") {
		return "", fmt.Errorf("%w: bad entry name %q", ErrUnsafeArchive, raw)
	}
	if path.IsAbs(raw) {
		return "", fmt.Errorf("%w: absolute entry name %q", ErrUnsafeArchive, raw)
	}
	for _, part := range strings.Split(raw, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: path traversal in %q", ErrUnsafeArchive, raw)
		}
	}
	name := path.Clean(raw)
	if name == "." {
		return "", nil
	}
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("%w: non-local entry name %q", ErrUnsafeArchive, raw)
	}
	return name, nil
}

// checkSymlink accepts only relative targets that stay inside the archive root once resolved from the link's directory.
func checkSymlink(name, target string) error {
	if target == "" || strings.ContainsAny(target, "\x00\\") || path.IsAbs(target) {
		return fmt.Errorf("%w: symlink %s -> %q", ErrUnsafeArchive, name, target)
	}
	resolved := path.Join(path.Dir(name), target)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return fmt.Errorf("%w: symlink %s escapes the target directory", ErrUnsafeArchive, name)
	}
	return nil
}

func mkParent(root *os.Root, name string) error {
	if dir := path.Dir(name); dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrUnsafeArchive, name, err)
		}
	}
	return nil
}

func writeFile(root *os.Root, name string, src io.Reader, mode fs.FileMode, limit int64) (int64, error) {
	if err := mkParent(root, name); err != nil {
		return 0, err
	}
	perm := fs.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", ErrUnsafeArchive, name, err)
	}
	n, err := io.Copy(f, io.LimitReader(src, limit+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", ErrUnsafeArchive, name, err)
	}
	if n > limit {
		return 0, fmt.Errorf("%w: unpacked size limit reached at %s", ErrTooLarge, name)
	}
	// O_CREATE applies the umask; make the mode exact.
	if err := root.Chmod(name, perm); err != nil {
		return 0, err
	}
	return n, nil
}
