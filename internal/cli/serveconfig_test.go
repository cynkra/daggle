package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cynkra/daggle/api"
	"github.com/cynkra/daggle/state"
)

func TestResolveDefaults(t *testing.T) {
	got, err := resolveServerSettings(state.ServerConfig{}, serveFlags{})
	if err != nil {
		t.Fatal(err)
	}

	if got.Bind != defaultBind {
		t.Errorf("bind = %q, want %q", got.Bind, defaultBind)
	}
	if got.Auth.Mode != api.AuthModeNone {
		t.Errorf("auth mode = %q, want %q", got.Auth.Mode, api.AuthModeNone)
	}
	if got.BasePath != "" {
		t.Errorf("base path = %q, want empty", got.BasePath)
	}
	if got.TrustProxy {
		t.Error("trust proxy should default to false")
	}
}

func TestResolvePrecedence(t *testing.T) {
	cfg := state.ServerConfig{
		Bind:     "10.0.0.1",
		Port:     1111,
		BasePath: "/from-file",
		Auth:     state.AuthConfig{Mode: api.AuthModeBasic, Username: "file-user", Password: "file-pass"},
	}

	t.Run("file only", func(t *testing.T) {
		got, err := resolveServerSettings(cfg, serveFlags{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Bind != "10.0.0.1" || got.Port != 1111 || got.BasePath != "/from-file" {
			t.Fatalf("file values not applied: %+v", got)
		}
	})

	t.Run("env overrides file", func(t *testing.T) {
		t.Setenv("DAGGLE_BIND_ADDR", "10.0.0.2")
		t.Setenv("DAGGLE_PORT", "2222")
		t.Setenv("DAGGLE_BASE_PATH", "/from-env")
		got, err := resolveServerSettings(cfg, serveFlags{})
		if err != nil {
			t.Fatal(err)
		}
		if got.Bind != "10.0.0.2" || got.Port != 2222 || got.BasePath != "/from-env" {
			t.Fatalf("env did not override file: %+v", got)
		}
	})

	t.Run("flag overrides env", func(t *testing.T) {
		t.Setenv("DAGGLE_BIND_ADDR", "10.0.0.2")
		t.Setenv("DAGGLE_PORT", "2222")
		flags := serveFlags{bind: "10.0.0.3", bindSet: true, port: 3333, portSet: true}
		got, err := resolveServerSettings(cfg, flags)
		if err != nil {
			t.Fatal(err)
		}
		if got.Bind != "10.0.0.3" || got.Port != 3333 {
			t.Fatalf("flag did not override env: %+v", got)
		}
	})

	t.Run("unset flag does not override", func(t *testing.T) {
		// A flag's zero value must not clobber configuration the operator set
		// elsewhere; only a flag they actually passed counts.
		got, err := resolveServerSettings(cfg, serveFlags{bind: "", port: 0})
		if err != nil {
			t.Fatal(err)
		}
		if got.Bind != "10.0.0.1" || got.Port != 1111 {
			t.Fatalf("unset flags clobbered config: %+v", got)
		}
	})
}

func TestResolveBasePathNormalized(t *testing.T) {
	for in, want := range map[string]string{
		"daggle":   "/daggle",
		"/daggle/": "/daggle",
		"/daggle":  "/daggle",
		"/":        "",
		"":         "",
	} {
		got, err := resolveServerSettings(state.ServerConfig{BasePath: in}, serveFlags{})
		if err != nil {
			t.Fatal(err)
		}
		if got.BasePath != want {
			t.Errorf("base path %q resolved to %q, want %q", in, got.BasePath, want)
		}
	}
}

func TestResolveSecretFiles(t *testing.T) {
	dir := t.TempDir()
	passFile := filepath.Join(dir, "password")
	// Trailing newline is what a decrypting entrypoint or an editor leaves.
	if err := os.WriteFile(passFile, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := state.ServerConfig{
		Auth: state.AuthConfig{Mode: api.AuthModeBasic, Username: "alice", PasswordFile: passFile},
	}
	got, err := resolveServerSettings(cfg, serveFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Auth.Password != "file-secret" {
		t.Errorf("password = %q, want %q", got.Auth.Password, "file-secret")
	}
}

func TestResolveSecretFileMissingIsFatal(t *testing.T) {
	// Starting unauthenticated because a secret file was missing is exactly
	// the failure this must not have.
	cfg := state.ServerConfig{
		Auth: state.AuthConfig{Mode: api.AuthModeBasic, Username: "alice", PasswordFile: "/nonexistent/password"},
	}
	if _, err := resolveServerSettings(cfg, serveFlags{}); err == nil {
		t.Fatal("expected an error for a missing password file")
	}
}

func TestResolveInlineSecretWinsOverFile(t *testing.T) {
	dir := t.TempDir()
	passFile := filepath.Join(dir, "password")
	if err := os.WriteFile(passFile, []byte("from-file"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := state.ServerConfig{
		Auth: state.AuthConfig{Mode: api.AuthModeBasic, Username: "alice", Password: "inline", PasswordFile: passFile},
	}
	got, err := resolveServerSettings(cfg, serveFlags{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Auth.Password != "inline" {
		t.Errorf("password = %q, want the inline value", got.Auth.Password)
	}
}

func TestResolveRejectsBadNumbers(t *testing.T) {
	t.Setenv("DAGGLE_PORT", "eight-thousand")
	if _, err := resolveServerSettings(state.ServerConfig{}, serveFlags{}); err == nil {
		t.Fatal("expected an error for a non-numeric DAGGLE_PORT")
	}
}

func TestValidateGuardrails(t *testing.T) {
	tests := []struct {
		name    string
		s       serverSettings
		wantErr string
	}{
		{
			name: "loopback with no auth is the historical default",
			s:    serverSettings{Bind: "127.0.0.1", Auth: api.Auth{Mode: api.AuthModeNone}},
		},
		{
			name:    "public bind with no auth is refused",
			s:       serverSettings{Bind: "0.0.0.0", Auth: api.Auth{Mode: api.AuthModeNone}},
			wantErr: "refusing to serve",
		},
		{
			name:    "hostname bind with no auth is refused",
			s:       serverSettings{Bind: "daggle.example.com", Auth: api.Auth{Mode: api.AuthModeNone}},
			wantErr: "refusing to serve",
		},
		{
			name: "public bind with basic auth is allowed",
			s:    serverSettings{Bind: "0.0.0.0", Auth: api.Auth{Mode: api.AuthModeBasic, Username: "a", Password: "b"}},
		},
		{
			name:    "basic without a password is refused",
			s:       serverSettings{Bind: "0.0.0.0", Auth: api.Auth{Mode: api.AuthModeBasic, Username: "a"}},
			wantErr: "needs both a username and a password",
		},
		{
			name:    "basic without a username is refused",
			s:       serverSettings{Bind: "0.0.0.0", Auth: api.Auth{Mode: api.AuthModeBasic, Password: "b"}},
			wantErr: "needs both a username and a password",
		},
		{
			name:    "unknown mode is refused",
			s:       serverSettings{Bind: "127.0.0.1", Auth: api.Auth{Mode: "oidc"}},
			wantErr: "not one of none, basic, token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.s.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestIsLoopbackHost(t *testing.T) {
	loopback := []string{"127.0.0.1", "127.0.0.53", "::1", "localhost", "LOCALHOST"}
	for _, h := range loopback {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false, want true", h)
		}
	}

	// "" means every interface, and a hostname could resolve anywhere: when
	// we cannot prove it is loopback, it is not.
	exposed := []string{"", "0.0.0.0", "::", "10.0.0.1", "daggle.example.com"}
	for _, h := range exposed {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true, want false", h)
		}
	}
}

func TestEnsureTokenUsesConfiguredValue(t *testing.T) {
	t.Setenv("DAGGLE_DATA_DIR", t.TempDir())

	tok, generated, err := ensureToken("configured-token")
	if err != nil {
		t.Fatal(err)
	}
	if tok != "configured-token" {
		t.Errorf("token = %q, want the configured value", tok)
	}
	if generated {
		t.Error("a configured token must not be reported as generated")
	}
	if _, err := os.Stat(tokenPath()); !os.IsNotExist(err) {
		t.Error("a configured token must not be written to disk")
	}
}

func TestEnsureTokenGeneratesAndPersists(t *testing.T) {
	t.Setenv("DAGGLE_DATA_DIR", t.TempDir())

	tok, generated, err := ensureToken("")
	if err != nil {
		t.Fatal(err)
	}
	if !generated {
		t.Error("first start should report the token as generated")
	}
	if len(tok) != 64 {
		t.Errorf("token length = %d, want 64 hex chars", len(tok))
	}

	info, err := os.Stat(tokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 600", perm)
	}

	// A restart must not invalidate every client's credentials.
	again, generated, err := ensureToken("")
	if err != nil {
		t.Fatal(err)
	}
	if generated {
		t.Error("second start should reuse the persisted token")
	}
	if again != tok {
		t.Errorf("token changed across restarts: %q then %q", tok, again)
	}
}

func TestServerSettingsAddr(t *testing.T) {
	s := serverSettings{Bind: "0.0.0.0", Port: 8787}
	if got := s.Addr(); got != "0.0.0.0:8787" {
		t.Errorf("Addr() = %q, want %q", got, "0.0.0.0:8787")
	}

	// IPv6 literals need brackets; net.JoinHostPort handles that for us.
	s = serverSettings{Bind: "::1", Port: 8787}
	if got := s.Addr(); got != "[::1]:8787" {
		t.Errorf("Addr() = %q, want %q", got, "[::1]:8787")
	}
}
