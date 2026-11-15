// Package web serves the admin SPA (web/, built by `make web` into dist/) from the homed binary.
//
// The handler expects full request paths and is mounted by its users:
//   - the hub at /admin/ (the SPA, its hashed assets under /admin/assets/ and the static files of web/public)
//     and at /join (phase 3);
//   - the LAN first-run listener at /firstrun and /admin/ (the page loads its assets from /admin/assets/).
//
// Every path that is not a file gets index.html, so client routes (/admin/devices/tv, /firstrun) survive a reload.
// Hashed assets are cached for a year; everything else revalidates by ETag. Every response carries a strict CSP:
// no inline scripts or styles and no eval (DESIGN.md §12).
package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// cspFile is shared with web/vite.config.ts, which sends the same policy from the mock preview.
//
//go:embed csp.txt
var cspFile string

// CSP returns the Content-Security-Policy the handler sends.
func CSP() string { return strings.TrimSpace(cspFile) }

const (
	assetsPrefix = "/admin/assets/"
	publicPrefix = "/admin/"
	immutable    = "public, max-age=31536000, immutable"
	revalidate   = "no-cache"
)

type file struct {
	body     []byte
	gzipped  []byte // nil when compression does not pay off
	etag     string
	mimeType string
}

// Handler serves the embedded build. Before `make web` it answers 503 with a short hint.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory
	}
	return New(sub)
}

// New serves the SPA build in fsys (index.html at its root); tests pass an fstest.MapFS.
func New(fsys fs.FS) http.Handler {
	files := map[string]*file{}
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(path.Base(p), ".") {
			return nil
		}
		if body, err := fs.ReadFile(fsys, p); err == nil {
			files[p] = load(p, body)
		}
		return nil
	})
	return &handler{files: files, index: files["index.html"], csp: CSP()}
}

func load(p string, body []byte) *file {
	sum := sha256.Sum256(body)
	f := &file{body: body, etag: `"` + hex.EncodeToString(sum[:12]) + `"`, mimeType: mimeOf(p)}
	if compressible(f.mimeType) && len(body) > 1024 {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(body)
		_ = zw.Close()
		if buf.Len() < len(body)*9/10 {
			f.gzipped = buf.Bytes()
		}
	}
	return f
}

func mimeOf(p string) string {
	switch path.Ext(p) {
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".webmanifest":
		return "application/manifest+json"
	case ".png":
		return "image/png"
	}
	if t := mime.TypeByExtension(path.Ext(p)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func compressible(mimeType string) bool {
	return strings.HasPrefix(mimeType, "text/") || strings.Contains(mimeType, "json") || strings.Contains(mimeType, "svg")
}

type handler struct {
	files map[string]*file
	index *file
	csp   string
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Security-Policy", h.csp)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("X-Frame-Options", "DENY")
	hdr.Set("Cross-Origin-Opener-Policy", "same-origin")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hdr.Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.index == nil {
		hdr.Set("Cache-Control", "no-store")
		http.Error(w, "The admin UI is not built into this homed: run `make web`, then rebuild homed.", http.StatusServiceUnavailable)
		return
	}
	p := path.Clean("/" + r.URL.Path)
	if strings.HasPrefix(p, assetsPrefix) {
		// A missing hashed asset is a 404, never index.html: a stale page must not run HTML as a script.
		f := h.files[strings.TrimPrefix(p, publicPrefix)]
		if f == nil {
			hdr.Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		h.serve(w, r, f, immutable)
		return
	}
	if name, ok := strings.CutPrefix(p, publicPrefix); ok && name != "index.html" {
		if f := h.files[name]; f != nil {
			h.serve(w, r, f, revalidate)
			return
		}
	}
	h.serve(w, r, h.index, revalidate)
}

func (h *handler) serve(w http.ResponseWriter, r *http.Request, f *file, cache string) {
	hdr := w.Header()
	hdr.Set("Cache-Control", cache)
	hdr.Set("ETag", f.etag)
	hdr.Set("Content-Type", f.mimeType)
	hdr.Add("Vary", "Accept-Encoding")
	if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, f.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := f.body
	if f.gzipped != nil && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		hdr.Set("Content-Encoding", "gzip")
		body = f.gzipped
	}
	hdr.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func etagMatches(header, etag string) bool {
	for part := range strings.SplitSeq(header, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == etag {
			return true
		}
	}
	return false
}

// acceptsGzip reads Accept-Encoding: gzip is accepted unless its q-value is 0.
func acceptsGzip(header string) bool {
	for part := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		q, found := strings.CutPrefix(strings.ReplaceAll(params, " ", ""), "q=")
		if !found {
			return true
		}
		v, err := strconv.ParseFloat(q, 64)
		return err != nil || v > 0
	}
	return false
}
