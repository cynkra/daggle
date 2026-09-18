package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cynkra/daggle/api"
	"github.com/cynkra/daggle/state"
)

// serveFlags holds the command-line half of the server settings. Only fields
// the operator actually passed are applied, so a flag never silently
// overrides config.yaml with its own default.
type serveFlags struct {
	port          int
	portSet       bool
	bind          string
	bindSet       bool
	basePath      string
	basePathSet   bool
	trustProxy    bool
	trustProxySet bool
	authMode      string
	authModeSet   bool
}

// serverSettings is the resolved server posture: what `daggle serve` will
// actually do after config file, environment and flags have been merged.
type serverSettings struct {
	Bind       string
	Port       int
	BasePath   string
	TrustProxy bool
	Auth       api.Auth
}

// defaultBind keeps the historical posture: unless told otherwise, the API is
// reachable only from inside the machine (or container) it runs in.
const defaultBind = "127.0.0.1"

// Addr returns the listen address for net/http.
func (s serverSettings) Addr() string {
	return net.JoinHostPort(s.Bind, strconv.Itoa(s.Port))
}

// resolveServerSettings merges the three configuration sources in increasing
// order of precedence: config.yaml, environment, flags.
//
// The environment sits in the middle because a container image bakes the file
// and the operator overrides individual values at deploy time; a flag is an
// explicit, one-off instruction and wins outright.
func resolveServerSettings(cfg state.ServerConfig, flags serveFlags) (serverSettings, error) {
	s := serverSettings{
		Bind:       cfg.Bind,
		Port:       cfg.Port,
		BasePath:   cfg.BasePath,
		TrustProxy: cfg.TrustProxy,
		Auth: api.Auth{
			Mode:     cfg.Auth.Mode,
			Username: cfg.Auth.Username,
			Password: cfg.Auth.Password,
			Token:    cfg.Auth.Token,
		},
	}

	// Secrets supplied as files: the shape credentials arrive in when a
	// container decrypts them at entrypoint time. An inline value wins.
	if s.Auth.Password == "" && cfg.Auth.PasswordFile != "" {
		v, err := readSecretFile(cfg.Auth.PasswordFile)
		if err != nil {
			return s, fmt.Errorf("auth.password_file: %w", err)
		}
		s.Auth.Password = v
	}
	if s.Auth.Token == "" && cfg.Auth.TokenFile != "" {
		v, err := readSecretFile(cfg.Auth.TokenFile)
		if err != nil {
			return s, fmt.Errorf("auth.token_file: %w", err)
		}
		s.Auth.Token = v
	}

	if v, ok := os.LookupEnv("DAGGLE_BIND_ADDR"); ok {
		s.Bind = v
	}
	if v, ok := os.LookupEnv("DAGGLE_PORT"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return s, fmt.Errorf("DAGGLE_PORT: %q is not a number", v)
		}
		s.Port = n
	}
	if v, ok := os.LookupEnv("DAGGLE_BASE_PATH"); ok {
		s.BasePath = v
	}
	if v, ok := os.LookupEnv("DAGGLE_TRUST_PROXY"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return s, fmt.Errorf("DAGGLE_TRUST_PROXY: %q is not a boolean", v)
		}
		s.TrustProxy = b
	}
	if v, ok := os.LookupEnv("DAGGLE_AUTH_MODE"); ok {
		s.Auth.Mode = v
	}
	if v, ok := os.LookupEnv("DAGGLE_AUTH_USERNAME"); ok {
		s.Auth.Username = v
	}
	if v, ok := os.LookupEnv("DAGGLE_AUTH_PASSWORD"); ok {
		s.Auth.Password = v
	}
	if v, ok := os.LookupEnv("DAGGLE_AUTH_PASSWORD_FILE"); ok {
		p, err := readSecretFile(v)
		if err != nil {
			return s, fmt.Errorf("DAGGLE_AUTH_PASSWORD_FILE: %w", err)
		}
		s.Auth.Password = p
	}
	if v, ok := os.LookupEnv("DAGGLE_AUTH_TOKEN"); ok {
		s.Auth.Token = v
	}
	if v, ok := os.LookupEnv("DAGGLE_AUTH_TOKEN_FILE"); ok {
		tok, err := readSecretFile(v)
		if err != nil {
			return s, fmt.Errorf("DAGGLE_AUTH_TOKEN_FILE: %w", err)
		}
		s.Auth.Token = tok
	}

	if flags.portSet {
		s.Port = flags.port
	}
	if flags.bindSet {
		s.Bind = flags.bind
	}
	if flags.basePathSet {
		s.BasePath = flags.basePath
	}
	if flags.trustProxySet {
		s.TrustProxy = flags.trustProxy
	}
	if flags.authModeSet {
		s.Auth.Mode = flags.authMode
	}

	if s.Bind == "" {
		s.Bind = defaultBind
	}
	if s.Auth.Mode == "" {
		s.Auth.Mode = api.AuthModeNone
	}
	s.BasePath = strings.TrimRight(strings.TrimSpace(s.BasePath), "/")
	if s.BasePath != "" && !strings.HasPrefix(s.BasePath, "/") {
		s.BasePath = "/" + s.BasePath
	}

	return s, nil
}

