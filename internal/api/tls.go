package api

import (
	"net"
	"net/http"
	"os"
	"time"
)

// RedirectToHTTPS answers every request with a 301 to the same path on
// https, at httpsPort.
//
// It exists because operators type a bare host:port. Once the UI moves to
// TLS, the address in everyone's muscle memory and bookmarks stops working,
// and the failure mode — a browser hanging on a TLS handshake that never
// comes — gives no hint about what changed. Redirecting turns that into a
// working link.
//
// http here is a signpost, never a fallback: the handler serves nothing but
// redirects, so an operator who ignores the scheme still ends up encrypted
// rather than quietly working in the clear.
//
// The Host header is stripped of any port before the target is built —
// otherwise a request to host:8080 would redirect to https://host:8080,
// straight back to this listener.
func RedirectToHTTPS(httpsPort string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host == "" {
			http.Error(w, "missing Host header", http.StatusBadRequest)
			return
		}
		if httpsPort != "" && httpsPort != "443" {
			host = net.JoinHostPort(host, httpsPort)
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}

// ListenAndServeTLS is ListenAndServe with a certificate. The timeout budget
// is identical and documented on ListenAndServe; TLS changes nothing about
// the worst-case action duration that WriteTimeout is sized for.
func (s *Server) ListenAndServeTLS(addr, certFile, keyFile string, handler http.Handler) error {
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 180 * time.Second,
	}
	return srv.ListenAndServeTLS(certFile, keyFile)
}

// TLSFromEnv reads the certificate pair. Both empty means "serve plain
// HTTP", which is the default and matches every station deployed so far.
func TLSFromEnv() (certFile, keyFile, addr, redirectPort string) {
	certFile = os.Getenv("DOCKER_UPDATE_TLS_CERT")
	keyFile = os.Getenv("DOCKER_UPDATE_TLS_KEY")

	addr = os.Getenv("DOCKER_UPDATE_TLS_ADDR")
	if addr == "" {
		addr = ":8443"
	}
	// The port callers should be sent to, derived from addr so the two
	// cannot drift.
	if _, p, err := net.SplitHostPort(addr); err == nil {
		redirectPort = p
	}
	return certFile, keyFile, addr, redirectPort
}
