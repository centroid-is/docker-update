package api

import (
	"crypto/subtle"
	"net/http"
	"os"
)

// BasicAuth guards the mux with HTTP Basic credentials when both
// DOCKER_UPDATE_BASIC_AUTH_USER and DOCKER_UPDATE_BASIC_AUTH_PASS are set.
//
// CLAUDE.md's Security constraint is "LAN-only, unauthenticated by default",
// and default is the operative word: with either variable empty the
// wrapper returns next unchanged and there is no behaviour change for any
// station already deployed. This is opt-in for sites that would rather not
// have per-container Update/Rollback buttons answer to anyone who can reach
// port 8080.
//
// What it is not: transport security. Basic credentials travel base64-encoded
// in every request, so on a plain-HTTP listener they are readable by anyone
// on the path. This raises the bar from "click the button" to "sniff the LAN
// first"; it does not make :8080 safe to expose beyond the HMI network. Put
// it behind TLS if that is the goal.
//
// /healthz is deliberately exempt. Docker healthchecks, uptime probes and
// `curl -f` smoke tests all hit it, and requiring credentials there would
// mean spreading them into places that have no business holding them. The
// endpoint reports liveness and a daemon ping — nothing an unauthenticated
// caller on the LAN could not infer from the port being open.
func BasicAuth(next http.Handler, user, pass string) http.Handler {
	if user == "" || pass == "" {
		return next
	}

	userDigest := []byte(user)
	passDigest := []byte(pass)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}

		gotUser, gotPass, ok := r.BasicAuth()
		// Both comparisons run unconditionally: short-circuiting on the
		// username would leak, by timing, whether a guessed username was
		// right — the cheapest half of a credential to brute-force.
		userOK := subtle.ConstantTimeCompare([]byte(gotUser), userDigest) == 1
		passOK := subtle.ConstantTimeCompare([]byte(gotPass), passDigest) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="docker-update", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// BasicAuthFromEnv reads the credential pair from the environment. It returns
// empty strings when unset, which BasicAuth treats as "disabled".
func BasicAuthFromEnv() (user, pass string) {
	return os.Getenv("DOCKER_UPDATE_BASIC_AUTH_USER"),
		os.Getenv("DOCKER_UPDATE_BASIC_AUTH_PASS")
}