// readSecretFile reads a credential from disk, trimming the trailing newline
// a generator or an editor leaves behind. A missing or unreadable file is an
// error: a deployment that points at a secret which is not there must fail
// loudly rather than start up unauthenticated.
func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return v, nil
}

// validate rejects combinations that would come up as a security problem
// rather than an error message. These run before the listener opens, so an
// unsafe daggle never accepts a single request.
func (s serverSettings) validate() error {
	switch s.Auth.Mode {
	case api.AuthModeNone, api.AuthModeBasic, api.AuthModeToken:
	default:
		return fmt.Errorf("auth mode %q is not one of none, basic, token", s.Auth.Mode)
	}

	if s.Auth.Mode == api.AuthModeNone && !isLoopbackHost(s.Bind) {
		return fmt.Errorf(
			"refusing to serve on %s with auth mode none: anyone who can reach that address could run arbitrary R and shell steps.\n"+
				"Set an auth mode (--auth-mode basic|token, DAGGLE_AUTH_MODE, or server.auth.mode in config.yaml), or bind to %s",
			s.Bind, defaultBind)
	}

	if s.Auth.Mode == api.AuthModeBasic {
		if s.Auth.Username == "" || s.Auth.Password == "" {
			return fmt.Errorf("auth mode basic needs both a username and a password " +
				"(server.auth.username / server.auth.password or password_file, or DAGGLE_AUTH_USERNAME / DAGGLE_AUTH_PASSWORD)")
		}
	}

	return nil
}

// isLoopbackHost reports whether a bind address only accepts connections from
// the same network namespace.
//
// A hostname that is not an IP literal counts as non-loopback: resolution can
// change under us, and the safe answer to "is this exposed?" when we cannot
// tell is yes. "" means the Go default of every interface.
func isLoopbackHost(host string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// tokenPath is where a generated shared token is persisted, under the data
// directory so it lands on the volume a deployment already declares.
func tokenPath() string {
	return filepath.Join(state.DataDir(), "auth", "token")
}

// ensureToken resolves the shared bearer token for token mode: an explicitly
// configured value wins, otherwise a previously generated one is reused, and
// only on a truly first start is a new token minted.
//
// Returns the token and whether it was newly generated, so the caller can
// print it once — an operator who cannot read the token cannot use the API.
func ensureToken(configured string) (string, bool, error) {
	if configured != "" {
		return configured, false, nil
	}

	path := tokenPath()
	if b, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, false, nil
		}
	}

	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false, fmt.Errorf("generate token: %w", err)
	}
	tok := hex.EncodeToString(buf)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", false, fmt.Errorf("create token directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("write token: %w", err)
	}
	return tok, true, nil
}
