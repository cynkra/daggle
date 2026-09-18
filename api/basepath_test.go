package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeBasePath(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/", ""},
		{"daggle", "/daggle"},
		{"/daggle", "/daggle"},
		{"/daggle/", "/daggle"},
		{"  /daggle/  ", "/daggle"},
		{"/a/b", "/a/b"},
	}

	for _, tt := range tests {
		if got := normalizeBasePath(tt.in); got != tt.want {
			t.Errorf("normalizeBasePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBasePathRouting(t *testing.T) {
	srv, _ := setupTestServer(t)
	WithBasePath("/daggle")(srv)
	h := srv.Handler()

	tests := []struct {
		name string
		path string
		want int
	}{
		{"prefixed api", "/daggle/api/v1/dags", http.StatusOK},
		{"prefixed ui", "/daggle/ui/", http.StatusOK},
		{"prefixed static", "/daggle/ui/static/style.css", http.StatusOK},
		{"unprefixed api is not served", "/api/v1/dags", http.StatusNotFound},
		{"unrelated path", "/other/", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Fatalf("GET %s = %d, want %d", tt.path, w.Code, tt.want)
			}
		})
	}
}

func TestBasePathBarePrefixRedirects(t *testing.T) {
	srv, _ := setupTestServer(t)
	WithBasePath("/daggle")(srv)

	req := httptest.NewRequest("GET", "/daggle", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusMovedPermanently)
	}
	if got := w.Header().Get("Location"); got != "/daggle/" {
		t.Fatalf("Location = %q, want %q", got, "/daggle/")
	}
}

func TestBasePathRootRedirectKeepsPrefix(t *testing.T) {
	srv, _ := setupTestServer(t)
	WithBasePath("/daggle")(srv)

	req := httptest.NewRequest("GET", "/daggle/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	// Without the prefix this would send the browser out of the mounted app.
	if got := w.Header().Get("Location"); got != "/daggle/ui/" {
		t.Fatalf("Location = %q, want %q", got, "/daggle/ui/")
	}
}

func TestBasePathUILinksArePrefixed(t *testing.T) {
	srv, _ := setupTestServer(t)
	WithBasePath("/daggle")(srv)

	req := httptest.NewRequest("GET", "/daggle/ui/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()

	// Every internal link the page emits has to carry the prefix, or the
	// stylesheet 404s and navigation escapes the mount point.
	for _, want := range []string{
		`href="/daggle/ui/static/style.css"`,
		`href="/daggle/ui/"`,
		`href="/daggle/api/v1/health"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %s", want)
		}
	}
	if strings.Contains(body, `href="/ui/`) {
		t.Error("page still contains an unprefixed /ui/ link")
	}
}

func TestNoBasePathIsUnchanged(t *testing.T) {
	srv, _ := setupTestServer(t)

	req := httptest.NewRequest("GET", "/ui/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if body := w.Body.String(); !strings.Contains(body, `href="/ui/static/style.css"`) {
		t.Error("unmounted server should emit root-relative links")
	}
}

func TestLinkPrefixes(t *testing.T) {
	srv, _ := setupTestServer(t)
	if got := srv.Link("/ui/"); got != "/ui/" {
		t.Errorf("Link without base path = %q, want %q", got, "/ui/")
	}
	WithBasePath("/daggle")(srv)
	if got := srv.Link("/ui/"); got != "/daggle/ui/" {
		t.Errorf("Link with base path = %q, want %q", got, "/daggle/ui/")
	}
}
