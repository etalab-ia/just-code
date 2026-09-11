package justcode

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the settings resolved from the environment for a run. Field
// semantics match the justfile and .env.example contract.
type Config struct {
	ProjectDir string
	Username   string
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
}

// EnvLookup is an injectable subset of os.LookupEnv, for tests.
type EnvLookup func(string) (string, bool)

const (
	DefaultUsername  = "opencode"
	DefaultPassword  = "albert-dev-pass"
	DefaultPort      = 4096
	DefaultTartImage = "ghcr.io/cirruslabs/macos-tahoe-base:latest"
	DefaultTartMTU   = "1280"
)

// LoadConfigEnv applies .env (if present) and resolves configuration from the
// process environment. It mirrors just's `set dotenv-load` + env_var_or_default
// behavior.
func LoadConfigEnv() Config {
	_ = ApplyDotenv(".env")
	return LoadConfig(os.LookupEnv)
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
		Username:   envDefault(lookup, "OPENCODE_SERVER_USERNAME", DefaultUsername),
		TartImage:  envDefault(lookup, "TART_IMAGE", DefaultTartImage),
		TartMTU:    envDefault(lookup, "TART_MTU", DefaultTartMTU),
		ProjectDir: envDefault(lookup, "PROJECT_DIR", "./workspace"),
	}
	if v, ok := lookup("OPENCODE_SERVER_PASSWORD"); ok {
		cfg.Password = v
		cfg.PasswordSet = true
	} else {
		cfg.Password = DefaultPassword
	}
	cfg.APIKey, _ = lookup("ALBERT_API_KEY")
	if abs, err := filepath.Abs(cfg.ProjectDir); err == nil {
		cfg.ProjectDir = abs
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
