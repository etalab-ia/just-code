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
		if f := locateFault(data, i); f.Name != "" {
			return ProjectDotenv{}, fmt.Errorf("%s: the value contains a NUL byte, which no dotenv loader accepts", f.Name)
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

// dotenvFault locates a byte offset in a dotenv file and attributes it to the
// variable whose assignment it belongs to, using the same statement grammar
// godotenv parses with.
type dotenvFault struct {
	// Name is the variable the fault belongs to, or "" when none could be
	// established safely. A caller must not print a name it did not get here.
	Name string
}

// locateFault walks the file the way the parser does — one statement at a
// time, with quoted values allowed to span physical lines — and returns the
// assignment name in effect at offset target.
//
// Walking the grammar rather than splitting on newlines is what keeps a
// continuation line of a quoted value from being mistaken for an assignment:
// in `K="first\nSECRET=...`, a line-based reader sees `SECRET=...` as a key and
// prints it, which is the non-disclosure guarantee broken. Only text before the
// value's own delimiter is ever returned, so no value reaches the caller.
func locateFault(data []byte, target int) dotenvFault {
	name := ""
	i := 0
	for i < len(data) {
		// Skip blank space and comments, as getStatementStart does.
		for i < len(data) && (isDotenvSpace(rune(data[i])) || data[i] == '\n') {
			i++
		}
		if i >= len(data) {
			break
		}
		if data[i] == '#' {
			for i < len(data) && data[i] != '\n' {
				i++
			}
			continue
		}
		start := i
		// Key: everything before the first '=' or ':', which godotenv accepts
		// as an assignment delimiter too (yaml style).
		nameStart := start
		for i < len(data) && data[i] != '=' && data[i] != ':' && data[i] != '\n' && data[i] != '\r' {
			i++
		}
		if i >= len(data) || (data[i] != '=' && data[i] != ':') {
			// Not an assignment: skip the rest of the physical line.
			for i < len(data) && data[i] != '\n' {
				i++
			}
			name = ""
			continue
		}
		key := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data[nameStart:i])), "export "))
		if !validSecretIdentifier(key) {
			key = "" // not a name we would have accepted, so not one we name
		}
		i++ // past the delimiter
		name = key
		// Value: a quoted value spans physical lines, so consume to its
		// closing quote; an unquoted one ends at the line end.
		for i < len(data) && (data[i] == ' ' || data[i] == '\t') {
			i++
		}
		if i < len(data) && (data[i] == '"' || data[i] == '\'') {
			quote := data[i]
			i++
			for i < len(data) {
				if data[i] == quote && (i == 0 || data[i-1] != '\\') {
					i++
					break
				}
				i++
			}
		}
		// The fault belongs to this statement when its offset falls inside it.
		next := i
		for next < len(data) && data[next] != '\n' {
			next++
		}
		if target < next || (target >= start && target < next) {
			return dotenvFault{Name: name}
		}
		i = next
		name = ""
	}
	return dotenvFault{Name: ""}
}

// isDotenvSpace mirrors godotenv: it treats '\r' and friends as spacing but
// leaves '\n' to end the statement.
func isDotenvSpace(r rune) bool {
	switch r {
	case '\t', '\v', '\f', '\r', ' ', 0x85, 0xA0:
		return true
	}
	return false
}

// invalidTextError names the variable whose assignment carries an undecodable
// sequence, falling back to the file when none can be established. The offset
// of the first invalid byte is what makes the message actionable without
// echoing the value that contains it.
func invalidTextError(data []byte, reason string) error {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			if f := locateFault(data, i); f.Name != "" {
				return fmt.Errorf("%s: %s", f.Name, reason)
			}
			return fmt.Errorf("the dotenv file contains text no loader can decode: %s", reason)
		}
		i += size
	}
	return fmt.Errorf("the dotenv file contains text no loader can decode: %s", reason)
}

// carriageReturnFault reports the assignment a fatal carriage return belongs
// to, or "" when none can be tied to the failure.
//
// A bare carriage return is a line ending to godotenv, so it is NOT itself a
// fault: it only explains a failure when it split an unquoted value, letting
// the following text be read as a new statement. This is therefore only
// consulted after the parser has failed, and it requires that the text after
// the carriage return could not have been a statement on its own — otherwise
// the real fault lies elsewhere and blaming the carriage return would send the
// user to the wrong line.
func carriageReturnFault(data []byte) string {
	for i := 0; i < len(data); i++ {
		if data[i] != '\r' {
			continue
		}
		if i+1 < len(data) && data[i+1] == '\n' {
			i++
			continue
		}
		// CRLF and a CR that merely separates two well-formed assignments are
		// both legal. Only report when the remainder of the physical line is
		// not an assignment, which is where godotenv would reject it.
		rest := data[i+1:]
		if end := bytes.IndexByte(rest, '\n'); end >= 0 {
			rest = rest[:end]
		}
		trimmed := strings.TrimSpace(string(rest))
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if _, _, ok := splitDotenvAssignment(trimmed); ok {
			continue
		}
		if f := locateFault(data, i); f.Name != "" {
			return f.Name
		}
		return ""
	}
	return ""
}

// splitDotenvAssignment reports whether the line is a well-formed assignment,
// accepting both '=' and the yaml-style ':' that godotenv parses.
func splitDotenvAssignment(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	for i := 0; i < len(line); i++ {
		if line[i] == '=' || line[i] == ':' {
			name := strings.TrimSpace(line[:i])
			if name == "" || !validSecretIdentifier(name) {
				return "", "", false
			}
			return name, strings.TrimSpace(line[i+1:]), true
		}
	}
	return "", "", false
}
