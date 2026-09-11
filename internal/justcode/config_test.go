package justcode

import "testing"

func lookupFrom(m map[string]string) EnvLookup {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestLoadConfigEmptyPasswordPreserved(t *testing.T) {
	// An explicitly empty password means "no auth", not the default.
	cfg := LoadConfig(lookupFrom(map[string]string{
		"OPENCODE_SERVER_PASSWORD": "",
	}))
	if !cfg.PasswordSet {
		t.Fatal("PasswordSet = false, want true for an explicitly empty password")
	}
	if cfg.Password != "" {
		t.Fatalf("Password = %q, want empty (no auth)", cfg.Password)
	}
}

func TestLoadConfigUnsetPasswordUsesDefault(t *testing.T) {
	cfg := LoadConfig(lookupFrom(map[string]string{}))
	if cfg.PasswordSet {
		t.Fatal("PasswordSet = true, want false when unset")
	}
	if cfg.Password != DefaultPassword {
		t.Fatalf("Password = %q, want %q", cfg.Password, DefaultPassword)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg := LoadConfig(lookupFrom(map[string]string{}))
	if cfg.Username != DefaultUsername {
		t.Errorf("Username = %q, want %q", cfg.Username, DefaultUsername)
	}
	if cfg.TartImage != DefaultTartImage {
		t.Errorf("TartImage = %q, want %q", cfg.TartImage, DefaultTartImage)
	}
	if cfg.TartMTU != DefaultTartMTU {
		t.Errorf("TartMTU = %q, want %q", cfg.TartMTU, DefaultTartMTU)
	}
	if cfg.TartVM != "opencode-tahoe-base-latest" {
		t.Errorf("TartVM = %q, want opencode-tahoe-base-latest", cfg.TartVM)
	}
}

func TestLoadConfigCustom(t *testing.T) {
	cfg := LoadConfig(lookupFrom(map[string]string{
		"OPENCODE_SERVER_USERNAME": "albert",
		"OPENCODE_SERVER_PASSWORD": "s3cret",
		"TART_IMAGE":               "ghcr.io/cirruslabs/macos-sonoma-base:latest",
		"TART_MTU":                 "1400",
		"PROJECT_DIR":              "/tmp/proj",
		"ALBERT_API_KEY":           "key123",
	}))
	if cfg.Username != "albert" || cfg.Password != "s3cret" || !cfg.PasswordSet {
		t.Errorf("credentials wrong: %+v", cfg)
	}
	if cfg.TartVM != "opencode-sonoma-base-latest" {
		t.Errorf("TartVM = %q", cfg.TartVM)
	}
	if cfg.TartMTU != "1400" || cfg.ProjectDir != "/tmp/proj" || cfg.APIKey != "key123" {
		t.Errorf("custom values wrong: %+v", cfg)
	}
}
