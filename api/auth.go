package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// Auth modes accepted by WithAuth.
const (
	// AuthModeNone disables authentication. Only safe on a loopback bind;
	// `daggle serve` refuses to combine it with a non-loopback address.
	AuthModeNone = "none"
	// AuthModeBasic accepts a single shared username and password over HTTP
	// Basic. This is the browser-facing mode: it makes the status UI usable
	// behind a reverse proxy.
	AuthModeBasic = "basic"
	// AuthModeToken accepts a single shared bearer token, supplied either as
	// "Authorization: Bearer <token>" or as the password half of HTTP Basic
	// with any username. The latter is what lets a browser reach the UI in
	// token mode.
	AuthModeToken = "token"
)

// Auth holds the resolved single-tenant credentials for the API server.
// The zero value means no authentication.
type Auth struct {
	Mode     string
	Username string
	Password string
	Token    string
}

// enabled reports whether any credential check applies.
func (a Auth) enabled() bool {
	return a.Mode == AuthModeBasic || a.Mode == AuthModeToken
}

// WithAuth configures single-tenant authentication for every route except the
// unauthenticated liveness probe (see Server.Handler).
func WithAuth(a Auth) ServerOption {
	return func(s *Server) {
		s.auth = a
	}
}

// secretEqual compares two secrets without leaking their contents or lengths
// through timing. Both sides are hashed first so the comparison always runs
// over a fixed 32 bytes.
func secretEqual(got, want string) bool {
	g := sha256.Sum256([]byte(got))
	w := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(g[:], w[:]) == 1
}

// bearerToken extracts the token from an "Authorization: Bearer <token>"
// header. Returns "" when the header is absent or uses another scheme.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// authorized reports whether the request carries acceptable credentials.
//
// Both branches evaluate their comparison unconditionally rather than
// short-circuiting on a missing header, so a wrong username costs the same as
// a wrong password.
func (a Auth) authorized(r *http.Request) bool {
	switch a.Mode {
	case AuthModeBasic:
		user, pass, ok := r.BasicAuth()
		userOK := secretEqual(user, a.Username)
		passOK := secretEqual(pass, a.Password)
		return ok && userOK && passOK
	case AuthModeToken:
		if tok := bearerToken(r); tok != "" {
			return secretEqual(tok, a.Token)
		}
		// Basic with the token as the password, any username. Browsers can
		// produce this; a bare bearer header they cannot.
		_, pass, ok := r.BasicAuth()
		return ok && secretEqual(pass, a.Token)
	default:
		return true
	}
}

// challenge writes the 401 that tells a client how to authenticate. Both modes
// advertise Basic so a browser shows its login prompt; token mode additionally
// advertises Bearer for API clients.
func (a Auth) challenge(w http.ResponseWriter) {
	if a.Mode == AuthModeToken {
		w.Header().Set("WWW-Authenticate", `Bearer realm="daggle", Basic realm="daggle"`)
	} else {
		w.Header().Set("WWW-Authenticate", `Basic realm="daggle"`)
	}
	writeError(w, http.StatusUnauthorized, "unauthorized")
}

// requireAuth wraps next with the configured credential check. Requests to
// publicPaths are always allowed through so a container healthcheck does not
// need credentials.
func (a Auth) requireAuth(next http.Handler, publicPaths map[string]bool) http.Handler {
	if !a.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		if !a.authorized(r) {
			a.challenge(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}
