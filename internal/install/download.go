package install

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

const maxChecksumsBytes = 1 << 20

var sha256RE = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// Progress receives download progress. total is 0 when unknown.
type Progress func(done, total int64)

// ParseChecksums reads a checksums.txt ("<sha256>  <name>" per line, "*name" for binary mode) into name -> hex.
func ParseChecksums(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, " ")
		name = strings.TrimPrefix(strings.TrimSpace(name), "*")
		if !ok || name == "" || !sha256RE.MatchString(sum) {
			return nil, fmt.Errorf("%w: malformed checksums line", ErrNoChecksum)
		}
		sum = strings.ToLower(sum)
		if prev, dup := out[name]; dup && prev != sum {
			return nil, fmt.Errorf("%w: conflicting lines for %s", ErrNoChecksum, name)
		}
		out[name] = sum
	}
	return out, sc.Err()
}

// Checksums downloads and parses the release's checksums.txt.
func (c *Client) Checksums(ctx context.Context, rel *Release) (map[string]string, error) {
	a, ok := rel.Asset(ChecksumsAsset)
	if !ok {
		return nil, fmt.Errorf("%w: release %s has no %s", ErrNoChecksum, rel.Tag, ChecksumsAsset)
	}
	data, err := c.get(ctx, a.URL, false, maxChecksumsBytes)
	if err != nil {
		return nil, err
	}
	return ParseChecksums(data)
}

// Download streams asset into a new 0600 file inside dir and returns its path; the caller removes it. maxBytes caps
// the download (the advertised size is checked up front too). The bytes are NOT trusted until Verify passes.
func (c *Client) Download(ctx context.Context, a Asset, dir string, maxBytes int64, progress Progress) (string, error) {
	if a.Size > maxBytes {
		return "", fmt.Errorf("%w: %s is %d bytes, limit %d", ErrTooLarge, a.Name, a.Size, maxBytes)
	}
	// The archive may be big and slow: no short API timeout, only the caller's context.
	resp, err := c.open(ctx, a.URL, false)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	total := resp.ContentLength
	if total > maxBytes {
		return "", fmt.Errorf("%w: %s is %d bytes, limit %d", ErrTooLarge, a.Name, total, maxBytes)
	}
	f, err := os.CreateTemp(dir, "download-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	fail := func(err error) (string, error) {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	var done int64
	buf := make([]byte, 64<<10)
	src := io.LimitReader(resp.Body, maxBytes+1)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > maxBytes {
				return fail(fmt.Errorf("%w: %s exceeds %d bytes", ErrTooLarge, a.Name, maxBytes))
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return fail(werr)
			}
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fail(fmt.Errorf("%w: download of %s: %w", ErrUpstream, a.Name, rerr))
		}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// Verify compares the sha256 of the file at path with the line for asset in the release's checksums.txt.
// Nothing may be unpacked before it returns nil. A missing checksums.txt or line is ErrNoChecksum, a different
// hash is ErrChecksumMismatch.
func (c *Client) Verify(ctx context.Context, rel *Release, a Asset, path string) error {
	sums, err := c.Checksums(ctx, rel)
	if err != nil {
		return err
	}
	want, ok := sums[a.Name]
	if !ok {
		return fmt.Errorf("%w: %s is not listed in %s", ErrNoChecksum, a.Name, ChecksumsAsset)
	}
	f, err := os.Open(path) //nolint:gosec // the caller passes the file Download just wrote
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	wantRaw, _ := hex.DecodeString(want)
	if subtle.ConstantTimeCompare(h.Sum(nil), wantRaw) != 1 {
		return fmt.Errorf("%w: %s", ErrChecksumMismatch, a.Name)
	}
	return nil
}
