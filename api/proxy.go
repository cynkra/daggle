package api

import (
	"net"
	"net/http"
	"strings"
)

// WithTrustProxy makes the server honour X-Forwarded-Proto, X-Forwarded-Host
// and X-Forwarded-For.
//
// Enable it only when daggle sits behind a reverse proxy that sets those
// headers itself. Pointed at the open internet it lets any client dictate the
// scheme and address the server believes it is serving.
func WithTrustProxy(trust bool) ServerOption {
	return func(s *Server) {
		s.trustProxy = trust
	}
}

// forwarded rewrites the request from the proxy's headers so handlers,
// redirects and logs see what the external client actually asked for rather
// than the internal hop.
//
// TLS terminates at the proxy in this deployment, so without this the server
// believes every request is plain http:// and any absolute URL it builds
// points at a scheme the client cannot use.
func forwarded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if proto := firstForwardedValue(r.Header.Get("X-Forwarded-Proto")); proto != "" {
			r.URL.Scheme = proto
		}
		if host := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); host != "" {
			r.Host = host
			r.URL.Host = host
		}
		if ip := firstForwardedValue(r.Header.Get("X-Forwarded-For")); ip != "" {
			// Keep the port shape of RemoteAddr so net.SplitHostPort still
			// works downstream; the proxy only forwards the client address.
			if _, port, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				r.RemoteAddr = net.JoinHostPort(ip, port)
			} else {
				r.RemoteAddr = ip
			}
		}
		next.ServeHTTP(w, r)
	})
}

// firstForwardedValue returns the leftmost entry of a comma-separated
// forwarded header, which is the original client's value; later entries are
// added by intermediate proxies.
func firstForwardedValue(h string) string {
	if h == "" {
		return ""
	}
	first, _, _ := strings.Cut(h, ",")
	return strings.TrimSpace(first)
}

// scheme reports the scheme the client used, after any trusted proxy rewrite.
func scheme(r *http.Request) string {
	if r.URL.Scheme != "" {
		return r.URL.Scheme
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
