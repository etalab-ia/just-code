package justcode

import (
	"strings"
	"testing"
)

func TestResolveIsolation(t *testing.T) {
	cases := []struct {
		name       string
		flag, pref string
		want       Isolation
	}{
		{"default is backend", "", "", IsolationBackend},
		{"flag backend", "backend", "", IsolationBackend},
		{"flag full", "full", "", IsolationFull},
		{"flag beats preference", "full", "backend", IsolationFull},
		{"preference used when no flag", "", "full", IsolationFull},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ResolveIsolation(c.flag, c.pref)
			if err != nil {
				t.Fatalf("ResolveIsolation(%q, %q): %v", c.flag, c.pref, err)
			}
			if got != c.want {
				t.Errorf("ResolveIsolation(%q, %q) = %q, want %q", c.flag, c.pref, got, c.want)
			}
		})
	}
}

func TestResolveIsolationRejectsUnknown(t *testing.T) {
	for _, value := range []string{"ful", "Full", "back-end", "0"} {
		_, err := ResolveIsolation(value, "")
		if err == nil {
			t.Fatalf("ResolveIsolation(%q) accepted an unknown value", value)
		}
		if !strings.Contains(err.Error(), "backend") || !strings.Contains(err.Error(), "full") {
			t.Errorf("error %q must list the accepted surface", err)
		}
	}
	// An invalid preference must also be rejected, not silently defaulted.
	if _, err := ResolveIsolation("", "sometimes"); err == nil {
		t.Fatal("an invalid ISOLATION preference must be rejected")
	}
}

func TestLoadConfigIsolation(t *testing.T) {
	if got := LoadConfig(lookupFrom(nil)).Isolation; got != IsolationBackend {
		t.Errorf("default Isolation = %q, want backend", got)
	}
	cfg := LoadConfig(lookupFrom(map[string]string{"ISOLATION": "full"}))
	if cfg.Isolation != IsolationFull {
		t.Errorf("Isolation = %q, want full", cfg.Isolation)
	}
	if cfg.IsolationErr != nil {
		t.Fatalf("unexpected IsolationErr: %v", cfg.IsolationErr)
	}
}

// TestLoadConfigInvalidIsolationIsNotFatal records the deferred-validation
// contract: an invalid ISOLATION must not break commands that never read it.
func TestLoadConfigInvalidIsolationIsNotFatal(t *testing.T) {
	cfg := LoadConfig(lookupFrom(map[string]string{"ISOLATION": "sometimes"}))
	if cfg.IsolationErr == nil {
		t.Fatal("expected IsolationErr for an invalid value")
	}
	if cfg.Isolation != IsolationBackend {
		t.Errorf("Isolation = %q, want the backend default despite the error", cfg.Isolation)
	}
}
