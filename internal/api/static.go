package api

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

// assetCacheControl is the long-cache directive applied to successful
// /assets/* responses. Held in a const so the test in
// handlers_assets_test.go and the writer wrapper below stay in sync.
const assetCacheControl = "public, max-age=31536000, immutable"

// immutableOnSuccessWriter is an http.ResponseWriter wrapper that only
// applies the immutable Cache-Control directive when http.FileServerFS
// responds with a 2xx status. Required because FileServerFS writes 404
// internally when an /assets/* path is missing, and the prior
// implementation set the Cache-Control header before delegating —
// meaning Pitfall 8's strict-404 carried the directive and browsers /
// CDNs honouring `immutable` on 4xx (Chromium, Firefox, most CDNs per
// RFC 9111 §3) would pin the wrong-asset 404 for a year.
//
// Behaviour: capture WriteHeader; if 2xx, set the immutable header
// before flushing the status line; otherwise pass through unchanged.
// The wrapper does NOT swallow the header — non-2xx responses still
// flow through with whatever Cache-Control (if any) the underlying
// handler set. http.FileServerFS sets none on its 404 path, which is
// the desired behaviour (no caching of asset-miss 404s).
type immutableOnSuccessWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *immutableOnSuccessWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		if status >= 200 && status < 300 {
			w.ResponseWriter.Header().Set("Cache-Control", assetCacheControl)
		}
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *immutableOnSuccessWriter) Write(b []byte) (int, error) {
	// http.ResponseWriter contract: Write before WriteHeader implies a
	// 200 status. Mirror that here so the immutable header is applied
	// even on handlers that skip the explicit WriteHeader call.
	if !w.wroteHeader {
		w.ResponseWriter.Header().Set("Cache-Control", assetCacheControl)
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

//go:embed all:dist
var distFS embed.FS

func init() {
	// Distroless minimal envs may lack a system mime.types file. Register the
	// four extensions we actually serve so the static handler emits correct
	// Content-Type headers. See research/PITFALLS.md Pitfall 8.
	_ = mime.AddExtensionType(".js", "application/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
	_ = mime.AddExtensionType(".svg", "image/svg+xml")
	_ = mime.AddExtensionType(".json", "application/json; charset=utf-8")
}

// staticHandler serves the embedded Svelte bundle.
//   - /assets/* — strict; 404 on miss; long immutable Cache-Control.
//   - /          — index.html with no-cache.
//   - everything else — 404.
//
// We do NOT fall back to index.html for /assets/* (research/PITFALLS.md
// Pitfall 8 — that's the MIME-type trap that breaks post-upgrade caches).
func newStaticHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// The //go:embed directive failed — this is a build-time bug, panic
		// is the right response (we'd never start up healthily otherwise).
		panic(err)
	}
	fileServer := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean(r.URL.Path)
		switch {
		case strings.HasPrefix(clean, "/assets/"):
			// Strict static serve. http.FileServerFS returns 404 on miss
			// and sets Content-Type via mime.TypeByExtension. The
			// immutableOnSuccessWriter applies the year-long Cache-Control
			// directive ONLY on 2xx — Pitfall 8's strict-404 path must
			// NOT carry `immutable` (CR-02 in 05-REVIEW.md). A cached
			// asset-miss 404 with `immutable` would lock browsers /
			// proxies onto the wrong response until manual cache clear.
			cw := &immutableOnSuccessWriter{ResponseWriter: w}
			fileServer.ServeHTTP(cw, r)
		case clean == "/" || clean == "/index.html":
			// http.FileServerFS auto-serves index.html for "/" without a
			// redirect; for "/index.html" it 301-redirects to "/" for
			// canonicalization. Normalize to "/" so we never emit the 301
			// (which would otherwise strip our Cache-Control header on the
			// final response and confuse the smoke test).
			w.Header().Set("Cache-Control", "no-cache")
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
		case clean == "/favicon.svg" || clean == "/favicon.ico":
			// Top-level static files copied from ui/public/ by Vite. NOT
			// served with the `immutable` directive — these have a fixed
			// URL (browsers expect /favicon.svg, not a hashed name), so
			// a cache-bust is impossible. Use a 1-day max-age so updates
			// are picked up within a day of deploy.
			w.Header().Set("Cache-Control", "public, max-age=86400")
			fileServer.ServeHTTP(w, r)
		default:
			// No SPA-fallback to index.html — we have no client-side router.
			http.NotFound(w, r)
		}
	})
}
