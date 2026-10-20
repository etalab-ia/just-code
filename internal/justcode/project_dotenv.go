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

// faultTokenPrefix and faultMaskPrefix label the markers the fault-attribution
// parse injects. They are named constants because the hostile-input test builds
// its input from the prefix: a test that hardcoded the string would silently
// stop testing anything the day the prefix changed, and would still pass.
const (
	faultTokenPrefix = "JUSTCODE_FLT_"
	faultMaskPrefix  = "JUSTCODE_MSK_"
)

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
	token := faultMarker(data, faultTokenPrefix)
	if token == "" {
		return ""
	}
	candidate := make([]byte, 0, len(data)+len(token))
	candidate = append(candidate, data[:offset]...)
	candidate = append(candidate, token...)
	candidate = append(candidate, data[offset+1:]...)
	// Parse with the same literal-dollar masking the main parse applies.
	// Without it the injected token is expanded away whenever it lands just
	// after a '$', because godotenv then reads it as a variable reference —
	// `K=secret$<NUL>` would lose the token and lose the attribution with it.
	mask := faultMarker(candidate, faultMaskPrefix)
	if mask == "" {
		return ""
	}
	name, parsed := keyHoldingMarker(candidate, mask, token)
	if !parsed {
		// Neutralising this byte did not make the file parse, so the byte is
		// not what decides the outcome. Naming a variable here would accuse a
		// line that may be innocent. This guard is what keeps a legal bare-CR
		// separator from being blamed for an unrelated failure elsewhere.
		return ""
	}
	if name != "" {
		return name
	}
	// The file parses, but the marker is in no value: a later duplicate
	// assignment overwrote it, because godotenv resolves duplicates
	// last-definition-wins. Cutting the input just after the marker removes
	// the overwriting assignment, so the same parser can report the key the
	// marker belongs to. This runs only on an input that already parsed, so it
	// cannot reintroduce the accusation the guard above prevents.
	if end := offset + len(token); end <= len(candidate) {
		truncated := make([]byte, 0, end+1)
		truncated = append(truncated, candidate[:end]...)
		truncated = append(truncated, '\n')
		if name, ok := keyHoldingMarker(truncated, mask, token); ok {
			return name
		}
	}
	return ""
}

// keyHoldingMarker parses the masked input and returns the single key whose
// value contains marker, plus whether the input parsed at all. A name is ""
// when no value holds the marker, or when more than one does (ambiguous, so
// naming one would be a guess).
func keyHoldingMarker(input []byte, mask, marker string) (string, bool) {
	values, err := godotenv.UnmarshalBytes([]byte(strings.ReplaceAll(string(input), "$", mask)))
	if err != nil {
		return "", false
	}
	name := ""
	for key, value := range values {
		if !strings.Contains(value, marker) {
			continue
		}
		if name != "" {
			return "", true // reached more than one value: ambiguous, so name none
		}
		name = key
	}
	if !validSecretIdentifier(name) {
		return "", true
	}
	return name, true
}

// faultMarker returns a fixed-length random ASCII token, with the given
// prefix, that does not occur in data. It returns "" if no such token could be
// produced in a few attempts.
//
// The length is fixed and the search bounded on purpose. Growing the token
// until it is absent costs quadratic time on hostile input: a permitted file
// containing the prefix followed by a long run of the extension byte makes
// every progressively longer candidate occur, so the loop copies an
// increasingly large string before parsing even begins. A random token of
// constant length is absent with overwhelming probability, and re-rolling a few
// times bounds the work.
//
// The two prefixes differ so that one marker can never be found as a substring
// of the other, which would make a masked dollar look like an injected token.
func faultMarker(data []byte, prefix string) string {
	for attempt := 0; attempt < 8; attempt++ {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return ""
		}
		token := prefix + hex.EncodeToString(buf)
		if !bytes.Contains(data, []byte(token)) {
			return token
		}
	}
	return ""
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
//
// Candidates are pre-filtered because the confirming substitution re-parses the
// whole input. Only a carriage return whose following statement is NOT a
// well-formed assignment can have split a value; a carriage return that merely
// separates two assignments is legal and is skipped without a parse. That keeps
// the scan linear on a file that is mostly legal separators — which a plain
// "try the first N" bound failed to do, because a legitimate file can carry
// many legal separators before the faulty one, and the bound then discarded the
// only candidate that mattered.
func carriageReturnFault(data []byte) string {
	for i := 0; i < len(data); i++ {
		if data[i] != '\r' {
			continue
		}
		if i+1 < len(data) && data[i+1] == '\n' {
			i++ // CRLF: an ordinary line ending.
			continue
		}
		if isAssignmentStatement(data[i+1:]) {
			continue // a legal separator between two assignments
		}
		if name := attributeFaultByte(data, i); name != "" {
			return name
		}
	}
	return ""
}

// isAssignmentStatement reports whether the text is a well-formed assignment,
// accepting both '=' and the yaml-style ':' that godotenv parses. It reads only
// the statement's own line, terminated by a newline or by a bare carriage
// return, because godotenv treats either as a line ending.
//
// This is a filter, not a validator: a false positive costs one extra parse,
// while hiding a real candidate would suppress the diagnosis. Where the two
// error, prefer calling something an assignment only when it plainly is.
func isAssignmentStatement(rest []byte) bool {
	end := len(rest)
	if j := bytes.IndexAny(rest, "\r\n"); j >= 0 {
		end = j
	}
	line := strings.TrimSpace(string(rest[:end]))
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	if line == "" || strings.HasPrefix(line, "#") {
		return false
	}
	delim := strings.IndexAny(line, "=:")
	if delim <= 0 {
		return false
	}
	return validSecretIdentifier(strings.TrimSpace(line[:delim]))
}
