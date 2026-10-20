package justcode

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/joho/godotenv"
)

const MaxProjectDotenvBytes = 1 << 20

type ProjectDotenv struct {
	Values     map[string]string
	Duplicates []string
}

// ParseProjectDotenv parses a bounded dotenv file as data. Dollar expansion
// in godotenv is disabled with a fresh sentinel so references remain literal;
// no environment is read and no command is evaluated. Duplicate assignments
// use godotenv's last-definition-wins rule and are reported for preview.
func ParseProjectDotenv(data []byte) (ProjectDotenv, error) {
	// The three checks below used to share one message ("too large or contains
	// invalid text"), which named neither the variable nor the rule and sent
	// the user looking for a size problem in a file whose actual fault was an
	// encoding one (#208). Each says what it found, and the two that have a
	// culpable line name its variable.
	if len(data) > MaxProjectDotenvBytes {
		return ProjectDotenv{}, fmt.Errorf("dotenv file is %d bytes, over the %d-byte import limit", len(data), MaxProjectDotenvBytes)
	}
	if !utf8.Valid(data) {
		const reason = "the value is not valid UTF-8; dotenv files must be UTF-8"
		if name := attributeFaultByte(data, firstInvalidUTF8Byte(data)); name != "" {
			return ProjectDotenv{}, fmt.Errorf("%s: %s", name, reason)
		}
		return ProjectDotenv{}, fmt.Errorf("the dotenv file contains text no loader can decode: %s", reason)
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		if name := attributeFaultByte(data, i); name != "" {
			return ProjectDotenv{}, fmt.Errorf("%s: the value contains a NUL byte, which no dotenv loader accepts", name)
		}
		return ProjectDotenv{}, fmt.Errorf("the dotenv file contains a NUL byte, which no dotenv loader accepts")
	}
	markerBytes := make([]byte, 16)
	if _, err := rand.Read(markerBytes); err != nil {
		return ProjectDotenv{}, fmt.Errorf("prepare literal dotenv parser")
	}
	marker := "JUSTCODE_LITERAL_DOLLAR_" + hex.EncodeToString(markerBytes) + "_"
	source := string(data)
	for strings.Contains(source, marker) {
		if _, err := rand.Read(markerBytes); err != nil {
			return ProjectDotenv{}, fmt.Errorf("prepare literal dotenv parser")
		}
		marker = "JUSTCODE_LITERAL_DOLLAR_" + hex.EncodeToString(markerBytes) + "_"
	}
	values, err := godotenv.Unmarshal(strings.ReplaceAll(source, "$", marker))
	if err != nil {
		// The parser's own error quotes the source around the fault ("near
		// %q"), and a malformed line may contain a secret value — so it is
		// never returned. Instead the one fault the importer can name without
		// the value is diagnosed here: a carriage return that is not part of
		// a CRLF pair ends the assignment where godotenv reads it, and the
		// rest of the line is then parsed as a new statement (#208).
		if name := carriageReturnFault(data); name != "" {
			// The remedy has to match the fault: converting the CR to LF
			// would leave the same trailing fragment on its own line, where
			// it is still not a valid statement. What repairs the file is
			// removing the CR from the value or making the remainder a real
			// assignment.
			return ProjectDotenv{}, fmt.Errorf("%s: the value contains a carriage return, which ends the assignment early, so the rest of the line is read as a separate statement that has no assignment; remove the carriage return from the value, or make the remainder a complete assignment", name)
		}
		// Anything else stays deliberately opaque: the reason cannot be given
		// without either quoting the source or guessing, and a wrong guess is
		// worse than an honest "unsupported". The message says what to do.
		return ProjectDotenv{}, fmt.Errorf("dotenv syntax is invalid or unsupported; the offending line is not quoted here because it may contain a secret value")
	}
	duplicates := map[string]bool{}
	seen := map[string]bool{}
	for key := range values {
		if !validSecretIdentifier(key) {
			// The name is safe to print — it is an identifier, not a value —
			// and printing it is the point: the qualification protocol requires
			// each refused class to name the variable, and a file with many
			// variables gives the user nothing to act on without it.
			return ProjectDotenv{}, fmt.Errorf("variable name %q is not supported; names may use letters, digits and underscore, may not start with a digit, and are limited to 128 characters", key)
		}
	}
	// Record repeated assignment lines without mistaking KEY=value text inside
	// a multiline quoted value for another assignment.
	var openQuote byte
	for _, line := range strings.Split(source, "\n") {
		if openQuote != 0 {
			if closesQuote(line, openQuote) {
				openQuote = 0
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "export ") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, rawValue, ok := strings.Cut(trimmed, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, exists := values[key]; !exists {
			continue
		}
		if seen[key] {
			duplicates[key] = true
		}
		seen[key] = true
		value := strings.TrimSpace(rawValue)
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"') && !closesQuote(value[1:], value[0]) {
			openQuote = value[0]
		}
	}
	for key, value := range values {
		values[key] = strings.ReplaceAll(value, marker, "$")
	}
	dupList := make([]string, 0, len(duplicates))
	for key := range duplicates {
		dupList = append(dupList, key)
	}
	sort.Strings(dupList)
	return ProjectDotenv{Values: values, Duplicates: dupList}, nil
}

func closesQuote(line string, quote byte) bool {
	for i := 0; i < len(line); i++ {
		if line[i] == quote && (i == 0 || line[i-1] != '\\') {
			return true
		}
	}
	return false
}

// attributeFaultByte returns the name of the variable whose value contains the
// byte at offset, by asking the parser rather than imitating it.
//
// The byte is replaced with an unambiguous ASCII token and the file is parsed
// again; the token surfaces in whichever value the parser assigned it to, and
// that value's key is the answer. A byte belonging to no value — inside a
// comment, or in a file that still fails to parse — yields no name, and the
// caller falls back to naming only the file.
//
// Delegating to the parser is deliberate. The first versions of this diagnostic
// walked the grammar by hand, and three review rounds each found a fresh
// divergence from it: quoted values spanning physical lines, comment blocks,
// escape parity, carriage returns inside quotes. A hand-written walker has to
// reproduce every rule the parser applies, and misses are silent; the parser
// applies them by construction and cannot drift.
func attributeFaultByte(data []byte, offset int) string {
	if offset < 0 || offset >= len(data) {
		return ""
	}
	token := faultToken(data)
	candidate := make([]byte, 0, len(data)+len(token))
	candidate = append(candidate, data[:offset]...)
	candidate = append(candidate, token...)
	candidate = append(candidate, data[offset+1:]...)
	values, err := godotenv.UnmarshalBytes(candidate)
	if err != nil {
		// Neutralising this byte did not make the file parse, so the byte is
		// not what decides the outcome. Naming a variable here would accuse a
		// line that may be innocent.
		return ""
	}
	name := ""
	for key, value := range values {
		if !strings.Contains(value, token) {
			continue
		}
		if name != "" {
			return "" // reached more than one value: ambiguous, so name none
		}
		name = key
	}
	if !validSecretIdentifier(name) {
		return ""
	}
	return name
}

// faultToken returns an ASCII token absent from data, so a substitution can be
// recognised without ambiguity. It grows until it does not occur.
func faultToken(data []byte) string {
	token := "JUSTCODE_FAULT_"
	for bytes.Contains(data, []byte(token)) {
		token += "X"
	}
	return token
}

// firstInvalidUTF8Byte returns the offset of the first byte that is not valid
// UTF-8, or -1 when the whole slice is valid.
func firstInvalidUTF8Byte(data []byte) int {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return -1
}

// carriageReturnFault names the variable a fatal carriage return belongs to,
// or "" when none can be tied to the failure.
//
// A bare carriage return is a line ending to godotenv, so it is not itself a
// fault; it explains a failure only when it split a value, letting the text
// after it be read as a new statement. Substituting it must therefore make the
// file parse for the attribution to stand — otherwise the real fault lies
// elsewhere and blaming the carriage return would send the user to the wrong
// line. A CRLF pair is an ordinary line ending and is never considered.
func carriageReturnFault(data []byte) string {
	for i := 0; i < len(data); i++ {
		if data[i] != '\r' {
			continue
		}
		if i+1 < len(data) && data[i+1] == '\n' {
			i++ // CRLF: an ordinary line ending.
			continue
		}
		if name := attributeFaultByte(data, i); name != "" {
			return name
		}
	}
	return ""
}
