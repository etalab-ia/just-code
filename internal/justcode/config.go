package justcode

import "os"

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
