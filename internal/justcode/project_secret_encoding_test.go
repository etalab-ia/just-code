package justcode

import (
	"strings"
	"testing"
)

// The encoding is the contract for "delivered exactly": a value that cannot be
// represented must be refused, never written in a form a loader would change.

func TestDotenvLiteralRoundTripsThroughTheQualifiedLoaders(t *testing.T) {
	// Verified against Node dotenv 16.4.7 (with and without dotenv-expand
	// 12.0.2), python-dotenv 1.2.2 and godotenv 1.5.1: single quotes preserve
	// all of these exactly.
	for _, value := range []string{
		"plain",
		"line1\nline2",
		"-----BEGIN KEY-----\nAAAA\n-----END KEY-----",
		"trailing   ",
		"   leading",
		"hash # inside",
		`has " quote`,
		// A backslash is safe on its own, including at the start: only a pair
		// and a trailing one are refused below.
		`back\slash`,
		`\leading`,
		"",
	} {
		literal, err := dotenvLiteral(value)
		if err != nil {
			t.Fatalf("%q must be representable: %v", value, err)
		}
		if !strings.HasPrefix(literal, "'") || !strings.HasSuffix(literal, "'") {
			t.Fatalf("%q must be single-quoted, got %q", value, literal)
		}
		if literal[1:len(literal)-1] != value {
			t.Fatalf("%q must survive verbatim inside the quotes, got %q", value, literal)
		}
	}
}

// The shapes quoting cannot carry are written unquoted, not refused: an
// unquoted value has no escape processing in any pinned loader.
func TestDotenvLiteralUsesTheRawFormForTheBackslashShapes(t *testing.T) {
	for name, value := range map[string]string{
		"backslash pair":     `a\\b`,
		"trailing backslash": `trailing\`,
		"windows path":       `C:\Users\`,
		"unc path":           `\\server\share`,
		"leading pair":       `\\leading`,
		"pair then a space":  `a\\b c`,
	} {
		literal, err := dotenvLiteral(value)
		if err != nil {
			t.Fatalf("%s (%q) must be representable: %v", name, value, err)
		}
		if literal != value {
			t.Fatalf("%s must be written unquoted and verbatim: got %q, want %q", name, literal, value)
		}
	}
}

// Quoting stays the default for everything it can carry, so nothing that
// worked before changes form.
func TestDotenvLiteralPrefersQuotesWhenTheyWork(t *testing.T) {
	for _, value := range []string{"plain", `a\b`, `\leading`, "has # hash", "trailing   ", ""} {
		literal, err := dotenvLiteral(value)
		if err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if !strings.HasPrefix(literal, "'") {
			t.Fatalf("%q is representable quoted, so it must stay quoted; got %q", value, literal)
		}
	}
}

func TestDotenvLiteralRefusesWhatNoLoaderAgreesOn(t *testing.T) {
	for name, value := range map[string]string{
		"single quote": "it's",
		"dollar":       "pre$VAR-post",
		"braced":       "a${VAR}b",
		// Neither form can carry these: quoting misparses the quote, and an
		// unquoted value is ended by the newline or trimmed of the space.
		"quote plus a pair": `it's a\\b`,
		"newline plus pair": "line1\nline2\\",
	} {
		if _, err := dotenvLiteral(value); err == nil {
			t.Fatalf("%s must be refused rather than written ambiguously", name)
		}
	}
}

func TestDotenvLiteralRefusalNamesBothForms(t *testing.T) {
	// Neither form can carry these: a quoted value expands the "$" (and the
	// trailing space is fine quoted), while an unquoted one trims the space.
	for name, value := range map[string]string{
		"dollar and a trailing space": "pre$VAR-post ",
		"newline and a pair":          "line1\nline2\\",
	} {
		_, err := dotenvLiteral(value)
		if err == nil {
			t.Fatalf("%s must be refused: neither form can carry it", name)
		}
		for _, want := range []string{"quoted:", "unquoted:"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal must explain both attempts (%q missing): %v", want, err)
			}
		}
	}
}

func TestGuestReadableDeliveryRefusesAnUnrepresentableValue(t *testing.T) {
	_, err := BuildGuestSecretFiles([]ProjectSecretDelivery{guestDelivery(".env", "DB_PASSWORD", "pa$$word")})
	if err == nil {
		t.Fatal("a value no encoding can carry must fail the delivery, not be written mangled")
	}
	if !strings.Contains(err.Error(), "DB_PASSWORD") || !strings.Contains(err.Error(), "$") {
		t.Fatalf("the error must name the variable and the offending character: %v", err)
	}
	// The case that is worse than mangling: a newline in a raw value makes
	// godotenv refuse the entire file, so it must never be written raw.
	files, err := BuildGuestSecretFiles([]ProjectSecretDelivery{guestDelivery(".env", "TLS_KEY", "-----BEGIN KEY-----\nAAAA\n-----END KEY-----")})
	if err != nil {
		t.Fatal(err)
	}
	line := ""
	for _, l := range strings.Split(files[0].Content, "\n") {
		if strings.HasPrefix(l, "TLS_KEY=") {
			line = l
		}
	}
	if line != "TLS_KEY='-----BEGIN KEY-----" {
		t.Fatalf("a multiline value must be single-quoted, not spread over lines: %q", files[0].Content)
	}
}
