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
		return ProjectDotenv{}, invalidTextError(data, "the value is not valid UTF-8; dotenv files must be UTF-8")
	}
	if i := bytes.IndexByte(data, 0); i >= 0 {
		if name := assignmentName(data, i); name != "" {
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
		if name := bareCarriageReturnName(data); name != "" {
			return ProjectDotenv{}, fmt.Errorf("%s: a carriage return inside a line is not supported; it ends the assignment early, so the rest of the line is read as another statement; convert the file to LF line endings", name)
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
			return ProjectDotenv{}, fmt.Errorf("dotenv contains an unsupported variable name")
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

// assignmentName returns the variable name assigned on the line containing
// byte offset i, or "" when that line holds no assignment. It reads only the
// text before the first "=", so a caller can name the culprit without
// returning any part of the value.
func assignmentName(data []byte, i int) string {
	if i < 0 || i > len(data) {
		return ""
	}
	start := bytes.LastIndexByte(data[:i], '\n') + 1
	end := len(data)
	if j := bytes.IndexByte(data[i:], '\n'); j >= 0 {
		end = i + j
	}
	line := strings.TrimSpace(string(data[start:end]))
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}
	if line == "" || strings.HasPrefix(line, "#") {
		return ""
	}
	name, _, ok := strings.Cut(line, "=")
	if !ok {
		return ""
	}
	name = strings.TrimSpace(name)
	if name == "" || !validSecretIdentifier(name) {
		// A name the importer would reject anyway: naming it here would
		// suggest the encoding was the problem.
		return ""
	}
	return name
}

// invalidTextError names the variable whose line carries an undecodable
// sequence, falling back to the file when no assignment can be identified.
// The offset of the first invalid byte is what makes the message actionable
// without echoing the value that contains it.
func invalidTextError(data []byte, reason string) error {
	// A byte-wise scan is enough to locate the offending line: the caller has
	// already established the data is not valid UTF-8, so invalid runes exist
	// and the loop terminates before the end.
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			if name := assignmentName(data, i); name != "" {
				return fmt.Errorf("%s: %s", name, reason)
			}
			return fmt.Errorf("the dotenv file contains text no loader can decode: %s", reason)
		}
		i += size
	}
	return fmt.Errorf("the dotenv file contains text no loader can decode: %s", reason)
}

// bareCarriageReturnName names the variable assigned on a line containing a
// carriage return that is not part of a CRLF pair. It is only called after the
// parser has already failed, so it reports a cause rather than predicting one:
// a bare carriage return is legal in some positions (at a line ending, or
// between two assignments) and only fatal when it splits a value, so a
// caller must not use it to refuse a file pre-emptively. It returns "" when
// every carriage return is a plain line ending.
func bareCarriageReturnName(data []byte) string {
	for i := 0; i < len(data); i++ {
		if data[i] != '\r' {
			continue
		}
		if i+1 < len(data) && data[i+1] == '\n' {
			i++ // CRLF: an ordinary line ending.
			continue
		}
		if name := assignmentName(data, i); name != "" {
			return name
		}
		return "the dotenv file" // a bare CR we cannot attribute to a variable
	}
	return ""
}
