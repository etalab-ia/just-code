package justcode

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds the settings resolved from the environment for a run. Field
// semantics match the .env.example contract.
type Config struct {
	// WorkspaceDir is the host directory mounted at the guest's /workspace.
	// It replaces the legacy PROJECT_DIR, which is still honoured.
	WorkspaceDir string
	Username     string
	// Password is the HTTP basic-auth password. It is empty when
	// OPENCODE_SERVER_PASSWORD was explicitly set to an empty value, which
	// means "no auth". PasswordSet records whether the variable was present,
	// distinguishing "unset" (use default) from "set empty" (no auth).
	Password    string
	PasswordSet bool
	TartImage   string
	TartVM      string
	TartMTU     string
	APIKey      string
	// StartTimeout bounds the backend health wait for the attach flow.
	// StartTimeoutErr records an invalid JUST_CODE_START_TIMEOUT so that
	// commands which never start a runtime (stop, clean, logs, doctor, check)
	// are not blocked by a typo in a variable they do not read.
	StartTimeout    time.Duration
	StartTimeoutErr error
}

// EnvLookup is an injectable subset of os.LookupEnv, for tests.
type EnvLookup func(string) (string, bool)

const (
	DefaultUsername  = "opencode"
	DefaultPassword  = "albert-dev-pass"
	DefaultPort      = 4096
	DefaultTartImage = "ghcr.io/cirruslabs/macos-tahoe-base:latest"
	DefaultTartMTU   = "1280"
	// DefaultStartTimeout is deliberately generous: a first Microsandbox boot
	// installs ~384 MiB of packages inside the microVM before OpenCode starts.
	DefaultStartTimeout = 300 * time.Second
)

// LoadConfigEnv applies .env and resolves configuration from the process
// environment. It mirrors just's `set dotenv-load` + env_var_or_default
// behavior, but for a standalone binary: it looks for .env in the working
// directory first, then next to the executable, so a binary placed anywhere
// still finds the configuration shipped beside it. Already-exported variables
// win over both.
func LoadConfigEnv() Config {
	for _, dir := range dotenvDirs() {
		_ = ApplyDotenv(filepath.Join(dir, ".env"))
	}
	if _, ok := os.LookupEnv("WORKSPACE_DIR"); !ok {
		if _, legacy := os.LookupEnv("PROJECT_DIR"); legacy {
			fmt.Fprintln(os.Stderr, "Warning: PROJECT_DIR is deprecated; use WORKSPACE_DIR instead.")
		}
	}
	return LoadConfig(os.LookupEnv)
}

// parseStartTimeout parses JUST_CODE_START_TIMEOUT, a whole number of seconds.
func parseStartTimeout(value string) (time.Duration, error) {
	if value == "" {
		return 0, fmt.Errorf("JUST_CODE_START_TIMEOUT must be a whole number of seconds")
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("JUST_CODE_START_TIMEOUT must be a whole number of seconds")
		}
	}
	seconds, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("JUST_CODE_START_TIMEOUT must be a whole number of seconds")
	}
	if seconds < 1 {
		return 0, fmt.Errorf("JUST_CODE_START_TIMEOUT must be at least 1 second")
	}
	return time.Duration(seconds) * time.Second, nil
}

// dotenvDirs lists the directories searched for a .env file, most specific
// first.
func dotenvDirs() []string {
	dirs := []string{"."}
	if exe, err := os.Executable(); err == nil {
		if dir := filepath.Dir(exe); dir != "." {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// LoadConfig resolves configuration from the environment. lookup defaults to
// os.LookupEnv. Using LookupEnv (rather than Getenv + default) is the fix for
// the empty-password bug: an explicitly empty OPENCODE_SERVER_PASSWORD must
// mean "no auth", not silently fall back to the default.
func LoadConfig(lookup EnvLookup) Config {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	cfg := Config{
		Username:     envDefault(lookup, "OPENCODE_SERVER_USERNAME", DefaultUsername),
		TartImage:    envDefault(lookup, "TART_IMAGE", DefaultTartImage),
		TartMTU:      envDefault(lookup, "TART_MTU", DefaultTartMTU),
		WorkspaceDir: envDefault(lookup, "WORKSPACE_DIR", envDefault(lookup, "PROJECT_DIR", "./workspace")),
		StartTimeout: DefaultStartTimeout,
	}
	if v, ok := lookup("JUST_CODE_START_TIMEOUT"); ok && v != "" {
		if d, err := parseStartTimeout(v); err != nil {
			cfg.StartTimeoutErr = err
		} else {
			cfg.StartTimeout = d
		}
	}
	if v, ok := lookup("OPENCODE_SERVER_PASSWORD"); ok {
		cfg.Password = v
		cfg.PasswordSet = true
	} else {
		cfg.Password = DefaultPassword
	}
	cfg.APIKey, _ = lookup("ALBERT_API_KEY")
	if abs, err := filepath.Abs(cfg.WorkspaceDir); err == nil {
		cfg.WorkspaceDir = abs
	}
	cfg.TartVM = VMName(cfg.TartImage)
	return cfg
}

// envDefault returns the value of key when it is set and non-empty, otherwise
// the default. It is used only for keys whose empty string has no special
// meaning.
func envDefault(lookup EnvLookup, key, def string) string {
	if v, ok := lookup(key); ok && v != "" {
		return v
	}
	return def
}

// ApplyDotenv loads a .env file (when present) and exports its keys into the
// process environment without overriding already-set variables. This mirrors
// just's `set dotenv-load`.
func ApplyDotenv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for k, v := range parseDotenv(string(data)) {
		if _, ok := os.LookupEnv(k); !ok {
			os.Setenv(k, v)
		}
	}
	return nil
}

// parseDotenv parses simple KEY=VALUE lines, ignoring blanks, comments, and
// optional `export ` prefixes. It does not expand variables.
func parseDotenv(content string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		m[key] = val
	}
	return m
}
