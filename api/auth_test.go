package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// authTestServer builds a server with the given auth configuration over the
// standard test fixtures.
func authTestServer(t *testing.T, a Auth, opts ...ServerOption) *Server {
	t.Helper()
	srv, _ := setupTestServer(t)
	for _, opt := range append([]ServerOption{WithAuth(a)}, opts...) {
		opt(srv)
	}
	return srv
}

func TestAuthNoneAllowsEverything(t *testing.T) {
	srv := authTestServer(t, Auth{Mode: AuthModeNone})

	req := httptest.NewRequest("GET", "/api/v1/dags", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAuthDefaultIsUnauthenticated(t *testing.T) {
	// The zero-value Auth must preserve the historical loopback posture:
	// adding the middleware cannot change behaviour for existing deployments.
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/api/v1/dags", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestAuthBasic(t *testing.T) {
	srv := authTestServer(t, Auth{Mode: AuthModeBasic, Username: "alice", Password: "s3cret"})

	tests := []struct {
		name     string
		user     string
		pass     string
		withAuth bool
		want     int
	}{
		{"no credentials", "", "", false, http.StatusUnauthorized},
		{"correct", "alice", "s3cret", true, http.StatusOK},
		{"wrong password", "alice", "nope", true, http.StatusUnauthorized},
		{"wrong username", "bob", "s3cret", true, http.StatusUnauthorized},
		{"empty password", "alice", "", true, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/dags", nil)
			if tt.withAuth {
				req.SetBasicAuth(tt.user, tt.pass)
			}
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestAuthBasicChallenge(t *testing.T) {
	srv := authTestServer(t, Auth{Mode: AuthModeBasic, Username: "alice", Password: "s3cret"})

	req := httptest.NewRequest("GET", "/api/v1/dags", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if got := w.Header().Get("WWW-Authenticate"); got != `Basic realm="daggle"` {
		t.Fatalf("WWW-Authenticate = %q, want a Basic challenge", got)
	}
}

func TestAuthToken(t *testing.T) {
	const token = "abc123"
	srv := authTestServer(t, Auth{Mode: AuthModeToken, Token: token})

	tests := []struct {
		name    string
		prepare func(*http.Request)
		want    int
	}{
		{"no credentials", func(*http.Request) {}, http.StatusUnauthorized},
		{"bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }, http.StatusOK},
		{"bearer lowercase scheme", func(r *http.Request) { r.Header.Set("Authorization", "bearer "+token) }, http.StatusOK},
		{"bearer wrong", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		// A browser cannot send a bearer header, so the token doubles as a
		// basic-auth password with any username.
		{"basic with token as password", func(r *http.Request) { r.SetBasicAuth("anyone", token) }, http.StatusOK},
		{"basic with wrong password", func(r *http.Request) { r.SetBasicAuth("anyone", "nope") }, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/dags", nil)
			tt.prepare(req)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Fatalf("status = %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestAuthGuardsStateChangingRoutes(t *testing.T) {
	// The endpoints that actually execute code are the ones that matter.
	srv := authTestServer(t, Auth{Mode: AuthModeBasic, Username: "alice", Password: "s3cret"})

	for _, path := range []string{
		"/api/v1/dags/test-dag/run",
		"/api/v1/runs/cleanup",
		"/api/v1/projects",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("POST", path, nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestLivenessIsPublicButHealthIsNot(t *testing.T) {
	srv := authTestServer(t, Auth{Mode: AuthModeToken, Token: "abc123"})

	// A container healthcheck must work without credentials...
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want %d", w.Code, http.StatusOK)
	}

	// ...but the detailed health endpoint reports scheduler state, so it stays
	// behind auth.
	req = httptest.NewRequest("GET", "/api/v1/health", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("/api/v1/health status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestLivenessIsPublicUnderBasePath(t *testing.T) {
	srv := authTestServer(t, Auth{Mode: AuthModeToken, Token: "abc123"}, WithBasePath("/daggle"))

	req := httptest.NewRequest("GET", "/daggle/healthz", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
