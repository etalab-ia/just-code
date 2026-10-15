package justcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/joho/godotenv"
)

// The encoding contract, checked against a real loader rather than against the
// encoder's own idea of what it wrote. Node and Python are covered by
// tests/integration/project-secrets/ (same encoded forms); this closes the
// loop for the Go loader, which is the strictest of the three: it refuses the
// whole file on a raw newline.

func TestEncodedValuesRoundTripThroughGodotenv(t *testing.T) {
	values := map[string]string{
		"PLAIN":     "plainvalue",
		"MULTILINE": "-----BEGIN KEY-----\nAAAA\n-----END KEY-----",
		"TRAILING":  "trailing   ",
		"LEADING":   "   leading",
		"HASH":      "hash # inside",
		"DQUOTE":    `has " quote`,
		"BACKSLASH": `back\slash`,
		"EMPTY":     "",
	}
	var b strings.Builder
	for name, value := range values {
		literal, err := dotenvLiteral(value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b.WriteString(name + "=" + literal + "\n")
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := godotenv.Read(path)
	if err != nil {
		t.Fatalf("the generated file must load: %v", err)
	}
	for name, want := range values {
		if got[name] != want {
			t.Fatalf("%s: godotenv read %q, want %q", name, got[name], want)
		}
	}
}

func TestARawValueWouldBreakTheWholeFile(t *testing.T) {
	// This is the defect the encoding exists to prevent: the failure is not
	// confined to the variable, godotenv refuses the entire file, so every
	// other secret in it is lost too.
	path := filepath.Join(t.TempDir(), ".env")
	raw := "TLS_KEY=-----BEGIN KEY-----\nAAAA\n-----END KEY-----\nAPI_KEY=still-here\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := godotenv.Read(path); err == nil {
		t.Fatal("a raw multiline value must break the file, which is why it is encoded")
	}
}

func TestATrailingBackslashIsRefusedBecauseGodotenvRejectsTheFile(t *testing.T) {
	// Even single-quoted, a trailing backslash escapes the closing quote: the
	// Go loader refuses the whole file, so the refusal in the encoder is
	// driven by this loader and not by caution.
	if _, err := dotenvLiteral(`trailing\`); err == nil {
		t.Fatal("a trailing backslash must be refused")
	}
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("K_TARGET='trailing\\'\nK_AFTER='after'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := godotenv.Read(path); err == nil {
		t.Fatal("godotenv must refuse a file whose quoted value ends with a backslash; if it no longer does, the refusal can be revisited")
	}
}
