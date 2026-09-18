package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// captureRequest runs a request through the middleware and returns what the
// inner handler saw.
func captureRequest(t *testing.T, trust bool, prepare func(*http.Request)) *http.Request {
	t.Helper()
	var seen *http.Request
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r })

	var h http.Handler = inner
	if trust {
		h = forwarded(inner)
	}

	req := httptest.NewRequest("GET", "/api/v1/dags", nil)
	req.RemoteAddr = "10.0.0.5:34567"
	prepare(req)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if seen == nil {
		t.Fatal("inner handler was not called")
	}
	return seen
}

func TestForwardedRewritesRequest(t *testing.T) {
	got := captureRequest(t, true, func(r *http.Request) {
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "dags.example.com")
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
	})

	if got.URL.Scheme != "https" {
		t.Errorf("scheme = %q, want %q", got.URL.Scheme, "https")
	}
	if got.Host != "dags.example.com" {
		t.Errorf("host = %q, want %q", got.Host, "dags.example.com")
	}
	if got.RemoteAddr != "203.0.113.7:34567" {
		t.Errorf("remote addr = %q, want the forwarded client address", got.RemoteAddr)
	}
}

func TestForwardedUsesLeftmostEntry(t *testing.T) {
	// The leftmost entry is the original client; the rest are proxy hops.
	got := captureRequest(t, true, func(r *http.Request) {
		r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1, 10.0.0.2")
		r.Header.Set("X-Forwarded-Proto", "https, http")
	})

	if got.RemoteAddr != "203.0.113.7:34567" {
		t.Errorf("remote addr = %q, want the leftmost entry", got.RemoteAddr)
	}
	if got.URL.Scheme != "https" {
		t.Errorf("scheme = %q, want %q", got.URL.Scheme, "https")
	}
}

func TestForwardedIgnoredWhenNotTrusted(t *testing.T) {
	// Without --trust-proxy a direct client must not be able to dictate the
	// scheme or forge its own address.
	got := captureRequest(t, false, func(r *http.Request) {
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "203.0.113.7")
	})

	if got.URL.Scheme == "https" {
		t.Error("untrusted X-Forwarded-Proto was applied")
	}
	if got.RemoteAddr != "10.0.0.5:34567" {
		t.Errorf("remote addr = %q, want the real peer address", got.RemoteAddr)
	}
}

func TestForwardedToleratesMissingHeaders(t *testing.T) {
	got := captureRequest(t, true, func(*http.Request) {})

	if got.RemoteAddr != "10.0.0.5:34567" {
		t.Errorf("remote addr = %q, want it unchanged", got.RemoteAddr)
	}
	if got.Host != "example.com" {
		t.Errorf("host = %q, want it unchanged", got.Host)
	}
}

func TestSchemeFallsBackToHTTP(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/v1/dags", nil)
	req.URL.Scheme = ""
	if got := scheme(req); got != "http" {
		t.Errorf("scheme = %q, want %q", got, "http")
	}

	req.URL.Scheme = "https"
	if got := scheme(req); got != "https" {
		t.Errorf("scheme = %q, want %q", got, "https")
	}
}

func TestTrustProxyOptionWiresMiddleware(t *testing.T) {
	srv, _ := setupTestServer(t)
	WithTrustProxy(true)(srv)

	req := httptest.NewRequest("GET", "/api/v1/dags", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
