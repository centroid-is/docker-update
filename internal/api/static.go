package api

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

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
			// and sets Content-Type via mime.TypeByExtension.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			fileServer.ServeHTTP(w, r)
		case clean == "/" || clean == "/index.html":
			// http.FileServerFS auto-serves index.html for "/" without a
			// redirect; for "/index.html" it 301-redirects to "/" for
			// canonicalization. Normalize to "/" so we never emit the 301
			// (which would otherwise strip our Cache-Control header on the
			// final response and confuse the smoke test).
			w.Header().Set("Cache-Control", "no-cache")
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
		default:
			// No SPA-fallback to index.html — we have no client-side router.
			http.NotFound(w, r)
		}
	})
}
