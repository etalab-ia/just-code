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

// A value that quoting cannot carry is not automatically refused: an unquoted
// value is never decoded, so anything the loaders do not strip, cut or expand
// passes through byte-for-byte. Non-ASCII is the large case of this, and it is
// most of the world's file names.
func TestDotenvLiteralUsesTheRawFormForNonASCII(t *testing.T) {
	for name, value := range map[string]string{
		"accented windows path":  `C:\Répertoire\`,
		"accented trailing path": `naïve\`,
		"greek":                  `Ω\`,
		"cjk":                    `秘密\`,
		"emoji":                  `🚀\`,
		"arabic":                 `مجلد\`,
		"mixed":                  `C:\Données\rép-🚀\`,
		"accents and pair":       `C:\\Répertoire`,
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

// A carriage return is refused by both forms. Quoting would normalise it to a
// newline in Node and Python; raw would end the value. Refusing is the only
// outcome that does not alter the secret.
func TestDotenvLiteralRefusesACarriageReturn(t *testing.T) {
	for name, value := range map[string]string{
		"bare cr":         "a\rb",
		"crlf":            "line1\r\nline2",
		"cr at the end":   "a\r",
		"cr in a key pem": "-----BEGIN KEY-----\r\nAAAA\r\n-----END KEY-----",
	} {
		_, err := dotenvLiteral(value)
		if err == nil {
			t.Fatalf("%s must be refused: a quoted value is normalised and a raw one ends at the CR", name)
		}
		if !strings.Contains(err.Error(), "carriage return") {
			t.Fatalf("%s: the refusal must name the carriage return: %v", name, err)
		}
	}
}

// The edge of an unquoted value is where the loaders trim. The raw form must
// refuse a character the loaders strip at the edge, whatever it is, because it
// would deliver the value with that character silently gone.
func TestDotenvRawObstacleRefusesATrimmedEdge(t *testing.T) {
	for name, value := range map[string]string{
		"leading space":            " a",
		"trailing space":           "a ",
		"leading tab":              "\ta",
		"trailing tab":             "a\t",
		"trailing nbsp":            "a\u00a0",
		"leading nbsp":             "\u00a0a",
		"trailing ideographic":     "a\u3000",
		"leading ideographic":      "\u3000a",
		"trailing hair space":      "a\u200a",
		"leading ogham space":      "\u1680a",
		"leading file separator":   "\x1ca",
		"trailing group separator": "a\x1d",
		"trailing bom":             "a\ufeff",
		"leading bom":              "\ufeffa",
		"vertical tab":             "\x0ba",
	} {
		if err := dotenvRawObstacle(value); err == nil {
			t.Fatalf("%s (%q) must be refused raw: the loaders strip that edge character", name, value)
		}
	}
	// The same characters are exact in the interior, so raw must carry them.
	for name, value := range map[string]string{
		"interior tab":            "a\tb",
		"interior nbsp":           "a\u00a0b",
		"interior ideographic":    "a\u3000b",
		"interior file separator": "a\x1cb",
		"interior bom":            "a\ufeffb",
	} {
		if err := dotenvRawObstacle(value); err != nil {
			t.Fatalf("%s (%q) must be representable raw: %v", name, value, err)
		}
	}
}

// The refusal above is about the raw form, not about the value: quoting keeps
// leading and trailing whitespace exactly, so those values are still
// delivered, in their quoted form. Only a value that needs raw for another
// reason (a backslash pair, a trailing backslash) and also carries a trimmed
// edge has no form left.
func TestDotenvLiteralStillDeliversWhitespaceEdgesByQuoting(t *testing.T) {
	for name, value := range map[string]string{
		"leading nbsp":   "\u00a0a",
		"trailing nbsp":  "a\u00a0",
		"trailing space": "a ",
		"leading tab":    "\ta",
		"trailing bom":   "a\ufeff",
		"interior nbsp":  "a\u00a0b",
	} {
		literal, err := dotenvLiteral(value)
		if err != nil {
			t.Fatalf("%s (%q) must be representable by quoting: %v", name, value, err)
		}
		if literal != "'"+value+"'" {
			t.Fatalf("%s must be single-quoted and verbatim: got %q", name, literal)
		}
	}
	// A pair plus a trimmed edge has no form: quoting collapses the pair,
	// raw drops the edge character.
	if _, err := dotenvLiteral(`a\\b `); err == nil {
		t.Fatal("a backslash pair with a trailing space must be refused: neither form is exact")
	}
}

// Invalid UTF-8 is refused before either form. Measured: python-dotenv raises
// UnicodeDecodeError and loses the file, Go and Node substitute replacement
// characters (and a quoted invalid byte can make godotenv read the following
// line into this value), so no encoding delivers the original bytes.
func TestDotenvLiteralRefusesInvalidUTF8(t *testing.T) {
	for name, value := range map[string]string{
		"truncated sequence": "ab\xc3",
		"lone continuation":  "C:\\R\xe9pertoire\\",
		"overlong":           "ab\xc0\xaf",
		"bad byte":           "ab\xff",
	} {
		_, err := dotenvLiteral(value)
		if err == nil {
			t.Fatalf("%s must be refused: the loaders decode it as replacement characters", name)
		}
		if !strings.Contains(err.Error(), "UTF-8") {
			t.Fatalf("%s: the refusal must name the encoding: %v", name, err)
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
