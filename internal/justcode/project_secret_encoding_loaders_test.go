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

func TestATrailingBackslashIsWrittenRawBecauseQuotingBreaksTheFile(t *testing.T) {
	// Even single-quoted, a trailing backslash escapes the closing quote: the
	// Go loader refuses the whole file. That is why the encoder writes this
	// shape unquoted, which godotenv reads exactly.
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("K_TARGET='trailing\\'\nK_AFTER='after'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := godotenv.Read(path); err == nil {
		t.Fatal("godotenv must refuse a file whose quoted value ends with a backslash, which is why the encoder does not emit that form")
	}
	literal, err := dotenvLiteral(`trailing\`)
	if err != nil {
		t.Fatalf("a trailing backslash must be delivered raw, not refused: %v", err)
	}
	if literal != `trailing\` {
		t.Fatalf("a trailing backslash must be written unquoted and verbatim: %q", literal)
	}
	if err := os.WriteFile(path, []byte("K_TARGET="+literal+"\nK_AFTER=after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := godotenv.Read(path)
	if err != nil {
		t.Fatalf("the raw form must load: %v", err)
	}
	if got["K_TARGET"] != `trailing\` || got["K_AFTER"] != "after" {
		t.Fatalf("raw trailing backslash: got %q, next line %q", got["K_TARGET"], got["K_AFTER"])
	}
}

// The values newly accepted by the widened raw predicate are checked against
// the strictest loader, not just against the predicate. A backslash pair or a
// trailing backslash forces the raw form; anything else stays quoted, so the
// table states which form each value must take and every value is read back.
func TestNonASCIIRawValuesRoundTripThroughGodotenv(t *testing.T) {
	values := map[string]struct {
		value string
		raw   bool
	}{
		"ACCENTED_PATH":   {`C:\Répertoire\`, true},
		"TRAILING_ACCENT": {`naïve\`, true},
		"GREEK":           {`Ω\`, true},
		"CJK":             {`秘密\`, true},
		"EMOJI":           {`🚀\`, true},
		"ARABIC":          {`مجلد\`, true},
		"MIXED":           {`C:\Données\rép-🚀\`, true},
		"INTERIOR_TAB":    {"a\tb", false},
		"INTERIOR_NBSP":   {"a\u00a0b", false},
	}
	var b strings.Builder
	b.WriteString("K_BEFORE='before'\n")
	for name, entry := range values {
		literal, err := dotenvLiteral(entry.value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		want := "'" + entry.value + "'"
		if entry.raw {
			want = entry.value
		}
		if literal != want {
			t.Fatalf("%s: got %q, want %q", name, literal, want)
		}
		b.WriteString(name + "=" + literal + "\n")
	}
	b.WriteString("K_AFTER='after'\n")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := godotenv.Read(path)
	if err != nil {
		t.Fatalf("the generated file must load: %v", err)
	}
	if got["K_BEFORE"] != "before" || got["K_AFTER"] != "after" {
		t.Fatalf("the quoted neighbours must survive too: %q %q", got["K_BEFORE"], got["K_AFTER"])
	}
	for name, entry := range values {
		if got[name] != entry.value {
			t.Fatalf("%s: godotenv read %q, want %q", name, got[name], entry.value)
		}
	}
}

func TestEncodedRawValuesRoundTripThroughGodotenv(t *testing.T) {
	// The raw fallback, checked against the strictest loader, in the shape a
	// generated file actually has: the raw value between quoted values.
	values := map[string]string{
		"PAIR":         `a\\b`,
		"WINDOWS_PATH": `C:\Users\`,
		"UNC":          `\\server\share`,
		"TRAILING":     `trailing\`,
		"EQUALS":       `a\=b`,
	}
	var b strings.Builder
	b.WriteString("K_BEFORE='before'\n")
	for name, value := range values {
		literal, err := dotenvLiteral(value)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		b.WriteString(name + "=" + literal + "\n")
	}
	b.WriteString("K_AFTER='after'\n")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := godotenv.Read(path)
	if err != nil {
		t.Fatalf("the generated file must load: %v", err)
	}
	if got["K_BEFORE"] != "before" || got["K_AFTER"] != "after" {
		t.Fatalf("the quoted neighbours must survive too: %q %q", got["K_BEFORE"], got["K_AFTER"])
	}
	for name, want := range values {
		if got[name] != want {
			t.Fatalf("%s: godotenv read %q, want %q", name, got[name], want)
		}
	}
}
