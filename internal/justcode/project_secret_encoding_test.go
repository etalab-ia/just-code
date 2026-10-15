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

func TestDotenvLiteralRefusesWhatNoLoaderAgreesOn(t *testing.T) {
	for name, value := range map[string]string{
		"single quote":       "it's",
		"dollar":             "pre$VAR-post",
		"braced":             "a${VAR}b",
		"backslash pair":     `a\\b`,
		"trailing backslash": `trailing\`,
	} {
		if _, err := dotenvLiteral(value); err == nil {
			t.Fatalf("%s must be refused rather than written ambiguously", name)
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
