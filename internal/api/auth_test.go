package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("reached"))
	})
}

func TestBasicAuth(t *testing.T) {
	tests := []struct {
		name     string
		user     string
		pass     string
		sendUser string
		sendPass string
		sendAuth bool
		path     string
		wantCode int
	}{
		{
			name: "disabled when both unset lets everything through",
			path: "/api/state", wantCode: http.StatusOK,
		},
		{
			name: "disabled when only the user is set",
			user: "centroid",
			path: "/api/state", wantCode: http.StatusOK,
		},
		{
			name: "disabled when only the password is set",
			pass: "hunter2",
			path: "/api/state", wantCode: http.StatusOK,
		},
		{
			name: "correct credentials pass",
			user: "centroid", pass: "hunter2",
			sendAuth: true, sendUser: "centroid", sendPass: "hunter2",
			path: "/api/state", wantCode: http.StatusOK,
		},
		{
			name: "wrong password is rejected",
			user: "centroid", pass: "hunter2",
			sendAuth: true, sendUser: "centroid", sendPass: "wrong",
			path: "/api/state", wantCode: http.StatusUnauthorized,
		},
		{
			name: "wrong username is rejected",
			user: "centroid", pass: "hunter2",
			sendAuth: true, sendUser: "someone", sendPass: "hunter2",
			path: "/api/state", wantCode: http.StatusUnauthorized,
		},
		{
			name: "no credentials at all is rejected",
			user: "centroid", pass: "hunter2",
			path: "/api/state", wantCode: http.StatusUnauthorized,
		},
		{
			name: "empty credentials are rejected, not treated as disabled",
			user: "centroid", pass: "hunter2",
			sendAuth: true, sendUser: "", sendPass: "",
			path: "/api/state", wantCode: http.StatusUnauthorized,
		},
		{
			name: "healthz stays open so probes need no credentials",
			user: "centroid", pass: "hunter2",
			path: "/healthz", wantCode: http.StatusOK,
		},
		{
			name: "action endpoints are guarded",
			user: "centroid", pass: "hunter2",
			path: "/api/containers/weston/update", wantCode: http.StatusUnauthorized,
		},
		{
			name: "self-update is guarded despite bypassing self-protection",
			user: "centroid", pass: "hunter2",
			path: "/api/self-update", wantCode: http.StatusUnauthorized,
		},
		{
			name: "the UI itself is guarded",
			user: "centroid", pass: "hunter2",
			path: "/", wantCode: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := BasicAuth(okHandler(), tc.user, tc.pass)
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.sendAuth {
				req.SetBasicAuth(tc.sendUser, tc.sendPass)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if rec.Code == http.StatusUnauthorized {
				if got := rec.Header().Get("WWW-Authenticate"); got == "" {
					t.Error("401 without WWW-Authenticate: browsers will not prompt")
				}
				if rec.Body.String() == "reached" {
					t.Error("handler ran despite a 401")
				}
			}
		})
	}
}

func TestRedirectToHTTPS(t *testing.T) {
	tests := []struct {
		name     string
		port     string
		host     string
		target   string
		wantLoc  string
		wantCode int
	}{
		{
			name: "bare host gets the tls port appended",
			port: "8443", host: "10.50.10.11", target: "/",
			wantLoc: "https://10.50.10.11:8443/", wantCode: http.StatusMovedPermanently,
		},
		{
			name: "the incoming port is replaced, not kept",
			port: "8443", host: "10.50.10.11:8080", target: "/",
			wantLoc: "https://10.50.10.11:8443/", wantCode: http.StatusMovedPermanently,
		},
		{
			name: "path and query survive",
			port: "8443", host: "hmi.local:8080", target: "/api/state?full=1",
			wantLoc: "https://hmi.local:8443/api/state?full=1", wantCode: http.StatusMovedPermanently,
		},
		{
			name: "port 443 is left implicit",
			port: "443", host: "hmi.local:8080", target: "/",
			wantLoc: "https://hmi.local/", wantCode: http.StatusMovedPermanently,
		},
		{
			name: "missing host is a client error, not a redirect to nowhere",
			port: "8443", host: "", target: "/",
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			RedirectToHTTPS(tc.port).ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantLoc != "" {
				if got := rec.Header().Get("Location"); got != tc.wantLoc {
					t.Errorf("Location = %q, want %q", got, tc.wantLoc)
				}
			}
		})
	}
}
